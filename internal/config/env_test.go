package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	t.Setenv("EXISTING_VALUE", "from-shell")
	path := filepath.Join(t.TempDir(), ".env")
	content := "# local values\nGITHUB_TOKEN='test-token'\nexport EXISTING_VALUE=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}
	if got := os.Getenv("GITHUB_TOKEN"); got != "test-token" {
		t.Fatalf("GITHUB_TOKEN = %q, want test-token", got)
	}
	if got := os.Getenv("EXISTING_VALUE"); got != "from-shell" {
		t.Fatalf("EXISTING_VALUE = %q, want from-shell", got)
	}
}

func TestLoadEnvFileRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("not a variable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := LoadEnvFile(path)
	if err == nil || !strings.Contains(err.Error(), "expected KEY=VALUE") {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}
}
