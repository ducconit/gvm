package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

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

// DoctorCheck represents a single diagnostic check performed by `g doctor`.
type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "OK", "WARN", "FAIL"
	Message string `json:"message"`
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

// resolveOrAutoDetect resolves "", "auto", or "gomod" by walking up from workDir for .go-version / go.work / go.mod.
func (m *Manager) resolveOrAutoDetect(versionSpec string) (string, error) {
	spec := strings.TrimSpace(versionSpec)
	if spec == "" || strings.EqualFold(spec, "auto") || strings.EqualFold(spec, "gomod") {
		pv, err := goversion.DetectProjectVersion("")
		if err != nil {
			return "", fmt.Errorf("auto-detect Go version: %w", err)
		}
		fmt.Fprintf(m.out, "Detected Go %s from %s\n", pv.Version, pv.SourceFile)
		return pv.Version, nil
	}
	return spec, nil
}

// Install resolves versionSpec (alias, prefix like "1.27", "latest", exact version, or ""/"auto" from go.mod),
// downloads the official archive for GOOS/GOARCH, verifies SHA256, extracts it into
// $GVM_HOME/versions/<version>, and sets it as active if no Go version is currently active.
func (m *Manager) Install(ctx context.Context, versionSpec string) (*store.InstalledVersion, error) {
	if err := m.store.EnsureDirs(); err != nil {
		return nil, err
	}

	resolvedSpec, err := m.resolveOrAutoDetect(versionSpec)
	if err != nil {
		return nil, err
	}

	expandedSpec, err := m.store.ResolveAlias(resolvedSpec)
	if err != nil {
		return nil, err
	}

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

// Use switches the active default Go version to versionSpec (installed version, prefix, alias, or ""/"auto" from go.mod).
// When auto-detecting from go.mod/.go-version and the target version is not yet installed, Use installs it automatically.
func (m *Manager) Use(ctx context.Context, versionSpec string) (*store.InstalledVersion, error) {
	isAuto := strings.TrimSpace(versionSpec) == "" || strings.EqualFold(strings.TrimSpace(versionSpec), "auto") || strings.EqualFold(strings.TrimSpace(versionSpec), "gomod")
	resolvedSpec, err := m.resolveOrAutoDetect(versionSpec)
	if err != nil {
		return nil, err
	}

	resolved, err := m.store.ResolveInstalled(resolvedSpec)
	if err != nil {
		if isAuto {
			fmt.Fprintf(m.out, "Version %s is not installed locally; installing automatically...\n", resolvedSpec)
			iv, instErr := m.Install(ctx, resolvedSpec)
			if instErr != nil {
				return nil, instErr
			}
			resolved = iv.Version
		} else {
			return nil, fmt.Errorf("%w (hint: run 'g install %s' first)", err, resolvedSpec)
		}
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

// Unalias removes a named alias.
func (m *Manager) Unalias(name string) error {
	return m.store.RemoveAlias(name)
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

// Exec runs a command with the environment (GOROOT and PATH) temporarily pointed at the specified Go version
// without altering the global default version.
func (m *Manager) Exec(ctx context.Context, versionSpec string, cmdArgs []string, in io.Reader, out, errOut io.Writer) error {
	if len(cmdArgs) == 0 {
		return errors.New("command to execute is required (e.g., g exec 1.27 -- go version)")
	}
	resolved, err := m.store.ResolveInstalled(versionSpec)
	if err != nil {
		return fmt.Errorf("%w (hint: run 'g install %s' first)", err, versionSpec)
	}

	cfg := m.store.Config()
	vDir := cfg.VersionDir(resolved)
	vBinDir := filepath.Join(vDir, "bin")

	binName := cmdArgs[0]
	if binName == "go" || binName == "gofmt" {
		if cfg.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(binName), ".exe") {
			binName += ".exe"
		}
		candidate := filepath.Join(vBinDir, binName)
		if _, err := os.Stat(candidate); err == nil {
			binName = candidate
		}
	}

	cmd := exec.CommandContext(ctx, binName, cmdArgs[1:]...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errOut

	env := os.Environ()
	newEnv := make([]string, 0, len(env)+2)
	pathUpdated := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOROOT=") {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			parts := strings.SplitN(kv, "=", 2)
			newEnv = append(newEnv, "PATH="+vBinDir+string(os.PathListSeparator)+parts[1])
			pathUpdated = true
			continue
		}
		newEnv = append(newEnv, kv)
	}
	if !pathUpdated {
		newEnv = append(newEnv, "PATH="+vBinDir)
	}
	newEnv = append(newEnv, "GOROOT="+vDir)
	cmd.Env = newEnv

	return cmd.Run()
}

// Clean removes temporary download/extraction staging files in $GVM_HOME/tmp
// and optionally runs `go clean -cache` and `go clean -modcache` using the active Go toolchain.
// Returns the bytes freed in $GVM_HOME/tmp.
func (m *Manager) Clean(ctx context.Context, goCache, modCache bool) (int64, error) {
	cfg := m.store.Config()
	tmpDir := cfg.TempDir()

	var freedBytes int64
	_ = filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			freedBytes += info.Size()
		}
		return nil
	})

	if err := os.RemoveAll(tmpDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return freedBytes, fmt.Errorf("clean temp dir %s: %w", tmpDir, err)
	}
	_ = os.MkdirAll(tmpDir, 0o755)

	if goCache || modCache {
		active, _ := m.store.ActiveVersion()
		if active != "" {
			goBin := cfg.GoBinaryPath(cfg.VersionDir(active))
			var args []string
			args = append(args, "clean")
			if goCache {
				args = append(args, "-cache")
			}
			if modCache {
				args = append(args, "-modcache")
			}
			cmd := exec.CommandContext(ctx, goBin, args...)
			cmd.Stdout = m.out
			cmd.Stderr = m.out
			if err := cmd.Run(); err != nil {
				return freedBytes, fmt.Errorf("run %s %v: %w", goBin, args, err)
			}
		}
	}

	return freedBytes, nil
}

// Doctor runs diagnostic checks on GVM_HOME, GOROOT symlink, PATH ordering, and remote API connectivity.
func (m *Manager) Doctor(ctx context.Context) ([]DoctorCheck, error) {
	cfg := m.store.Config()
	var checks []DoctorCheck

	// 1. Check GVM_HOME directory
	if err := m.store.EnsureDirs(); err != nil {
		checks = append(checks, DoctorCheck{
			Name:    "GVM_HOME",
			Status:  "FAIL",
			Message: fmt.Sprintf("Cannot create or access %s: %v", cfg.GVMHome, err),
		})
	} else {
		checks = append(checks, DoctorCheck{
			Name:    "GVM_HOME",
			Status:  "OK",
			Message: fmt.Sprintf("Directory ready at %s", cfg.GVMHome),
		})
	}

	// 2. Check active version & GOROOT symlink ($GVM_HOME/go)
	active, _ := m.store.ActiveVersion()
	if active == "" {
		checks = append(checks, DoctorCheck{
			Name:    "Active Go Version",
			Status:  "WARN",
			Message: "No active Go version selected (run 'g i latest' or 'g use <version>')",
		})
	} else {
		target, err := filepath.EvalSymlinks(cfg.CurrentDir())
		if err != nil {
			checks = append(checks, DoctorCheck{
				Name:    "GOROOT Symlink",
				Status:  "WARN",
				Message: fmt.Sprintf("Active version is %s, but %s symlink is missing (run 'g use %s')", active, cfg.CurrentDir(), active),
			})
		} else {
			checks = append(checks, DoctorCheck{
				Name:    "Active Go & GOROOT",
				Status:  "OK",
				Message: fmt.Sprintf("Go %s active (%s -> %s)", active, cfg.CurrentDir(), target),
			})
		}
	}

	// 3. Check `go` binary in PATH
	goInPath, lookErr := exec.LookPath("go")
	if lookErr != nil {
		checks = append(checks, DoctorCheck{
			Name:    "PATH Resolution",
			Status:  "WARN",
			Message: fmt.Sprintf("'go' binary not found in current PATH (ensure %s or ~/.local/bin is in PATH)", cfg.CurrentBinDir()),
		})
	} else {
		resolvedGo, _ := filepath.EvalSymlinks(goInPath)
		expectedGo, _ := filepath.EvalSymlinks(cfg.ActiveGoBinaryPath())
		if active != "" && expectedGo != "" && resolvedGo != expectedGo {
			checks = append(checks, DoctorCheck{
				Name:    "PATH Resolution",
				Status:  "WARN",
				Message: fmt.Sprintf("'go' in PATH resolves to %s instead of %s (another Go installation may precede gvm in PATH)", goInPath, cfg.ActiveGoBinaryPath()),
			})
		} else {
			checks = append(checks, DoctorCheck{
				Name:    "PATH Resolution",
				Status:  "OK",
				Message: fmt.Sprintf("'go' resolves to %s", goInPath),
			})
		}
	}

	// 4. Check remote release feed reachability
	rels, err := m.remote.FetchReleases(ctx, false)
	if err != nil {
		checks = append(checks, DoctorCheck{
			Name:    "Official Release Feed",
			Status:  "WARN",
			Message: fmt.Sprintf("Cannot reach Go release feed: %v", err),
		})
	} else {
		checks = append(checks, DoctorCheck{
			Name:    "Official Release Feed",
			Status:  "OK",
			Message: fmt.Sprintf("Reachable (%d stable release series available)", len(rels)),
		})
	}

	return checks, nil
}
