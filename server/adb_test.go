package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeADBCall struct {
	path        string
	args        []string
	env         []string
	maxOutput   int64
	hasDeadline bool
}

type fakeADBRunner struct {
	calls  []fakeADBCall
	result ADBResult
	err    error
}

func (f *fakeADBRunner) Run(ctx context.Context, path string, args []string, env []string, maxOutput int64) (ADBResult, error) {
	_, ok := ctx.Deadline()
	f.calls = append(f.calls, fakeADBCall{
		path: path, args: append([]string(nil), args...), env: append([]string(nil), env...),
		maxOutput: maxOutput, hasDeadline: ok,
	})
	return f.result, f.err
}

func TestADBManagerDisabledDoesNotInvokeRunner(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "false")
	fake := &fakeADBRunner{}
	m := NewADBManager(t.TempDir(), fake)
	if m.Enabled() {
		t.Fatal("ADB must be disabled by default/config")
	}
	_, err := m.Version(context.Background())
	var adbErr *ADBError
	if !errors.As(err, &adbErr) || adbErr.Code != "disabled" {
		t.Fatalf("expected disabled error, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("runner invoked while disabled: %#v", fake.calls)
	}
}

func TestADBManagerVersionAndEnvironment(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	t.Setenv("DROIDTV_ADB_PATH", "/custom/adb")
	t.Setenv("DROIDTV_ADB_ADMIN_TOKEN", "super-secret")
	root := t.TempDir()
	fake := &fakeADBRunner{result: ADBResult{Stdout: "Android Debug Bridge version 1.0.41\nVersion 35.0.2\n"}}
	m := NewADBManager(root, fake)
	got, err := m.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Android Debug Bridge version 1.0.41" {
		t.Fatalf("unexpected version %q", got)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("got %d calls", len(fake.calls))
	}
	call := fake.calls[0]
	if call.path != "/custom/adb" || !reflect.DeepEqual(call.args, []string{"version"}) {
		t.Fatalf("unexpected call: %#v", call)
	}
	if !call.hasDeadline || call.maxOutput != defaultADBMaxOutput {
		t.Fatalf("deadline/output bound not applied: %#v", call)
	}
	env := strings.Join(call.env, "\n")
	if strings.Contains(env, "super-secret") {
		t.Fatal("administrator secret leaked into ADB process environment")
	}
	if !strings.Contains(env, "HOME="+filepath.Join(root, "data", "adb")) {
		t.Fatalf("persistent HOME missing: %s", env)
	}
	if !strings.Contains(env, "ADB_SERVER_SOCKET="+adbServerSocket) {
		t.Fatalf("isolated ADB server socket missing: %s", env)
	}
}

func TestADBManagerRequiresExplicitSerial(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	fake := &fakeADBRunner{}
	m := NewADBManager(t.TempDir(), fake)
	if _, err := m.runDevice(context.Background(), "", "shell", "getprop"); err == nil {
		t.Fatal("expected empty target rejection")
	}
	if _, err := m.runDevice(context.Background(), "-d", "shell", "getprop"); err == nil {
		t.Fatal("expected option-like target rejection")
	}
	_, err := m.runDevice(context.Background(), "living-room:5555", "shell", "getprop", "ro.product.model")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-s", "living-room:5555", "shell", "getprop", "ro.product.model"}
	if !reflect.DeepEqual(fake.calls[0].args, want) {
		t.Fatalf("args = %#v, want %#v", fake.calls[0].args, want)
	}
}

func TestADBHomePersistsAcrossManagerReconstruction(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	root := t.TempDir()
	m1 := NewADBManager(root, &fakeADBRunner{})
	if m1.initErr != nil {
		t.Fatal(m1.initErr)
	}
	key := filepath.Join(m1.Home(), ".android", "adbkey")
	if err := os.WriteFile(key, []byte("test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	m2 := NewADBManager(root, &fakeADBRunner{})
	if m2.Home() != m1.Home() {
		t.Fatalf("home changed: %q != %q", m2.Home(), m1.Home())
	}
	data, err := os.ReadFile(filepath.Join(m2.Home(), ".android", "adbkey"))
	if err != nil || string(data) != "test-key" {
		t.Fatalf("persistent key not reused: %q, %v", data, err)
	}
	info, err := os.Stat(filepath.Join(m2.Home(), ".android"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("android home permissions too broad: %o", info.Mode().Perm())
	}
}

func TestLimitedBufferBoundsOutput(t *testing.T) {
	b := newLimitedBuffer(5)
	n, err := b.Write([]byte("123456789"))
	if err != nil || n != 9 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if b.String() != "12345" || !b.truncated {
		t.Fatalf("buffer=%q truncated=%v", b.String(), b.truncated)
	}
}

func TestADBErrorDoesNotIncludeArguments(t *testing.T) {
	t.Setenv("DROIDTV_ADB_ENABLED", "true")
	fake := &fakeADBRunner{err: &ADBError{Code: "command_failed", ExitCode: 1, Message: "ADB command failed with exit status 1"}}
	m := NewADBManager(t.TempDir(), fake)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := m.runHost(ctx, "pair", "host:1234", "123456")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "123456") || strings.Contains(err.Error(), "host:1234") {
		t.Fatalf("sensitive arguments leaked in error: %v", err)
	}
}
