package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "client"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client", "index.html"), []byte("version=__VERSION__"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client", "app.js"), []byte("const version='__VERSION__';"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	s.eventTimeout = 20 * time.Millisecond
	s.inactivityTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, st := range s.states {
			st.mu.Lock()
			if st.cancel != nil {
				st.cancel()
			}
			st.mu.Unlock()
		}
	})
	return s
}

func requestJSON(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() != 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w, out
}

func TestStatusAndTVLifecycle(t *testing.T) {
	s := testServer(t)
	w, out := requestJSON(t, s, http.MethodGet, "/api/status", nil)
	if w.Code != 200 || out["tv_id"] != nil || out["tv_name"] != "No TV selected" {
		t.Fatalf("unexpected status: %d %#v", w.Code, out)
	}

	w, out = requestJSON(t, s, http.MethodPost, "/api/tvs", map[string]any{"name": "Living Room", "host": "192.168.1.10"})
	if w.Code != 201 {
		t.Fatalf("add TV: %d %s", w.Code, w.Body.String())
	}
	tv := out["tv"].(map[string]any)
	id := tv["id"].(string)
	if id == "" || tv["connected"] != false {
		t.Fatalf("bad TV: %#v", tv)
	}

	_, out = requestJSON(t, s, http.MethodGet, "/api/status", nil)
	if out["tv_id"] != id || out["tv_name"] != "Living Room" {
		t.Fatalf("single-TV resolution failed: %#v", out)
	}

	certDir := s.tvDir(id)
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "cert.pem"), []byte("cert"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "key.pem"), []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	w, out = requestJSON(t, s, http.MethodDelete, "/api/tvs/"+id, nil)
	if w.Code != 200 || out["status"] != "forgotten" {
		t.Fatalf("forget: %d %#v", w.Code, out)
	}
	if _, err := os.Stat(certDir); !os.IsNotExist(err) {
		t.Fatalf("credentials remain: %v", err)
	}
}

func TestLegacyConfigAndCredentialsMigration(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(root, "client"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	config := "server_port: 7777\ntv_ip: 10.0.0.5\ntv_name: Den\napps:\n  - name: Netflix\n    id: com.netflix.ninja\n    icon: mdi-netflix\n"
	if err := os.WriteFile(filepath.Join(data, "config.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "cert.pem"), []byte("old-cert"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "key.pem"), []byte("old-key"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if s.config.ServerPort != 7777 || len(s.apps) != 1 || len(s.tvs) != 1 {
		t.Fatalf("migration failed: %#v %#v", s.config, s)
	}
	id := s.tvOrder[0]
	b, err := os.ReadFile(filepath.Join(s.tvDir(id), "cert.pem"))
	if err != nil || string(b) != "old-cert" {
		t.Fatalf("cert migration failed: %q %v", b, err)
	}
	if got := s.tvs[id].AppIDs; len(got) != 1 || got[0] != s.appOrder[0] {
		t.Fatalf("app migration failed: %#v", got)
	}
}

func TestLauncherCRUDIconsAndAvailability(t *testing.T) {
	s := testServer(t)
	_, tvOut := requestJSON(t, s, http.MethodPost, "/api/tvs", map[string]any{"name": "Living", "host": "tv.local"})
	tvID := tvOut["tv"].(map[string]any)["id"].(string)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"name": "Netflix", "package_id": "com.netflix.ninja", "icon_class": "mdi-netflix",
	} {
		if err := mw.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="icon_file"; filename="icon.png"`)
	h.Set("Content-Type", "image/png")
	p, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(append([]byte("\x89PNG\r\n\x1a\n"), []byte("data")...)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/apps", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("add app: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	app := created["app"].(map[string]any)
	appID := app["id"].(string)
	if app["has_uploaded_icon"] != true || !strings.HasPrefix(app["icon"].(string), "icons/") {
		t.Fatalf("bad app: %#v", app)
	}

	w, out := requestJSON(t, s, http.MethodPut, "/api/apps/"+appID, map[string]any{"icon_class": ""})
	if w.Code != 200 || out["app"].(map[string]any)["icon_class"] != "" {
		t.Fatalf("clear icon class: %d %#v", w.Code, out)
	}

	w, out = requestJSON(t, s, http.MethodPut, "/api/tvs/"+tvID+"/apps", map[string]any{"app_ids": []string{appID}})
	if w.Code != 200 {
		t.Fatalf("set TV apps: %d %s", w.Code, w.Body.String())
	}
	ids := out["tv"].(map[string]any)["app_ids"].([]any)
	if len(ids) != 1 || ids[0] != appID {
		t.Fatalf("availability: %#v", ids)
	}

	iconFile := s.apps[appID].IconFile
	w, out = requestJSON(t, s, http.MethodDelete, "/api/apps/"+appID, nil)
	if w.Code != 200 || out["status"] != "deleted" {
		t.Fatalf("delete app: %d %#v", w.Code, out)
	}
	if _, err := os.Stat(filepath.Join(s.iconsDir(), iconFile)); !os.IsNotExist(err) {
		t.Fatalf("icon remains: %v", err)
	}
	if len(s.tvs[tvID].AppIDs) != 0 {
		t.Fatalf("launcher remains on TV: %#v", s.tvs[tvID].AppIDs)
	}
}

func TestEventsPreserveLongPollShape(t *testing.T) {
	s := testServer(t)
	_, tvOut := requestJSON(t, s, http.MethodPost, "/api/tvs", map[string]any{"name": "Living", "host": "tv.local"})
	tvID := tvOut["tv"].(map[string]any)["id"].(string)
	s.broadcast(Event{Type: "ime_show", TVID: tvID, Data: map[string]any{"value": "hello", "label": "Search", "start": 0, "end": 5}})
	req := httptest.NewRequest(http.MethodGet, "/api/events?tv_id="+tvID, nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var event map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "ime_show" || event["tv_id"] != tvID || event["data"].(map[string]any)["value"] != "hello" {
		t.Fatalf("event: %#v", event)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/events?tv_id=missing", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	event = nil
	if err := json.Unmarshal(w.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "keepalive" || event["tv_id"] != nil {
		t.Fatalf("keepalive: %#v", event)
	}
	if _, exists := event["data"]; exists {
		t.Fatalf("keepalive unexpectedly contains data: %#v", event)
	}
}

func TestStaticSubpathAndVersionSubstitution(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/", "/remote/", "/remote/index.html", "/remote/app.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != 200 || strings.Contains(w.Body.String(), "__VERSION__") || !strings.Contains(w.Body.String(), "1.0.0") {
			t.Fatalf("static %s: %d %q", path, w.Code, w.Body.String())
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Fatalf("missing no-cache for %s: %q", path, cc)
		}
	}
}

func TestMCPListsAndCallsEveryAPISurface(t *testing.T) {
	s := testServer(t)
	w, out := requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	if w.Code != 200 {
		t.Fatalf("initialize: %d %s", w.Code, w.Body.String())
	}
	result := out["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize result: %#v", result)
	}

	_, out = requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 24 {
		t.Fatalf("tool count = %d", len(tools))
	}
	wantTools := map[string]bool{
		"status": true, "list_tvs": true, "add_tv": true, "forget_tv": true,
		"set_tv_apps": true, "list_apps": true, "add_app": true, "update_app": true,
		"reorder_apps": true, "delete_app": true, "connect_tv": true,
		"submit_pairing_code": true, "send_key": true, "send_text": true,
		"launch_app": true, "next_event": true,
		"adb_status": true, "adb_pair": true, "adb_connect": true,
		"adb_disconnect": true, "adb_forget": true,
		"adb_device_info": true, "adb_packages": true, "adb_launchables": true,
	}
	for _, raw := range tools {
		name := raw.(map[string]any)["name"].(string)
		if !wantTools[name] {
			t.Errorf("unexpected MCP tool %q", name)
		}
		delete(wantTools, name)
	}
	if len(wantTools) != 0 {
		t.Fatalf("missing MCP tools: %#v", wantTools)
	}

	_, out = requestJSON(t, s, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "add_tv", "arguments": map[string]any{"name": "Office", "host": "10.0.0.8"}}})
	call := out["result"].(map[string]any)
	if call["isError"] != false {
		t.Fatalf("tool failed: %#v", call)
	}
	structured := call["structuredContent"].(map[string]any)
	if structured["tv"].(map[string]any)["name"] != "Office" {
		t.Fatalf("bad tool result: %#v", structured)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(ctx)
	for _, tool := range mcpTools() {
		if tool.Name == "add_tv" || tool.Name == "connect_tv" {
			continue
		}
		_, err := s.callTool(req, tool.Name, map[string]any{})
		if err != nil && strings.HasPrefix(err.Error(), "unknown tool:") {
			t.Errorf("listed tool %q has no dispatch path", tool.Name)
		}
	}
}

func TestCertificatePersistenceAndProtocolEncoding(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	cert1, generated, err := ensureCertificate(certPath, keyPath)
	if err != nil || !generated {
		t.Fatalf("generate: %v %v", generated, err)
	}
	cert2, generated, err := ensureCertificate(certPath, keyPath)
	if err != nil || generated {
		t.Fatalf("reload: %v %v", generated, err)
	}
	if len(cert1.Certificate) == 0 || len(cert2.Certificate) == 0 || !bytes.Equal(cert1.Certificate[0], cert2.Certificate[0]) {
		t.Fatal("certificate changed")
	}

	raw := imeBatchMessage(7, 8, "hé", true)
	top, err := parseWire(raw)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := firstField(top, 21)
	if !ok {
		t.Fatal("missing IME batch")
	}
	batch, err := parseWire(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := nestedVarint(f.bytes, 1); !ok || v != 7 {
		t.Fatalf("ime counter=%d", v)
	}
	if v, ok := nestedVarint(f.bytes, 2); !ok || v != 8 {
		t.Fatalf("field counter=%d", v)
	}
	edit, ok := firstField(batch, 3)
	if !ok {
		t.Fatal("missing edit")
	}
	editFields, err := parseWire(edit.bytes)
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := firstField(editFields, 2)
	if !ok {
		t.Fatal("missing object")
	}
	objFields, err := parseWire(obj.bytes)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := firstField(objFields, 3)
	if !ok || string(text.bytes) != "hé" {
		t.Fatalf("text=%q", text.bytes)
	}
	if _, ok := keyCode("KEYCODE_PROG_BLUE"); !ok {
		t.Fatal("frontend key missing")
	}
}

func TestInactivityDisconnect(t *testing.T) {
	s := testServer(t)
	s.inactivityTimeout = 50 * time.Millisecond

	w, out := requestJSON(t, s, http.MethodPost, "/api/tvs", map[string]any{"name": "Living Room", "host": "127.0.0.1"})
	if w.Code != 201 {
		t.Fatalf("add TV: %d %s", w.Code, w.Body.String())
	}
	tv := out["tv"].(map[string]any)
	id := tv["id"].(string)

	st := s.state(id)
	st.mu.Lock()
	st.lastClientActivity = time.Now()
	st.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockRemote := &Remote{done: make(chan error, 1), closed: make(chan struct{})}

	st.mu.Lock()
	st.remote = mockRemote
	st.running = true
	st.mu.Unlock()

	go s.monitor(ctx, id, st, mockRemote)

	time.Sleep(150 * time.Millisecond)

	st.mu.Lock()
	running := st.running
	remote := st.remote
	st.mu.Unlock()

	if running || remote != nil {
		t.Fatalf("expected TV connection to disconnect due to inactivity, running=%v remote=%v", running, remote)
	}
}
