package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ducconit/gvm/internal/platform"
)

func seedFakeInstalledVersion(t *testing.T, cfg platform.EnvConfig, version string) {
	t.Helper()
	vDir := cfg.VersionDir(version)
	binDir := filepath.Join(vDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.GoBinaryPath(vDir), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vDir, "VERSION"), []byte("go"+version), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStore_LifecycleAndAliases(t *testing.T) {
	gvmHome := t.TempDir()
	cfg := platform.EnvConfig{
		HomeDir: gvmHome,
		GVMHome: gvmHome,
		GOOS:    "linux",
		GOARCH:  "amd64",
	}

	st, err := New(cfg)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}

	seedFakeInstalledVersion(t, cfg, "1.26.4")
	seedFakeInstalledVersion(t, cfg, "1.27.1")
	seedFakeInstalledVersion(t, cfg, "1.27.3")

	if err := st.SetActiveVersion("1.27.3"); err != nil {
		t.Fatalf("SetActiveVersion: %v", err)
	}
	active, err := st.ActiveVersion()
	if err != nil || active != "1.27.3" {
		t.Fatalf("ActiveVersion = %q, err=%v", active, err)
	}

	if err := st.SetAlias("like", "1.27"); err != nil {
		t.Fatalf("SetAlias: %v", err)
	}

	// ResolveInstalled via alias "like" -> "1.27" -> "1.27.3"
	resolved, err := st.ResolveInstalled("like")
	if err != nil || resolved != "1.27.3" {
		t.Fatalf("ResolveInstalled(like) = %q, err=%v", resolved, err)
	}

	list, err := st.ListInstalled()
	if err != nil || len(list) != 3 {
		t.Fatalf("ListInstalled len=%d, err=%v", len(list), err)
	}
	if !list[2].Active || list[2].Version != "1.27.3" || len(list[2].Aliases) != 1 {
		t.Fatalf("unexpected active entry: %+v", list[2])
	}

	if err := st.RemoveVersion("1.26.4"); err != nil {
		t.Fatalf("RemoveVersion: %v", err)
	}
	if st.IsInstalled("1.26.4") {
		t.Fatalf("expected 1.26.4 to be removed")
	}
}
