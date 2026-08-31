package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func stringsSchema(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func mcpTools() []mcpTool {
	return []mcpTool{
		{"status", "Get connection status, enabled apps, and server version for a TV.", obj(map[string]any{"tv_id": str("TV ID; omitted only when exactly one TV exists")})},
		{"list_tvs", "List configured TVs and live connection states.", obj(map[string]any{})},
		{"add_tv", "Add a TV to the persistent registry.", obj(map[string]any{"name": str("Display name"), "host": str("IP address or host name")}, "name", "host")},
		{"forget_tv", "Forget a TV and delete only its generated pairing credentials.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"set_tv_apps", "Set and order the launchers enabled for a TV.", obj(map[string]any{"tv_id": str("TV ID"), "app_ids": stringsSchema("Ordered launcher IDs")}, "tv_id", "app_ids")},
		{"list_apps", "List all shared app launchers.", obj(map[string]any{})},
		{"add_app", "Create a shared app launcher; uploaded icons are optional base64 raster data.", obj(map[string]any{"name": str("Launcher name"), "package_id": str("Android package ID or app link accepted by the TV"), "icon_class": str("Optional mdi-* icon class"), "icon_base64": str("Optional base64 PNG, JPEG, WebP, or GIF"), "icon_content_type": str("MIME type for icon_base64")}, "name", "package_id")},
		{"update_app", "Update a shared app launcher.", obj(map[string]any{"app_id": str("Launcher ID"), "name": str("Launcher name"), "package_id": str("Android package ID or app link"), "icon_class": str("Optional mdi-* icon class"), "remove_icon": boolean("Remove the uploaded icon"), "icon_base64": str("Optional replacement icon"), "icon_content_type": str("MIME type for icon_base64")}, "app_id")},
		{"reorder_apps", "Reorder the shared launcher library.", obj(map[string]any{"app_ids": stringsSchema("Every existing launcher ID, in order")}, "app_ids")},
		{"delete_app", "Delete a shared launcher from the library and all TVs.", obj(map[string]any{"app_id": str("Launcher ID")}, "app_id")},
		{"connect_tv", "Start connecting or pairing a TV.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"submit_pairing_code", "Submit the six-character code displayed by the TV.", obj(map[string]any{"tv_id": str("TV ID"), "code": str("Pairing code")}, "tv_id", "code")},
		{"adb_status", "Get authenticated ADB status separately from Remote v2 state.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"adb_pair", "Pair secure wireless ADB using an explicit pairing endpoint and six-digit code.", obj(map[string]any{"tv_id": str("TV ID"), "endpoint": str("Explicit pairing host:port"), "code": str("Six-digit pairing code")}, "tv_id", "endpoint", "code")},
		{"adb_connect", "Connect ADB to an explicit host:port for the selected TV.", obj(map[string]any{"tv_id": str("TV ID"), "endpoint": str("Explicit connect host:port")}, "tv_id", "endpoint")},
		{"adb_disconnect", "Disconnect the selected TV using its stored ADB serial.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"adb_forget", "Forget only the selected TV's local ADB association.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"adb_device_info", "Read allowlisted device information for the selected ADB-connected TV.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"adb_packages", "List the selected TV's bounded installed-package inventory for the current Android user.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"adb_launchables", "List Leanback-launchable components for the selected TV and current Android user.", obj(map[string]any{"tv_id": str("TV ID")}, "tv_id")},
		{"install_apk", "Install or update one APK on the selected ADB-connected TV. MCP uses base64 and an 8 MiB decoded limit.", obj(map[string]any{"tv_id": str("TV ID"), "filename": str("APK filename ending in .apk"), "apk_base64": str("Base64-encoded APK; decoded payload must be 8 MiB or smaller")}, "tv_id", "filename", "apk_base64")},
		{"send_key", "Send an Android KEYCODE_* command.", obj(map[string]any{"tv_id": str("TV ID"), "key": str("Android key code name")}, "tv_id", "key")},
		{"send_text", "Send text through the TV IME, optionally followed by Enter.", obj(map[string]any{"tv_id": str("TV ID"), "text": str("Text to enter"), "enter": boolean("Send KEYCODE_ENTER after the text")}, "tv_id", "text")},
		{"launch_app", "Launch an enabled shared launcher on a TV.", obj(map[string]any{"tv_id": str("TV ID"), "launcher_id": str("Launcher ID or configured package/app link")}, "tv_id", "launcher_id")},
		{"next_event", "Long-poll the next IME event or keepalive for a TV.", obj(map[string]any{"tv_id": str("TV ID"), "timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "description": "Long-poll timeout"}}, "tv_id")},
	}
}

func (s *Server) mcpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Session-Id, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			apiError(w, 405, "MCP uses POST requests")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxMCPRequestBytes)
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				s.writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32600, Message: "MCP request exceeds the 16 MiB transport limit"}})
				return
			}
			s.writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}})
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result, err := s.handleMCP(r, req)
		if err != nil {
			s.writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: err.Error()}})
			return
		}
		s.writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	})
}
func (s *Server) writeRPC(w http.ResponseWriter, res rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleMCP(r *http.Request, req rpcRequest) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "droidtv-remote", "version": s.version}}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		v, err := s.callTool(r, p.Name, p.Arguments)
		return toolResult(v, err), nil
	default:
		return nil, fmt.Errorf("method not found: %s", req.Method)
	}
}

func toolResult(v any, err error) map[string]any {
	if err != nil {
		out := map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}
		var adbErr *ADBError
		if errors.As(err, &adbErr) {
			out["structuredContent"] = map[string]any{"error": adbStructuredError(err)}
		}
		return out
	}
	b, _ := json.Marshal(v)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": v, "isError": false}
}
func argString(a map[string]any, k string) string { v, _ := a[k].(string); return v }
func argStrings(a map[string]any, k string) []string {
	raw, _ := a[k].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if x, ok := v.(string); ok {
			out = append(out, x)
		}
	}
	return out
}
func argBool(a map[string]any, k string) bool { v, _ := a[k].(bool); return v }

func (s *Server) callTool(r *http.Request, name string, a map[string]any) (any, error) {
	if strings.HasPrefix(name, "adb_") || name == "install_apk" {
		if err := s.requireADBAuthorization(r); err != nil {
			return nil, err
		}
	}
	switch name {
	case "status":
		id := s.resolveTV(argString(a, "tv_id"))
		var connected, connecting, pairing bool
		tvName := "No TV selected"
		if id != "" {
			v := s.tvStatus(id)
			connected = v["connected"].(bool)
			connecting = v["connecting"].(bool)
			pairing = v["pairing_in_progress"].(bool)
			tvName = v["name"].(string)
		}
		return map[string]any{"tv_id": nullable(id), "connected": connected, "pairing_in_progress": pairing, "connecting": connecting, "tv_name": tvName, "apps": s.appsForTV(id), "version": s.version}, nil
	case "list_tvs":
		s.mu.RLock()
		ids := append([]string(nil), s.tvOrder...)
		s.mu.RUnlock()
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, s.tvStatus(id))
		}
		return map[string]any{"tvs": out}, nil
	case "add_tv":
		tv, _, err := s.addTV(argString(a, "name"), argString(a, "host"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"tv": tv}, nil
	case "forget_tv":
		id := argString(a, "tv_id")
		if err := s.forgetTV(id); err != nil {
			return nil, err
		}
		return map[string]any{"status": "forgotten", "tv_id": id}, nil
	case "set_tv_apps":
		tv, err := s.setTVApps(argString(a, "tv_id"), argStrings(a, "app_ids"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"tv": tv}, nil
	case "list_apps":
		s.mu.RLock()
		apps := make([]AppJSON, 0, len(s.appOrder))
		for _, id := range s.appOrder {
			if app := s.apps[id]; app != nil {
				apps = append(apps, s.appJSON(app))
			}
		}
		s.mu.RUnlock()
		return map[string]any{"apps": apps}, nil
	case "add_app":
		f, err := mcpAppForm(a)
		if err != nil {
			return nil, err
		}
		app, _, err := s.addApp(f)
		if err != nil {
			return nil, err
		}
		return map[string]any{"app": app}, nil
	case "update_app":
		f, err := mcpAppForm(a)
		if err != nil {
			return nil, err
		}
		f.RemoveIcon = argBool(a, "remove_icon")
		app, _, err := s.updateApp(argString(a, "app_id"), f)
		if err != nil {
			return nil, err
		}
		return map[string]any{"app": app}, nil
	case "reorder_apps":
		if err := s.reorderApps(argStrings(a, "app_ids")); err != nil {
			return nil, err
		}
		return s.callTool(r, "list_apps", nil)
	case "delete_app":
		id := argString(a, "app_id")
		if err := s.deleteApp(id); err != nil {
			return nil, err
		}
		return map[string]any{"status": "deleted", "app_id": id}, nil
	case "connect_tv":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return map[string]any{"status": s.startConnection(id), "tv_id": id}, nil
	case "submit_pairing_code":
		if err := s.submitPairCode(argString(a, "tv_id"), argString(a, "code")); err != nil {
			return nil, err
		}
		return map[string]any{"status": "submitted"}, nil
	case "adb_status":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbStatusResult(r.Context(), id)
	case "adb_pair":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		code := argString(a, "code")
		result, err := s.adbPair(r.Context(), id, argString(a, "endpoint"), code)
		code = ""
		return result, err
	case "adb_connect":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbConnect(r.Context(), id, argString(a, "endpoint"))
	case "adb_disconnect":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbDisconnect(r.Context(), id)
	case "adb_forget":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbForget(id)
	case "adb_device_info":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbDeviceInfo(r.Context(), id)
	case "adb_packages":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbPackages(r.Context(), id)
	case "adb_launchables":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.adbLaunchables(r.Context(), id)
	case "install_apk":
		id := s.resolveTV(argString(a, "tv_id"))
		if id == "" {
			return nil, errors.New("Unknown TV")
		}
		return s.installAPKBase64(r.Context(), id, argString(a, "filename"), argString(a, "apk_base64"))
	case "send_key":
		if err := s.sendKey(argString(a, "tv_id"), argString(a, "key")); err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok"}, nil
	case "send_text":
		if err := s.sendText(argString(a, "tv_id"), argString(a, "text"), argBool(a, "enter")); err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok"}, nil
	case "launch_app":
		if err := s.launchApp(argString(a, "tv_id"), argString(a, "launcher_id")); err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok"}, nil
	case "next_event":
		seconds := 30
		if v, ok := a["timeout_seconds"].(float64); ok && v >= 1 && v <= 30 {
			seconds = int(v)
		}
		id := s.resolveTV(argString(a, "tv_id"))
		return s.nextEvent(r.Context(), id, time.Duration(seconds)*time.Second), nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func mcpAppForm(a map[string]any) (appForm, error) {
	f := appForm{Name: argString(a, "name"), PackageID: argString(a, "package_id"), IconClass: argString(a, "icon_class")}
	_, f.NameSet = a["name"]
	_, f.PackageIDSet = a["package_id"]
	_, f.IconSet = a["icon_class"]
	encoded := argString(a, "icon_base64")
	if encoded != "" {
		b, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return f, errors.New("icon_base64 is not valid base64")
		}
		f.IconData = b
		f.IconType = argString(a, "icon_content_type")
	}
	return f, nil
}
