package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type adbRecordedCall struct {
	args []string
	env  []string
}

type scriptedADBRunner struct {
	mu            sync.Mutex
	calls         []adbRecordedCall
	devicesOutput string
	pairGUID      string
	failCommand   string
	failErr       error
}

func (r *scriptedADBRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, adbRecordedCall{args: append([]string(nil), args...), env: append([]string(nil), env...)})
	if len(args) > 0 && args[0] == r.failCommand {
		if r.failErr != nil {
			return ADBResult{}, r.failErr
		}
		return ADBResult{ExitCode: 1, Stderr: "command failed"}, &ADBError{Code: "command_failed", ExitCode: 1, Message: "ADB command failed with exit status 1"}
	}
	if len(args) == 0 {
		return ADBResult{}, nil
	}
	switch args[0] {
	case "version":
		return ADBResult{Stdout: "Android Debug Bridge version 1.0.41\nVersion 35.0.2\n"}, nil
	case "start-server":
		return ADBResult{}, nil
	case "devices":
		return ADBResult{Stdout: "List of devices attached\n" + r.devicesOutput}, nil
	case "pair":
		guid := r.pairGUID
		if guid == "" {
			guid = "adb-test-guid"
		}
		return ADBResult{Stdout: "Successfully paired to " + args[1] + " [guid=" + guid + "]\n"}, nil
	case "connect":
		return ADBResult{Stdout: "connected to " + args[1] + "\n"}, nil
	case "disconnect":
		return ADBResult{Stdout: "disconnected " + args[1] + "\n"}, nil
	default:
		return ADBResult{}, nil
	}
}

func (r *scriptedADBRunner) setDevices(output string) {
	r.mu.Lock()
	r.devicesOutput = output
	r.mu.Unlock()
}

func (r *scriptedADBRunner) snapshotCalls() []adbRecordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]adbRecordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func adbTestServer(t *testing.T, enabled bool) (*Server, *scriptedADBRunner) {
	t.Helper()
	if enabled {
		t.Setenv("DROIDTV_ADB_ENABLED", "true")
	} else {
		t.Setenv("DROIDTV_ADB_ENABLED", "false")
	}
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "test-admin-token")
	s := testServer(t)
	runner := &scriptedADBRunner{pairGUID: "adb-secure-guid"}
	s.adb.runner = runner
	s.adb.initErr = nil
	return s, runner
}

func adbRequestJSON(t *testing.T, s *Server, method, path string, body any, token string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var raw string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response %q: %v", w.Body.String(), err)
		}
	}
	return w, out
}

func addADBTestTV(t *testing.T, s *Server, name, host string) string {
	t.Helper()
	tv, status, err := s.addTV(name, host)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("add TV: status=%d err=%v", status, err)
	}
	return tv["id"].(string)
}

func TestADBEndpointAuthorizationNoStoreAndDisabledState(t *testing.T) {
	s, runner := adbTestServer(t, true)
	id := addADBTestTV(t, s, "Living", "tv.local")
	path := "/api/tvs/" + id + "/adb/status"

	w, out := adbRequestJSON(t, s, http.MethodGet, path, nil, "")
	if w.Code != http.StatusUnauthorized || out["code"] != "unauthorized" {
		t.Fatalf("missing token: %d %#v", w.Code, out)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store header: %#v", w.Header())
	}
	w, out = adbRequestJSON(t, s, http.MethodGet, path, nil, "wrong")
	if w.Code != http.StatusUnauthorized || out["code"] != "unauthorized" {
		t.Fatalf("wrong token: %d %#v", w.Code, out)
	}
	if len(runner.snapshotCalls()) != 0 {
		t.Fatalf("unauthorized requests reached ADB: %#v", runner.snapshotCalls())
	}

	w, out = adbRequestJSON(t, s, http.MethodGet, path, nil, "test-admin-token")
	if w.Code != http.StatusOK {
		t.Fatalf("authorized status: %d %#v", w.Code, out)
	}
	adb := out["adb"].(map[string]any)
	if adb["state"] != "unpaired" || adb["enabled"] != true || adb["available"] != true {
		t.Fatalf("unexpected ADB status: %#v", adb)
	}
	remote := out["remote"].(map[string]any)
	if remote["connected"] != false || remote["pairing_in_progress"] != false {
		t.Fatalf("Remote v2 state was not distinct: %#v", remote)
	}
	if strings.Contains(w.Body.String(), "test-admin-token") {
		t.Fatal("administrator token leaked in response")
	}

	disabled, _ := adbTestServer(t, false)
	disabledID := addADBTestTV(t, disabled, "Bedroom", "bed.local")
	w, out = adbRequestJSON(t, disabled, http.MethodGet, "/api/tvs/"+disabledID+"/adb/status", nil, "test-admin-token")
	if w.Code != http.StatusOK || out["adb"].(map[string]any)["state"] != "disabled" {
		t.Fatalf("disabled state: %d %#v", w.Code, out)
	}
}

func TestADBEnabledWithoutAdministratorTokenIsRejected(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "")
	s := testServer(t)
	var adbErr *ADBError
	if !errors.As(s.adb.initErr, &adbErr) || adbErr.Code != "missing_admin_token" {
		t.Fatalf("enabled ADB without token did not get configuration error: %v", s.adb.initErr)
	}
}

func TestADBPairConnectPersistenceIsolationDisconnectAndForget(t *testing.T) {
	s, runner := adbTestServer(t, true)
	first := addADBTestTV(t, s, "Living", "living.local")
	second := addADBTestTV(t, s, "Bedroom", "bed.local")
	pairEndpoint := "10.0.0.10:37199"
	connectOne := "10.0.0.10:42123"
	connectTwo := "10.0.0.11:5555"
	code := "123456"

	w, out := adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+first+"/adb/pair", map[string]any{
		"endpoint": pairEndpoint,
		"code":     code,
	}, "test-admin-token")
	if w.Code != http.StatusOK || out["paired"] != true || out["pair_guid"] != "adb-secure-guid" {
		t.Fatalf("pair: %d %#v", w.Code, out)
	}
	if s.tvs[first].ADBPairGUID != "adb-secure-guid" || s.tvs[second].ADBPairGUID != "" {
		t.Fatalf("pair association crossed TVs: first=%#v second=%#v", s.tvs[first], s.tvs[second])
	}

	runner.setDevices(connectOne + "\tdevice\n")
	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+first+"/adb/connect", map[string]any{"endpoint": connectOne}, "test-admin-token")
	if w.Code != http.StatusOK {
		t.Fatalf("connect first: %d %#v", w.Code, out)
	}
	if s.tvs[first].ADBSerial != connectOne || s.tvs[first].ADBEndpoint != connectOne {
		t.Fatalf("first association not persisted: %#v", s.tvs[first])
	}
	if out["adb"].(map[string]any)["state"] != "connected" {
		t.Fatalf("connect state = %#v", out)
	}

	runner.setDevices(connectOne + "\tdevice\n" + connectTwo + "\tdevice\n")
	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+second+"/adb/connect", map[string]any{"endpoint": connectTwo}, "test-admin-token")
	if w.Code != http.StatusOK || s.tvs[second].ADBSerial != connectTwo {
		t.Fatalf("connect second: %d %#v tv=%#v", w.Code, out, s.tvs[second])
	}

	persisted, err := os.ReadFile(s.tvsPath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(persisted)
	if strings.Contains(text, code) || strings.Contains(text, "test-admin-token") {
		t.Fatalf("secret persisted in tvs.yaml: %q", text)
	}
	if !strings.Contains(text, "adb_pair_guid:") || !strings.Contains(text, "adb_endpoint:") {
		t.Fatalf("ADB association fields missing: %q", text)
	}

	root := s.root
	reloaded, err := NewServer(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.tvs[first].ADBSerial != connectOne || reloaded.tvs[first].ADBPairGUID != "adb-secure-guid" {
		t.Fatalf("first association did not survive restart: %#v", reloaded.tvs[first])
	}
	if reloaded.tvs[second].ADBSerial != connectTwo {
		t.Fatalf("second association did not survive restart: %#v", reloaded.tvs[second])
	}

	runner.setDevices(connectTwo + "\tdevice\n")
	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+first+"/adb/disconnect", nil, "test-admin-token")
	if w.Code != http.StatusOK || out["adb"].(map[string]any)["state"] != "offline" {
		t.Fatalf("disconnect first: %d %#v", w.Code, out)
	}
	calls := runner.snapshotCalls()
	foundDisconnect := false
	for _, call := range calls {
		if len(call.args) == 2 && call.args[0] == "disconnect" && call.args[1] == connectOne {
			foundDisconnect = true
		}
	}
	if !foundDisconnect {
		t.Fatalf("disconnect did not use stored first-TV serial: %#v", calls)
	}

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+first+"/adb/forget", nil, "test-admin-token")
	if w.Code != http.StatusOK || out["state"] != "unpaired" {
		t.Fatalf("forget first: %d %#v", w.Code, out)
	}
	if s.tvs[first].ADBSerial != "" || s.tvs[first].ADBPairGUID != "" || s.tvs[first].ADBEndpoint != "" {
		t.Fatalf("first association remains: %#v", s.tvs[first])
	}
	if s.tvs[second].ADBSerial != connectTwo {
		t.Fatalf("forgetting first altered second: %#v", s.tvs[second])
	}
	if !strings.Contains(out["warning"].(string), "not revoked") {
		t.Fatalf("missing remote-revocation warning: %#v", out)
	}
}

func TestADBValidationDeviceStatesAndTimeoutMapping(t *testing.T) {
	s, runner := adbTestServer(t, true)
	id := addADBTestTV(t, s, "Living", "tv.local")

	w, out := adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+id+"/adb/pair", map[string]any{
		"endpoint": "not-an-endpoint",
		"code":     "123456",
	}, "test-admin-token")
	if w.Code != http.StatusBadRequest || out["code"] != "invalid_endpoint" {
		t.Fatalf("bad endpoint: %d %#v", w.Code, out)
	}
	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+id+"/adb/pair", map[string]any{
		"endpoint": "10.0.0.1:37000",
		"code":     "12AB56",
	}, "test-admin-token")
	if w.Code != http.StatusBadRequest || out["code"] != "invalid_pairing_code" {
		t.Fatalf("bad code: %d %#v", w.Code, out)
	}

	endpoint := "10.0.0.1:5555"
	runner.setDevices(endpoint + "\tunauthorized\n")
	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+id+"/adb/connect", map[string]any{"endpoint": endpoint}, "test-admin-token")
	if w.Code != http.StatusOK || out["adb"].(map[string]any)["state"] != "unauthorized" {
		t.Fatalf("unauthorized device state: %d %#v", w.Code, out)
	}
	runner.setDevices(endpoint + "\toffline\n")
	w, out = adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/status", nil, "test-admin-token")
	if w.Code != http.StatusOK || out["adb"].(map[string]any)["state"] != "offline" {
		t.Fatalf("offline device state: %d %#v", w.Code, out)
	}

	runner.failCommand = "connect"
	runner.failErr = &ADBError{Code: "timeout", TimedOut: true, Message: "ADB command timed out"}
	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+id+"/adb/connect", map[string]any{"endpoint": "10.0.0.2:5555"}, "test-admin-token")
	if w.Code != http.StatusGatewayTimeout || out["code"] != "timeout" {
		t.Fatalf("timeout mapping: %d %#v", w.Code, out)
	}
}

func mcpADBCall(t *testing.T, s *Server, name string, args map[string]any, token string) map[string]any {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("MCP HTTP status: %d %s", w.Code, w.Body.String())
	}
	response := parseJSONMap(t, w.Body.Bytes())
	return response["result"].(map[string]any)
}

func TestADBRESTMCPAuthorizationAndStatusParity(t *testing.T) {
	s, _ := adbTestServer(t, true)
	id := addADBTestTV(t, s, "Living", "tv.local")

	result := mcpADBCall(t, s, "adb_status", map[string]any{"tv_id": id}, "")
	if result["isError"] != true {
		t.Fatalf("unauthorized MCP call succeeded: %#v", result)
	}
	errContent := result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if errContent["code"] != "unauthorized" {
		t.Fatalf("MCP auth code: %#v", result)
	}

	w, rest := adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/status", nil, "test-admin-token")
	if w.Code != http.StatusOK {
		t.Fatalf("REST status failed: %d %#v", w.Code, rest)
	}
	result = mcpADBCall(t, s, "adb_status", map[string]any{"tv_id": id}, "test-admin-token")
	if result["isError"] != false {
		t.Fatalf("MCP status failed: %#v", result)
	}
	mcp := result["structuredContent"].(map[string]any)
	if mcp["tv_id"] != rest["tv_id"] {
		t.Fatalf("TV mismatch REST=%#v MCP=%#v", rest, mcp)
	}
	if mcp["adb"].(map[string]any)["state"] != rest["adb"].(map[string]any)["state"] {
		t.Fatalf("ADB state mismatch REST=%#v MCP=%#v", rest, mcp)
	}
}

func TestADBOldTVRecordsLoadAndRoundTrip(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "false")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "client"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	old := "tvs:\n- id: \"one\"\n  name: \"One\"\n  host: \"one.local\"\n  app_ids:\n- id: \"two\"\n  name: \"Two\"\n  host: \"two.local\"\n  app_ids:\n"
	if err := os.WriteFile(filepath.Join(root, "data", "tvs.yaml"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.tvOrder) != 2 || s.tvOrder[0] != "one" || s.tvOrder[1] != "two" {
		t.Fatalf("TV order changed: %#v", s.tvOrder)
	}
	if s.tvs["one"].ADBSerial != "" || s.tvs["one"].ADBEndpoint != "" || s.tvs["one"].ADBPairGUID != "" {
		t.Fatalf("old record gained association: %#v", s.tvs["one"])
	}
	serial, endpoint, guid := "one.local:5555", "one.local:5555", "adb-guid"
	if err := s.updateADBAssociation("one", &serial, &endpoint, &guid); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewServer(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.tvOrder[0] != "one" || reloaded.tvOrder[1] != "two" {
		t.Fatalf("round-trip order changed: %#v", reloaded.tvOrder)
	}
	if reloaded.tvs["one"].ADBSerial != serial || reloaded.tvs["one"].ADBPairGUID != guid {
		t.Fatalf("ADB fields failed to round trip: %#v", reloaded.tvs["one"])
	}
	if reloaded.tvs["two"].ADBSerial != "" {
		t.Fatalf("unrelated record changed: %#v", reloaded.tvs["two"])
	}
}
