package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type apkInstallRunner struct {
	mu               sync.Mutex
	calls            [][]string
	serials          []string
	postInstall      map[string]bool
	initialInstalled bool
	installResult    ADBResult
	installErr       error
	panicInstall     bool
	installCalls     int
	activeInstalls   int
	maxActive        int
	installPaths     []string
	installModes     []os.FileMode
	blockFirst       bool
	started          chan struct{}
	release          chan struct{}
}

func (r *apkInstallRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), args...))
	r.mu.Unlock()

	if len(args) == 1 && args[0] == "version" {
		return ADBResult{Stdout: "Android Debug Bridge version 1.0.41\n"}, nil
	}
	if len(args) == 1 && args[0] == "start-server" {
		return ADBResult{}, nil
	}
	if len(args) == 1 && args[0] == "devices" {
		r.mu.Lock()
		serials := append([]string(nil), r.serials...)
		r.mu.Unlock()
		var out strings.Builder
		out.WriteString("List of devices attached\n")
		for _, serial := range serials {
			out.WriteString(serial + "\tdevice\n")
		}
		return ADBResult{Stdout: out.String()}, nil
	}
	if len(args) < 3 || args[0] != "-s" {
		return ADBResult{}, nil
	}
	serial := args[1]
	cmd := args[2:]

	if len(cmd) == 3 && cmd[0] == "shell" && cmd[1] == "am" && cmd[2] == "get-current-user" {
		return ADBResult{Stdout: "0\n"}, nil
	}
	if len(cmd) >= 4 && cmd[0] == "shell" && cmd[1] == "pm" && cmd[2] == "list" && cmd[3] == "packages" {
		r.mu.Lock()
		post := r.postInstall != nil && r.postInstall[serial]
		initial := r.initialInstalled
		r.mu.Unlock()

		if len(cmd) >= 5 && cmd[4] == "-3" {
			if initial || post {
				return ADBResult{Stdout: "package:com.example.app\n"}, nil
			}
			return ADBResult{}, nil
		}
		if len(cmd) >= 5 && cmd[4] == "-d" {
			return ADBResult{}, nil
		}
		if strings.Contains(strings.Join(cmd, " "), "--show-versioncode") {
			switch {
			case post:
				return ADBResult{Stdout: "package:com.example.app versionCode:2\n"}, nil
			case initial:
				return ADBResult{Stdout: "package:com.example.app versionCode:1\n"}, nil
			default:
				return ADBResult{}, nil
			}
		}
		switch {
		case post, initial:
			return ADBResult{Stdout: "package:com.example.app\n"}, nil
		default:
			return ADBResult{}, nil
		}
	}
	if len(cmd) >= 4 && cmd[0] == "shell" && cmd[1] == "cmd" && cmd[2] == "package" && cmd[3] == "query-activities" {
		r.mu.Lock()
		post := r.postInstall != nil && r.postInstall[serial]
		initial := r.initialInstalled
		r.mu.Unlock()
		if post || initial {
			return ADBResult{Stdout: "com.example.app/.TvActivity\n"}, nil
		}
		return ADBResult{}, nil
	}
	if len(cmd) == 3 && cmd[0] == "install" && cmd[1] == "-r" {
		apkPath := cmd[2]
		info, statErr := os.Stat(apkPath)

		r.mu.Lock()
		r.installCalls++
		callNumber := r.installCalls
		r.activeInstalls++
		if r.activeInstalls > r.maxActive {
			r.maxActive = r.activeInstalls
		}
		r.installPaths = append(r.installPaths, apkPath)
		if statErr == nil {
			r.installModes = append(r.installModes, info.Mode().Perm())
		} else {
			r.installModes = append(r.installModes, 0)
		}
		result, installErr := r.installResult, r.installErr
		panicInstall := r.panicInstall
		block := r.blockFirst && callNumber == 1
		started, release := r.started, r.release
		r.mu.Unlock()

		if block {
			if started != nil {
				started <- struct{}{}
			}
			if release != nil {
				select {
				case <-release:
				case <-ctx.Done():
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						installErr = &ADBError{Code: "timeout", TimedOut: true, Message: "ADB command timed out"}
					} else {
						installErr = &ADBError{Code: "canceled", Message: "ADB command was canceled"}
					}
				}
			}
		}
		if panicInstall {
			panic("simulated install panic")
		}
		if result.Stdout == "" && result.Stderr == "" && installErr == nil {
			result.Stdout = "Performing Streamed Install\nSuccess\n"
		}

		r.mu.Lock()
		if installErr == nil && strings.Contains(strings.ToLower(result.Stdout+"\n"+result.Stderr), "success") {
			if r.postInstall == nil {
				r.postInstall = map[string]bool{}
			}
			r.postInstall[serial] = true
		}
		r.activeInstalls--
		r.mu.Unlock()
		return result, installErr
	}
	return ADBResult{}, nil
}

func (r *apkInstallRunner) snapshot() (calls [][]string, installCalls, maxActive int, paths []string, modes []os.FileMode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		calls = append(calls, append([]string(nil), call...))
	}
	return calls, r.installCalls, r.maxActive, append([]string(nil), r.installPaths...), append([]os.FileMode(nil), r.installModes...)
}

func apkInstallTestServer(t *testing.T, runner *apkInstallRunner) (*Server, string, string) {
	t.Helper()
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "apk-token")
	s := testServer(t)
	s.adb.runner = runner
	s.adb.initErr = nil
	s.adbTempDir = t.TempDir()
	s.adb.cfg.APKMaxBytes = 1024 * 1024
	s.adb.cfg.InstallTimeout = 2 * time.Second

	first := addADBTestTV(t, s, "Living", "living.local")
	second := addADBTestTV(t, s, "Bedroom", "bedroom.local")
	firstSerial, secondSerial := "10.0.0.30:5555", "10.0.0.31:5555"
	if err := s.updateADBAssociation(first, &firstSerial, &firstSerial, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.updateADBAssociation(second, &secondSerial, &secondSerial, nil); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.serials = []string{firstSerial, secondSerial}
	if runner.postInstall == nil {
		runner.postInstall = map[string]bool{}
	}
	runner.mu.Unlock()
	return s, first, second
}

func fakeAPK() []byte {
	return []byte{'P', 'K', 0x03, 0x04, 'f', 'a', 'k', 'e', '-', 'a', 'p', 'k'}
}

func apkMultipartRequestPath(t *testing.T, s *Server, path, filename string, payload []byte, token string, extra bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("apk", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if extra {
		if err := writer.WriteField("extra", "not-allowed"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		out = parseJSONMap(t, w.Body.Bytes())
	}
	return w, out
}

func apkMultipartRequest(t *testing.T, s *Server, tvID, filename string, payload []byte, token string, extra bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return apkMultipartRequestPath(t, s, "/api/tvs/"+tvID+"/adb/install-apk", filename, payload, token, extra)
}

func assertTempDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary APK files remain: %#v", entries)
	}
}

func adbCode(t *testing.T, err error) string {
	t.Helper()
	var adbErr *ADBError
	if !errors.As(err, &adbErr) {
		t.Fatalf("expected ADBError, got %T %v", err, err)
	}
	return adbErr.Code
}

func TestADBAPKInstallAndUpdateStreamCleanupAndTargeting(t *testing.T) {
	for _, tc := range []struct {
		name             string
		initialInstalled bool
		wantOperation    string
	}{
		{name: "install", initialInstalled: false, wantOperation: "install"},
		{name: "update", initialInstalled: true, wantOperation: "update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &apkInstallRunner{initialInstalled: tc.initialInstalled}
			s, first, _ := apkInstallTestServer(t, runner)
			payload := fakeAPK()
			sum := sha256.Sum256(payload)

			w, out := apkMultipartRequest(t, s, first, "example.apk", payload, "apk-token", false)
			if w.Code != http.StatusOK {
				t.Fatalf("install status=%d body=%#v", w.Code, out)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("missing no-store: %#v", w.Header())
			}
			if out["sha256"] != hex.EncodeToString(sum[:]) || out["operation"] != tc.wantOperation {
				t.Fatalf("install result: %#v", out)
			}
			pkg := out["package"].(map[string]any)
			if pkg["package_id"] != "com.example.app" || pkg["version_code"] != "2" {
				t.Fatalf("refreshed package result: %#v", pkg)
			}

			_, installCalls, _, paths, modes := runner.snapshot()
			if installCalls != 1 || len(paths) != 1 || len(modes) != 1 {
				t.Fatalf("install calls=%d paths=%#v modes=%#v", installCalls, paths, modes)
			}
			if modes[0] != 0600 {
				t.Fatalf("temporary APK mode = %o", modes[0])
			}
			if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
				t.Fatalf("temporary APK retained after request: %v", err)
			}

			calls, _, _, _, _ := runner.snapshot()
			var installArgs []string
			for _, call := range calls {
				if len(call) >= 3 && call[0] == "-s" && call[2] == "install" {
					installArgs = call
				}
			}
			if len(installArgs) != 5 || installArgs[0] != "-s" || installArgs[1] != "10.0.0.30:5555" ||
				installArgs[2] != "install" || installArgs[3] != "-r" || installArgs[4] != paths[0] {
				t.Fatalf("unsafe/unexpected install args: %#v", installArgs)
			}
			if strings.Contains(strings.Join(installArgs, " "), "example.apk") {
				t.Fatalf("request filename reached ADB arguments: %#v", installArgs)
			}
			assertTempDirEmpty(t, s.adbTempDir)
		})
	}
}

func TestADBAPKUploadValidationRejectsBeforeInstall(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		payload    []byte
		extra      bool
		maxBytes   int64
		wantStatus int
		wantCode   string
	}{
		{name: "wrong extension", filename: "example.zip", payload: fakeAPK(), wantStatus: 400, wantCode: "invalid_upload"},
		{name: "empty", filename: "example.apk", payload: nil, wantStatus: 400, wantCode: "invalid_apk"},
		{name: "bad signature", filename: "example.apk", payload: []byte("not-an-apk"), wantStatus: 400, wantCode: "invalid_apk"},
		{name: "extra part", filename: "example.apk", payload: fakeAPK(), extra: true, wantStatus: 400, wantCode: "invalid_upload"},
		{name: "too large", filename: "example.apk", payload: fakeAPK(), maxBytes: 8, wantStatus: 413, wantCode: "upload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &apkInstallRunner{}
			s, first, _ := apkInstallTestServer(t, runner)
			if tc.maxBytes > 0 {
				s.adb.cfg.APKMaxBytes = tc.maxBytes
			}
			w, out := apkMultipartRequest(t, s, first, tc.filename, tc.payload, "apk-token", tc.extra)
			if w.Code != tc.wantStatus || out["code"] != tc.wantCode {
				t.Fatalf("status=%d body=%#v", w.Code, out)
			}
			_, installs, _, _, _ := runner.snapshot()
			if installs != 0 {
				t.Fatalf("invalid upload reached install: %d", installs)
			}
			assertTempDirEmpty(t, s.adbTempDir)
		})
	}

	runner := &apkInstallRunner{}
	s, first, _ := apkInstallTestServer(t, runner)
	w, out := apkMultipartRequest(t, s, first, "example.apk", fakeAPK(), "", false)
	if w.Code != http.StatusUnauthorized || out["code"] != "unauthorized" {
		t.Fatalf("unauthorized upload: %d %#v", w.Code, out)
	}
	calls, installs, _, _, _ := runner.snapshot()
	if installs != 0 || len(calls) != 0 {
		t.Fatalf("unauthorized request reached ADB: %#v", calls)
	}
	assertTempDirEmpty(t, s.adbTempDir)

	for _, filename := range []string{"../evil.apk", "..\\evil.apk", "evil.apk\n--grant-all"} {
		if code := adbCode(t, validateAPKFilename(filename)); code != "invalid_upload" {
			t.Fatalf("filename %q code=%q", filename, code)
		}
	}
}

func TestADBAPKFailureMappingAndCleanup(t *testing.T) {
	mappings := []struct {
		text string
		err  error
		code string
	}{
		{text: "error: device offline", err: &ADBError{Code: "command_failed"}, code: "offline"},
		{text: "error: device unauthorized", err: &ADBError{Code: "command_failed"}, code: "unauthorized_device"},
		{text: "Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]", err: &ADBError{Code: "command_failed"}, code: "insufficient_storage"},
		{text: "Failure [INSTALL_FAILED_NO_MATCHING_ABIS]", err: &ADBError{Code: "command_failed"}, code: "incompatible_abi"},
		{text: "Failure [INSTALL_FAILED_OLDER_SDK]", err: &ADBError{Code: "command_failed"}, code: "incompatible_sdk"},
		{text: "Failure [INSTALL_PARSE_FAILED_BAD_MANIFEST]", err: &ADBError{Code: "command_failed"}, code: "malformed_apk"},
		{text: "Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE: signatures do not match]", err: &ADBError{Code: "command_failed"}, code: "signature_mismatch"},
		{text: "Failure [INSTALL_FAILED_VERSION_DOWNGRADE]", err: &ADBError{Code: "command_failed"}, code: "version_downgrade"},
		{text: "Failure [INSTALL_FAILED_INTERNAL_ERROR]", err: &ADBError{Code: "command_failed"}, code: "package_manager_failure"},
		{text: "", err: &ADBError{Code: "timeout", TimedOut: true, Message: "ADB command timed out"}, code: "timeout"},
	}
	for _, tc := range mappings {
		err := mapAPKInstallFailure(ADBResult{Stderr: tc.text}, tc.err)
		if code := adbCode(t, err); code != tc.code {
			t.Fatalf("%q mapped to %q, want %q", tc.text, code, tc.code)
		}
		if strings.Contains(err.Error(), "/tmp/") || strings.Contains(err.Error(), "apk-token") {
			t.Fatalf("failure leaked host path/credential: %v", err)
		}
	}

	for _, tc := range []struct {
		name       string
		result     ADBResult
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "package manager", result: ADBResult{Stderr: "Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]"}, err: &ADBError{Code: "command_failed"}, wantStatus: http.StatusInsufficientStorage, wantCode: "insufficient_storage"},
		{name: "timeout", err: &ADBError{Code: "timeout", TimedOut: true, Message: "ADB command timed out"}, wantStatus: http.StatusGatewayTimeout, wantCode: "timeout"},
		{name: "manager error", err: &ADBError{Code: "unavailable", Message: "ADB executable is unavailable"}, wantStatus: http.StatusServiceUnavailable, wantCode: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &apkInstallRunner{installResult: tc.result, installErr: tc.err}
			s, first, _ := apkInstallTestServer(t, runner)
			w, out := apkMultipartRequest(t, s, first, "example.apk", fakeAPK(), "apk-token", false)
			if w.Code != tc.wantStatus || out["code"] != tc.wantCode {
				t.Fatalf("status=%d body=%#v", w.Code, out)
			}
			_, installs, _, paths, _ := runner.snapshot()
			if installs != 1 || len(paths) != 1 {
				t.Fatalf("install not attempted as expected: %d %#v", installs, paths)
			}
			if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
				t.Fatalf("temporary APK retained after failure: %v", err)
			}
			assertTempDirEmpty(t, s.adbTempDir)
		})
	}
}

func TestADBAPKCancellationAndPanicCleanup(t *testing.T) {
	temp := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := streamAPKToTemp(ctx, temp, "example.apk", bytes.NewReader(fakeAPK()), 1024)
	if code := adbCode(t, err); code != "canceled" {
		t.Fatalf("cancellation code=%q", code)
	}
	assertTempDirEmpty(t, temp)

	runner := &apkInstallRunner{panicInstall: true}
	s, first, _ := apkInstallTestServer(t, runner)
	w, _ := apkMultipartRequest(t, s, first, "example.apk", fakeAPK(), "apk-token", false)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic response status=%d body=%s", w.Code, w.Body.String())
	}
	_, _, _, paths, _ := runner.snapshot()
	if len(paths) != 1 {
		t.Fatalf("panic path not recorded: %#v", paths)
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("temporary APK retained after panic: %v", err)
	}
	assertTempDirEmpty(t, s.adbTempDir)
}

func TestADBAPKPerTVSerializationAndCrossTVTargeting(t *testing.T) {
	runner := &apkInstallRunner{
		blockFirst: true,
		started:    make(chan struct{}, 1),
		release:    make(chan struct{}),
	}
	s, first, second := apkInstallTestServer(t, runner)
	makeArtifact := func(name string) adbAPKTemp {
		path := s.adbTempDir + "/" + name
		if err := os.WriteFile(path, fakeAPK(), 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(fakeAPK())
		return adbAPKTemp{Path: path, Filename: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(fakeAPK()))}
	}
	firstArtifact := makeArtifact("first.apk")
	secondArtifact := makeArtifact("second.apk")
	defer os.Remove(firstArtifact.Path)
	defer os.Remove(secondArtifact.Path)

	errs := make(chan error, 2)
	go func() {
		_, err := s.installAPKArtifact(context.Background(), first, firstArtifact)
		errs <- err
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first install did not start")
	}
	go func() {
		_, err := s.installAPKArtifact(context.Background(), first, secondArtifact)
		errs <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_, installs, maxActive, _, _ := runner.snapshot()
	if installs != 1 || maxActive != 1 {
		t.Fatalf("same-TV installs were not serialized: installs=%d maxActive=%d", installs, maxActive)
	}
	close(runner.release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("serialized install failed: %v", err)
		}
	}
	_, installs, maxActive, _, _ = runner.snapshot()
	if installs != 2 || maxActive != 1 {
		t.Fatalf("serialized completion: installs=%d maxActive=%d", installs, maxActive)
	}

	runner.mu.Lock()
	runner.blockFirst = false
	runner.postInstall[second] = false
	runner.mu.Unlock()
	thirdArtifact := makeArtifact("third.apk")
	defer os.Remove(thirdArtifact.Path)
	if _, err := s.installAPKArtifact(context.Background(), second, thirdArtifact); err != nil {
		t.Fatal(err)
	}
	calls, _, _, _, _ := runner.snapshot()
	foundFirst, foundSecond := false, false
	for _, call := range calls {
		if len(call) == 5 && call[0] == "-s" && call[2] == "install" {
			if call[1] == "10.0.0.30:5555" {
				foundFirst = true
			}
			if call[1] == "10.0.0.31:5555" {
				foundSecond = true
			}
		}
	}
	if !foundFirst || !foundSecond {
		t.Fatalf("cross-TV install targeting missing: %#v", calls)
	}
}

func TestADBAPKRetainedSubpathAndLargeBodyBound(t *testing.T) {
	runner := &apkInstallRunner{}
	s, first, _ := apkInstallTestServer(t, runner)

	w, out := apkMultipartRequestPath(t, s, "/remote/api/tvs/"+first+"/adb/install-apk", "example.apk", fakeAPK(), "apk-token", false)
	if w.Code != http.StatusOK || out["status"] != "success" {
		t.Fatalf("retained-subpath install: %d %#v", w.Code, out)
	}

	runner = &apkInstallRunner{}
	s, first, _ = apkInstallTestServer(t, runner)
	s.adb.cfg.APKMaxBytes = 64 * 1024
	large := append([]byte{'P', 'K', 0x03, 0x04}, bytes.Repeat([]byte{'x'}, 128*1024)...)
	w, out = apkMultipartRequestPath(t, s, "/remote/api/tvs/"+first+"/adb/install-apk", "large.apk", large, "apk-token", false)
	if w.Code != http.StatusRequestEntityTooLarge || out["code"] != "upload_too_large" {
		t.Fatalf("large retained-subpath upload: %d %#v", w.Code, out)
	}
	_, installs, _, _, _ := runner.snapshot()
	if installs != 0 {
		t.Fatalf("oversized upload reached ADB: %d", installs)
	}
	assertTempDirEmpty(t, s.adbTempDir)
}

func TestADBAPKClientCancellationDuringInstallCleanup(t *testing.T) {
	runner := &apkInstallRunner{
		blockFirst: true,
		started:    make(chan struct{}, 1),
	}
	s, first, _ := apkInstallTestServer(t, runner)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("apk", "example.apk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fakeAPK()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/tvs/"+first+"/adb/install-apk", &body).WithContext(ctx)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer apk-token")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("install did not reach ADB before cancellation")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled install handler did not return")
	}
	if w.Code != http.StatusRequestTimeout {
		t.Fatalf("canceled install status=%d body=%s", w.Code, w.Body.String())
	}
	out := parseJSONMap(t, w.Body.Bytes())
	if out["code"] != "canceled" {
		t.Fatalf("canceled install body=%#v", out)
	}
	_, installs, _, paths, _ := runner.snapshot()
	if installs != 1 || len(paths) != 1 {
		t.Fatalf("canceled install calls=%d paths=%#v", installs, paths)
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("temporary APK retained after client cancellation: %v", err)
	}
	assertTempDirEmpty(t, s.adbTempDir)
}

func TestADBAPKConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DROIDTV_ADB_APK_MAX_BYTES", "")
	t.Setenv("DROIDTV_ADB_INSTALL_TIMEOUT", "")
	cfg := loadADBConfig(t.TempDir())
	if cfg.APKMaxBytes != 128*1024*1024 || cfg.InstallTimeout != 5*time.Minute {
		t.Fatalf("default APK config: bytes=%d timeout=%s", cfg.APKMaxBytes, cfg.InstallTimeout)
	}

	t.Setenv("DROIDTV_ADB_APK_MAX_BYTES", "1048576")
	t.Setenv("DROIDTV_ADB_INSTALL_TIMEOUT", "45s")
	cfg = loadADBConfig(t.TempDir())
	if cfg.APKMaxBytes != 1048576 || cfg.InstallTimeout != 45*time.Second {
		t.Fatalf("override APK config: bytes=%d timeout=%s", cfg.APKMaxBytes, cfg.InstallTimeout)
	}
}

func TestADBAPKRESTMCPParityAndMCPBound(t *testing.T) {
	makeServer := func(t *testing.T, installResult ADBResult, installErr error) (*Server, *apkInstallRunner, string) {
		runner := &apkInstallRunner{installResult: installResult, installErr: installErr}
		s, first, _ := apkInstallTestServer(t, runner)
		return s, runner, first
	}

	restServer, _, restTV := makeServer(t, ADBResult{}, nil)
	w, rest := apkMultipartRequest(t, restServer, restTV, "example.apk", fakeAPK(), "apk-token", false)
	if w.Code != http.StatusOK {
		t.Fatalf("REST install: %d %#v", w.Code, rest)
	}

	mcpServer, _, mcpTV := makeServer(t, ADBResult{}, nil)
	mcp := mcpADBCall(t, mcpServer, "install_apk", map[string]any{
		"tv_id":      mcpTV,
		"filename":   "example.apk",
		"apk_base64": base64.StdEncoding.EncodeToString(fakeAPK()),
	}, "apk-token")
	if mcp["isError"] != false {
		t.Fatalf("MCP install failed: %#v", mcp)
	}
	structured := mcp["structuredContent"].(map[string]any)
	if structured["status"] != rest["status"] || structured["operation"] != rest["operation"] || structured["sha256"] != rest["sha256"] {
		t.Fatalf("REST/MCP success mismatch: REST=%#v MCP=%#v", rest, structured)
	}
	if structured["package"].(map[string]any)["package_id"] != rest["package"].(map[string]any)["package_id"] {
		t.Fatalf("REST/MCP package mismatch: REST=%#v MCP=%#v", rest, structured)
	}

	restFailServer, _, restFailTV := makeServer(t, ADBResult{Stderr: "Failure [INSTALL_FAILED_VERSION_DOWNGRADE]"}, &ADBError{Code: "command_failed"})
	w, restFail := apkMultipartRequest(t, restFailServer, restFailTV, "example.apk", fakeAPK(), "apk-token", false)
	if w.Code != http.StatusConflict || restFail["code"] != "version_downgrade" {
		t.Fatalf("REST failure: %d %#v", w.Code, restFail)
	}
	mcpFailServer, _, mcpFailTV := makeServer(t, ADBResult{Stderr: "Failure [INSTALL_FAILED_VERSION_DOWNGRADE]"}, &ADBError{Code: "command_failed"})
	mcpFail := mcpADBCall(t, mcpFailServer, "install_apk", map[string]any{
		"tv_id":      mcpFailTV,
		"filename":   "example.apk",
		"apk_base64": base64.StdEncoding.EncodeToString(fakeAPK()),
	}, "apk-token")
	if mcpFail["isError"] != true {
		t.Fatalf("MCP failure unexpectedly succeeded: %#v", mcpFail)
	}
	mcpErr := mcpFail["structuredContent"].(map[string]any)["error"].(map[string]any)
	if mcpErr["code"] != restFail["code"] {
		t.Fatalf("REST/MCP failure mismatch: REST=%#v MCP=%#v", restFail, mcpErr)
	}

	tooLarge := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxMCPAPKBytes)+5)
	_, err := mcpServer.installAPKBase64(context.Background(), mcpTV, "example.apk", tooLarge)
	if code := adbCode(t, err); code != "upload_too_large" {
		t.Fatalf("MCP decoded-size bound code=%q", code)
	}
}
