package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ducconit/gvm/internal/goversion"
	"github.com/ducconit/gvm/internal/manager"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/store"
	"github.com/ducconit/gvm/internal/updater"
)

type cliTestRemote struct {
	releases []goversion.Release
	archives map[string][]byte
}

func newCLITestRemote(t *testing.T, versions []string) *cliTestRemote {
	t.Helper()
	r := &cliTestRemote{
		archives: make(map[string][]byte),
	}
	for _, v := range versions {
		filename := "go" + v + ".linux-amd64.tar.gz"
		data := makeFakeGoTarGz(t, v)
		sum := sha256.Sum256(data)
		r.archives[filename] = data
		r.releases = append(r.releases, goversion.Release{
			Version: "go" + v,
			Stable:  true,
			Files: []goversion.ReleaseFile{
				{
					Filename: filename,
					OS:       "linux",
					Arch:     "amd64",
					Version:  "go" + v,
					SHA256:   hex.EncodeToString(sum[:]),
					Size:     int64(len(data)),
					Kind:     "archive",
				},
			},
		})
	}
	return r
}

func makeFakeGoTarGz(t *testing.T, version string) []byte {
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

func (r *cliTestRemote) FetchReleases(ctx context.Context, includeAll bool) ([]goversion.Release, error) {
	return r.releases, nil
}

func (r *cliTestRemote) DownloadArchive(ctx context.Context, filename, destPath, expectedSHA256 string, progress io.Writer) error {
	b, ok := r.archives[filename]
	if !ok {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, b, 0o644)
}

func TestCLI_AllCommandsEndToEnd(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	fakeExe := filepath.Join(tmpDir, "g")
	if err := os.WriteFile(fakeExe, []byte("g-v1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Setup mock update server for `g update` and `g update <version>`
	var updateSrv *httptest.Server
	updateSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(updater.CLIRelease{
				TagName: "v1.1.0",
				Assets: []updater.ReleaseAsset{
					{Name: "g-linux-amd64", BrowserDownloadURL: updateSrv.URL + "/bin/1.1.0"},
				},
			})
		case "/releases/tags/v1.0.5":
			_ = json.NewEncoder(w).Encode(updater.CLIRelease{
				TagName: "v1.0.5",
				Assets: []updater.ReleaseAsset{
					{Name: "g-linux-amd64", BrowserDownloadURL: updateSrv.URL + "/bin/1.0.5"},
				},
			})
		case "/bin/1.1.0":
			_, _ = w.Write([]byte("g-v1.1.0"))
		case "/bin/1.0.5":
			_, _ = w.Write([]byte("g-v1.0.5"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer updateSrv.Close()

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
	remote := newCLITestRemote(t, []string{"1.25.0", "1.26.4", "1.27.1", "1.27.3"})

	runCLI := func(stdin string, args ...string) (string, error) {
		var out, errOut bytes.Buffer
		mgr := manager.New(st, remote, &out)
		upd := &updater.Updater{
			Client:         updateSrv.Client(),
			UpdateAPIURL:   updateSrv.URL + "/releases",
			ExecutablePath: fakeExe,
			GOOS:           "linux",
			GOARCH:         "amd64",
			CurrentVersion: "1.0.0",
		}
		deps := &Dependencies{
			Config:  cfg,
			Store:   st,
			Remote:  remote,
			Manager: mgr,
			Updater: upd,
			In:      strings.NewReader(stdin),
			Out:     &out,
			ErrOut:  &errOut,
		}
		err := Execute(ctx, args, deps)
		return out.String(), err
	}

	// 1. Help (`g -h`, `g --help`, `g help`)
	for _, helpArgs := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		out, err := runCLI("", helpArgs...)
		if err != nil || !strings.Contains(out, "cross-platform Golang version manager") {
			t.Fatalf("help %v failed: err=%v, out=%s", helpArgs, err, out)
		}
	}

	// 2. Alias (`g alias like 1.27`)
	out, err := runCLI("", "alias", "like", "1.27")
	if err != nil || !strings.Contains(out, `"like" -> "1.27"`) {
		t.Fatalf("g alias like 1.27 failed: err=%v, out=%s", err, out)
	}

	// 3. Install via `g install <version>` and `g i <version>`
	out, err = runCLI("", "install", "like")
	if err != nil || !strings.Contains(out, "Successfully installed Go 1.27.3") {
		t.Fatalf("g install like failed: err=%v, out=%s", err, out)
	}
	out, err = runCLI("", "i", "1.26.4")
	if err != nil || !strings.Contains(out, "Successfully installed Go 1.26.4") {
		t.Fatalf("g i 1.26.4 failed: err=%v, out=%s", err, out)
	}
	out, err = runCLI("", "i", "1.25.0")
	if err != nil || !strings.Contains(out, "Successfully installed Go 1.25.0") {
		t.Fatalf("g i 1.25.0 failed: err=%v, out=%s", err, out)
	}

	// 4. Version (`g -v` and `g version`)
	for _, vArgs := range [][]string{{"-v"}, {"version"}} {
		out, err = runCLI("", vArgs...)
		if err != nil || !strings.Contains(out, "g version") || !strings.Contains(out, "1.27.3") {
			t.Fatalf("g %v failed: err=%v, out=%s", vArgs, err, out)
		}
	}

	// 5. List local (`g ls`) and remote (`g ls -r`, `g ls --remote`)
	out, err = runCLI("", "ls")
	if err != nil || !strings.Contains(out, "1.27.3") || !strings.Contains(out, "like->1.27") {
		t.Fatalf("g ls failed: err=%v, out=%s", err, out)
	}
	for _, rFlag := range []string{"-r", "--remote"} {
		out, err = runCLI("", "ls", rFlag)
		if err != nil || !strings.Contains(out, "Official Go releases") || !strings.Contains(out, "1.27.3") {
			t.Fatalf("g ls %s failed: err=%v, out=%s", rFlag, err, out)
		}
	}

	// 6. Use (`g use 1.26.4` then `g use like`)
	out, err = runCLI("", "use", "1.26.4")
	if err != nil || !strings.Contains(out, "Now using Go 1.26.4") {
		t.Fatalf("g use 1.26.4 failed: err=%v, out=%s", err, out)
	}
	out, err = runCLI("", "use", "like")
	if err != nil || !strings.Contains(out, "Now using Go 1.27.3") {
		t.Fatalf("g use like failed: err=%v, out=%s", err, out)
	}

	// 7. Env (`g env`)
	out, err = runCLI("", "env")
	if err != nil || !strings.Contains(out, "ACTIVE_VERSION=1.27.3") || !strings.Contains(out, "GOROOT=") {
		t.Fatalf("g env failed: err=%v, out=%s", err, out)
	}

	// 8. Skills install (`g skills install`, `g skills install --ide antigravity`, `g skills install --path ...`)
	out, err = runCLI("", "skills", "install")
	if err != nil || !strings.Contains(out, filepath.Join(tmpDir, ".agents", "skills", "g")) {
		t.Fatalf("g skills install default failed: err=%v, out=%s", err, out)
	}
	out, err = runCLI("", "skills", "install", "--ide", "antigravity")
	if err != nil || !strings.Contains(out, filepath.Join(tmpDir, ".gemini", "config", "skills", "g")) {
		t.Fatalf("g skills install --ide antigravity failed: err=%v, out=%s", err, out)
	}
	customSkillDir := filepath.Join(tmpDir, "custom-skills")
	out, err = runCLI("", "skills", "install", "--path", customSkillDir)
	if err != nil || !strings.Contains(out, filepath.Join(customSkillDir, "g")) {
		t.Fatalf("g skills install --path failed: err=%v, out=%s", err, out)
	}

	// 9. MCP server (`g mcp`)
	out, err = runCLI(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n", "mcp")
	if err != nil || !strings.Contains(out, "g-mcp-server") {
		t.Fatalf("g mcp failed: err=%v, out=%s", err, out)
	}

	// 10. Update (`g update` and `g update 1.0.5`)
	out, err = runCLI("", "update")
	if err != nil || !strings.Contains(out, "v1.1.0") {
		t.Fatalf("g update failed: err=%v, out=%s", err, out)
	}
	out, err = runCLI("", "update", "1.0.5")
	if err != nil || !strings.Contains(out, "v1.0.5") {
		t.Fatalf("g update 1.0.5 failed: err=%v, out=%s", err, out)
	}

	// 11. Uninstall specific Go version (`g uninstall 1.25.0`)
	out, err = runCLI("", "uninstall", "1.25.0")
	if err != nil || !strings.Contains(out, "Uninstalled Go 1.25.0") {
		t.Fatalf("g uninstall 1.25.0 failed: err=%v, out=%s", err, out)
	}

	// 12. Uninstall --all (`g uninstall --all` removes 1.26.4, keeps active 1.27.3)
	out, err = runCLI("", "uninstall", "--all")
	if err != nil || !strings.Contains(out, "Uninstalled Go 1.26.4") {
		t.Fatalf("g uninstall --all failed: err=%v, out=%s", err, out)
	}
	if !st.IsInstalled("1.27.3") {
		t.Fatalf("expected active 1.27.3 to remain installed after g uninstall --all")
	}

	// 13. Uninstall self (`g uninstall`)
	out, err = runCLI("", "uninstall")
	if err != nil || !strings.Contains(out, "Uninstalled g CLI") {
		t.Fatalf("g uninstall (self) failed: err=%v, out=%s", err, out)
	}
}
