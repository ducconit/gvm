package manager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ducconit/gvm/internal/goversion"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/store"
)

type fakeRemoteClient struct {
	releases []goversion.Release
	archives map[string][]byte
}

func newFakeRemoteClient(t *testing.T, versions []string, goos, goarch string) *fakeRemoteClient {
	t.Helper()
	fc := &fakeRemoteClient{
		archives: make(map[string][]byte),
	}
	for _, v := range versions {
		ext := ".tar.gz"
		if goos == "windows" {
			ext = ".zip"
		}
		filename := "go" + v + "." + goos + "-" + goarch + ext
		archiveBytes := buildFakeGoTarGz(t, v)
		sum := sha256.Sum256(archiveBytes)
		fc.archives[filename] = archiveBytes
		fc.releases = append(fc.releases, goversion.Release{
			Version: "go" + v,
			Stable:  true,
			Files: []goversion.ReleaseFile{
				{
					Filename: filename,
					OS:       goos,
					Arch:     goarch,
					Version:  "go" + v,
					SHA256:   hex.EncodeToString(sum[:]),
					Size:     int64(len(archiveBytes)),
					Kind:     "archive",
				},
			},
		})
	}
	return fc
}

func buildFakeGoTarGz(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	files := map[string]string{
		"go/VERSION": "go" + version,
		"go/bin/go":  "#!/bin/sh\necho \"go version go" + version + " linux/amd64\"\n",
	}
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gzw.Close()
	return buf.Bytes()
}

func (f *fakeRemoteClient) FetchReleases(ctx context.Context, includeAll bool) ([]goversion.Release, error) {
	return f.releases, nil
}

func (f *fakeRemoteClient) DownloadArchive(ctx context.Context, filename, destPath, expectedSHA256 string, progress io.Writer) error {
	data, ok := f.archives[filename]
	if !ok {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0o644)
}

func TestManager_FullWorkflow(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	fakeExe := filepath.Join(tmpDir, "g-bin")
	if err := os.WriteFile(fakeExe, []byte("fake-g-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := platform.EnvConfig{
		HomeDir:        tmpDir,
		GVMHome:        filepath.Join(tmpDir, ".gvm"),
		GOOS:           "linux",
		GOARCH:         "amd64",
		ExecutablePath: fakeExe,
	}
	st, err := store.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	fc := newFakeRemoteClient(t, []string{"1.25.2", "1.26.4", "1.27.1", "1.27.3"}, "linux", "amd64")
	var out bytes.Buffer
	mgr := New(st, fc, &out)

	// 1. Set alias `g alias like 1.27`
	if err := mgr.Alias("like", "1.27"); err != nil {
		t.Fatalf("Alias(like, 1.27): %v", err)
	}

	// 2. Install via alias `g install like` -> should install 1.27.3 and auto-activate
	iv, err := mgr.Install(ctx, "like")
	if err != nil {
		t.Fatalf("Install(like): %v", err)
	}
	if iv.Version != "1.27.3" || !iv.Active {
		t.Fatalf("expected 1.27.3 active, got %+v", iv)
	}

	// 3. Install 1.26.4 and 1.25.2
	if _, err := mgr.Install(ctx, "1.26.4"); err != nil {
		t.Fatalf("Install(1.26.4): %v", err)
	}
	if _, err := mgr.Install(ctx, "1.25.2"); err != nil {
		t.Fatalf("Install(1.25.2): %v", err)
	}

	// 4. Switch active version to 1.26.4 (`g use 1.26`)
	used, err := mgr.Use(ctx, "1.26")
	if err != nil || used.Version != "1.26.4" {
		t.Fatalf("Use(1.26) = %+v, err=%v", used, err)
	}

	// 5. Switch back via alias (`g use like` -> 1.27.3)
	usedLike, err := mgr.Use(ctx, "like")
	if err != nil || usedLike.Version != "1.27.3" {
		t.Fatalf("Use(like) = %+v, err=%v", usedLike, err)
	}

	// 6. List remote versions
	remoteList, err := mgr.ListRemote(ctx, false)
	if err != nil || len(remoteList) != 4 {
		t.Fatalf("ListRemote len=%d, err=%v", len(remoteList), err)
	}

	// 7. Uninstall --all (should remove 1.25.2 and 1.26.4, keeping active 1.27.3)
	removed, err := mgr.UninstallVersion("", true)
	if err != nil || len(removed) != 2 {
		t.Fatalf("UninstallVersion(--all) removed=%v, err=%v", removed, err)
	}
	localAfterAll, err := mgr.ListLocal()
	if err != nil || len(localAfterAll) != 1 || localAfterAll[0].Version != "1.27.3" {
		t.Fatalf("expected only 1.27.3 remaining after --all, got %+v (err=%v)", localAfterAll, err)
	}

	// 8. Self-uninstall (`g uninstall`)
	if err := mgr.UninstallSelf(); err != nil {
		t.Fatalf("UninstallSelf: %v", err)
	}
	if _, err := os.Stat(cfg.GVMHome); !os.IsNotExist(err) {
		t.Fatalf("expected GVM_HOME removed, stat err=%v", err)
	}
	if _, err := os.Stat(fakeExe); !os.IsNotExist(err) {
		t.Fatalf("expected CLI binary removed, stat err=%v", err)
	}
}
