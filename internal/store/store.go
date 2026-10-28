package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ducconit/gvm/internal/goversion"
	"github.com/ducconit/gvm/internal/platform"
)

// InstalledVersion represents a locally installed Go version under $GVM_HOME/versions/<version>.
type InstalledVersion struct {
	Version string   `json:"version"`
	Path    string   `json:"path"`
	Active  bool     `json:"active"`
	Aliases []string `json:"aliases,omitempty"`
}

// Store manages local state inside $GVM_HOME (installed versions, aliases.json, and current symlink/state).
type Store struct {
	_   [0]func() // noCopy guard
	mu  sync.Mutex
	cfg platform.EnvConfig
}

// New initializes a Store for the given EnvConfig.
func New(cfg platform.EnvConfig) (*Store, error) {
	if cfg.GVMHome == "" {
		return nil, errors.New("gvm_home cannot be empty")
	}
	return &Store{cfg: cfg}, nil
}

// Config returns the underlying platform.EnvConfig.
func (s *Store) Config() platform.EnvConfig {
	return s.cfg
}

// EnsureDirs creates $GVM_HOME, $GVM_HOME/versions, and $GVM_HOME/tmp if they do not exist.
func (s *Store) EnsureDirs() error {
	for _, dir := range []string{s.cfg.GVMHome, s.cfg.VersionsDir(), s.cfg.TempDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// GetAliases reads the custom alias map (aliasName -> targetVersionSpec) from $GVM_HOME/aliases.json.
func (s *Store) GetAliases() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAliasesLocked()
}

func (s *Store) readAliasesLocked() (map[string]string, error) {
	data, err := os.ReadFile(s.cfg.AliasesFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("read aliases file: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return make(map[string]string), nil
	}
	var aliases map[string]string
	if err := json.Unmarshal(data, &aliases); err != nil {
		return nil, fmt.Errorf("parse aliases file: %w", err)
	}
	if aliases == nil {
		aliases = make(map[string]string)
	}
	return aliases, nil
}

func (s *Store) writeAliasesLocked(aliases map[string]string) error {
	if err := s.EnsureDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(aliases, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal aliases: %w", err)
	}
	tmpFile := s.cfg.AliasesFile() + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o644); err != nil {
		return fmt.Errorf("write temp aliases file: %w", err)
	}
	if err := os.Rename(tmpFile, s.cfg.AliasesFile()); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("replace aliases file: %w", err)
	}
	return nil
}

// SetAlias sets a user alias (e.g. "like" -> "1.27") in $GVM_HOME/aliases.json.
func (s *Store) SetAlias(name, targetSpec string) error {
	aliasName := strings.TrimSpace(strings.ToLower(name))
	if aliasName == "" {
		return errors.New("alias name cannot be empty")
	}
	if strings.ContainsAny(aliasName, " /\\ \t\n") {
		return fmt.Errorf("invalid alias name %q", name)
	}
	target := goversion.Normalize(targetSpec)
	if target == "" {
		return errors.New("alias target version cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	aliases, err := s.readAliasesLocked()
	if err != nil {
		return err
	}
	aliases[aliasName] = target
	return s.writeAliasesLocked(aliases)
}

// ResolveAlias expands a custom alias if one exists (up to 4 hops to avoid cycles).
// If query is not a registered alias, it returns goversion.Normalize(query).
func (s *Store) ResolveAlias(query string) (string, error) {
	s.mu.Lock()
	aliases, err := s.readAliasesLocked()
	s.mu.Unlock()
	if err != nil {
		return "", err
	}

	curr := strings.TrimSpace(strings.ToLower(query))
	for range 4 {
		next, ok := aliases[curr]
		if !ok {
			break
		}
		curr = next
	}
	return goversion.Normalize(curr), nil
}

// ActiveVersion returns the currently active Go version string (or "" if none is active).
func (s *Store) ActiveVersion() (string, error) {
	// 1. Check $GVM_HOME/current_version state file first
	if data, err := os.ReadFile(s.cfg.CurrentVersionFile()); err == nil {
		v := goversion.Normalize(string(data))
		if v != "" && s.IsInstalled(v) {
			return v, nil
		}
	}

	// 2. Fallback to inspecting $GVM_HOME/current symlink target
	target, err := filepath.EvalSymlinks(s.cfg.CurrentDir())
	if err == nil && target != "" {
		base := goversion.Normalize(filepath.Base(target))
		if base != "" && s.IsInstalled(base) {
			return base, nil
		}
	}
	return "", nil
}

// IsInstalled returns true if $GVM_HOME/versions/<v>/bin/go (or go.exe) exists.
func (s *Store) IsInstalled(version string) bool {
	v := goversion.Normalize(version)
	if v == "" {
		return false
	}
	vDir := s.cfg.VersionDir(v)
	info, err := os.Stat(vDir)
	if err != nil || !info.IsDir() {
		return false
	}
	// Check either bin/go or bin/go.exe or VERSION file
	if _, err := os.Stat(s.cfg.GoBinaryPath(vDir)); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(vDir, "VERSION")); err == nil {
		return true
	}
	return false
}

// SetActiveVersion switches $GVM_HOME/current to point to the installed version and persists current_version.
func (s *Store) SetActiveVersion(version string) error {
	v := goversion.Normalize(version)
	if !s.IsInstalled(v) {
		return fmt.Errorf("Go version %s is not installed", v)
	}
	if err := s.EnsureDirs(); err != nil {
		return err
	}
	vDir := s.cfg.VersionDir(v)
	if err := platform.SwitchCurrentLink(s.cfg.GVMHome, vDir); err != nil {
		return err
	}
	if err := os.WriteFile(s.cfg.CurrentVersionFile(), []byte(v+"\n"), 0o644); err != nil {
		return fmt.Errorf("write current_version file: %w", err)
	}
	return nil
}

// ClearActiveVersion removes the active symlink and current_version state file.
func (s *Store) ClearActiveVersion() error {
	_ = os.Remove(s.cfg.CurrentVersionFile())
	return platform.RemoveCurrentLink(s.cfg.GVMHome)
}

// ListInstalled returns all locally installed Go versions sorted ascending by semver,
// annotated with whether each version is active, its directory path, and matching aliases.
func (s *Store) ListInstalled() ([]InstalledVersion, error) {
	entries, err := os.ReadDir(s.cfg.VersionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read versions dir: %w", err)
	}

	active, _ := s.ActiveVersion()
	aliases, err := s.GetAliases()
	if err != nil {
		return nil, err
	}

	var rawVersions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		v := goversion.Normalize(entry.Name())
		if s.IsInstalled(v) {
			rawVersions = append(rawVersions, v)
		}
	}

	slices.SortFunc(rawVersions, goversion.CompareSemver)

	// Map each alias to the installed version it currently resolves to
	aliasByVersion := make(map[string][]string)
	for aliasName, targetSpec := range aliases {
		if resolved, err := goversion.ResolveFromList(targetSpec, rawVersions); err == nil {
			aliasByVersion[resolved] = append(aliasByVersion[resolved], fmt.Sprintf("%s->%s", aliasName, targetSpec))
		}
	}
	for v := range aliasByVersion {
		slices.Sort(aliasByVersion[v])
	}

	result := make([]InstalledVersion, 0, len(rawVersions))
	for _, v := range rawVersions {
		result = append(result, InstalledVersion{
			Version: v,
			Path:    s.cfg.VersionDir(v),
			Active:  v == active,
			Aliases: aliasByVersion[v],
		})
	}
	return result, nil
}

// ResolveInstalled resolves a version query (exact, prefix like "1.27", "latest", or custom alias like "like")
// to an installed version string.
func (s *Store) ResolveInstalled(query string) (string, error) {
	expanded, err := s.ResolveAlias(query)
	if err != nil {
		return "", err
	}
	installed, err := s.ListInstalled()
	if err != nil {
		return "", err
	}
	if len(installed) == 0 {
		return "", errors.New("no Go versions are currently installed")
	}
	versions := make([]string, len(installed))
	for i, iv := range installed {
		versions[i] = iv.Version
	}
	resolved, err := goversion.ResolveFromList(expanded, versions)
	if err != nil {
		return "", fmt.Errorf("installed Go version matching %q (resolved: %q) not found", query, expanded)
	}
	return resolved, nil
}

// RemoveVersion deletes $GVM_HOME/versions/<version>. If it was the active version, it clears the active link.
func (s *Store) RemoveVersion(version string) error {
	v := goversion.Normalize(version)
	if !s.IsInstalled(v) {
		return fmt.Errorf("Go version %s is not installed", v)
	}
	active, _ := s.ActiveVersion()
	if active == v {
		_ = s.ClearActiveVersion()
	}
	vDir := s.cfg.VersionDir(v)
	if err := os.RemoveAll(vDir); err != nil {
		return fmt.Errorf("remove version directory %s: %w", vDir, err)
	}
	return nil
}
