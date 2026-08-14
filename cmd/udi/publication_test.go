package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishDataAndSite(t *testing.T) {
	root := t.TempDir()
	stage, err := os.MkdirTemp(root, ".refresh-")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "data", "latest.json"), "old-data")
	writeTestFile(t, filepath.Join(root, "dist", "index.html"), "old-site")
	writeTestFile(t, filepath.Join(stage, "data", "latest.json"), "new-data")
	writeTestFile(t, filepath.Join(stage, "dist", "index.html"), "new-site")
	if err := publishDataAndSite(root, filepath.Join(root, "dist"), stage); err != nil {
		t.Fatalf("publishDataAndSite() error = %v", err)
	}
	assertTestFile(t, filepath.Join(root, "data", "latest.json"), "new-data")
	assertTestFile(t, filepath.Join(root, "dist", "index.html"), "new-site")
}

func TestPublishDataRestoresCurrentWhenReplacementMissing(t *testing.T) {
	root := t.TempDir()
	stage, err := os.MkdirTemp(root, ".refresh-")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "data", "latest.json"), "old-data")
	if err := publishData(root, stage); err == nil {
		t.Fatal("publishData() error = nil, want missing replacement error")
	}
	assertTestFile(t, filepath.Join(root, "data", "latest.json"), "old-data")
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertTestFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
