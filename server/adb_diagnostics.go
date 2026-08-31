package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxADBScreenshotBytes = int64(8 * 1024 * 1024)
	maxADBLogBytes         = int64(512 * 1024)
	maxADBLogLines         = 2000
	adbDiagnosticTimeout   = 10 * time.Second
)

var (
	adbPNGSignature     = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	adbBearerSecretRE   = regexp.MustCompile(`(?i)\bbearer[ \t]+[A-Za-z0-9._~+/=-]{6,}`)
	adbPairingSecretRE  = regexp.MustCompile(`(?i)(pair(?:ing)?(?:[ _-]?code)?[=: ]+)[0-9]{6}`)
	adbDiagnosticNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

type ADBScreenshotCapture struct {
	Data   []byte
	Size   int64
	SHA256 string
}

type ADBLogCapture struct {
	Text      string
	Size      int64
	SHA256    string
	Lines     int
	Truncated bool
	Warning   string
}

type ADBRebootConfirmation struct {
	TVID   string `json:"tv_id"`
	TVName string `json:"tv_name"`
	State  string `json:"state"`
}

type ADBRebootRequest struct {
	Confirmation ADBRebootConfirmation `json:"confirmation"`
}

func hashDiagnostic(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func safeDiagnosticFilename(tvID, suffix string) string {
	base := adbDiagnosticNameRE.ReplaceAllString(strings.TrimSpace(tvID), "-")
	base = strings.Trim(base, ".-_")
	if base == "" {
		base = "tv"
	}
	if len(base) > 64 {
		base = base[:64]
	}
	return "droidtv-remote-" + base + "-" + suffix
}

func auditADBDiagnostic(action, tvID, result string, size int64, sha string) {
	safeTVID := adbDiagnosticNameRE.ReplaceAllString(tvID, "-")
	fields := fmt.Sprintf("adb_audit action=%s tv_id=%s at=%s result=%s", action, safeTVID, time.Now().UTC().Format(time.RFC3339), result)
	if size >= 0 {
		fields += fmt.Sprintf(" size_bytes=%d", size)
	}
	if sha != "" {
		fields += " sha256=" + sha
	}
	log.Print(fields)
}

func validateADBScreenshot(data []byte) error {
	if int64(len(data)) > maxADBScreenshotBytes {
		return &ADBError{Code: "capture_too_large", Message: "Screenshot exceeds the bounded capture size"}
	}
	if len(data) < 20 || !bytes.Equal(data[:len(adbPNGSignature)], adbPNGSignature) {
		return &ADBError{Code: "malformed_capture", Message: "ADB screenshot did not return a valid PNG"}
	}
	if len(data) < 12 || !bytes.Equal(data[len(data)-8:len(data)-4], []byte("IEND")) {
		return &ADBError{Code: "malformed_capture", Message: "ADB screenshot PNG is incomplete"}
	}
	return nil
}

func (s *Server) adbScreenshot(ctx context.Context, tvID string) (ADBScreenshotCapture, error) {
	state := s.state(tvID)
	state.adbAdminMu.Lock()
	defer state.adbAdminMu.Unlock()

	serial, err := s.requireADBConnected(ctx, tvID)
	if err != nil {
		auditADBDiagnostic("screenshot", tvID, adbErrorCode(err), -1, "")
		return ADBScreenshotCapture{}, err
	}
	result, runErr := s.adb.runDeviceWithBounds(ctx, serial, adbDiagnosticTimeout, maxADBScreenshotBytes+1, "exec-out", "screencap", "-p")
	if mapped := mapADBOperationError(result, runErr); mapped != nil {
		auditADBDiagnostic("screenshot", tvID, adbErrorCode(mapped), -1, "")
		return ADBScreenshotCapture{}, mapped
	}
	data := []byte(result.Stdout)
	if result.Truncated || int64(len(data)) > maxADBScreenshotBytes {
		err := &ADBError{Code: "capture_too_large", Message: "Screenshot exceeds the bounded capture size"}
		auditADBDiagnostic("screenshot", tvID, err.Code, int64(len(data)), "")
		return ADBScreenshotCapture{}, err
	}
	if err := validateADBScreenshot(data); err != nil {
		auditADBDiagnostic("screenshot", tvID, adbErrorCode(err), int64(len(data)), "")
		return ADBScreenshotCapture{}, err
	}
	capture := ADBScreenshotCapture{Data: data, Size: int64(len(data)), SHA256: hashDiagnostic(data)}
	auditADBDiagnostic("screenshot", tvID, "success", capture.Size, capture.SHA256)
	return capture, nil
}

func redactADBLogs(text, adminToken string) string {
	if adminToken != "" {
		text = strings.ReplaceAll(text, adminToken, "<redacted-admin-token>")
	}
	text = adbBearerSecretRE.ReplaceAllString(text, "Bearer <redacted>")
	text = adbPairingSecretRE.ReplaceAllString(text, "$1<redacted>")
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	return text
}

func boundADBLogText(text string) (string, bool) {
	truncated := false
	lines := strings.Split(text, "\n")
	if len(lines) > maxADBLogLines {
		lines = lines[len(lines)-maxADBLogLines:]
		text = strings.Join(lines, "\n")
		truncated = true
	}
	if int64(len(text)) > maxADBLogBytes {
		text = text[len(text)-int(maxADBLogBytes):]
		truncated = true
		if i := strings.IndexByte(text, '\n'); i >= 0 && i+1 < len(text) {
			text = text[i+1:]
		}
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	return text, truncated
}

func countADBLogLines(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func (s *Server) adbLogs(ctx context.Context, tvID string) (ADBLogCapture, error) {
	state := s.state(tvID)
	state.adbAdminMu.Lock()
	defer state.adbAdminMu.Unlock()

	serial, err := s.requireADBConnected(ctx, tvID)
	if err != nil {
		auditADBDiagnostic("logs", tvID, adbErrorCode(err), -1, "")
		return ADBLogCapture{}, err
	}
	result, runErr := s.adb.runDeviceWithBounds(ctx, serial, adbDiagnosticTimeout, maxADBLogBytes+1,
		"shell", "logcat", "-d", "-t", strconv.Itoa(maxADBLogLines), "-v", "threadtime")
	if mapped := mapADBOperationError(result, runErr); mapped != nil {
		auditADBDiagnostic("logs", tvID, adbErrorCode(mapped), -1, "")
		return ADBLogCapture{}, mapped
	}
	text := redactADBLogs(result.Stdout, s.adbAdminToken)
	text, bounded := boundADBLogText(text)
	truncated := result.Truncated || bounded
	warning := "Device logs can contain sensitive application, account, network, and content information. Store and share this download carefully."
	if truncated {
		warning += " Output reached a configured capture limit and was truncated."
	}
	data := []byte(text)
	capture := ADBLogCapture{
		Text:      text,
		Size:      int64(len(data)),
		SHA256:    hashDiagnostic(data),
		Lines:     countADBLogLines(text),
		Truncated: truncated,
		Warning:   warning,
	}
	auditADBDiagnostic("logs", tvID, "success", capture.Size, capture.SHA256)
	return capture, nil
}

func (s *Server) setADBRebootOffline(tvID string) {
	state := s.state(tvID)
	state.mu.Lock()
	state.adbState = "offline"
	state.adbOfflineChecks = 1
	state.mu.Unlock()
}

func (s *Server) adbReboot(ctx context.Context, tvID string, request ADBRebootRequest) (map[string]any, error) {
	state := s.state(tvID)
	state.adbAdminMu.Lock()
	defer state.adbAdminMu.Unlock()

	tv, err := s.copyTV(tvID)
	if err != nil {
		auditADBDiagnostic("reboot", tvID, adbErrorCode(err), -1, "")
		return nil, err
	}
	serial, err := s.requireADBConnected(ctx, tvID)
	if err != nil {
		auditADBDiagnostic("reboot", tvID, adbErrorCode(err), -1, "")
		return nil, err
	}
	if request.Confirmation.TVID != tvID ||
		request.Confirmation.TVName != tv.Name ||
		request.Confirmation.State != "connected" {
		err := &ADBError{Code: "stale_reboot_confirmation", Message: "Reboot confirmation does not match the selected TV and current connected state"}
		auditADBDiagnostic("reboot", tvID, err.Code, -1, "")
		return nil, err
	}
	result, runErr := s.adb.runDeviceWithBounds(ctx, serial, adbDiagnosticTimeout, 64*1024, "reboot")
	if mapped := mapADBOperationError(result, runErr); mapped != nil {
		auditADBDiagnostic("reboot", tvID, adbErrorCode(mapped), -1, "")
		return nil, mapped
	}
	s.setADBRebootOffline(tvID)
	auditADBDiagnostic("reboot", tvID, "command_sent", 0, "")
	return map[string]any{
		"tv_id":        tvID,
		"tv_name":      tv.Name,
		"status":       "accepted",
		"command_sent": true,
		"adb_state":    "offline",
		"message":      "Reboot command was sent. The TV will disconnect while restarting; reconnect is detected normally after it returns.",
	}, nil
}

func (s *Server) handleADBScreenshot(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	tvID := r.PathValue("tv_id")
	capture, err := s.adbScreenshot(r.Context(), tvID)
	if err != nil {
		writeADBError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeDiagnosticFilename(tvID, "screenshot.png")))
	w.Header().Set("Content-Length", strconv.FormatInt(capture.Size, 10))
	w.Header().Set("X-Content-SHA256", capture.SHA256)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(capture.Data); err != nil {
		auditADBDiagnostic("screenshot_transfer", tvID, "canceled", capture.Size, capture.SHA256)
	}
}

func (s *Server) handleADBLogs(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	tvID := r.PathValue("tv_id")
	capture, err := s.adbLogs(r.Context(), tvID)
	if err != nil {
		writeADBError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeDiagnosticFilename(tvID, "logs.txt")))
	w.Header().Set("Content-Length", strconv.FormatInt(capture.Size, 10))
	w.Header().Set("X-Content-SHA256", capture.SHA256)
	w.Header().Set("X-ADB-Log-Lines", strconv.Itoa(capture.Lines))
	if capture.Truncated {
		w.Header().Set("X-ADB-Truncated", "true")
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(capture.Text)); err != nil {
		auditADBDiagnostic("logs_transfer", tvID, "canceled", capture.Size, capture.SHA256)
	}
}

func (s *Server) handleADBReboot(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	var in ADBRebootRequest
	if err := decodeJSON(r, &in); err != nil {
		writeADBError(w, &ADBError{Code: "invalid_reboot_confirmation", Message: "Invalid reboot confirmation"})
		return
	}
	result, err := s.adbReboot(r.Context(), r.PathValue("tv_id"), in)
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusAccepted, result)
}
