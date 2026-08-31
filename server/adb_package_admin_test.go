package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type packageAdminRunner struct {
	mu       sync.Mutex
	packages map[string]map[string]bool
	system   map[string]map[string]bool
	calls    [][]string
}

func newPackageAdminRunner() *packageAdminRunner {
	return &packageAdminRunner{
		packages: map[string]map[string]bool{
			"living:5555": {
				"tv.stream.alpha":               true,
				"tv.vendor.priv":                true,
				"com.google.android.tvlauncher": true,
			},
			"bedroom:5555": {
				"tv.stream.beta": true,
			},
		},
		system: map[string]map[string]bool{
			"living:5555": {
				"com.vendor.system": true,
			},
			"bedroom:5555": {},
		},
	}
}

func (r *packageAdminRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
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
		return ADBResult{Stdout: "List of devices attached\nliving:5555\tdevice\nbedroom:5555\tdevice\n"}, nil
	}
	if len(args) < 3 || args[0] != "-s" {
		return ADBResult{}, nil
	}
	serial := args[1]
	cmd := args[2:]
	if len(cmd) >= 3 && cmd[0] == "shell" && cmd[1] == "am" && cmd[2] == "get-current-user" {
		return ADBResult{Stdout: "0\n"}, nil
	}
	if len(cmd) >= 4 && cmd[0] == "shell" && cmd[1] == "cmd" && cmd[2] == "package" && cmd[3] == "query-activities" {
		return ADBResult{Stdout: ""}, nil
	}
	if len(cmd) >= 4 && cmd[0] == "shell" && cmd[1] == "pm" && cmd[2] == "list" && cmd[3] == "packages" {
		thirdOnly := false
		disabledOnly := false
		for _, arg := range cmd[4:] {
			if arg == "-3" {
				thirdOnly = true
			}
			if arg == "-d" {
				disabledOnly = true
			}
		}
		var lines []string
		for pkg, enabled := range r.packages[serial] {
			if disabledOnly && enabled {
				continue
			}
			lines = append(lines, "package:"+pkg+" versionCode:12")
		}
		if !thirdOnly && !disabledOnly {
			for pkg := range r.system[serial] {
				lines = append(lines, "package:"+pkg+" versionCode:1")
			}
		}
		return ADBResult{Stdout: strings.Join(lines, "\n") + "\n"}, nil
	}
	if len(cmd) >= 6 && cmd[0] == "shell" && cmd[1] == "pm" && cmd[2] == "path" {
		pkg := cmd[len(cmd)-1]
		if _, ok := r.packages[serial][pkg]; ok {
			if pkg == "tv.vendor.priv" {
				return ADBResult{Stdout: "package:/system/priv-app/" + pkg + "/base.apk\n"}, nil
			}
			return ADBResult{Stdout: "package:/data/app/" + pkg + "/base.apk\n"}, nil
		}
		if _, ok := r.system[serial][pkg]; ok {
			return ADBResult{Stdout: "package:/system/priv-app/" + pkg + "/base.apk\n"}, nil
		}
		return ADBResult{}, nil
	}
	if len(cmd) >= 6 && cmd[0] == "shell" && cmd[1] == "pm" {
		action := cmd[2]
		pkg := cmd[len(cmd)-1]
		switch action {
		case "clear":
			return ADBResult{Stdout: "Success\n"}, nil
		case "enable":
			if _, ok := r.packages[serial][pkg]; ok {
				r.packages[serial][pkg] = true
			}
			return ADBResult{Stdout: "Package " + pkg + " new state: enabled\n"}, nil
		case "disable-user":
			if _, ok := r.packages[serial][pkg]; ok {
				r.packages[serial][pkg] = false
			}
			return ADBResult{Stdout: "Package " + pkg + " new state: disabled-user\n"}, nil
		case "uninstall":
			delete(r.packages[serial], pkg)
			return ADBResult{Stdout: "Success\n"}, nil
		}
	}
	return ADBResult{}, nil
}

func (r *packageAdminRunner) snapshotCalls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i := range r.calls {
		out[i] = append([]string(nil), r.calls[i]...)
	}
	return out
}

func packageAdminTestServer(t *testing.T) (*Server, *packageAdminRunner, string, string, string) {
	t.Helper()
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "test-admin-token")
	s := testServer(t)
	runner := newPackageAdminRunner()
	s.adb.runner = runner
	s.adb.initErr = nil
	living := addADBTestTV(t, s, "Living", "living.local")
	bedroom := addADBTestTV(t, s, "Bedroom", "bedroom.local")
	livingSerial := "living:5555"
	bedroomSerial := "bedroom:5555"
	if err := s.updateADBAssociation(living, &livingSerial, &livingSerial, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.updateADBAssociation(bedroom, &bedroomSerial, &bedroomSerial, nil); err != nil {
		t.Fatal(err)
	}
	app, status, err := s.addApp(appForm{Name: "Alpha", PackageID: "tv.stream.alpha"})
	if err != nil || status != http.StatusCreated {
		t.Fatalf("add launcher: status=%d err=%v", status, err)
	}
	if _, err := s.setTVApps(living, []string{app.ID}); err != nil {
		t.Fatal(err)
	}
	return s, runner, living, bedroom, app.ID
}

func packageConfirmation(tvID, packageID, action string, user int, enabled bool) map[string]any {
	return map[string]any{
		"package_id": packageID,
		"confirmation": map[string]any{
			"tv_id":        tvID,
			"package_id":   packageID,
			"action":       action,
			"current_user": user,
			"enabled":      enabled,
		},
	}
}

func TestADBPackageAdministrationGuardsAndCurrentUserUninstall(t *testing.T) {
	s, runner, living, _, launcherID := packageAdminTestServer(t)

	w, out := adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/disable",
		packageConfirmation(living, "tv.stream.alpha", "disable", 0, true), "test-admin-token")
	if w.Code != http.StatusOK {
		t.Fatalf("disable: %d %#v", w.Code, out)
	}
	if out["installed"] != true {
		t.Fatalf("disable result: %#v", out)
	}
	pkg := out["package"].(map[string]any)
	if pkg["enabled"] != false {
		t.Fatalf("package was not read back disabled: %#v", pkg)
	}
	tv, err := s.copyTV(living)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range tv.AppIDs {
		if id == launcherID {
			t.Fatalf("disabled package launcher remained enabled for selected TV: %#v", tv.AppIDs)
		}
	}
	s.mu.RLock()
	if s.apps[launcherID] == nil {
		t.Fatal("shared launcher record was deleted")
	}
	s.mu.RUnlock()

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/uninstall",
		packageConfirmation(living, "tv.stream.alpha", "uninstall", 0, false), "test-admin-token")
	if w.Code != http.StatusOK || out["installed"] != false {
		t.Fatalf("uninstall: %d %#v", w.Code, out)
	}
	foundUninstall := false
	for _, call := range runner.snapshotCalls() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "shell pm uninstall --user 0 tv.stream.alpha") {
			foundUninstall = true
			if !strings.HasPrefix(joined, "-s living:5555 ") {
				t.Fatalf("uninstall targeted wrong serial: %q", joined)
			}
		}
	}
	if !foundUninstall {
		t.Fatal("current-user uninstall command was not issued")
	}
}

func TestADBPackageAdministrationRejectsSystemProtectedAndStaleStateBeforeMutation(t *testing.T) {
	s, runner, living, _, _ := packageAdminTestServer(t)

	inventory, err := s.adb.PackageInventory(context.Background(), "living:5555")
	if err != nil {
		t.Fatal(err)
	}
	misleading, ok := findADBPackage(inventory, "com.google.android.tvlauncher")
	if !ok || !misleading.ThirdParty || !misleading.Protected {
		t.Fatalf("misleading vendor classification fixture was not represented as third-party + protected: %#v", misleading)
	}

	before := len(runner.snapshotCalls())
	w, out := adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/clear",
		packageConfirmation(living, "com.google.android.tvlauncher", "clear", 0, true), "test-admin-token")
	if w.Code != http.StatusConflict || out["code"] != "protected_package" {
		t.Fatalf("protected package: %d %#v", w.Code, out)
	}
	for _, call := range runner.snapshotCalls()[before:] {
		if strings.Contains(strings.Join(call, " "), " pm clear ") {
			t.Fatalf("protected package reached mutation command: %#v", call)
		}
	}

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/disable",
		packageConfirmation(living, "com.vendor.system", "disable", 0, true), "test-admin-token")
	if w.Code != http.StatusConflict || out["code"] != "protected_package" {
		t.Fatalf("system package: %d %#v", w.Code, out)
	}

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/clear",
		packageConfirmation(living, "tv.vendor.priv", "clear", 0, true), "test-admin-token")
	if w.Code != http.StatusConflict || out["code"] != "protected_package" {
		t.Fatalf("privileged path: %d %#v", w.Code, out)
	}

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/clear",
		packageConfirmation(living, "tv.stream.missing", "clear", 0, true), "test-admin-token")
	if w.Code != http.StatusConflict || out["code"] != "package_not_found" {
		t.Fatalf("missing package: %d %#v", w.Code, out)
	}

	w, out = adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+living+"/adb/packages/disable",
		packageConfirmation(living, "tv.stream.alpha", "disable", 0, false), "test-admin-token")
	if w.Code != http.StatusConflict || out["code"] != "stale_package_state" {
		t.Fatalf("stale state: %d %#v", w.Code, out)
	}
}

func TestADBPackageAdministrationRESTMCPParityAndMultiTVIsolation(t *testing.T) {
	s, runner, living, bedroom, _ := packageAdminTestServer(t)

	w, out := adbRequestJSON(t, s, http.MethodPost, "/api/tvs/"+bedroom+"/adb/packages/clear",
		packageConfirmation(bedroom, "tv.stream.beta", "clear", 0, true), "")
	if w.Code != http.StatusUnauthorized || out["code"] != "unauthorized" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("REST auth/no-store: %d %#v headers=%#v", w.Code, out, w.Header())
	}

	result := mcpADBCall(t, s, "adb_clear_package", map[string]any{
		"tv_id":       living,
		"package_id":  "tv.stream.alpha",
		"confirmation": map[string]any{
			"tv_id": living, "package_id": "tv.stream.alpha", "action": "clear", "current_user": 0, "enabled": true,
		},
	}, "test-admin-token")
	if result["isError"] != false {
		t.Fatalf("MCP clear failed: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["tv_id"] != living || structured["action"] != "clear" {
		t.Fatalf("MCP result mismatch: %#v", structured)
	}

	for _, call := range runner.snapshotCalls() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "tv.stream.alpha") && strings.Contains(joined, "shell pm clear") &&
			!strings.HasPrefix(joined, "-s living:5555 ") {
			t.Fatalf("alpha mutation crossed TV target: %q", joined)
		}
	}
	if _, ok := runner.packages["bedroom:5555"]["tv.stream.beta"]; !ok {
		t.Fatal("living-room operation altered bedroom package")
	}
}

func TestADBPackageMutationRequestJSONShape(t *testing.T) {
	user := 0
	enabled := true
	in := ADBPackageMutationRequest{
		PackageID: "tv.stream.alpha",
		Confirmation: ADBPackageConfirmation{
			TVID: "living", PackageID: "tv.stream.alpha", Action: "clear", CurrentUser: &user, Enabled: &enabled,
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "serial") || !strings.Contains(string(b), "\"confirmation\"") {
		t.Fatalf("unexpected mutation JSON: %s", b)
	}
}
