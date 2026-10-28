package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdater_SelfUpdateLatestAndSpecificVersion(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	exePath := filepath.Join(tmpDir, "g")
	if err := os.WriteFile(exePath, []byte("old-binary-v1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	v2Payload := []byte("new-binary-v2.0.0")
	v2Sum := sha256.Sum256(v2Payload)
	v2Hex := hex.EncodeToString(v2Sum[:])

	v15Payload := []byte("specific-binary-v1.5.0")
	v15Sum := sha256.Sum256(v15Payload)
	v15Hex := hex.EncodeToString(v15Sum[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_ = json.NewEncoder(w).Encode(CLIRelease{
				TagName: "v2.0.0",
				Assets: []ReleaseAsset{
					{
						Name:               "g-linux-amd64",
						BrowserDownloadURL: srv.URL + "/download/g-v2.0.0",
						SHA256:             v2Hex,
					},
				},
			})
		case "/releases/tags/v1.5.0":
			_ = json.NewEncoder(w).Encode(CLIRelease{
				TagName: "v1.5.0",
				Assets: []ReleaseAsset{
					{
						Name:               "g-linux-amd64",
						BrowserDownloadURL: srv.URL + "/download/g-v1.5.0",
						SHA256:             v15Hex,
					},
				},
			})
		case "/download/g-v2.0.0":
			_, _ = w.Write(v2Payload)
		case "/download/g-v1.5.0":
			_, _ = w.Write(v15Payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	upd := &Updater{
		Client:         srv.Client(),
		UpdateAPIURL:   srv.URL + "/releases",
		ExecutablePath: exePath,
		GOOS:           "linux",
		GOARCH:         "amd64",
		CurrentVersion: "1.0.0",
	}

	// 1. `g update` (latest) -> updates to 2.0.0
	ver, err := upd.SelfUpdate(ctx, "")
	if err != nil || ver != "2.0.0" {
		t.Fatalf("SelfUpdate('') = %q, err=%v", ver, err)
	}
	gotBytes, err := os.ReadFile(exePath)
	if err != nil || string(gotBytes) != string(v2Payload) {
		t.Fatalf("expected binary updated to v2.0.0, got %q", string(gotBytes))
	}

	// 2. `g update 1.5.0` (specific version) -> updates to 1.5.0
	ver2, err := upd.SelfUpdate(ctx, "1.5.0")
	if err != nil || ver2 != "1.5.0" {
		t.Fatalf("SelfUpdate('1.5.0') = %q, err=%v", ver2, err)
	}
	gotBytes2, err := os.ReadFile(exePath)
	if err != nil || string(gotBytes2) != string(v15Payload) {
		t.Fatalf("expected binary updated to v1.5.0, got %q", string(gotBytes2))
	}
}
