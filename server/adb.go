package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultADBTimeout        = 15 * time.Second
	defaultADBInstallTimeout = 5 * time.Minute
	defaultADBMaxOutput      = int64(256 * 1024)
	defaultADBAPKMaxBytes    = int64(128 * 1024 * 1024)
	adbServerSocket          = "tcp:127.0.0.1:5038"
)

type ADBConfig struct {
	Enabled        bool
	Path           string
	Home           string
	ServerSocket   string
	Timeout        time.Duration
	InstallTimeout time.Duration
	MaxOutput      int64
	APKMaxBytes    int64
}

type ADBResult struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

type ADBError struct {
	Code      string
	ExitCode  int
	TimedOut  bool
	Truncated bool
	Message   string
}

func (e *ADBError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type ADBRunner interface {
	Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error)
}

type execADBRunner struct{}

type limitedBuffer struct {
	buf       bytes.Buffer
	remaining int64
	truncated bool
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	if limit < 0 {
		limit = 0
	}
	return &limitedBuffer{remaining: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	available := b.remaining
	if available > 0 {
		take := int64(len(p))
		if take > available {
			take = available
		}
		_, _ = b.buf.Write(p[:take])
		b.remaining -= take
	}
	if int64(len(p)) > available {
		b.truncated = true
	}
	return n, nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

func (execADBRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
	stdout := newLimitedBuffer(maxOutput)
	stderr := newLimitedBuffer(maxOutput)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := ADBResult{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  0,
		Truncated: stdout.truncated || stderr.truncated,
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, &ADBError{Code: "timeout", TimedOut: true, Truncated: result.Truncated, Message: "ADB command timed out"}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return result, &ADBError{Code: "canceled", Truncated: result.Truncated, Message: "ADB command was canceled"}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, &ADBError{
			Code:      "command_failed",
			ExitCode:  result.ExitCode,
			Truncated: result.Truncated,
			Message:   fmt.Sprintf("ADB command failed with exit status %d", result.ExitCode),
		}
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return result, &ADBError{Code: "unavailable", Message: "ADB executable is unavailable"}
	}
	return result, &ADBError{Code: "unavailable", Message: "ADB executable could not be started"}
}

type ADBManager struct {
	cfg     ADBConfig
	runner  ADBRunner
	initErr error

	mu      sync.Mutex
	started bool
}

func loadADBConfig(root string) ADBConfig {
	enabled := false
	if v := strings.TrimSpace(os.Getenv("DROIDTV_ADB_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			enabled = b
		}
	}
	path := strings.TrimSpace(os.Getenv("DROIDTV_ADB_PATH"))
	if path == "" {
		path = "adb"
	}
	apkMaxBytes := int64(defaultADBAPKMaxBytes)
	if raw := strings.TrimSpace(os.Getenv("DROIDTV_ADB_APK_MAX_BYTES")); raw != "" {
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
			apkMaxBytes = value
		}
	}
	installTimeout := defaultADBInstallTimeout
	if raw := strings.TrimSpace(os.Getenv("DROIDTV_ADB_INSTALL_TIMEOUT")); raw != "" {
		if value, err := time.ParseDuration(raw); err == nil && value > 0 {
			installTimeout = value
		}
	}
	return ADBConfig{
		Enabled:        enabled,
		Path:           path,
		Home:           filepath.Join(root, "data", "adb"),
		ServerSocket:   adbServerSocket,
		Timeout:        defaultADBTimeout,
		InstallTimeout: installTimeout,
		MaxOutput:      defaultADBMaxOutput,
		APKMaxBytes:    apkMaxBytes,
	}
}

func NewADBManager(root string, runner ADBRunner) *ADBManager {
	if runner == nil {
		runner = execADBRunner{}
	}
	m := &ADBManager{cfg: loadADBConfig(root), runner: runner}
	if m.cfg.Enabled {
		m.initErr = m.ensureHome()
	}
	return m
}

func (m *ADBManager) Enabled() bool { return m != nil && m.cfg.Enabled }
func (m *ADBManager) Home() string {
	if m == nil {
		return ""
	}
	return m.cfg.Home
}

func (m *ADBManager) ensureHome() error {
	if err := os.MkdirAll(m.cfg.Home, 0700); err != nil {
		return fmt.Errorf("prepare ADB home: %w", err)
	}
	if err := os.Chmod(m.cfg.Home, 0700); err != nil {
		return fmt.Errorf("secure ADB home: %w", err)
	}
	androidDir := filepath.Join(m.cfg.Home, ".android")
	if err := os.MkdirAll(androidDir, 0700); err != nil {
		return fmt.Errorf("prepare Android home: %w", err)
	}
	if err := os.Chmod(androidDir, 0700); err != nil {
		return fmt.Errorf("secure Android home: %w", err)
	}
	for _, name := range []string{"adbkey", "adbkey.pub", "adb_known_hosts.pb"} {
		path := filepath.Join(androidDir, name)
		if _, err := os.Stat(path); err == nil {
			_ = os.Chmod(path, 0600)
		}
	}
	return nil
}

func (m *ADBManager) env() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+4)
	for _, item := range base {
		if strings.HasPrefix(item, "HOME=") ||
			strings.HasPrefix(item, "ANDROID_SDK_HOME=") ||
			strings.HasPrefix(item, "ADB_SERVER_SOCKET=") ||
			strings.HasPrefix(item, "DROIDTV_ADB_ADMIN_TOKEN=") {
			continue
		}
		out = append(out, item)
	}
	out = append(out,
		"HOME="+m.cfg.Home,
		"ANDROID_SDK_HOME="+m.cfg.Home,
		"ADB_SERVER_SOCKET="+m.cfg.ServerSocket,
	)
	return out
}

func (m *ADBManager) runWithTimeout(ctx context.Context, timeout time.Duration, args ...string) (ADBResult, error) {
	if m == nil || !m.cfg.Enabled {
		return ADBResult{}, &ADBError{Code: "disabled", Message: "ADB integration is disabled"}
	}
	if m.initErr != nil {
		var adbErr *ADBError
		if errors.As(m.initErr, &adbErr) {
			return ADBResult{}, adbErr
		}
		return ADBResult{}, &ADBError{Code: "unavailable", Message: "ADB runtime storage is unavailable"}
	}
	if timeout <= 0 {
		timeout = m.cfg.Timeout
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.runner.Run(deadline, m.cfg.Path, append([]string(nil), args...), m.env(), m.cfg.MaxOutput)
}

func (m *ADBManager) run(ctx context.Context, args ...string) (ADBResult, error) {
	return m.runWithTimeout(ctx, m.cfg.Timeout, args...)
}

func (m *ADBManager) runHost(ctx context.Context, args ...string) (ADBResult, error) {
	return m.run(ctx, args...)
}

func (m *ADBManager) runDevice(ctx context.Context, serial string, args ...string) (ADBResult, error) {
	serial = strings.TrimSpace(serial)
	if serial == "" || strings.HasPrefix(serial, "-") || strings.ContainsAny(serial, "\r\n\t ") {
		return ADBResult{}, &ADBError{Code: "invalid_target", Message: "An explicit ADB device serial is required"}
	}
	full := make([]string, 0, len(args)+2)
	full = append(full, "-s", serial)
	full = append(full, args...)
	return m.run(ctx, full...)
}

func (m *ADBManager) Version(ctx context.Context) (string, error) {
	result, err := m.runHost(ctx, "version")
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(result.Stdout)
	if line == "" {
		line = strings.TrimSpace(result.Stderr)
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if line == "" {
		return "", &ADBError{Code: "unavailable", Message: "ADB version output was empty"}
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return line, nil
}

type ADBAvailability struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (m *ADBManager) Availability(ctx context.Context) ADBAvailability {
	v := ADBAvailability{Enabled: m != nil && m.cfg.Enabled}
	if !v.Enabled {
		return v
	}
	version, err := m.Version(ctx)
	if err != nil {
		var adbErr *ADBError
		if errors.As(err, &adbErr) {
			v.Error = adbErr.Code
		} else {
			v.Error = "unavailable"
		}
		return v
	}
	v.Available = true
	v.Version = version
	return v
}

func (m *ADBManager) ensureServer(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}
	if _, err := m.runHost(ctx, "start-server"); err != nil {
		return err
	}
	m.started = true
	return nil
}

func (m *ADBManager) Close(ctx context.Context) error {
	if m == nil || !m.cfg.Enabled {
		return nil
	}
	m.mu.Lock()
	owned := m.started
	m.started = false
	m.mu.Unlock()
	if !owned {
		return nil
	}
	_, err := m.runHost(ctx, "kill-server")
	return err
}

var _ io.Writer = (*limitedBuffer)(nil)
