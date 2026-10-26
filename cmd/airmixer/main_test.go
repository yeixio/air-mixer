package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "airmixer dev ") {
		t.Errorf("version output %q", out.String())
	}
}

func TestServerBadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  web_port: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"start", "--config", path})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "web_port") {
		t.Errorf("Execute = %v", err)
	}
}
