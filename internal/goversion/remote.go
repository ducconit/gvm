package goversion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultReleasesAPIURL is the official Go downloads JSON feed.
	DefaultReleasesAPIURL = "https://go.dev/dl/?mode=json&include=all"
	// DefaultDownloadBaseURL is the base URL for downloading official Go archives.
	DefaultDownloadBaseURL = "https://go.dev/dl/"
)

// Release represents a single Go release entry from https://go.dev/dl/?mode=json.
type Release struct {
	Version string        `json:"version"`
	Stable  bool          `json:"stable"`
	Files   []ReleaseFile `json:"files"`
}

// ReleaseFile represents a single downloadable artifact for a Go release.
type ReleaseFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"` // "archive", "installer", "source"
}

// RemoteClient defines the consumer contract for querying and downloading Go releases.
type RemoteClient interface {
	FetchReleases(ctx context.Context, includeAll bool) ([]Release, error)
	DownloadArchive(ctx context.Context, filename, destPath, expectedSHA256 string, progress io.Writer) error
}

// HTTPClient implements RemoteClient against go.dev/dl (or a custom mirror).
type HTTPClient struct {
	Client          *http.Client
	ReleasesAPIURL  string
	DownloadBaseURL string
}

// NewHTTPClient creates a tuned HTTPClient for querying and downloading Go distributions.
func NewHTTPClient() *HTTPClient {
	apiURL := strings.TrimSpace(os.Getenv("GVM_GO_API_URL"))
	if apiURL == "" {
		apiURL = DefaultReleasesAPIURL
	}
	dlBase := strings.TrimSpace(os.Getenv("GVM_GO_DL_BASE_URL"))
	if dlBase == "" {
		dlBase = DefaultDownloadBaseURL
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &HTTPClient{
		Client: &http.Client{
			Transport: transport,
		},
		ReleasesAPIURL:  apiURL,
		DownloadBaseURL: dlBase,
	}
}

// FetchReleases retrieves the list of Go releases from go.dev/dl.
func (c *HTTPClient) FetchReleases(ctx context.Context, includeAll bool) ([]Release, error) {
	url := c.ReleasesAPIURL
	if !includeAll && strings.Contains(url, "&include=all") {
		url = strings.ReplaceAll(url, "&include=all", "")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "g-cli/1.0 (https://github.com/ducconit/gvm)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch go releases from %s: %w", url, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch go releases failed with HTTP %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode go releases JSON: %w", err)
	}
	return releases, nil
}

// DownloadArchive downloads a release archive to destPath and verifies its SHA256 checksum.
func (c *HTTPClient) DownloadArchive(ctx context.Context, filename, destPath, expectedSHA256 string, progress io.Writer) error {
	base := strings.TrimRight(c.DownloadBaseURL, "/")
	url := base + "/" + strings.TrimLeft(filename, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "g-cli/1.0 (https://github.com/ducconit/gvm)")

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s failed with HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	tmpPath := destPath + ".part"
	outFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp download file: %w", err)
	}

	hasher := sha256.New()
	mw := io.MultiWriter(outFile, hasher)

	var copyErr error
	if progress != nil && resp.ContentLength > 0 {
		pr := &progressReader{
			r:     resp.Body,
			total: resp.ContentLength,
			out:   progress,
			label: filename,
		}
		_, copyErr = io.Copy(mw, pr)
		fmt.Fprintln(progress)
	} else {
		_, copyErr = io.Copy(mw, resp.Body)
	}

	closeErr := outFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write archive %s: %w", filename, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close archive %s: %w", filename, closeErr)
	}

	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA256 != "" && !strings.EqualFold(actualSHA256, strings.TrimSpace(expectedSHA256)) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("SHA256 checksum mismatch for %s: expected %s, got %s", filename, expectedSHA256, actualSHA256)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("finalize archive %s: %w", destPath, err)
	}
	return nil
}

type progressReader struct {
	r       io.Reader
	total   int64
	current int64
	lastPct int64
	out     io.Writer
	label   string
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.current += int64(n)
		pct := (p.current * 100) / p.total
		if pct >= p.lastPct+10 || p.current == p.total {
			p.lastPct = pct
			fmt.Fprintf(p.out, "\rDownloading %s... %d%% (%d/%d MB)", p.label, pct, p.current/(1<<20), p.total/(1<<20))
		}
	}
	return n, err
}
