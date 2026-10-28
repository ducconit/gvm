package goversion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeAndCompareSemver(t *testing.T) {
	if got := Normalize("go1.27.3"); got != "1.27.3" {
		t.Fatalf("Normalize(go1.27.3) = %q, want 1.27.3", got)
	}
	if got := ToGoTag("1.27.3"); got != "go1.27.3" {
		t.Fatalf("ToGoTag(1.27.3) = %q, want go1.27.3", got)
	}

	cases := []struct {
		a, b string
		want int
	}{
		{"1.27.3", "1.27.3", 0},
		{"1.27.3", "1.27.2", 1},
		{"1.26.9", "1.27.0", -1},
		{"1.27.0", "1.27rc1", 1},
		{"1.27rc1", "1.27beta2", 1},
	}
	for _, tc := range cases {
		if got := CompareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestResolveRemoteVersion_And_FromList(t *testing.T) {
	releases := []Release{
		{
			Version: "go1.27.1",
			Stable:  true,
			Files: []ReleaseFile{
				{Filename: "go1.27.1.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", Kind: "archive"},
			},
		},
		{
			Version: "go1.27.3",
			Stable:  true,
			Files: []ReleaseFile{
				{Filename: "go1.27.3.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", Kind: "archive"},
				{Filename: "go1.27.3.windows-amd64.zip", OS: "windows", Arch: "amd64", Kind: "archive"},
				{Filename: "go1.27.3.darwin-arm64.tar.gz", OS: "darwin", Arch: "arm64", Kind: "archive"},
			},
		},
		{
			Version: "go1.28rc1",
			Stable:  false,
			Files: []ReleaseFile{
				{Filename: "go1.28rc1.linux-amd64.tar.gz", OS: "linux", Arch: "amd64", Kind: "archive"},
			},
		},
	}

	// Prefix "1.27" -> resolves to highest stable "go1.27.3"
	rel, rf, err := ResolveRemoteVersion("1.27", releases, "linux", "amd64")
	if err != nil {
		t.Fatalf("ResolveRemoteVersion(1.27): %v", err)
	}
	if rel.Version != "go1.27.3" || rf.Filename != "go1.27.3.linux-amd64.tar.gz" {
		t.Fatalf("unexpected resolution: %+v / %+v", rel, rf)
	}

	// "latest" -> resolves to highest stable "go1.27.3", skipping unstable "go1.28rc1"
	relLatest, _, err := ResolveRemoteVersion("latest", releases, "darwin", "arm64")
	if err != nil || relLatest.Version != "go1.27.3" {
		t.Fatalf("ResolveRemoteVersion(latest) = %v, err=%v", relLatest, err)
	}

	// Exact prerelease "1.28rc1" -> resolves to "go1.28rc1"
	relRC, _, err := ResolveRemoteVersion("1.28rc1", releases, "linux", "amd64")
	if err != nil || relRC.Version != "go1.28rc1" {
		t.Fatalf("ResolveRemoteVersion(1.28rc1) = %v, err=%v", relRC, err)
	}

	// Local list resolution
	v, err := ResolveFromList("1.27", []string{"1.26.4", "1.27.1", "1.27.3"})
	if err != nil || v != "1.27.3" {
		t.Fatalf("ResolveFromList(1.27) = %q, err=%v", v, err)
	}
}

func TestHTTPClient_FetchAndDownloadWithChecksum(t *testing.T) {
	payload := []byte("fake-archive-binary-content")
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl/":
			_ = json.NewEncoder(w).Encode([]Release{
				{
					Version: "go1.27.3",
					Stable:  true,
					Files: []ReleaseFile{
						{
							Filename: "go1.27.3.linux-amd64.tar.gz",
							OS:       "linux",
							Arch:     "amd64",
							Version:  "go1.27.3",
							SHA256:   sumHex,
							Size:     int64(len(payload)),
							Kind:     "archive",
						},
					},
				},
			})
		case "/dl/go1.27.3.linux-amd64.tar.gz":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := &HTTPClient{
		Client:          srv.Client(),
		ReleasesAPIURL:  srv.URL + "/dl/?mode=json&include=all",
		DownloadBaseURL: srv.URL + "/dl/",
	}

	ctx := t.Context()
	releases, err := client.FetchReleases(ctx, true)
	if err != nil || len(releases) != 1 {
		t.Fatalf("FetchReleases: len=%d, err=%v", len(releases), err)
	}

	dest := filepath.Join(t.TempDir(), "go1.27.3.linux-amd64.tar.gz")
	var progressBuf bytes.Buffer
	if err := client.DownloadArchive(ctx, "go1.27.3.linux-amd64.tar.gz", dest, sumHex, &progressBuf); err != nil {
		t.Fatalf("DownloadArchive valid checksum failed: %v", err)
	}

	// Verify checksum mismatch is caught and cleans up temp file
	badDest := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	err = client.DownloadArchive(ctx, "go1.27.3.linux-amd64.tar.gz", badDest, "0000000000000000000000000000000000000000000000000000000000000000", nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}
	if _, statErr := os.Stat(badDest); !os.IsNotExist(statErr) {
		t.Fatalf("expected corrupt file not to exist at %s", badDest)
	}
}
