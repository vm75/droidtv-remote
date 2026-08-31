package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type inventoryRunner struct {
	mu        sync.Mutex
	calls     [][]string
	responses map[string]ADBResult
	errs      map[string]error
}

func (r *inventoryRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), args...))
	keyArgs := args
	if len(args) >= 2 && args[0] == "-s" {
		keyArgs = args[2:]
	}
	key := strings.Join(keyArgs, " ")
	result := r.responses[key]
	return result, r.errs[key]
}

func (r *inventoryRunner) set(key, stdout string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.responses == nil {
		r.responses = map[string]ADBResult{}
	}
	r.responses[key] = ADBResult{Stdout: stdout}
	if r.errs != nil {
		delete(r.errs, key)
	}
}

func (r *inventoryRunner) setResult(key string, result ADBResult, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.responses == nil {
		r.responses = map[string]ADBResult{}
	}
	if r.errs == nil {
		r.errs = map[string]error{}
	}
	r.responses[key] = result
	r.errs[key] = err
}

func (r *inventoryRunner) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i := range r.calls {
		out[i] = append([]string(nil), r.calls[i]...)
	}
	return out
}

func inventoryTestServer(t *testing.T) (*Server, *inventoryRunner, string) {
	t.Helper()
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "inventory-token")
	s := testServer(t)
	runner := &inventoryRunner{responses: map[string]ADBResult{}, errs: map[string]error{}}
	s.adb.runner = runner
	s.adb.initErr = nil
	id := addADBTestTV(t, s, "Living", "living.local")
	serial := "10.0.0.20:5555"
	if err := s.updateADBAssociation(id, &serial, &serial, nil); err != nil {
		t.Fatal(err)
	}
	return s, runner, id
}

func seedInventoryFixture(r *inventoryRunner, user string) {
	r.set("shell am get-current-user", user+"\n")
	r.set("shell getprop ro.product.manufacturer", "Google\n")
	r.set("shell getprop ro.product.model", "Chromecast HD\n")
	r.set("shell getprop ro.product.name", "sabrina\n")
	r.set("shell getprop ro.build.version.release", "14\n")
	r.set("shell getprop ro.build.version.sdk", "34\n")
	r.set("shell getprop ro.build.display.id", "UTT3.240625.001\n")
	r.set("shell getprop ro.product.cpu.abilist", "arm64-v8a,armeabi-v7a\n")
	r.set("shell pm list packages --user "+user+" --show-versioncode",
		"package:com.vendor.system versionCode:100\n"+
			"vendor noise ignored\n"+
			"package:com.example.video versionCode:42\n"+
			"package:com.example.disabled versionCode:7\n")
	r.set("shell pm list packages -3 --user "+user,
		"package:com.example.video\npackage:com.example.disabled\n")
	r.set("shell pm list packages -d --user "+user,
		"package:com.example.disabled\n")
	r.set("shell cmd package query-activities --brief --user "+user+" -a android.intent.action.MAIN -c android.intent.category.LEANBACK_LAUNCHER",
		"priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=false\n"+
			"com.example.video/.TvActivity\n"+
			"noise from vendor resolver\n")
}

func TestADBInventoryParsersAreBoundedDeterministicAndNoiseTolerant(t *testing.T) {
	pkgs, malformed, truncated := parsePackageLines(
		"package:com.zeta versionCode:2\nnoise\npackage:bad/id versionCode:1\npackage:com.alpha versionCode:1\n",
		10,
	)
	if truncated || malformed != 2 || pkgs["com.alpha"] != "1" || pkgs["com.zeta"] != "2" {
		t.Fatalf("package parser: pkgs=%#v malformed=%d truncated=%v", pkgs, malformed, truncated)
	}

	launchers, malformed, truncated := parseLeanbackComponents(
		"vendor header\ncom.zeta/.Main\ncom.alpha/com.alpha.TV\ncom.alpha/com.alpha.TV\nmalformed/component/extra\n",
		10,
	)
	if truncated || malformed != 2 {
		t.Fatalf("launcher parser malformed=%d truncated=%v items=%#v", malformed, truncated, launchers)
	}
	if len(launchers) != 2 || launchers[0].PackageID != "com.alpha" || launchers[1].PackageID != "com.zeta" {
		t.Fatalf("launchers not deterministic: %#v", launchers)
	}

	_, _, truncated = parseLeanbackComponents("com.a/.One\ncom.b/.Two\n", 1)
	if !truncated {
		t.Fatal("launcher record cap was not reported")
	}
}

func TestADBInventoryRESTAndMCPParityAndSerialIsolation(t *testing.T) {
	s, runner, id := inventoryTestServer(t)
	seedInventoryFixture(runner, "10")

	w, device := adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/device", nil, "inventory-token")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("device info response: %d headers=%#v body=%#v", w.Code, w.Header(), device)
	}
	d := device["device"].(map[string]any)
	if d["manufacturer"] != "Google" || d["model"] != "Chromecast HD" || d["api_level"] != float64(34) || d["current_user"] != float64(10) {
		t.Fatalf("device info: %#v", d)
	}
	if _, exists := d["serial"]; exists {
		t.Fatalf("non-allowlisted field leaked: %#v", d)
	}

	w, rest := adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/packages", nil, "inventory-token")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("packages response: %d %#v", w.Code, rest)
	}
	inventory := rest["inventory"].(map[string]any)
	packages := inventory["packages"].([]any)
	if len(packages) != 3 {
		t.Fatalf("package count: %#v", inventory)
	}
	first := packages[0].(map[string]any)
	second := packages[1].(map[string]any)
	third := packages[2].(map[string]any)
	if first["package_id"] != "com.example.disabled" || second["package_id"] != "com.example.video" || third["package_id"] != "com.vendor.system" {
		t.Fatalf("packages not sorted: %#v", packages)
	}
	if first["classification"] != "third_party" || first["enabled"] != false {
		t.Fatalf("disabled classification: %#v", first)
	}
	if second["tv_launchable"] != true || second["component"] != "com.example.video/.TvActivity" || second["version_code"] != "42" {
		t.Fatalf("launchable package join: %#v", second)
	}
	if third["classification"] != "system" || third["enabled"] != true {
		t.Fatalf("system classification: %#v", third)
	}
	if warnings, ok := inventory["warnings"].([]any); !ok || len(warnings) == 0 {
		t.Fatalf("expected partial/noise warning: %#v", inventory)
	}

	mcp := mcpADBCall(t, s, "adb_packages", map[string]any{"tv_id": id}, "inventory-token")
	if mcp["isError"] != false {
		t.Fatalf("MCP packages failed: %#v", mcp)
	}
	structured := mcp["structuredContent"].(map[string]any)
	restJSON, _ := json.Marshal(rest)
	mcpJSON, _ := json.Marshal(structured)
	if string(restJSON) != string(mcpJSON) {
		t.Fatalf("REST/MCP mismatch\nREST=%s\nMCP=%s", restJSON, mcpJSON)
	}

	secondID := addADBTestTV(t, s, "Bedroom", "bedroom.local")
	secondSerial := "10.0.0.21:5555"
	if err := s.updateADBAssociation(secondID, &secondSerial, &secondSerial, nil); err != nil {
		t.Fatal(err)
	}
	runner.set("shell am get-current-user", "10\n")
	w, _ = adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+secondID+"/adb/launchables", nil, "inventory-token")
	if w.Code != http.StatusOK {
		t.Fatalf("second-TV inventory failed: %d", w.Code)
	}
	calls := runner.snapshot()
	foundFirst, foundSecond := false, false
	for _, args := range calls {
		if len(args) < 3 || args[0] != "-s" {
			t.Fatalf("device inventory command was not explicitly targeted: %#v", args)
		}
		switch args[1] {
		case "10.0.0.20:5555":
			foundFirst = true
		case "10.0.0.21:5555":
			foundSecond = true
		default:
			t.Fatalf("inventory targeted unknown serial: %#v", args)
		}
	}
	if !foundFirst || !foundSecond {
		t.Fatalf("missing multi-TV targeting: %#v", calls)
	}
}

func TestADBInventoryAuthFailureOfflineUnsupportedAndTruncation(t *testing.T) {
	s, runner, id := inventoryTestServer(t)
	seedInventoryFixture(runner, "0")

	before := len(runner.snapshot())
	w, out := adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/packages", nil, "")
	if w.Code != http.StatusUnauthorized || out["code"] != "unauthorized" || len(runner.snapshot()) != before {
		t.Fatalf("unauthorized inventory reached ADB: code=%d out=%#v", w.Code, out)
	}

	runner.setResult(
		"shell am get-current-user",
		ADBResult{Stderr: "error: device offline"},
		&ADBError{Code: "command_failed", Message: "ADB command failed"},
	)
	w, out = adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/packages", nil, "inventory-token")
	if w.Code != http.StatusConflict || out["code"] != "offline" {
		t.Fatalf("offline mapping: %d %#v", w.Code, out)
	}

	runner.set("shell am get-current-user", "0\n")
	runner.setResult(
		"shell cmd package query-activities --brief --user 0 -a android.intent.action.MAIN -c android.intent.category.LEANBACK_LAUNCHER",
		ADBResult{Stderr: "Unknown command: query-activities"},
		errors.New("exit status 1"),
	)
	w, out = adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/launchables", nil, "inventory-token")
	if w.Code != http.StatusNotImplemented || out["code"] != "unsupported_command" {
		t.Fatalf("unsupported mapping: %d %#v", w.Code, out)
	}
	mcp := mcpADBCall(t, s, "adb_launchables", map[string]any{"tv_id": id}, "inventory-token")
	if mcp["isError"] != true {
		t.Fatalf("unsupported MCP inventory unexpectedly succeeded: %#v", mcp)
	}
	mcpErr := mcp["structuredContent"].(map[string]any)["error"].(map[string]any)
	if mcpErr["code"] != "unsupported_command" {
		t.Fatalf("REST/MCP unsupported mismatch: REST=%#v MCP=%#v", out, mcpErr)
	}

	seedInventoryFixture(runner, "0")
	runner.setResult(
		"shell pm list packages --user 0 --show-versioncode",
		ADBResult{Stdout: "package:com.example.one versionCode:1\n", Truncated: true},
		nil,
	)
	w, out = adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/packages", nil, "inventory-token")
	if w.Code != http.StatusOK {
		t.Fatalf("truncated inventory failed: %d %#v", w.Code, out)
	}
	inventory := out["inventory"].(map[string]any)
	if inventory["truncated"] != true {
		t.Fatalf("truncation not explicit: %#v", inventory)
	}

	runner.set("shell am get-current-user", "not-a-user\n")
	w, out = adbRequestJSON(t, s, http.MethodGet, "/api/tvs/"+id+"/adb/device", nil, "inventory-token")
	if w.Code != http.StatusBadGateway || out["code"] != "malformed_output" {
		t.Fatalf("malformed current-user mapping: %d %#v", w.Code, out)
	}
}
