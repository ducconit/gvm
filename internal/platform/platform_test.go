package platform

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestTarGz(t *testing.T, archivePath string, entries map[string]string, symlinks map[string]string) {
	t.Helper()
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer f.Close()

	gzw := gzip.NewWriter(f)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	for name, content := range entries {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar body %s: %v", name, err)
		}
	}

	for linkName, target := range symlinks {
		hdr := &tar.Header{
			Name:     linkName,
			Typeflag: tar.TypeSymlink,
			Linkname: target,
			Mode:     0o777,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write symlink header %s: %v", linkName, err)
		}
	}
}

func createTestZip(t *testing.T, archivePath string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
}

func TestExtractArchive_TarGzAndZipSuccess(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	// 1. Test .tar.gz with leading "go/" prefix stripped
	tarPath := filepath.Join(tmpDir, "go1.27.3.linux-amd64.tar.gz")
	createTestTarGz(t, tarPath, map[string]string{
		"go/bin/go":    "#!/bin/sh\necho go1.27.3\n",
		"go/VERSION":   "go1.27.3",
		"go/src/hello": "package main",
	}, map[string]string{
		"go/bin/gofmt": "go",
	})

	destTar := filepath.Join(tmpDir, "extracted-tar")
	if err := ExtractArchive(ctx, tarPath, destTar); err != nil {
		t.Fatalf("ExtractArchive(.tar.gz) unexpected error: %v", err)
	}

	versionBytes, err := os.ReadFile(filepath.Join(destTar, "VERSION"))
	if err != nil || string(versionBytes) != "go1.27.3" {
		t.Fatalf("expected VERSION=go1.27.3, got %q (err=%v)", string(versionBytes), err)
	}

	// 2. Test .zip with leading "go/" prefix stripped
	zipPath := filepath.Join(tmpDir, "go1.27.3.windows-amd64.zip")
	createTestZip(t, zipPath, map[string]string{
		"go/bin/go.exe": "MZ-fake-go-exe",
		"go/VERSION":    "go1.27.3",
	})

	destZip := filepath.Join(tmpDir, "extracted-zip")
	if err := ExtractArchive(ctx, zipPath, destZip); err != nil {
		t.Fatalf("ExtractArchive(.zip) unexpected error: %v", err)
	}

	exeBytes, err := os.ReadFile(filepath.Join(destZip, "bin", "go.exe"))
	if err != nil || string(exeBytes) != "MZ-fake-go-exe" {
		t.Fatalf("expected go.exe content, got %q (err=%v)", string(exeBytes), err)
	}
}

func TestExtractArchive_PathTraversalRejected(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	t.Run("tar-slip relative traversal", func(t *testing.T) {
		tarPath := filepath.Join(tmpDir, "evil.tar.gz")
		createTestTarGz(t, tarPath, map[string]string{
			"go/../../evil.txt": "pwned",
		}, nil)

		err := ExtractArchive(ctx, tarPath, filepath.Join(tmpDir, "out1"))
		if err == nil || !strings.Contains(err.Error(), "traversal") {
			t.Fatalf("expected path traversal error, got: %v", err)
		}
	})

	t.Run("tar symlink escape", func(t *testing.T) {
		tarPath := filepath.Join(tmpDir, "evil-symlink.tar.gz")
		createTestTarGz(t, tarPath, nil, map[string]string{
			"go/bin/escape": "../../../etc/passwd",
		})

		err := ExtractArchive(ctx, tarPath, filepath.Join(tmpDir, "out2"))
		if err == nil || !strings.Contains(err.Error(), "escapes destination") {
			t.Fatalf("expected symlink escape error, got: %v", err)
		}
	})

	t.Run("zip-slip relative traversal", func(t *testing.T) {
		zipPath := filepath.Join(tmpDir, "evil.zip")
		createTestZip(t, zipPath, map[string]string{
			"../outside.txt": "pwned",
		})

		err := ExtractArchive(ctx, zipPath, filepath.Join(tmpDir, "out3"))
		if err == nil || !strings.Contains(err.Error(), "traversal") {
			t.Fatalf("expected zip path traversal error, got: %v", err)
		}
	})
}

func TestSwitchCurrentLink_And_FormatEnv(t *testing.T) {
	gvmHome := t.TempDir()
	v1Dir := filepath.Join(gvmHome, "versions", "1.27.3")
	v2Dir := filepath.Join(gvmHome, "versions", "1.26.1")
	if err := os.MkdirAll(filepath.Join(v1Dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(v2Dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SwitchCurrentLink(gvmHome, v1Dir); err != nil {
		t.Fatalf("SwitchCurrentLink v1: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(gvmHome, "go"))
	if err != nil || resolved != v1Dir {
		t.Fatalf("expected go -> %s, got %s (err=%v)", v1Dir, resolved, err)
	}

	// Switch to v2 atomically
	if err := SwitchCurrentLink(gvmHome, v2Dir); err != nil {
		t.Fatalf("SwitchCurrentLink v2: %v", err)
	}
	resolved2, err := filepath.EvalSymlinks(filepath.Join(gvmHome, "go"))
	if err != nil || resolved2 != v2Dir {
		t.Fatalf("expected go -> %s, got %s (err=%v)", v2Dir, resolved2, err)
	}

	if err := RemoveCurrentLink(gvmHome); err != nil {
		t.Fatalf("RemoveCurrentLink: %v", err)
	}

	cfg := EnvConfig{
		HomeDir: gvmHome,
		GVMHome: gvmHome,
		GOOS:    "linux",
		GOARCH:  "amd64",
	}
	out := FormatEnvExport(cfg, "1.27.3")
	if !strings.Contains(out, "ACTIVE_VERSION=1.27.3") || !strings.Contains(out, "export GOROOT=") {
		t.Fatalf("unexpected FormatEnvExport output:\n%s", out)
	}

	// Verify EnsureShellEnvConfigured adds GOROOT and PATH to .bashrc if missing, and is idempotent on second call
	updated, err := EnsureShellEnvConfigured(cfg)
	if err != nil || len(updated) != 1 {
		t.Fatalf("EnsureShellEnvConfigured first call: updated=%v, err=%v", updated, err)
	}
	updatedAgain, err := EnsureShellEnvConfigured(cfg)
	if err != nil || len(updatedAgain) != 0 {
		t.Fatalf("EnsureShellEnvConfigured second call should be idempotent: updated=%v, err=%v", updatedAgain, err)
	}
}
