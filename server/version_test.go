package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientFilesUseVersionPlaceholder(t *testing.T) {
	root := filepath.Clean("..")
	versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(versionBytes))
	for _, rel := range []string{"client/sw.js", "client/app.js", "client/index.html", "client/manifest.json", "client/reset.html"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		source := string(b)
		if !strings.Contains(source, "__VERSION__") {
			t.Errorf("%s must contain __VERSION__", rel)
		}
		if strings.Contains(source, version) {
			t.Errorf("%s must not contain hard-coded version %q", rel, version)
		}
	}
}
