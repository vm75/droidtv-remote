package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const maxIconBytes = 2 * 1024 * 1024

var (
	errAppDisabled     = errors.New("App is not enabled for this TV")
	errNoText          = errors.New("No text provided")
	errNotConnected    = errors.New("Not connected to TV")
	errUnknownLauncher = errors.New("Unknown app launcher")
)

type TVState struct {
	mu                 sync.Mutex
	remote             *Remote
	connecting         bool
	pairing            bool
	running            bool
	cancel             context.CancelFunc
	pairCode           chan string
	lastClientActivity time.Time
	activeClients      int
	adbState           string
}

type Event struct {
	Type string
	Data map[string]any
	TVID string
}

func (e Event) MarshalJSON() ([]byte, error) {
	v := map[string]any{"type": e.Type, "tv_id": nullable(e.TVID)}
	if e.Data != nil {
		v["data"] = e.Data
	}
	return json.Marshal(v)
}

type eventBucket struct {
	pending *Event
	waiters map[uint64]chan Event
	next    uint64
}

type Server struct {
	root              string
	version           string
	config            Config
	mu                sync.RWMutex
	apps              map[string]*App
	appOrder          []string
	tvs               map[string]*TV
	tvOrder           []string
	states            map[string]*TVState
	eventsMu          sync.Mutex
	events            map[string]*eventBucket
	mux               *http.ServeMux
	eventTimeout      time.Duration
	inactivityTimeout time.Duration
	adb               *ADBManager
	adbAdminToken     string
}

func NewServer(root, version string) (*Server, error) {
	s := &Server{root: root, version: version, apps: map[string]*App{}, tvs: map[string]*TV{}, states: map[string]*TVState{}, events: map[string]*eventBucket{}, eventTimeout: 30 * time.Second, inactivityTimeout: 5 * time.Minute}
	s.adb = NewADBManager(root, nil)
	s.adbAdminToken = strings.TrimSpace(os.Getenv("DROIDTV_ADB_ADMIN_TOKEN"))
	if s.adb.Enabled() && s.adbAdminToken == "" {
		s.adb.initErr = &ADBError{Code: "missing_admin_token", Message: "ADB administrator token is required when ADB is enabled"}
	}
	s.config = loadConfig(filepath.Join(root, "data", "config.yaml"))
	if err := s.loadApps(); err != nil {
		return nil, err
	}
	if err := s.loadTVs(); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) appsPath() string       { return filepath.Join(s.root, "data", "apps.yaml") }
func (s *Server) tvsPath() string        { return filepath.Join(s.root, "data", "tvs.yaml") }
func (s *Server) iconsDir() string       { return filepath.Join(s.root, "data", "icons") }
func (s *Server) tvDir(id string) string { return filepath.Join(s.root, "data", "tvs", id) }

func (s *Server) loadApps() error {
	rows, err := parseListMaps(s.appsPath(), "apps")
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, row := range rows {
		id, _ := row["id"].(string)
		name, _ := row["name"].(string)
		pkg, _ := row["package_id"].(string)
		icon, _ := row["icon"].(string)
		iconFile, _ := row["icon_file"].(string)
		if !validID(id) || strings.TrimSpace(name) == "" || strings.TrimSpace(pkg) == "" {
			continue
		}
		if !validIconFilename(iconFile) {
			iconFile = ""
		}
		s.apps[id] = &App{ID: id, Name: name, PackageID: pkg, Icon: icon, IconFile: iconFile}
		s.appOrder = append(s.appOrder, id)
	}
	if !exists {
		for _, legacy := range s.config.LegacyApps {
			if legacy.Name == "" || legacy.PackageID == "" {
				continue
			}
			seed := legacy.PackageID
			id := legacyID(seed)
			for n := 1; s.apps[id] != nil; n++ {
				id = legacyID(fmt.Sprintf("%s:%s:%d", legacy.PackageID, legacy.Name, n))
			}
			a := &App{ID: id, Name: legacy.Name, PackageID: legacy.PackageID, Icon: legacy.Icon}
			s.apps[id] = a
			s.appOrder = append(s.appOrder, id)
		}
		return s.saveAppsLocked()
	}
	return nil
}

func (s *Server) loadTVs() error {
	rows, err := parseListMaps(s.tvsPath(), "tvs")
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	changed := false
	for _, row := range rows {
		id, _ := row["id"].(string)
		name, _ := row["name"].(string)
		host, _ := row["host"].(string)
		adbSerial, _ := row["adb_serial"].(string)
		adbEndpoint, _ := row["adb_endpoint"].(string)
		adbPairGUID, _ := row["adb_pair_guid"].(string)
		if !validID(id) || name == "" || host == "" {
			continue
		}
		rawIDs, present := row["app_ids"]
		ids, ok := rawIDs.([]string)
		if !present {
			ids = append([]string(nil), s.appOrder...)
			changed = true
		} else if !ok {
			ids = []string{}
			changed = true
		}
		clean := make([]string, 0, len(ids))
		for _, aid := range ids {
			if s.apps[aid] != nil && !slices.Contains(clean, aid) {
				clean = append(clean, aid)
			}
		}
		if len(clean) != len(ids) {
			changed = true
		}
		s.tvs[id] = &TV{ID: id, Name: name, Host: host, AppIDs: clean, ADBSerial: adbSerial, ADBEndpoint: adbEndpoint, ADBPairGUID: adbPairGUID}
		s.tvOrder = append(s.tvOrder, id)
	}
	if !exists && strings.TrimSpace(s.config.TVIP) != "" {
		id := legacyID(s.config.TVIP)
		name := strings.TrimSpace(s.config.TVName)
		if name == "" {
			name = "Android TV"
		}
		s.tvs[id] = &TV{ID: id, Name: name, Host: s.config.TVIP, AppIDs: append([]string(nil), s.appOrder...)}
		s.tvOrder = append(s.tvOrder, id)
		dir := s.tvDir(id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		for _, fn := range []string{"cert.pem", "key.pem"} {
			old := filepath.Join(s.root, "data", fn)
			dst := filepath.Join(dir, fn)
			if _, e := os.Stat(old); e == nil {
				if _, e = os.Stat(dst); os.IsNotExist(e) {
					b, err := os.ReadFile(old)
					if err != nil {
						return err
					}
					if err := os.WriteFile(dst, b, 0600); err != nil {
						return err
					}
				}
			}
		}
		return s.saveTVsLocked()
	}
	if exists && changed {
		return s.saveTVsLocked()
	}
	return nil
}

func validID(s string) bool { return regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(s) }
func validIconFilename(s string) bool {
	return s == "" || regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}\.(png|jpg|webp|gif)$`).MatchString(s)
}

func (s *Server) appSliceLocked() []*App {
	out := make([]*App, 0, len(s.appOrder))
	for _, id := range s.appOrder {
		if a := s.apps[id]; a != nil {
			out = append(out, a)
		}
	}
	return out
}
func (s *Server) tvSliceLocked() []*TV {
	out := make([]*TV, 0, len(s.tvOrder))
	for _, id := range s.tvOrder {
		if t := s.tvs[id]; t != nil {
			out = append(out, t)
		}
	}
	return out
}
func (s *Server) saveAppsLocked() error {
	return saveApps(s.appsPath(), s.appSliceLocked(), s.appOrder)
}
func (s *Server) saveTVsLocked() error { return saveTVs(s.tvsPath(), s.tvSliceLocked(), s.tvOrder) }

func (s *Server) state(id string) *TVState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[id]
	if st == nil {
		st = &TVState{}
		s.states[id] = st
	}
	return st
}
func (s *Server) resolveTV(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.tvs[id] != nil {
		return id
	}
	if id == "" && len(s.tvOrder) == 1 {
		return s.tvOrder[0]
	}
	return ""
}

func (s *Server) appJSON(a *App) AppJSON {
	icon := a.Icon
	if a.IconFile != "" {
		icon = "icons/" + a.IconFile
	}
	return AppJSON{ID: a.ID, Name: a.Name, PackageID: a.PackageID, Icon: icon, IconClass: a.Icon, HasUploadedIcon: a.IconFile != ""}
}
func (s *Server) appsForTV(id string) []AppJSON {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.tvs[id]
	if t == nil {
		return []AppJSON{}
	}
	out := make([]AppJSON, 0, len(t.AppIDs))
	for _, aid := range t.AppIDs {
		if a := s.apps[aid]; a != nil {
			out = append(out, s.appJSON(a))
		}
	}
	return out
}

func (s *Server) tvStatus(id string) map[string]any {
	s.touchActivity(id)
	s.mu.RLock()
	t := s.tvs[id]
	var copy TV
	if t != nil {
		copy = *t
		copy.AppIDs = append([]string(nil), t.AppIDs...)
	}
	s.mu.RUnlock()
	st := s.state(id)
	st.mu.Lock()
	connected := st.remote != nil
	connecting := st.connecting
	pairing := st.pairing
	st.mu.Unlock()
	return map[string]any{"id": copy.ID, "name": copy.Name, "host": copy.Host, "app_ids": copy.AppIDs, "connected": connected, "connecting": connecting, "pairing_in_progress": pairing}
}

func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Server) touchActivity(id string) {
	if id == "" {
		return
	}
	st := s.state(id)
	st.mu.Lock()
	st.lastClientActivity = time.Now()
	st.mu.Unlock()
}

func (s *Server) beginClientSession(id string) func() {
	if id == "" {
		return func() {}
	}
	st := s.state(id)
	st.mu.Lock()
	st.activeClients++
	st.lastClientActivity = time.Now()
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if st.activeClients > 0 {
			st.activeClients--
		}
		st.lastClientActivity = time.Now()
		st.mu.Unlock()
	}
}

func (s *Server) startConnection(id string) string {
	s.touchActivity(id)
	st := s.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.remote != nil {
		return "connected"
	}
	if st.running || st.connecting || st.pairing {
		return "already_in_progress"
	}
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	st.running = true
	st.connecting = true
	go s.initialConnect(ctx, id, st)
	return "connecting"
}

func (s *Server) initialConnect(ctx context.Context, id string, st *TVState) {
	remote, err := s.initializeTV(ctx, id, st)
	if err != nil {
		log.Printf("connect %s: %v", id, err)
		st.mu.Lock()
		st.remote = nil
		st.connecting = false
		st.pairing = false
		st.running = false
		st.mu.Unlock()
		return
	}
	s.monitor(ctx, id, st, remote)
}

func (s *Server) initializeTV(ctx context.Context, id string, st *TVState) (*Remote, error) {
	s.mu.RLock()
	tv := s.tvs[id]
	var host string
	if tv != nil {
		host = tv.Host
	}
	s.mu.RUnlock()
	if host == "" {
		return nil, errors.New("Unknown TV")
	}
	dir := s.tvDir(id)
	cert, _, err := ensureCertificate(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, err
	}
	cb := func(kind string, data map[string]any) {
		delete(data, "counter")
		s.broadcast(Event{Type: kind, Data: data, TVID: id})
	}
	remote, err := connectRemote(ctx, host, cert, cb)
	if err == nil {
		st.mu.Lock()
		st.remote = remote
		st.connecting = false
		st.pairing = false
		st.mu.Unlock()
		return remote, nil
	}
	if !errors.Is(err, errPairingRequired) {
		return nil, err
	}
	st.mu.Lock()
	st.connecting = false
	st.pairing = true
	st.pairCode = make(chan string, 1)
	codeCh := st.pairCode
	st.mu.Unlock()
	pair, err := startPairing(ctx, host, cert)
	if err != nil {
		return nil, err
	}
	var code string
	select {
	case code = <-codeCh:
	case <-time.After(120 * time.Second):
		pair.Close()
		return nil, errors.New("Pairing timeout")
	case <-ctx.Done():
		pair.Close()
		return nil, ctx.Err()
	}
	if err := pair.Finish(code); err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.pairing = false
	st.connecting = true
	st.mu.Unlock()
	remote, err = connectRemote(ctx, host, cert, cb)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.remote = remote
	st.connecting = false
	st.mu.Unlock()
	return remote, nil
}

func (s *Server) monitor(ctx context.Context, id string, st *TVState, remote *Remote) {
	timeout := s.inactivityTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	interval := timeout / 5
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	} else if interval > 1*time.Second {
		interval = 1 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	connectionLost := false
	for !connectionLost {
		select {
		case err := <-remote.Done():
			log.Printf("connection %s lost: %v", id, err)
			connectionLost = true
		case <-ctx.Done():
			remote.Close()
			st.mu.Lock()
			st.running = false
			st.remote = nil
			st.mu.Unlock()
			return
		case <-ticker.C:
			st.mu.Lock()
			inactive := st.activeClients == 0 && !st.lastClientActivity.IsZero() && time.Since(st.lastClientActivity) >= timeout
			st.mu.Unlock()
			if inactive {
				log.Printf("disconnecting %s due to inactivity", id)
				remote.Close()
				st.mu.Lock()
				st.running = false
				st.remote = nil
				st.mu.Unlock()
				return
			}
		}
	}

	st.mu.Lock()
	if st.remote == remote {
		st.remote = nil
	}
	st.connecting = false
	st.mu.Unlock()

	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		st.mu.Lock()
		st.running = false
		st.mu.Unlock()
		return
	}

	retries := 0
	for {
		st.mu.Lock()
		inactive := st.activeClients == 0 && !st.lastClientActivity.IsZero() && time.Since(st.lastClientActivity) >= timeout
		st.mu.Unlock()
		if inactive {
			log.Printf("stopping auto-reconnect %s due to inactivity", id)
			st.mu.Lock()
			st.running = false
			st.mu.Unlock()
			return
		}

		s.mu.RLock()
		exists := s.tvs[id] != nil
		s.mu.RUnlock()
		if !exists {
			st.mu.Lock()
			st.running = false
			st.mu.Unlock()
			return
		}
		st.mu.Lock()
		st.connecting = true
		st.mu.Unlock()
		next, err := s.initializeTV(ctx, id, st)
		if err == nil {
			log.Printf("auto-reconnect %s succeeded", id)
			s.monitor(ctx, id, st, next)
			return
		}
		retries++
		st.mu.Lock()
		st.connecting = false
		st.pairing = false
		st.mu.Unlock()
		delay := time.Duration(5+2*retries) * time.Second
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		log.Printf("auto-reconnect %s failed: %v; retrying in %s", id, err, delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			st.mu.Lock()
			st.running = false
			st.mu.Unlock()
			return
		}
	}
}

func (s *Server) disconnect(id string) {
	st := s.state(id)
	st.mu.Lock()
	if st.cancel != nil {
		st.cancel()
	}
	if st.remote != nil {
		st.remote.Close()
	}
	st.remote = nil
	st.connecting = false
	st.pairing = false
	st.running = false
	st.pairCode = nil
	st.mu.Unlock()
}

func (s *Server) broadcast(event Event) {
	key := event.TVID
	s.eventsMu.Lock()
	b := s.events[key]
	if b == nil {
		b = &eventBucket{waiters: map[uint64]chan Event{}}
		s.events[key] = b
	}
	if len(b.waiters) > 0 {
		for id, ch := range b.waiters {
			select {
			case ch <- event:
			default:
			}
			delete(b.waiters, id)
		}
	} else {
		copy := event
		b.pending = &copy
	}
	s.eventsMu.Unlock()
}

func (s *Server) nextEvent(ctx context.Context, id string, timeout time.Duration) Event {
	s.eventsMu.Lock()
	b := s.events[id]
	if b == nil {
		b = &eventBucket{waiters: map[uint64]chan Event{}}
		s.events[id] = b
	}
	if b.pending != nil {
		e := *b.pending
		b.pending = nil
		s.eventsMu.Unlock()
		return e
	}
	b.next++
	n := b.next
	ch := make(chan Event, 1)
	b.waiters[n] = ch
	s.eventsMu.Unlock()
	select {
	case e := <-ch:
		return e
	case <-time.After(timeout):
		s.eventsMu.Lock()
		delete(b.waiters, n)
		s.eventsMu.Unlock()
		return Event{Type: "keepalive", TVID: id}
	case <-ctx.Done():
		s.eventsMu.Lock()
		delete(b.waiters, n)
		s.eventsMu.Unlock()
		return Event{Type: "keepalive", TVID: id}
	}
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]any{"error": msg})
}
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 3*1024*1024)).Decode(v)
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/status", s.handleStatus)
	m.HandleFunc("GET /api/tvs", s.handleTVs)
	m.HandleFunc("POST /api/tvs", s.handleAddTV)
	m.HandleFunc("DELETE /api/tvs/{tv_id}", s.handleForgetTV)
	m.HandleFunc("PUT /api/tvs/{tv_id}/apps", s.handleTVApps)
	m.HandleFunc("GET /api/apps", s.handleApps)
	m.HandleFunc("POST /api/apps", s.handleAddApp)
	m.HandleFunc("PUT /api/apps/reorder", s.handleReorderApps)
	m.HandleFunc("PUT /api/apps/{app_id}", s.handleUpdateApp)
	m.HandleFunc("DELETE /api/apps/{app_id}", s.handleDeleteApp)
	m.HandleFunc("POST /api/connect", s.handleConnect)
	m.HandleFunc("POST /api/pairing_code", s.handlePairCode)
	m.HandleFunc("POST /api/send_key", s.handleSendKey)
	m.HandleFunc("POST /api/send_text", s.handleSendText)
	m.HandleFunc("POST /api/launch_app", s.handleLaunchApp)
	m.HandleFunc("GET /api/events", s.handleEvents)
	m.HandleFunc("GET /api/tvs/{tv_id}/adb/status", s.handleADBStatus)
	m.HandleFunc("POST /api/tvs/{tv_id}/adb/pair", s.handleADBPair)
	m.HandleFunc("POST /api/tvs/{tv_id}/adb/connect", s.handleADBConnect)
	m.HandleFunc("POST /api/tvs/{tv_id}/adb/disconnect", s.handleADBDisconnect)
	m.HandleFunc("POST /api/tvs/{tv_id}/adb/forget", s.handleADBForget)
	m.HandleFunc("GET /api/tvs/{tv_id}/adb/device", s.handleADBDeviceInfo)
	m.HandleFunc("GET /api/tvs/{tv_id}/adb/packages", s.handleADBPackages)
	m.HandleFunc("GET /api/tvs/{tv_id}/adb/launchables", s.handleADBLaunchables)
	m.Handle("/mcp", s.mcpHandler())
	m.Handle("/mcp/", s.mcpHandler())
	m.HandleFunc("/", s.handleStatic)
	s.mux = m
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("panic handling %s %s: %v", r.Method, r.URL.Path, v)
			apiError(w, 500, fmt.Sprint(v))
		}
	}()
	p := s.normalizePath(r.URL.Path)
	if p != r.URL.Path {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Path = p
		clone.URL = &u
		r = clone
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) normalizePath(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i < len(parts); i++ {
		cand := "/" + strings.Join(parts[i:], "/")
		if strings.HasPrefix(cand, "/api/") || cand == "/mcp" || strings.HasPrefix(cand, "/mcp/") || strings.HasPrefix(cand, "/icons/") {
			return cand
		}
		clean := filepath.Clean(strings.TrimPrefix(cand, "/"))
		if clean != "." && !strings.HasPrefix(clean, "..") {
			if st, err := os.Stat(filepath.Join(s.root, "client", clean)); err == nil && !st.IsDir() {
				return cand
			}
		}
	}
	base := parts[len(parts)-1]
	if !strings.Contains(base, ".") {
		return "/"
	}
	return path
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := s.resolveTV(r.URL.Query().Get("tv_id"))
	var connected, connecting, pairing bool
	name := "No TV selected"
	if id != "" {
		v := s.tvStatus(id)
		connected = v["connected"].(bool)
		connecting = v["connecting"].(bool)
		pairing = v["pairing_in_progress"].(bool)
		name = v["name"].(string)
	}
	jsonResponse(w, 200, map[string]any{"tv_id": nullable(id), "connected": connected, "pairing_in_progress": pairing, "connecting": connecting, "tv_name": name, "apps": s.appsForTV(id), "version": s.version})
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (s *Server) handleTVs(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ids := append([]string(nil), s.tvOrder...)
	s.mu.RUnlock()
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.tvStatus(id))
	}
	jsonResponse(w, 200, map[string]any{"tvs": out})
}

func (s *Server) addTV(name, host string) (map[string]any, int, error) {
	name = strings.TrimSpace(name)
	host = strings.TrimSpace(host)
	if name == "" || len(name) > 100 {
		return nil, 400, errors.New("TV name is required (100 characters maximum)")
	}
	if host == "" || len(host) > 255 || strings.IndexFunc(host, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }) >= 0 {
		return nil, 400, errors.New("A valid TV IP address or host name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tvs {
		if strings.EqualFold(t.Host, host) {
			return nil, 409, errors.New("That TV address is already configured")
		}
	}
	id, err := randomID()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	s.tvs[id] = &TV{ID: id, Name: name, Host: host, AppIDs: append([]string(nil), s.appOrder...)}
	s.tvOrder = append(s.tvOrder, id)
	if err := s.saveTVsLocked(); err != nil {
		return nil, 500, err
	}
	return s.tvStatusUnlocked(id), 201, nil
}
func (s *Server) tvStatusUnlocked(id string) map[string]any {
	t := s.tvs[id]
	copy := *t
	copy.AppIDs = append([]string(nil), t.AppIDs...)
	st := s.states[id]
	connected, connecting, pairing := false, false, false
	if st != nil {
		st.mu.Lock()
		connected = st.remote != nil
		connecting = st.connecting
		pairing = st.pairing
		st.mu.Unlock()
	}
	return map[string]any{"id": copy.ID, "name": copy.Name, "host": copy.Host, "app_ids": copy.AppIDs, "connected": connected, "connecting": connecting, "pairing_in_progress": pairing}
}
func (s *Server) handleAddTV(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Host string `json:"host"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	tv, status, err := s.addTV(in.Name, in.Host)
	if err != nil {
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, status, map[string]any{"tv": tv})
}
func (s *Server) forgetTV(id string) error {
	s.mu.RLock()
	_, ok := s.tvs[id]
	s.mu.RUnlock()
	if !ok {
		return errors.New("Unknown TV")
	}
	s.disconnect(id)
	s.mu.Lock()
	delete(s.states, id)
	delete(s.tvs, id)
	s.tvOrder = slices.DeleteFunc(s.tvOrder, func(v string) bool { return v == id })
	err := s.saveTVsLocked()
	s.mu.Unlock()
	_ = os.Remove(filepath.Join(s.tvDir(id), "cert.pem"))
	_ = os.Remove(filepath.Join(s.tvDir(id), "key.pem"))
	_ = os.Remove(s.tvDir(id))
	return err
}
func (s *Server) handleForgetTV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("tv_id")
	if err := s.forgetTV(id); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "Unknown TV" {
			status = http.StatusNotFound
		}
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "forgotten", "tv_id": id})
}
func (s *Server) setTVApps(id string, ids []string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tvs[id]
	if t == nil {
		return nil, errors.New("Unknown TV")
	}
	clean := []string{}
	for _, aid := range ids {
		if s.apps[aid] == nil {
			return nil, fmt.Errorf("Unknown app launcher: %s", aid)
		}
		if !slices.Contains(clean, aid) {
			clean = append(clean, aid)
		}
	}
	t.AppIDs = clean
	if err := s.saveTVsLocked(); err != nil {
		return nil, err
	}
	return s.tvStatusUnlocked(id), nil
}
func (s *Server) handleTVApps(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppIDs []string `json:"app_ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	tv, err := s.setTVApps(r.PathValue("tv_id"), in.AppIDs)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "Unknown TV" {
			status = http.StatusNotFound
		} else if strings.HasPrefix(err.Error(), "Unknown app launcher:") {
			status = http.StatusBadRequest
		}
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"tv": tv})
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	out := make([]AppJSON, 0, len(s.appOrder))
	for _, id := range s.appOrder {
		if a := s.apps[id]; a != nil {
			out = append(out, s.appJSON(a))
		}
	}
	s.mu.RUnlock()
	jsonResponse(w, 200, map[string]any{"apps": out})
}

type appForm struct {
	Name, PackageID, IconClass     string
	NameSet, PackageIDSet, IconSet bool
	RemoveIcon                     bool
	IconType                       string
	IconData                       []byte
}

func readLimitedFile(f multipart.File) ([]byte, error) {
	return io.ReadAll(io.LimitReader(f, maxIconBytes+1))
}
func readAppForm(r *http.Request) (appForm, error) {
	var out appForm
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var v map[string]any
		if err := decodeJSON(r, &v); err != nil {
			return out, err
		}
		if x, ok := v["name"].(string); ok {
			out.Name, out.NameSet = x, true
		}
		if x, ok := v["package_id"].(string); ok {
			out.PackageID, out.PackageIDSet = x, true
		}
		if x, ok := v["icon_class"].(string); ok {
			out.IconClass, out.IconSet = x, true
		}
		out.RemoveIcon = fmt.Sprint(v["remove_icon"]) == "true"
		return out, nil
	}
	if strings.HasPrefix(ct, "multipart/") {
		if err := r.ParseMultipartForm(maxIconBytes + 65536); err != nil {
			return out, err
		}
		out.Name, out.NameSet = r.FormValue("name"), r.MultipartForm.Value["name"] != nil
		out.PackageID, out.PackageIDSet = r.FormValue("package_id"), r.MultipartForm.Value["package_id"] != nil
		out.IconClass, out.IconSet = r.FormValue("icon_class"), r.MultipartForm.Value["icon_class"] != nil
		out.RemoveIcon = strings.EqualFold(r.FormValue("remove_icon"), "true")
		f, h, err := r.FormFile("icon_file")
		if err == nil {
			defer f.Close()
			b, e := readLimitedFile(f)
			if e != nil {
				return out, e
			}
			out.IconData = b
			out.IconType = strings.ToLower(strings.Split(h.Header.Get("Content-Type"), ";")[0])
		} else if !errors.Is(err, http.ErrMissingFile) {
			return out, err
		}
		return out, nil
	}
	if err := r.ParseForm(); err != nil {
		return out, err
	}
	out.Name, out.NameSet = r.FormValue("name"), r.Form.Has("name")
	out.PackageID, out.PackageIDSet = r.FormValue("package_id"), r.Form.Has("package_id")
	out.IconClass, out.IconSet = r.FormValue("icon_class"), r.Form.Has("icon_class")
	out.RemoveIcon = strings.EqualFold(r.FormValue("remove_icon"), "true")
	return out, nil
}
func validateApp(name, pkg, icon string) error {
	if strings.TrimSpace(name) == "" || len(name) > 100 {
		return errors.New("App name is required (100 characters maximum)")
	}
	if strings.TrimSpace(pkg) == "" || len(pkg) > 255 || strings.IndexFunc(pkg, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }) >= 0 {
		return errors.New("A valid Android package ID is required")
	}
	if icon != "" && !regexp.MustCompile(`^mdi-[A-Za-z0-9-]{1,90}$`).MatchString(icon) {
		return errors.New("Material icon class must start with mdi- and contain only letters, numbers, or dashes")
	}
	return nil
}
func validateIcon(contentType string, b []byte) (string, error) {
	if len(b) == 0 {
		return "", errors.New("Uploaded icon is empty")
	}
	if len(b) > maxIconBytes {
		return "", errors.New("Icon must be 2 MB or smaller")
	}
	ok := false
	ext := ""
	switch contentType {
	case "image/png":
		ext = "png"
		ok = len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n"
	case "image/jpeg":
		ext = "jpg"
		ok = len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff
	case "image/webp":
		ext = "webp"
		ok = len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	case "image/gif":
		ext = "gif"
		ok = len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a")
	default:
		return "", errors.New("Icon must be PNG, JPEG, WebP, or GIF")
	}
	if !ok {
		return "", errors.New("Uploaded icon content does not match its image type")
	}
	return ext, nil
}
func (s *Server) writeIcon(id, ext string, b []byte) (string, error) {
	if err := os.MkdirAll(s.iconsDir(), 0755); err != nil {
		return "", err
	}
	suffix, err := randomID()
	if err != nil {
		return "", err
	}
	name := id + "-" + suffix[:8] + "." + ext
	path := filepath.Join(s.iconsDir(), name)
	if err := writeAtomic(path, b); err != nil {
		return "", err
	}
	return name, nil
}
func (s *Server) addApp(f appForm) (AppJSON, int, error) {
	f.Name = strings.TrimSpace(f.Name)
	f.PackageID = strings.TrimSpace(f.PackageID)
	f.IconClass = strings.TrimSpace(f.IconClass)
	if err := validateApp(f.Name, f.PackageID, f.IconClass); err != nil {
		return AppJSON{}, 400, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.apps {
		if a.PackageID == f.PackageID {
			return AppJSON{}, 409, errors.New("That package ID already has a launcher")
		}
	}
	id, err := randomID()
	if err != nil {
		return AppJSON{}, http.StatusInternalServerError, err
	}
	a := &App{ID: id, Name: f.Name, PackageID: f.PackageID, Icon: f.IconClass}
	if len(f.IconData) > 0 {
		ext, err := validateIcon(f.IconType, f.IconData)
		if err != nil {
			return AppJSON{}, 400, err
		}
		name, err := s.writeIcon(id, ext, f.IconData)
		if err != nil {
			return AppJSON{}, 500, err
		}
		a.IconFile = name
	}
	s.apps[id] = a
	s.appOrder = append(s.appOrder, id)
	if err := s.saveAppsLocked(); err != nil {
		return AppJSON{}, 500, err
	}
	return s.appJSON(a), 201, nil
}
func (s *Server) handleAddApp(w http.ResponseWriter, r *http.Request) {
	f, err := readAppForm(r)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	app, status, err := s.addApp(f)
	if err != nil {
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, status, map[string]any{"app": app})
}
func (s *Server) updateApp(id string, f appForm) (AppJSON, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.apps[id]
	if a == nil {
		return AppJSON{}, 404, errors.New("Unknown app launcher")
	}
	name := a.Name
	if f.NameSet {
		name = strings.TrimSpace(f.Name)
	}
	pkg := a.PackageID
	if f.PackageIDSet {
		pkg = strings.TrimSpace(f.PackageID)
	}
	icon := a.Icon
	if f.IconSet {
		icon = strings.TrimSpace(f.IconClass)
	}
	if err := validateApp(name, pkg, icon); err != nil {
		return AppJSON{}, 400, err
	}
	for oid, o := range s.apps {
		if oid != id && o.PackageID == pkg {
			return AppJSON{}, 409, errors.New("That package ID already has a launcher")
		}
	}
	old := a.IconFile
	a.Name = name
	a.PackageID = pkg
	if f.IconSet {
		a.Icon = icon
	}
	if len(f.IconData) > 0 {
		ext, err := validateIcon(f.IconType, f.IconData)
		if err != nil {
			return AppJSON{}, 400, err
		}
		fn, err := s.writeIcon(id, ext, f.IconData)
		if err != nil {
			return AppJSON{}, 500, err
		}
		a.IconFile = fn
		if old != "" && old != fn {
			_ = os.Remove(filepath.Join(s.iconsDir(), old))
		}
	} else if f.RemoveIcon {
		if old != "" {
			_ = os.Remove(filepath.Join(s.iconsDir(), old))
		}
		a.IconFile = ""
	}
	if err := s.saveAppsLocked(); err != nil {
		return AppJSON{}, 500, err
	}
	return s.appJSON(a), 200, nil
}
func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	f, err := readAppForm(r)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	app, status, err := s.updateApp(r.PathValue("app_id"), f)
	if err != nil {
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"app": app})
}
func (s *Server) deleteApp(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.apps[id]
	if a == nil {
		return errors.New("Unknown app launcher")
	}
	if a.IconFile != "" {
		_ = os.Remove(filepath.Join(s.iconsDir(), a.IconFile))
	}
	delete(s.apps, id)
	s.appOrder = slices.DeleteFunc(s.appOrder, func(v string) bool { return v == id })
	for _, t := range s.tvs {
		t.AppIDs = slices.DeleteFunc(t.AppIDs, func(v string) bool { return v == id })
	}
	if err := s.saveAppsLocked(); err != nil {
		return err
	}
	return s.saveTVsLocked()
}
func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("app_id")
	if err := s.deleteApp(id); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "Unknown app launcher" {
			status = http.StatusNotFound
		}
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "deleted", "app_id": id})
}
func (s *Server) reorderApps(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) != len(s.apps) {
		return errors.New("app_ids must contain all existing app launcher IDs")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if s.apps[id] == nil || seen[id] {
			return errors.New("app_ids must contain all existing app launcher IDs")
		}
		seen[id] = true
	}
	s.appOrder = append([]string(nil), ids...)
	return s.saveAppsLocked()
}
func (s *Server) handleReorderApps(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppIDs []string `json:"app_ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.reorderApps(in.AppIDs); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "app_ids must contain all existing app launcher IDs" {
			status = http.StatusBadRequest
		}
		apiError(w, status, err.Error())
		return
	}
	s.handleApps(w, r)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TVID string `json:"tv_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	id := s.resolveTV(in.TVID)
	if id == "" {
		apiError(w, 404, "Unknown TV")
		return
	}
	jsonResponse(w, 200, map[string]any{"status": s.startConnection(id), "tv_id": id})
}
func (s *Server) submitPairCode(id, code string) error {
	id = s.resolveTV(id)
	if id == "" {
		return errors.New("Not waiting for pairing code")
	}
	st := s.state(id)
	st.mu.Lock()
	ch := st.pairCode
	waiting := st.pairing && ch != nil
	st.mu.Unlock()
	if !waiting {
		return errors.New("Not waiting for pairing code")
	}
	select {
	case ch <- strings.TrimSpace(code):
		return nil
	default:
		return errors.New("Not waiting for pairing code")
	}
}
func (s *Server) handlePairCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TVID string `json:"tv_id"`
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.submitPairCode(in.TVID, in.Code); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "submitted"})
}
func (s *Server) remoteFor(id string) (string, *TVState, *Remote) {
	id = s.resolveTV(id)
	if id == "" {
		return "", nil, nil
	}
	st := s.state(id)
	st.mu.Lock()
	r := st.remote
	st.mu.Unlock()
	return id, st, r
}
func (s *Server) sendKey(id, key string) error {
	_, st, r := s.remoteFor(id)
	if r == nil {
		return errNotConnected
	}
	if err := r.SendKey(key); err != nil {
		if errors.Is(err, io.ErrClosedPipe) {
			st.mu.Lock()
			st.remote = nil
			st.mu.Unlock()
			return errNotConnected
		}
		return err
	}
	return nil
}
func (s *Server) handleSendKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TVID string `json:"tv_id"`
		Key  string `json:"key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.sendKey(in.TVID, in.Key); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errNotConnected) {
			status = http.StatusBadRequest
		}
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "ok"})
}
func (s *Server) sendText(id, text string, enter bool) error {
	_, st, r := s.remoteFor(id)
	if r == nil {
		return errNotConnected
	}
	if text == "" {
		return errNoText
	}
	if err := r.SendText(text); err != nil {
		st.mu.Lock()
		if errors.Is(err, io.ErrClosedPipe) {
			st.remote = nil
			st.mu.Unlock()
			return errNotConnected
		}
		st.mu.Unlock()
		return err
	}
	if enter {
		time.Sleep(500 * time.Millisecond)
		return r.SendKey("KEYCODE_ENTER")
	}
	return nil
}
func (s *Server) handleSendText(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TVID  string `json:"tv_id"`
		Text  string `json:"text"`
		Enter bool   `json:"enter"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := s.sendText(in.TVID, in.Text, in.Enter); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errNotConnected) || errors.Is(err, errNoText) {
			status = http.StatusBadRequest
		}
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "ok"})
}
func (s *Server) launchApp(id, requested string) error {
	tvID, _, r := s.remoteFor(id)
	s.mu.RLock()
	launcher := s.apps[requested]
	if launcher == nil {
		for _, a := range s.apps {
			if a.PackageID == requested {
				launcher = a
				break
			}
		}
	}
	tv := s.tvs[tvID]
	enabled := launcher != nil && tv != nil && slices.Contains(tv.AppIDs, launcher.ID)
	var pkg string
	if launcher != nil {
		pkg = launcher.PackageID
	}
	s.mu.RUnlock()
	if launcher == nil {
		return errUnknownLauncher
	}
	if !enabled {
		return errAppDisabled
	}
	if r == nil {
		return errNotConnected
	}
	return r.Launch(pkg)
}
func (s *Server) handleLaunchApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TVID       string `json:"tv_id"`
		LauncherID string `json:"launcher_id"`
		AppID      string `json:"app_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	requested := in.LauncherID
	if requested == "" {
		requested = in.AppID
	}
	if err := s.launchApp(in.TVID, requested); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errUnknownLauncher):
			status = http.StatusNotFound
		case errors.Is(err, errAppDisabled):
			status = http.StatusForbidden
		case errors.Is(err, errNotConnected):
			status = http.StatusBadRequest
		}
		apiError(w, status, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "ok"})
}
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := s.resolveTV(r.URL.Query().Get("tv_id"))
	if id != "" {
		endSession := s.beginClientSession(id)
		defer endSession()
	}
	jsonResponse(w, 200, s.nextEvent(r.Context(), id, s.eventTimeout))
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
	if path == "." || path == "" {
		path = "index.html"
	}
	if strings.HasPrefix(path, "icons/") {
		rel := strings.TrimPrefix(path, "icons/")
		if filepath.Base(rel) != rel {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.iconsDir(), rel))
		return
	}
	if strings.Contains(path, "..") || filepath.IsAbs(path) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.root, "client", path)
	b, err := os.ReadFile(full)
	if err != nil {
		if !strings.Contains(filepath.Base(path), ".") {
			b, err = os.ReadFile(filepath.Join(s.root, "client", "index.html"))
			path = "index.html"
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	switch filepath.Ext(path) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	}
	switch filepath.Base(path) {
	case "index.html", "app.js", "sw.js", "manifest.json", "reset.html":
		b = []byte(strings.ReplaceAll(string(b), "__VERSION__", s.version))
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}
	_, _ = w.Write(b)
}
