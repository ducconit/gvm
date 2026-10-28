package mcp

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ducconit/gvm/internal/goversion"
	"github.com/ducconit/gvm/internal/manager"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/store"
)

type stubRemote struct{}

func (stubRemote) FetchReleases(ctx context.Context, includeAll bool) ([]goversion.Release, error) {
	return nil, nil
}

func (stubRemote) DownloadArchive(ctx context.Context, filename, destPath, expectedSHA256 string, progress io.Writer) error {
	return nil
}

func TestMCPServer_InitializeListAndCallTools(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	cfg := platform.EnvConfig{
		HomeDir: tmpDir,
		GVMHome: filepath.Join(tmpDir, ".gvm"),
		GOOS:    "linux",
		GOARCH:  "amd64",
	}
	st, err := store.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mgr := manager.New(st, stubRemote{}, io.Discard)
	srv := New(mgr)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"g_alias","arguments":{"name":"like","version":"1.27"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"g_version","arguments":{}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := srv.Serve(ctx, strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	respLines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(respLines) != 4 {
		t.Fatalf("expected 4 responses (notification skipped), got %d:\n%s", len(respLines), out.String())
	}
	if !strings.Contains(respLines[0], "g-mcp-server") {
		t.Fatalf("unexpected initialize response: %s", respLines[0])
	}
	if !strings.Contains(respLines[1], "g_install") {
		t.Fatalf("unexpected tools/list response: %s", respLines[1])
	}
	if !strings.Contains(respLines[2], "1.27") {
		t.Fatalf("unexpected g_alias response: %s", respLines[2])
	}
}
