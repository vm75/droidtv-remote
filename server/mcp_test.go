package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPHTTPProtocol(t *testing.T) {
	s := testServer(t)

	// Test OPTIONS (CORS preflight)
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", w.Code)
	}
	if origin := w.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("CORS origin = %q, want *", origin)
	}
	if methods := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "POST") {
		t.Fatalf("CORS methods = %q, want POST", methods)
	}

	// Test DELETE (Session disconnect)
	req = httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", w.Code)
	}

	// Test GET (Method not allowed)
	w, errOut := requestJSON(t, s, http.MethodGet, "/mcp", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", w.Code)
	}
	if errOut["error"] != "MCP uses POST requests" {
		t.Fatalf("GET error = %#v, want 'MCP uses POST requests'", errOut["error"])
	}

	// Test malformed JSON body
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{invalid json}"))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Malformed JSON status = %d, want 200", w.Code)
	}
	out := parseJSONMap(t, w.Body.Bytes())
	rpcErr, _ := out["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32700 {
		t.Fatalf("Malformed JSON rpc error = %#v, want code -32700", rpcErr)
	}

	// Test notification (no ID)
	w, out = requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("Notification status = %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("Notification body = %q, want empty", w.Body.String())
	}

	// Test ping method
	w, out = requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "ping",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ping status = %d, want 200", w.Code)
	}
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("ping result = %#v", out["result"])
	}
	if len(res) != 0 {
		t.Fatalf("ping result length = %d, want empty object", len(res))
	}

	// Test unknown JSON-RPC method
	w, out = requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      11,
		"method":  "unknown/method",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("unknown method HTTP status = %d, want 200", w.Code)
	}
	rpcErr, _ = out["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32601 {
		t.Fatalf("unknown method error = %#v, want code -32601", rpcErr)
	}
}

func TestMCPToolCallStatusAndTVs(t *testing.T) {
	s := testServer(t)

	// Status when no TVs exist
	res := callMCPTool(t, s, "status", map[string]any{})
	if res["isError"].(bool) {
		t.Fatalf("status failed: %#v", res)
	}
	structContent := res["structuredContent"].(map[string]any)
	if structContent["tv_id"] != nil || structContent["tv_name"] != "No TV selected" {
		t.Fatalf("status no tv = %#v", structContent)
	}
	if structContent["version"] != "1.0.0" {
		t.Fatalf("status version = %v, want 1.0.0", structContent["version"])
	}

	// add_tv with missing fields -> tool error
	res = callMCPTool(t, s, "add_tv", map[string]any{"name": "Living Room"})
	if !res["isError"].(bool) {
		t.Fatalf("add_tv missing host should fail: %#v", res)
	}

	// add_tv valid
	res = callMCPTool(t, s, "add_tv", map[string]any{"name": "Living Room", "host": "192.168.1.50"})
	if res["isError"].(bool) {
		t.Fatalf("add_tv failed: %#v", res)
	}
	tv := res["structuredContent"].(map[string]any)["tv"].(map[string]any)
	tvID := tv["id"].(string)
	if tvID == "" || tv["name"] != "Living Room" || tv["host"] != "192.168.1.50" {
		t.Fatalf("bad tv added: %#v", tv)
	}

	// list_tvs
	res = callMCPTool(t, s, "list_tvs", map[string]any{})
	if res["isError"].(bool) {
		t.Fatalf("list_tvs failed: %#v", res)
	}
	tvs := res["structuredContent"].(map[string]any)["tvs"].([]any)
	if len(tvs) != 1 || tvs[0].(map[string]any)["id"] != tvID {
		t.Fatalf("list_tvs output = %#v", tvs)
	}

	// status when single TV exists (auto-resolved when tv_id omitted)
	res = callMCPTool(t, s, "status", map[string]any{})
	if res["isError"].(bool) {
		t.Fatalf("status single TV failed: %#v", res)
	}
	structContent = res["structuredContent"].(map[string]any)
	if structContent["tv_id"] != tvID || structContent["tv_name"] != "Living Room" {
		t.Fatalf("status single TV auto-resolve failed: %#v", structContent)
	}

	// status with explicit tv_id
	res = callMCPTool(t, s, "status", map[string]any{"tv_id": tvID})
	if res["isError"].(bool) {
		t.Fatalf("status explicit tv_id failed: %#v", res)
	}
	if res["structuredContent"].(map[string]any)["tv_id"] != tvID {
		t.Fatalf("status explicit tv_id mismatch: %#v", res)
	}

	// add a second TV for connection and forgetting
	tv2Res := callMCPTool(t, s, "add_tv", map[string]any{"name": "Bed Room", "host": "192.168.1.51"})
	tv2ID := tv2Res["structuredContent"].(map[string]any)["tv"].(map[string]any)["id"].(string)

	// connect_tv unknown TV
	res = callMCPTool(t, s, "connect_tv", map[string]any{"tv_id": "nonexistent"})
	if !res["isError"].(bool) {
		t.Fatalf("connect_tv unknown TV should fail: %#v", res)
	}

	// connect_tv valid TV
	res = callMCPTool(t, s, "connect_tv", map[string]any{"tv_id": tv2ID})
	if res["isError"].(bool) {
		t.Fatalf("connect_tv failed: %#v", res)
	}
	if res["structuredContent"].(map[string]any)["tv_id"] != tv2ID {
		t.Fatalf("connect_tv result = %#v", res)
	}
	s.disconnect(tv2ID)

	// submit_pairing_code when not pairing -> tool error
	res = callMCPTool(t, s, "submit_pairing_code", map[string]any{"tv_id": tvID, "code": "123456"})
	if !res["isError"].(bool) {
		t.Fatalf("submit_pairing_code when not pairing should fail: %#v", res)
	}

	// submit_pairing_code when pairing
	st := s.state(tvID)
	st.mu.Lock()
	st.pairing = true
	pairChan := make(chan string, 1)
	st.pairCode = pairChan
	st.mu.Unlock()

	res = callMCPTool(t, s, "submit_pairing_code", map[string]any{"tv_id": tvID, "code": "654321"})
	if res["isError"].(bool) {
		t.Fatalf("submit_pairing_code when pairing failed: %#v", res)
	}
	if res["structuredContent"].(map[string]any)["status"] != "submitted" {
		t.Fatalf("submit_pairing_code result = %#v", res)
	}
	if code := <-pairChan; code != "654321" {
		t.Fatalf("received pair code = %q, want '654321'", code)
	}

	// forget_tv unknown TV
	res = callMCPTool(t, s, "forget_tv", map[string]any{"tv_id": "nonexistent"})
	if !res["isError"].(bool) {
		t.Fatalf("forget_tv unknown TV should fail: %#v", res)
	}

	// forget_tv valid TV
	res = callMCPTool(t, s, "forget_tv", map[string]any{"tv_id": tv2ID})
	if res["isError"].(bool) {
		t.Fatalf("forget_tv failed: %#v", res)
	}
	if res["structuredContent"].(map[string]any)["status"] != "forgotten" {
		t.Fatalf("forget_tv result = %#v", res)
	}
}

func TestMCPToolCallApps(t *testing.T) {
	s := testServer(t)

	// list_apps initially empty
	res := callMCPTool(t, s, "list_apps", map[string]any{})
	if res["isError"].(bool) {
		t.Fatalf("list_apps failed: %#v", res)
	}

	// add_app invalid (missing name or package_id)
	res = callMCPTool(t, s, "add_app", map[string]any{"name": "YouTube"})
	if !res["isError"].(bool) {
		t.Fatalf("add_app missing package_id should fail: %#v", res)
	}

	// add_app invalid base64
	res = callMCPTool(t, s, "add_app", map[string]any{
		"name":        "YouTube",
		"package_id":  "com.google.android.youtube.tv",
		"icon_base64": "!!!not_base64!!!",
	})
	if !res["isError"].(bool) {
		t.Fatalf("add_app invalid base64 should fail: %#v", res)
	}

	// add_app valid with base64 PNG icon
	pngData := "\x89PNG\r\n\x1a\n" + "test-png-data"
	b64Png := base64.StdEncoding.EncodeToString([]byte(pngData))
	res = callMCPTool(t, s, "add_app", map[string]any{
		"name":              "YouTube",
		"package_id":        "com.google.android.youtube.tv",
		"icon_class":        "mdi-youtube",
		"icon_base64":       b64Png,
		"icon_content_type": "image/png",
	})
	if res["isError"].(bool) {
		t.Fatalf("add_app valid failed: %#v", res)
	}
	app := res["structuredContent"].(map[string]any)["app"].(map[string]any)
	appID := app["id"].(string)
	if app["name"] != "YouTube" || app["package_id"] != "com.google.android.youtube.tv" {
		t.Fatalf("add_app app mismatch: %#v", app)
	}
	if app["has_uploaded_icon"] != true {
		t.Fatalf("add_app base64 icon not saved: %#v", app)
	}

	// add a second app
	res = callMCPTool(t, s, "add_app", map[string]any{
		"name":       "Netflix",
		"package_id": "com.netflix.ninja",
		"icon_class": "mdi-netflix",
	})
	if res["isError"].(bool) {
		t.Fatalf("add_app second app failed: %#v", res)
	}
	app2ID := res["structuredContent"].(map[string]any)["app"].(map[string]any)["id"].(string)

	// update_app
	res = callMCPTool(t, s, "update_app", map[string]any{
		"app_id":      appID,
		"name":        "YouTube TV",
		"remove_icon": true,
	})
	if res["isError"].(bool) {
		t.Fatalf("update_app failed: %#v", res)
	}
	updatedApp := res["structuredContent"].(map[string]any)["app"].(map[string]any)
	if updatedApp["name"] != "YouTube TV" || updatedApp["has_uploaded_icon"] != false {
		t.Fatalf("update_app result mismatch: %#v", updatedApp)
	}

	// update_app unknown app
	res = callMCPTool(t, s, "update_app", map[string]any{"app_id": "nonexistent", "name": "Fake"})
	if !res["isError"].(bool) {
		t.Fatalf("update_app unknown app should fail: %#v", res)
	}

	// reorder_apps
	res = callMCPTool(t, s, "reorder_apps", map[string]any{"app_ids": []string{app2ID, appID}})
	if res["isError"].(bool) {
		t.Fatalf("reorder_apps failed: %#v", res)
	}
	apps := res["structuredContent"].(map[string]any)["apps"].([]any)
	if len(apps) != 2 || apps[0].(map[string]any)["id"] != app2ID || apps[1].(map[string]any)["id"] != appID {
		t.Fatalf("reorder_apps order mismatch: %#v", apps)
	}

	// set_tv_apps for a TV
	tvRes := callMCPTool(t, s, "add_tv", map[string]any{"name": "Bedroom", "host": "192.168.1.60"})
	tvID := tvRes["structuredContent"].(map[string]any)["tv"].(map[string]any)["id"].(string)

	res = callMCPTool(t, s, "set_tv_apps", map[string]any{"tv_id": tvID, "app_ids": []string{appID}})
	if res["isError"].(bool) {
		t.Fatalf("set_tv_apps failed: %#v", res)
	}
	tvApps := res["structuredContent"].(map[string]any)["tv"].(map[string]any)["app_ids"].([]any)
	if len(tvApps) != 1 || tvApps[0] != appID {
		t.Fatalf("set_tv_apps result mismatch: %#v", tvApps)
	}

	// set_tv_apps unknown app ID -> tool error
	res = callMCPTool(t, s, "set_tv_apps", map[string]any{"tv_id": tvID, "app_ids": []string{"invalid_app"}})
	if !res["isError"].(bool) {
		t.Fatalf("set_tv_apps invalid app ID should fail: %#v", res)
	}

	// delete_app unknown app
	res = callMCPTool(t, s, "delete_app", map[string]any{"app_id": "nonexistent"})
	if !res["isError"].(bool) {
		t.Fatalf("delete_app unknown app should fail: %#v", res)
	}

	// delete_app valid app
	res = callMCPTool(t, s, "delete_app", map[string]any{"app_id": appID})
	if res["isError"].(bool) {
		t.Fatalf("delete_app failed: %#v", res)
	}
	if res["structuredContent"].(map[string]any)["status"] != "deleted" {
		t.Fatalf("delete_app status mismatch: %#v", res)
	}
}

func TestMCPToolCallRemoteControl(t *testing.T) {
	s := testServer(t)
	callMCPTool(t, s, "add_app", map[string]any{"name": "Netflix", "package_id": "com.netflix.ninja"})
	tvRes := callMCPTool(t, s, "add_tv", map[string]any{"name": "Den", "host": "10.0.0.15"})
	tvID := tvRes["structuredContent"].(map[string]any)["tv"].(map[string]any)["id"].(string)

	// 1. When NOT connected
	res := callMCPTool(t, s, "send_key", map[string]any{"tv_id": tvID, "key": "KEYCODE_HOME"})
	if !res["isError"].(bool) {
		t.Fatalf("send_key when disconnected should fail: %#v", res)
	}

	res = callMCPTool(t, s, "send_text", map[string]any{"tv_id": tvID, "text": "SearchQuery"})
	if !res["isError"].(bool) {
		t.Fatalf("send_text when disconnected should fail: %#v", res)
	}

	res = callMCPTool(t, s, "launch_app", map[string]any{"tv_id": tvID, "launcher_id": "com.netflix.ninja"})
	if !res["isError"].(bool) {
		t.Fatalf("launch_app when disconnected should fail: %#v", res)
	}

	// 2. Attach mock connected Remote
	cert, _, err := ensureCertificate(t.TempDir()+"/cert.pem", t.TempDir()+"/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	serverTLS := tls.Server(serverConn, &tls.Config{Certificates: []tls.Certificate{cert}})
	go func() {
		_ = serverTLS.Handshake()
		buf := make([]byte, 4096)
		for {
			_, err := serverTLS.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	clientTLS := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true})
	_ = clientTLS.Handshake()

	mockRemote := &Remote{
		conn:   clientTLS,
		reader: bufio.NewReader(serverTLS),
		done:   make(chan error, 1),
		closed: make(chan struct{}),
	}
	defer mockRemote.Close()

	st := s.state(tvID)
	st.mu.Lock()
	st.remote = mockRemote
	st.mu.Unlock()

	// send_key valid
	res = callMCPTool(t, s, "send_key", map[string]any{"tv_id": tvID, "key": "KEYCODE_HOME"})
	if res["isError"].(bool) {
		t.Fatalf("send_key when connected failed: %#v", res)
	}

	// send_key invalid key code
	res = callMCPTool(t, s, "send_key", map[string]any{"tv_id": tvID, "key": "KEYCODE_INVALID_KEY_123"})
	if !res["isError"].(bool) {
		t.Fatalf("send_key invalid key should fail: %#v", res)
	}

	// send_text
	res = callMCPTool(t, s, "send_text", map[string]any{"tv_id": tvID, "text": "SearchQuery", "enter": false})
	if res["isError"].(bool) {
		t.Fatalf("send_text failed: %#v", res)
	}

	// launch_app with configured launcher or package ID
	res = callMCPTool(t, s, "launch_app", map[string]any{"tv_id": tvID, "launcher_id": "com.netflix.ninja"})
	if res["isError"].(bool) {
		t.Fatalf("launch_app failed: %#v", res)
	}

	// launch_app invalid app ID
	res = callMCPTool(t, s, "launch_app", map[string]any{"tv_id": tvID, "launcher_id": "nonexistent_app_id"})
	if !res["isError"].(bool) {
		t.Fatalf("launch_app invalid app ID should fail: %#v", res)
	}

	// next_event long-polling timeout
	res = callMCPTool(t, s, "next_event", map[string]any{"tv_id": tvID, "timeout_seconds": 1})
	if res["isError"].(bool) {
		t.Fatalf("next_event failed: %#v", res)
	}
	eventData := res["structuredContent"].(map[string]any)
	if eventData["type"] != "keepalive" {
		t.Fatalf("next_event type = %v, want keepalive", eventData["type"])
	}

	// Unknown tool execution
	res = callMCPTool(t, s, "non_existent_tool", map[string]any{})
	if !res["isError"].(bool) {
		t.Fatalf("non_existent_tool should fail: %#v", res)
	}
}

func callMCPTool(t *testing.T, s *Server, toolName string, args map[string]any) map[string]any {
	t.Helper()
	w, out := requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("MCP tool %s HTTP status = %d", toolName, w.Code)
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP tool %s response missing result: %#v", toolName, out)
	}
	return result
}

func parseJSONMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("parse JSON failed: %v, raw: %q", err, string(b))
	}
	return out
}
