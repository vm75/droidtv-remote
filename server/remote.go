package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	remotePort     = 6466
	pairingPort    = 6467
	activeFeatures = 1 | 2 | 4 | 32 | 64 | 512
)

var errPairingRequired = errors.New("pairing required")

type remoteEvent func(kind string, data map[string]any)

type Remote struct {
	host         string
	cert         tls.Certificate
	conn         *tls.Conn
	reader       *bufio.Reader
	writeMu      sync.Mutex
	stateMu      sync.RWMutex
	imeCounter   int
	fieldCounter int
	done         chan error
	closed       chan struct{}
	once         sync.Once
	onEvent      remoteEvent
}

func ensureCertificate(certPath, keyPath string) (tls.Certificate, bool, error) {
	if _, err := os.Stat(certPath); err == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		return cert, false, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return tls.Certificate{}, false, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, false, err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "droidtv-remote"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	certTmp, keyTmp := certPath+".tmp", keyPath+".tmp"
	if err := os.WriteFile(certTmp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return tls.Certificate{}, false, err
	}
	if err := os.WriteFile(keyTmp, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		return tls.Certificate{}, false, err
	}
	if err := os.Rename(certTmp, certPath); err != nil {
		return tls.Certificate{}, false, err
	}
	if err := os.Rename(keyTmp, keyPath); err != nil {
		return tls.Certificate{}, false, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	return cert, true, err
}

func tlsConnect(ctx context.Context, host string, port int, cert tls.Certificate) (*tls.Conn, error) {
	d := net.Dialer{Timeout: 8 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	conn := tls.Client(raw, cfg)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func connectRemote(ctx context.Context, host string, cert tls.Certificate, onEvent remoteEvent) (*Remote, error) {
	conn, err := tlsConnect(ctx, host, remotePort, cert)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", errPairingRequired, err)
	}
	r := &Remote{host: host, cert: cert, conn: conn, reader: bufio.NewReader(conn), done: make(chan error, 1), closed: make(chan struct{}), onEvent: onEvent}
	started := make(chan struct{}, 1)
	go r.readLoop(started)
	select {
	case <-started:
		return r, nil
	case err := <-r.done:
		if err == nil {
			err = io.EOF
		}
		r.Close()
		return nil, err
	case <-time.After(10 * time.Second):
		r.Close()
		return nil, errors.New("timed out waiting for Android TV remote service")
	case <-ctx.Done():
		r.Close()
		return nil, ctx.Err()
	}
}

func (r *Remote) Done() <-chan error { return r.done }

func (r *Remote) Close() {
	r.once.Do(func() {
		close(r.closed)
		if r.conn != nil {
			_ = r.conn.Close()
		}
	})
}

func (r *Remote) send(payload []byte) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	select {
	case <-r.closed:
		return io.ErrClosedPipe
	default:
	}
	return writeFrame(r.conn, payload)
}

func (r *Remote) sendFromLoop(payload []byte) bool {
	if err := r.send(payload); err != nil {
		select {
		case r.done <- err:
		default:
		}
		return false
	}
	return true
}

func configureMessage(features uint64) []byte {
	var device []byte
	device = pbVarint(device, 3, 1)
	device = pbString(device, 4, "1")
	device = pbString(device, 5, "atvremote")
	device = pbString(device, 6, "1.0.0")
	var cfg []byte
	cfg = pbVarint(cfg, 1, features)
	cfg = pbBytes(cfg, 2, device)
	return pbBytes(nil, 1, cfg)
}

func setActiveMessage(features uint64) []byte {
	return pbBytes(nil, 2, pbVarint(nil, 1, features))
}

func (r *Remote) readLoop(started chan<- struct{}) {
	for {
		raw, err := readFrame(r.reader, 4*1024*1024)
		if err != nil {
			select {
			case r.done <- err:
			default:
			}
			return
		}
		fields, err := parseWire(raw)
		if err != nil {
			continue
		}
		for _, top := range fields {
			switch top.number {
			case 1:
				supported, ok := nestedVarint(top.bytes, 1)
				if !ok {
					supported = activeFeatures
				}
				if !r.sendFromLoop(configureMessage(supported & activeFeatures)) {
					return
				}
			case 2:
				if !r.sendFromLoop(setActiveMessage(activeFeatures)) {
					return
				}
			case 8:
				v, _ := nestedVarint(top.bytes, 1)
				if !r.sendFromLoop(pbBytes(nil, 9, pbVarint(nil, 1, v))) {
					return
				}
			case 20:
				inner, _ := parseWire(top.bytes)
				if f, ok := firstField(inner, 2); ok {
					if info, found := textFieldStatus(f.bytes); found && r.onEvent != nil {
						r.onEvent("ime_focus", info)
					}
				}
			case 21:
				inner, _ := parseWire(top.bytes)
				r.stateMu.Lock()
				if f, ok := firstField(inner, 1); ok {
					r.imeCounter = int(f.value)
				}
				if f, ok := firstField(inner, 2); ok {
					r.fieldCounter = int(f.value)
				}
				r.stateMu.Unlock()
			case 22:
				inner, _ := parseWire(top.bytes)
				if f, ok := firstField(inner, 2); ok {
					if info, found := textFieldStatus(f.bytes); found {
						if r.onEvent != nil {
							r.onEvent("ime_show", info)
						}
						counter, _ := info["counter"].(int)
						if !r.sendFromLoop(imeBatchMessage(counter, counter, "", false)) {
							return
						}
					}
				}
			case 40:
				select {
				case started <- struct{}{}:
				default:
				}
			}
		}
	}
}

func imeBatchMessage(imeCounter, fieldCounter int, text string, insert bool) []byte {
	end := utf8.RuneCountInString(text) - 1
	if end < 0 {
		end = 0
	}
	var obj []byte
	obj = pbVarint(obj, 1, uint64(end))
	obj = pbVarint(obj, 2, uint64(end))
	obj = pbString(obj, 3, text)
	var edit []byte
	if insert {
		edit = pbVarint(edit, 1, 1)
	}
	edit = pbBytes(edit, 2, obj)
	var batch []byte
	batch = pbVarint(batch, 1, uint64(imeCounter))
	batch = pbVarint(batch, 2, uint64(fieldCounter))
	batch = pbBytes(batch, 3, edit)
	return pbBytes(nil, 21, batch)
}

func (r *Remote) SendText(text string) error {
	if text == "" {
		return errors.New("text cannot be empty")
	}
	r.stateMu.RLock()
	a, b := r.imeCounter, r.fieldCounter
	r.stateMu.RUnlock()
	return r.send(imeBatchMessage(a, b, text, true))
}

func (r *Remote) SendKey(name string) error {
	code, ok := keyCode(name)
	if !ok {
		return fmt.Errorf("unknown key code: %s", name)
	}
	var key []byte
	key = pbVarint(key, 1, uint64(code))
	key = pbVarint(key, 2, 3)
	return r.send(pbBytes(nil, 10, key))
}

func (r *Remote) Launch(link string) error {
	if u, err := url.Parse(link); err == nil && u.Scheme == "" {
		link = "market://launch?id=" + link
	}
	return r.send(pbBytes(nil, 90, pbString(nil, 1, link)))
}

func keyCode(name string) (int, bool) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if !strings.HasPrefix(name, "KEYCODE_") {
		name = "KEYCODE_" + name
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(name, "KEYCODE_")); err == nil && n >= 0 && n <= 304 {
		return n, true
	}
	codes := map[string]int{
		"KEYCODE_UNKNOWN": 0, "KEYCODE_HOME": 3, "KEYCODE_BACK": 4,
		"KEYCODE_0": 7, "KEYCODE_1": 8, "KEYCODE_2": 9, "KEYCODE_3": 10, "KEYCODE_4": 11, "KEYCODE_5": 12, "KEYCODE_6": 13, "KEYCODE_7": 14, "KEYCODE_8": 15, "KEYCODE_9": 16,
		"KEYCODE_DPAD_UP": 19, "KEYCODE_DPAD_DOWN": 20, "KEYCODE_DPAD_LEFT": 21, "KEYCODE_DPAD_RIGHT": 22, "KEYCODE_DPAD_CENTER": 23,
		"KEYCODE_VOLUME_UP": 24, "KEYCODE_VOLUME_DOWN": 25, "KEYCODE_POWER": 26,
		"KEYCODE_ENTER": 66, "KEYCODE_DEL": 67, "KEYCODE_MENU": 82, "KEYCODE_SEARCH": 84,
		"KEYCODE_MEDIA_PLAY_PAUSE": 85, "KEYCODE_MEDIA_STOP": 86, "KEYCODE_MEDIA_NEXT": 87, "KEYCODE_MEDIA_PREVIOUS": 88, "KEYCODE_MEDIA_REWIND": 89, "KEYCODE_MEDIA_FAST_FORWARD": 90,
		"KEYCODE_MEDIA_PLAY": 126, "KEYCODE_MEDIA_PAUSE": 127, "KEYCODE_VOLUME_MUTE": 164, "KEYCODE_INFO": 165, "KEYCODE_CHANNEL_UP": 166, "KEYCODE_CHANNEL_DOWN": 167,
		"KEYCODE_SETTINGS": 176, "KEYCODE_PROG_RED": 183, "KEYCODE_PROG_GREEN": 184, "KEYCODE_PROG_YELLOW": 185, "KEYCODE_PROG_BLUE": 186, "KEYCODE_APP_SWITCH": 187,
		"KEYCODE_TV": 170, "KEYCODE_GUIDE": 172, "KEYCODE_DVR": 173, "KEYCODE_CAPTIONS": 175, "KEYCODE_TV_INPUT": 178,
	}
	if len(name) == 9 && name[8] >= 'A' && name[8] <= 'Z' {
		return 29 + int(name[8]-'A'), true
	}
	v, ok := codes[name]
	return v, ok
}

type PairSession struct {
	conn   *tls.Conn
	reader *bufio.Reader
	cert   tls.Certificate
}

func startPairing(ctx context.Context, host string, cert tls.Certificate) (*PairSession, error) {
	conn, err := tlsConnect(ctx, host, pairingPort, cert)
	if err != nil {
		return nil, err
	}
	p := &PairSession{conn: conn, reader: bufio.NewReader(conn), cert: cert}
	request := pbString(nil, 1, "atvremote")
	request = pbString(request, 2, "droidtv-remote")
	if err := p.exchange(pairingMessage(10, request)); err != nil {
		p.Close()
		return nil, err
	}
	encoding := pbVarint(nil, 1, 3)
	encoding = pbVarint(encoding, 2, 6)
	option := pbBytes(nil, 1, encoding)
	option = pbVarint(option, 3, 1)
	if err := p.exchange(pairingMessage(20, option)); err != nil {
		p.Close()
		return nil, err
	}
	config := pbBytes(nil, 1, encoding)
	config = pbVarint(config, 2, 1)
	if err := p.exchange(pairingMessage(30, config)); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func pairingMessage(field int, child []byte) []byte {
	var m []byte
	m = pbVarint(m, 1, 2)
	m = pbVarint(m, 2, 200)
	m = pbVarint(m, 3, uint64(field))
	return pbBytes(m, field, child)
}

func (p *PairSession) exchange(msg []byte) error {
	if err := writeFrame(p.conn, msg); err != nil {
		return err
	}
	raw, err := readFrame(p.reader, 64*1024)
	if err != nil {
		return err
	}
	fields, err := parseWire(raw)
	if err != nil {
		return err
	}
	if s, ok := firstField(fields, 2); !ok || s.value != 200 {
		return errors.New("Android TV rejected pairing message")
	}
	return nil
}

func (p *PairSession) Finish(code string) error {
	defer p.Close()
	code = strings.TrimSpace(code)
	if !regexp.MustCompile(`^[0-9A-Fa-f]{6}$`).MatchString(code) {
		return errors.New("pairing code must be 6 hexadecimal characters")
	}
	leaf, err := x509.ParseCertificate(p.cert.Certificate[0])
	if err != nil {
		return err
	}
	client, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("client certificate is not RSA")
	}
	peers := p.conn.ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return errors.New("TV did not provide a certificate")
	}
	server, ok := peers[0].PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("TV certificate is not RSA")
	}
	suffix, err := hex.DecodeString(code[2:])
	if err != nil {
		return err
	}
	h := sha256.New()
	h.Write(client.N.Bytes())
	h.Write([]byte{1, 0, 1})
	h.Write(server.N.Bytes())
	h.Write([]byte{1, 0, 1})
	h.Write(suffix)
	secret := h.Sum(nil)
	expected, _ := hex.DecodeString(code[:2])
	if len(expected) != 1 || secret[0] != expected[0] {
		return errors.New("invalid pairing code")
	}
	child := pbBytes(nil, 1, secret)
	return p.exchange(pairingMessage(40, child))
}

func (p *PairSession) Close() {
	if p != nil && p.conn != nil {
		_ = p.conn.Close()
	}
}
