package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type diagnosticRunner struct {
	mu             sync.Mutex
	calls          [][]string
	deviceState    string
	screenshotMode string
	logMode        string
}

func validDiagnosticPNG() []byte {
	return append(append([]byte(nil), adbPNGSignature...), []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}...)
}

func (r *diagnosticRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) == 0 {
		return ADBResult{}, nil
	}
	switch args[0] {
	case "version":
		return ADBResult{Stdout: "Android Debug Bridge version 1.0.41\nVersion 35.0.2\n"}, nil
	case "start-server":
		return ADBResult{}, nil
	case "devices":
		state := r.deviceState
		if state == "" {
			state = "device"
		}
		return ADBResult{Stdout: "List of devices attached\nliving:5555\t" + state + "\nbedroom:5555\tdevice\n"}, nil
	}
	if len(args) < 3 || args[0] != "-s" {
		return ADBResult{}, nil
	}
	cmd := args[2:]
	if len(cmd) == 3 && cmd[0] == "exec-out" && cmd[1] == "screencap" && cmd[2] == "-p" {
		switch r.screenshotMode {
		case "malformed":
			return ADBResult{Stdout: "not-a-png"}, nil
		case "oversize":
			return ADBResult{Stdout: string(validDiagnosticPNG()), Truncated: true}, nil
		case "timeout":
			return ADBResult{}, &ADBError{Code: "timeout", TimedOut: true, Message: "ADB command timed out"}
		case "canceled":
			return ADBResult{}, &ADBError{Code: "canceled", Message: "ADB command was canceled"}
		case "failed":
			return ADBResult{ExitCode: 1, Stderr: "capture failed"}, &ADBError{Code: "command_failed", ExitCode: 1, Message: "ADB command failed"}
		default:
			return ADBResult{Stdout: string(validDiagnosticPNG())}, nil
		}
	}
	if len(cmd) >= 2 && cmd[0] == "shell" && cmd[1] == "logcat" {
		switch r.logMode {
		case "timeout":
			return ADBResult{}, &ADBError{Code: "timeout", TimedOut: true, Message: "ADB command timed out"}
		case "failed":
			return ADBResult{ExitCode: 1, Stderr: "logcat failed"}, &ADBError{Code: "command_failed", ExitCode: 1, Message: "ADB command failed"}
		default:
			return ADBResult{Stdout: "08-31 token=test-admin-token\nAuthorization: Bearer abcdef123456\npairing code=123456\nnormal line\n"}, nil
		}
	}
	if len(cmd) == 1 && cmd[0] == "reboot" {
		return ADBResult{}, nil
	}
	return ADBResult{}, nil
}

func (r *diagnosticRunner) snapshotCalls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i := range r.calls {
		out[i] = append([]string(nil), r.calls[i]...)
	}
	return out
}

func diagnosticTestServer(t *testing.T) (*Server, *diagnosticRunner, string, string) {
	t.Helper()
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "test-admin-token")
	s := testServer(t)
	runner := &diagnosticRunner{deviceState: "device"}
	s.adb.runner = runner
	s.adb.initErr = nil
	living := addADBTestTV(t, s, "Living Room", "living.local")
	bedroom := addADBTestTV(t, s, "Bedroom", "bedroom.local")
	livingSerial := "living:5555"
	bedroomSerial := "bedroom:5555"
	if err := s.updateADBAssociation(living, &livingSerial, &livingSerial, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.updateADBAssociation(bedroom, &bedroomSerial, &bedroomSerial, nil); err != nil {
		t.Fatal(err)
	}
	return s, runner, living, bedroom
}

func diagnosticRequest(t *testing.T, s *Server, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func TestADBScreenshotRESTBoundsAuthAndTargeting(t *testing.T) {
	s, runner, living, _ := diagnosticTestServer(t)
	path := "/api/tvs/" + living + "/adb/screenshot"

	w := diagnosticRequest(t, s, path, "")
	if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("screenshot auth/no-store: %d %#v", w.Code, w.Header())
	}
	w = diagnosticRequest(t, s, path, "test-admin-token")
	if w.Code != http.StatusOK {
		t.Fatalf("screenshot: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "image/png" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("screenshot headers: %#v", w.Header())
	}
	if !bytes.Equal(w.Body.Bytes(), validDiagnosticPNG()) {
		t.Fatal("screenshot body changed")
	}
	for _, call := range runner.snapshotCalls() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "exec-out screencap -p") && !strings.HasPrefix(joined, "-s living:5555 ") {
			t.Fatalf("screenshot targeted wrong TV: %q", joined)
		}
	}

	for _, tc := range []struct {
		mode string
		code string
		http int
	}{
		{"malformed", "malformed_capture", http.StatusUnprocessableEntity},
		{"oversize", "capture_too_large", http.StatusRequestEntityTooLarge},
		{"timeout", "timeout", http.StatusGatewayTimeout},
		{"canceled", "canceled", http.StatusRequestTimeout},
		{"failed", "command_failed", http.StatusBadGateway},
	} {
		runner.screenshotMode = tc.mode
		w = diagnosticRequest(t, s, path, "test-admin-token")
		if w.Code != tc.http || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%s screenshot failure: %d %s", tc.mode, w.Code, w.Body.String())
		}
	}
	runner.screenshotMode = ""
	runner.deviceState = "offline"
	w = diagnosticRequest(t, s, path, "test-admin-token")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "offline") {
		t.Fatalf("offline screenshot: %d %s", w.Code, w.Body.String())
	}
}

func TestADBLogsRESTAndMCPAreFiniteRedactedAndBounded(t *testing.T) {
	s, runner, living, _ := diagnosticTestServer(t)
	w := diagnosticRequest(t, s, "/api/tvs/"+living+"/adb/logs", "test-admin-token")
	if w.Code != http.StatusOK {
		t.Fatalf("logs: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"test-admin-token", "abcdef123456", "123456"} {
		if strings.Contains(body, secret) {
			t.Fatalf("log secret leaked: %q", secret)
		}
	}
	if !strings.Contains(body, "<redacted") || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("log output/headers: %q %#v", body, w.Header())
	}
	foundBoundedCommand := false
	for _, call := range runner.snapshotCalls() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "shell logcat -d -t 2000 -v threadtime") {
			foundBoundedCommand = true
		}
	}
	if !foundBoundedCommand {
		t.Fatal("bounded finite logcat command was not used")
	}

	result := mcpADBCall(t, s, "adb_logs", map[string]any{"tv_id": living}, "test-admin-token")
	if result["isError"] != false {
		t.Fatalf("MCP logs failed: %#v", result)
	}
	content := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(content, "sensitive") || strings.Contains(content, "test-admin-token") || strings.Contains(content, "123456") {
		t.Fatalf("MCP log content not safely redacted/warned: %q", content)
	}
	meta := result["structuredContent"].(map[string]any)
	if meta["size_bytes"].(float64) > float64(maxADBLogBytes) {
		t.Fatalf("MCP log response exceeded bound: %#v", meta)
	}
}

func TestADBScreenshotMCPUsesBoundedImageRepresentation(t *testing.T) {
	s, _, living, _ := diagnosticTestServer(t)
	result := mcpADBCall(t, s, "adb_screenshot", map[string]any{"tv_id": living}, "")
	if result["isError"] != true {
		t.Fatalf("unauthorized MCP screenshot succeeded: %#v", result)
	}
	result = mcpADBCall(t, s, "adb_screenshot", map[string]any{"tv_id": living}, "test-admin-token")
	if result["isError"] != false {
		t.Fatalf("MCP screenshot failed: %#v", result)
	}
	image := result["content"].([]any)[0].(map[string]any)
	if image["type"] != "image" || image["mimeType"] != "image/png" {
		t.Fatalf("MCP screenshot representation: %#v", image)
	}
	decoded, err := base64.StdEncoding.DecodeString(image["data"].(string))
	if err != nil || !bytes.Equal(decoded, validDiagnosticPNG()) || int64(len(decoded)) > maxADBScreenshotBytes {
		t.Fatalf("MCP screenshot data invalid: len=%d err=%v", len(decoded), err)
	}
}

func TestADBRebootRequiresExactConfirmationAndReportsCommandSentOnly(t *testing.T) {
	s, runner, living, bedroom := diagnosticTestServer(t)

	w, out := adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/reboot", map[string]any{
		"confirmation": map[string]any{"tv_id": bedroom, "tv_name": "Living Room", "state": "connected"},
	}, "test-admin-token")
	if w.Code != http.StatusConflict || out["code"] != "stale_reboot_confirmation" {
		t.Fatalf("stale reboot confirmation: %d %#v", w.Code, out)
	}
	for _, call := range runner.snapshotCalls() {
		if len(call) >= 3 && call[0] == "-s" && call[2] == "reboot" {
			t.Fatalf("stale confirmation reached reboot: %#v", call)
		}
	}

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/reboot", map[string]any{
		"confirmation": map[string]any{"tv_id": living, "tv_name": "Living Room", "state": "connected"},
	}, "test-admin-token")
	if w.Code != http.StatusAccepted || out["status"] != "accepted" || out["command_sent"] != true || out["adb_state"] != "offline" {
		t.Fatalf("reboot response: %d %#v", w.Code, out)
	}
	if strings.Contains(strings.ToLower(out["message"].(string)), "completed") {
		t.Fatalf("reboot falsely claimed completion: %#v", out)
	}
	found := false
	for _, call := range runner.snapshotCalls() {
		if strings.Join(call, " ") == "-s living:5555 reboot" {
			found = true
		}
	}
	if !found {
		t.Fatal("explicit selected-TV reboot command was not sent")
	}

	w, status := adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+living+"/adb/status", nil, "test-admin-token")
	if w.Code != http.StatusOK || status["adb"].(map[string]any)["state"] != "offline" {
		t.Fatalf("post-reboot ADB state: %d %#v", w.Code, status)
	}
}
