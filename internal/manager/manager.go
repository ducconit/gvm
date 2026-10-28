package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/ducconit/gvm/internal/goversion"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/store"
)

// RemoteVersionInfo represents an official Go release available for the current platform.
type RemoteVersionInfo struct {
	Version   string   `json:"version"`
	Stable    bool     `json:"stable"`
	Installed bool     `json:"installed"`
	Active    bool     `json:"active"`
	Aliases   []string `json:"aliases,omitempty"`
}

// Manager orchestrates version installation, activation, aliasing, listing, and uninstallation.
type Manager struct {
	store  *store.Store
	remote goversion.RemoteClient
	out    io.Writer
}

// New creates a new Manager instance.
func New(st *store.Store, remote goversion.RemoteClient, out io.Writer) *Manager {
	if out == nil {
		out = io.Discard
	}
	return &Manager{
		store:  st,
		remote: remote,
		out:    out,
	}
}

// Store returns the underlying local store.
func (m *Manager) Store() *store.Store {
	return m.store
}

// Config returns the platform configuration.
func (m *Manager) Config() platform.EnvConfig {
	return m.store.Config()
}

// Install resolves versionSpec (alias, prefix like "1.27", "latest", or exact version),
// downloads the official archive for GOOS/GOARCH, verifies SHA256, extracts it into
// $GVM_HOME/versions/<version>, and sets it as active if no Go version is currently active.
func (m *Manager) Install(ctx context.Context, versionSpec string) (*store.InstalledVersion, error) {
	if err := m.store.EnsureDirs(); err != nil {
		return nil, err
	}

	expandedSpec, err := m.store.ResolveAlias(versionSpec)
	if err != nil {
		return nil, err
	}

	// If an exact 3-part version (e.g. "1.27.3") is already installed locally, return it fast without network call
	// unless the user specified a prefix ("1.27") or "latest" which requires checking remote releases.
	releases, err := m.remote.FetchReleases(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("query official Go releases: %w", err)
	}

	cfg := m.store.Config()
	rel, rf, err := goversion.ResolveRemoteVersion(expandedSpec, releases, cfg.GOOS, cfg.GOARCH)
	if err != nil {
		return nil, err
	}

	targetVersion := goversion.Normalize(rel.Version)
	targetDir := cfg.VersionDir(targetVersion)

	if !m.store.IsInstalled(targetVersion) {
		archiveDest := filepath.Join(cfg.TempDir(), rf.Filename)
		defer func() { _ = os.Remove(archiveDest) }()

		if err := m.remote.DownloadArchive(ctx, rf.Filename, archiveDest, rf.SHA256, m.out); err != nil {
			return nil, err
		}

		// Extract to staging dir first, then atomically rename into versions/<targetVersion>
		stagingDir := filepath.Join(cfg.TempDir(), "extract-"+targetVersion)
		_ = os.RemoveAll(stagingDir)
		defer func() { _ = os.RemoveAll(stagingDir) }()

		fmt.Fprintf(m.out, "Extracting %s to %s...\n", rf.Filename, targetDir)
		if err := platform.ExtractArchive(ctx, archiveDest, stagingDir); err != nil {
			return nil, fmt.Errorf("extract %s: %w", rf.Filename, err)
		}

		_ = os.RemoveAll(targetDir)
		if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(stagingDir, targetDir); err != nil {
			return nil, fmt.Errorf("finalize installation directory %s: %w", targetDir, err)
		}
	} else {
		fmt.Fprintf(m.out, "Go %s is already installed at %s\n", targetVersion, targetDir)
	}

	// Activate automatically if no active version is set yet (first installed version becomes default)
	active, _ := m.store.ActiveVersion()
	if active == "" {
		if err := m.store.SetActiveVersion(targetVersion); err != nil {
			return nil, err
		}
		active = targetVersion
	}
	if updatedFiles, _ := platform.EnsureShellEnvConfigured(cfg); len(updatedFiles) > 0 {
		fmt.Fprintf(m.out, "Added GOROOT (%s) and PATH to: %v\n", cfg.CurrentDir(), updatedFiles)
	}

	return &store.InstalledVersion{
		Version: targetVersion,
		Path:    targetDir,
		Active:  active == targetVersion,
	}, nil
}

// Use switches the active default Go version to versionSpec (installed version, prefix, or alias).
func (m *Manager) Use(ctx context.Context, versionSpec string) (*store.InstalledVersion, error) {
	resolved, err := m.store.ResolveInstalled(versionSpec)
	if err != nil {
		return nil, fmt.Errorf("%w (hint: run 'g install %s' first)", err, versionSpec)
	}
	if err := m.store.SetActiveVersion(resolved); err != nil {
		return nil, err
	}
	cfg := m.store.Config()
	if updatedFiles, _ := platform.EnsureShellEnvConfigured(cfg); len(updatedFiles) > 0 {
		fmt.Fprintf(m.out, "Added GOROOT (%s) and PATH to: %v\n", cfg.CurrentDir(), updatedFiles)
	}
	return &store.InstalledVersion{
		Version: resolved,
		Path:    cfg.VersionDir(resolved),
		Active:  true,
	}, nil
}

// Alias sets a named alias for a version or prefix (e.g. `g alias like 1.27`).
func (m *Manager) Alias(name, targetSpec string) error {
	return m.store.SetAlias(name, targetSpec)
}

// ListLocal returns all locally installed Go versions with active status, aliases, and directory path.
func (m *Manager) ListLocal() ([]store.InstalledVersion, error) {
	return m.store.ListInstalled()
}

// ListRemote queries official Go releases for the current platform and annotates installed/active/alias status.
func (m *Manager) ListRemote(ctx context.Context, includeUnstable bool) ([]RemoteVersionInfo, error) {
	releases, err := m.remote.FetchReleases(ctx, true)
	if err != nil {
		return nil, err
	}

	cfg := m.store.Config()
	installedList, _ := m.store.ListInstalled()
	installedMap := make(map[string]store.InstalledVersion, len(installedList))
	for _, iv := range installedList {
		installedMap[iv.Version] = iv
	}
	aliases, _ := m.store.GetAliases()

	// Sort releases ascending by semver so newest versions appear at the bottom (easy to read in terminal)
	sorted := slices.Clone(releases)
	slices.SortFunc(sorted, func(a, b goversion.Release) int {
		return goversion.CompareSemver(a.Version, b.Version)
	})

	var availableVersions []string
	for _, rel := range sorted {
		if !includeUnstable && !rel.Stable {
			continue
		}
		hasPlatformArchive := false
		for _, f := range rel.Files {
			if f.Kind == "archive" && f.OS == cfg.GOOS && f.Arch == cfg.GOARCH {
				hasPlatformArchive = true
				break
			}
		}
		if hasPlatformArchive {
			availableVersions = append(availableVersions, goversion.Normalize(rel.Version))
		}
	}

	aliasByRemoteVersion := make(map[string][]string)
	for aliasName, targetSpec := range aliases {
		if resolved, err := goversion.ResolveFromList(targetSpec, availableVersions); err == nil {
			aliasByRemoteVersion[resolved] = append(aliasByRemoteVersion[resolved], fmt.Sprintf("%s->%s", aliasName, targetSpec))
		}
	}
	for v := range aliasByRemoteVersion {
		slices.Sort(aliasByRemoteVersion[v])
	}

	result := make([]RemoteVersionInfo, 0, len(availableVersions))
	for _, rel := range sorted {
		if !includeUnstable && !rel.Stable {
			continue
		}
		hasPlatformArchive := false
		for _, f := range rel.Files {
			if f.Kind == "archive" && f.OS == cfg.GOOS && f.Arch == cfg.GOARCH {
				hasPlatformArchive = true
				break
			}
		}
		if !hasPlatformArchive {
			continue
		}
		v := goversion.Normalize(rel.Version)
		iv, isInst := installedMap[v]
		result = append(result, RemoteVersionInfo{
			Version:   v,
			Stable:    rel.Stable,
			Installed: isInst,
			Active:    iv.Active,
			Aliases:   aliasByRemoteVersion[v],
		})
	}
	return result, nil
}

// UninstallVersion removes either a specific installed Go version (by exact version, prefix, or alias)
// OR, when allExceptActive is true, removes all installed Go versions except the currently active version.
func (m *Manager) UninstallVersion(versionSpec string, allExceptActive bool) ([]string, error) {
	installed, err := m.store.ListInstalled()
	if err != nil {
		return nil, err
	}
	if len(installed) == 0 {
		return nil, errors.New("no Go versions are currently installed")
	}

	if allExceptActive {
		active, _ := m.store.ActiveVersion()
		// If the user also provided a specific version/alias alongside --all, keep that version or the active version
		keepVersion := active
		if versionSpec != "" {
			if resolved, err := m.store.ResolveInstalled(versionSpec); err == nil {
				keepVersion = resolved
			}
		}
		var removed []string
		for _, iv := range installed {
			if iv.Version == keepVersion {
				continue
			}
			if err := m.store.RemoveVersion(iv.Version); err != nil {
				return removed, err
			}
			removed = append(removed, iv.Version)
		}
		return removed, nil
	}

	if versionSpec == "" {
		return nil, errors.New("version or alias is required")
	}
	resolved, err := m.store.ResolveInstalled(versionSpec)
	if err != nil {
		return nil, err
	}
	if err := m.store.RemoveVersion(resolved); err != nil {
		return nil, err
	}
	return []string{resolved}, nil
}

// UninstallSelf removes the $GVM_HOME directory and the running `g` CLI executable itself.
func (m *Manager) UninstallSelf() error {
	cfg := m.store.Config()
	if cfg.GVMHome != "" {
		if err := os.RemoveAll(cfg.GVMHome); err != nil {
			return fmt.Errorf("remove GVM_HOME (%s): %w", cfg.GVMHome, err)
		}
	}

	exePath := cfg.ExecutablePath
	if exePath == "" {
		return nil
	}
	if _, err := os.Stat(exePath); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err := os.Remove(exePath); err != nil {
		if runtime.GOOS == "windows" {
			// On Windows a running binary cannot be unlinked directly; rename to .old and schedule cmd deletion
			oldPath := exePath + ".old"
			_ = os.Remove(oldPath)
			if renErr := os.Rename(exePath, oldPath); renErr != nil {
				return fmt.Errorf("remove CLI binary %s: %w", exePath, err)
			}
			cmd := exec.Command("cmd", "/c", "ping 127.0.0.1 -n 2 > nul & del /f /q", oldPath)
			_ = cmd.Start()
			return nil
		}
		return fmt.Errorf("remove CLI binary %s: %w", exePath, err)
	}
	return nil
}
