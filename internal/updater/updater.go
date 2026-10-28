package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultUpdateAPIURL points to the GitHub Releases API for the g CLI.
	// Can be overridden via GVM_UPDATE_API_URL.
	DefaultUpdateAPIURL = "https://api.github.com/repos/ducconit/gvm/releases"
)

// ReleaseAsset describes a downloadable CLI asset in a release.
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	SHA256             string `json:"sha256,omitempty"`
}

// CLIRelease describes a single release of the `g` CLI.
type CLIRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []ReleaseAsset `json:"assets"`
}

// Updater handles self-updating the `g` CLI executable (`g update` and `g update <version>`).
type Updater struct {
	Client         *http.Client
	UpdateAPIURL   string
	ExecutablePath string
	GOOS           string
	GOARCH         string
	CurrentVersion string
}

// New creates a new Updater with sensible defaults.
func New(exePath, goos, goarch, currentVersion string) *Updater {
	apiURL := strings.TrimSpace(os.Getenv("GVM_UPDATE_API_URL"))
	if apiURL == "" {
		apiURL = DefaultUpdateAPIURL
	}
	return &Updater{
		Client: &http.Client{
			Timeout: 60 * time.Second,
		},
		UpdateAPIURL:   apiURL,
		ExecutablePath: exePath,
		GOOS:           goos,
		GOARCH:         goarch,
		CurrentVersion: normalizeTag(currentVersion),
	}
}

func normalizeTag(v string) string {
	s := strings.TrimSpace(strings.ToLower(v))
	s = strings.TrimPrefix(s, "v")
	return s
}

// SelfUpdate updates the `g` CLI binary to the latest release (when targetVersion is "" or "latest")
// or to a specified version (e.g. `g update 1.2.0` or `g update v1.2.0`).
// Returns the updated version string.
func (u *Updater) SelfUpdate(ctx context.Context, targetVersion string) (string, error) {
	if u.ExecutablePath == "" {
		return "", errors.New("executable path is unknown; cannot self-update")
	}

	rel, err := u.fetchTargetRelease(ctx, targetVersion)
	if err != nil {
		return "", err
	}

	resolvedVersion := normalizeTag(rel.TagName)
	if resolvedVersion == "" {
		return "", errors.New("release response missing tag_name")
	}

	asset, err := u.selectAsset(rel.Assets)
	if err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(u.ExecutablePath), ".g-update-*")
	if err != nil {
		// Fallback to OS temp dir if executable directory is not writable
		tmpDir, err = os.MkdirTemp("", "g-update-*")
		if err != nil {
			return "", fmt.Errorf("create temp update dir: %w", err)
		}
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	downloadedFile := filepath.Join(tmpDir, asset.Name)
	if err := u.downloadAsset(ctx, asset, downloadedFile); err != nil {
		return "", err
	}

	newBinPath, err := u.extractOrPrepareBinary(downloadedFile, tmpDir)
	if err != nil {
		return "", err
	}

	if err := u.replaceExecutable(newBinPath); err != nil {
		return "", err
	}

	return resolvedVersion, nil
}

func (u *Updater) fetchTargetRelease(ctx context.Context, targetVersion string) (*CLIRelease, error) {
	base := strings.TrimRight(u.UpdateAPIURL, "/")
	q := normalizeTag(targetVersion)

	var url string
	if q == "" || q == "latest" {
		url = base + "/latest"
	} else {
		url = base + "/tags/v" + q
	}

	rel, status, err := u.getReleaseJSON(ctx, url)
	if err == nil {
		return rel, nil
	}
	// If /tags/v<version> returned 404, try /tags/<version> or listing all releases
	if status == http.StatusNotFound && q != "" && q != "latest" {
		if rel2, _, err2 := u.getReleaseJSON(ctx, base+"/tags/"+q); err2 == nil {
			return rel2, nil
		}
	}
	return nil, err
}

func (u *Updater) getReleaseJSON(ctx context.Context, url string) (*CLIRelease, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "g-cli-updater/1.0")
	req.Header.Set("Accept", "application/vnd.github+json, application/json")

	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("query CLI release %s: %w", url, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("release endpoint %s returned HTTP %d", url, resp.StatusCode)
	}

	var rel CLIRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode CLI release JSON: %w", err)
	}
	return &rel, resp.StatusCode, nil
}

func (u *Updater) selectAsset(assets []ReleaseAsset) (*ReleaseAsset, error) {
	osTarget := strings.ToLower(u.GOOS)
	archTarget := strings.ToLower(u.GOARCH)

	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		if strings.Contains(name, osTarget) && strings.Contains(name, archTarget) {
			return &assets[i], nil
		}
	}
	return nil, fmt.Errorf("no CLI binary asset found in release for platform %s/%s", u.GOOS, u.GOARCH)
}

func (u *Updater) downloadAsset(ctx context.Context, asset *ReleaseAsset, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "g-cli-updater/1.0")

	resp, err := u.Client.Do(req)
	if err != nil {
		return fmt.Errorf("download update asset %s: %w", asset.Name, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download asset %s returned HTTP %d", asset.Name, resp.StatusCode)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}

	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hasher), io.LimitReader(resp.Body, 256<<20))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}

	if asset.SHA256 != "" {
		actual := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actual, strings.TrimSpace(asset.SHA256)) {
			return fmt.Errorf("update asset SHA256 mismatch: expected %s, got %s", asset.SHA256, actual)
		}
	}
	return nil
}

func (u *Updater) extractOrPrepareBinary(downloadedFile, tmpDir string) (string, error) {
	lower := strings.ToLower(downloadedFile)
	wantBin := "g"
	if u.GOOS == "windows" {
		wantBin = "g.exe"
	}

	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		f, err := os.Open(downloadedFile)
		if err != nil {
			return "", err
		}
		defer f.Close()
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return "", err
		}
		defer gzr.Close()
		tr := tar.NewReader(gzr)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			base := filepath.Base(hdr.Name)
			if base == wantBin || base == "g" || base == "g.exe" {
				outBin := filepath.Join(tmpDir, "extracted-"+wantBin)
				outFile, err := os.OpenFile(outBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
				if err != nil {
					return "", err
				}
				_, copyErr := io.Copy(outFile, io.LimitReader(tr, 256<<20))
				_ = outFile.Close()
				if copyErr != nil {
					return "", copyErr
				}
				return outBin, nil
			}
		}
		return "", fmt.Errorf("binary %s not found inside archive %s", wantBin, filepath.Base(downloadedFile))

	case strings.HasSuffix(lower, ".zip"):
		zr, err := zip.OpenReader(downloadedFile)
		if err != nil {
			return "", err
		}
		defer zr.Close()
		for _, zf := range zr.File {
			base := filepath.Base(zf.Name)
			if base == wantBin || base == "g" || base == "g.exe" {
				rc, err := zf.Open()
				if err != nil {
					return "", err
				}
				outBin := filepath.Join(tmpDir, "extracted-"+wantBin)
				outFile, err := os.OpenFile(outBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
				if err != nil {
					_ = rc.Close()
					return "", err
				}
				_, copyErr := io.Copy(outFile, io.LimitReader(rc, 256<<20))
				_ = rc.Close()
				_ = outFile.Close()
				if copyErr != nil {
					return "", copyErr
				}
				return outBin, nil
			}
		}
		return "", fmt.Errorf("binary %s not found inside zip %s", wantBin, filepath.Base(downloadedFile))

	default:
		if err := os.Chmod(downloadedFile, 0o755); err != nil {
			return "", err
		}
		return downloadedFile, nil
	}
}

func (u *Updater) replaceExecutable(newBinPath string) error {
	targetPath := u.ExecutablePath
	stagePath := targetPath + ".new"
	_ = os.Remove(stagePath)

	// Copy to stagePath in the same directory as targetPath so os.Rename is on the same filesystem
	in, err := os.Open(newBinPath)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(stagePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		_ = in.Close()
		return fmt.Errorf("stage updated binary at %s: %w", stagePath, err)
	}
	_, copyErr := io.Copy(out, in)
	_ = in.Close()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(stagePath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(stagePath)
		return closeErr
	}

	if runtime.GOOS == "windows" {
		oldPath := targetPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(targetPath, oldPath); err != nil {
			_ = os.Remove(stagePath)
			return fmt.Errorf("rename running windows binary: %w", err)
		}
		if err := os.Rename(stagePath, targetPath); err != nil {
			_ = os.Rename(oldPath, targetPath)
			return fmt.Errorf("activate updated windows binary: %w", err)
		}
		return nil
	}

	if err := os.Rename(stagePath, targetPath); err != nil {
		_ = os.Remove(stagePath)
		return fmt.Errorf("replace executable %s: %w", targetPath, err)
	}
	return nil
}
