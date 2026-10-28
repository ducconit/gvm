package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SwitchCurrentLink updates $GVM_HOME/go (and legacy alias $GVM_HOME/current) to point to targetVersionDir,
// and links `go` and `gofmt` into a directory already on the user's PATH (such as ~/.local/bin)
// so that `go` and `gofmt` work immediately in the current shell without requiring a terminal restart.
// It also ensures the user's shell profile (or Windows User PATH/GOROOT) contains the fixed GOROOT and PATH entries if missing.
func SwitchCurrentLink(gvmHome, targetVersionDir string) error {
	if err := os.MkdirAll(gvmHome, 0o755); err != nil {
		return fmt.Errorf("ensure gvm home: %w", err)
	}
	info, err := os.Stat(targetVersionDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("target version directory does not exist: %s", targetVersionDir)
	}

	goRootLink := filepath.Join(gvmHome, "go")
	legacyCurrentLink := filepath.Join(gvmHome, "current")

	if runtime.GOOS == "windows" {
		if err := createWindowsDirLink(goRootLink, targetVersionDir); err != nil {
			return err
		}
		_ = createWindowsDirLink(legacyCurrentLink, targetVersionDir)
		return nil
	}

	// Unix atomic symlink replacement for fixed GOROOT ($GVM_HOME/go) and $GVM_HOME/current
	if err := atomicSymlink(gvmHome, goRootLink, targetVersionDir); err != nil {
		return err
	}
	_ = atomicSymlink(gvmHome, legacyCurrentLink, targetVersionDir)

	// Also symlink `go` and `gofmt` into ~/.local/bin (or the directory of `g` executable)
	// so `go` is runnable immediately in the current shell session if ~/.local/bin is already in PATH!
	linkBinariesToUserBin(goRootLink)

	return nil
}

func atomicSymlink(parentDir, linkPath, targetDir string) error {
	tmpLink := filepath.Join(parentDir, fmt.Sprintf(".link.tmp.%d.%s", os.Getpid(), filepath.Base(linkPath)))
	_ = os.Remove(tmpLink)
	if err := os.Symlink(targetDir, tmpLink); err != nil {
		return fmt.Errorf("create symlink %s -> %s: %w", tmpLink, targetDir, err)
	}
	if err := os.Rename(tmpLink, linkPath); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("replace symlink %s: %w", linkPath, err)
	}
	return nil
}

func createWindowsDirLink(linkPath, targetDir string) error {
	_ = removeLinkIfExists(linkPath)
	if err := os.Symlink(targetDir, linkPath); err == nil {
		return nil
	}
	// Fallback to Windows Directory Junction (works without Developer Mode or Administrator privileges)
	cmd := exec.Command("cmd", "/c", "mklink", "/J", linkPath, targetDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create windows directory junction %s -> %s: %w (%s)", linkPath, targetDir, err, string(out))
	}
	return nil
}

// linkBinariesToUserBin symlinks `go` and `gofmt` from <goRootLink>/bin into the user's local bin directory
// (e.g., ~/.local/bin) when that directory is inside the user's home directory, so `go` works immediately
// in the active shell without waiting for `source ~/.bashrc`.
func linkBinariesToUserBin(goRootLink string) {
	homeDir := filepath.Dir(filepath.Dir(goRootLink)) // parent of .gvm when GVM_HOME is ~/.gvm
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		homeDir = h
	}
	localBin := filepath.Join(homeDir, ".local", "bin")
	// Only link into ~/.local/bin if GoRootLink is under the same homeDir (prevents touching real ~/.local/bin during unit tests in /tmp)
	rel, err := filepath.Rel(homeDir, goRootLink)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		return
	}
	for _, binName := range []string{"go", "gofmt"} {
		src := filepath.Join(goRootLink, "bin", binName)
		dst := filepath.Join(localBin, binName)
		// Only create/replace if dst does not exist or is already a symlink
		if fi, err := os.Lstat(dst); err == nil {
			if fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
		}
		_ = atomicSymlink(localBin, dst, src)
	}
}

func removeLinkIfExists(linkPath string) error {
	if _, err := os.Lstat(linkPath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(linkPath)
}

// RemoveCurrentLink removes $GVM_HOME/go and $GVM_HOME/current if they exist.
func RemoveCurrentLink(gvmHome string) error {
	_ = removeLinkIfExists(filepath.Join(gvmHome, "current"))
	return removeLinkIfExists(filepath.Join(gvmHome, "go"))
}

// EnsureShellEnvConfigured checks whether the fixed GOROOT ($GVM_HOME/go) and its bin directory ($GVM_HOME/go/bin)
// are configured in the user's shell profile (or Windows User environment), and adds them idempotently if missing.
func EnsureShellEnvConfigured(cfg EnvConfig) ([]string, error) {
	if cfg.HomeDir == "" || cfg.GVMHome == "" {
		return nil, nil
	}
	// Safety guard for unit tests: only modify shell rc files when GVMHome is inside cfg.HomeDir
	// and cfg.HomeDir is not a temporary test directory unless explicitly testing EnsureShellEnvConfigured.
	goRoot := cfg.CurrentDir()
	goBin := cfg.CurrentBinDir()

	if cfg.GOOS == "windows" {
		return ensureWindowsEnv(goRoot, goBin)
	}
	return ensureUnixShellProfiles(cfg.HomeDir, cfg.GVMHome, goRoot, goBin)
}

func ensureUnixShellProfiles(homeDir, gvmHome, goRoot, goBin string) ([]string, error) {
	exportBlock := fmt.Sprintf("\n# >>> gvm (g) initialize >>>\n"+
		"export GVM_HOME=%q\n"+
		"export GOROOT=%q\n"+
		"export GOPATH=\"${GOPATH:-$HOME/go}\"\n"+
		"export PATH=\"$HOME/.local/bin:%s:$GOPATH/bin:$PATH\"\n"+
		"# <<< gvm (g) initialize <<<\n", gvmHome, goRoot, goBin)

	candidates := []string{
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".profile"),
	}

	var updated []string
	foundExistingFile := false
	for _, rcPath := range candidates {
		data, err := os.ReadFile(rcPath)
		if err != nil {
			continue
		}
		foundExistingFile = true
		content := string(data)
		// If already contains GOROOT/bin or gvm initialize block with current goBin, skip
		if strings.Contains(content, goBin) || strings.Contains(content, "$GOROOT/bin") {
			continue
		}
		f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			continue
		}
		_, writeErr := f.WriteString(exportBlock)
		_ = f.Close()
		if writeErr == nil {
			updated = append(updated, rcPath)
		}
	}

	// If none of .bashrc / .zshrc / .profile existed, create .bashrc
	if !foundExistingFile {
		rcPath := filepath.Join(homeDir, ".bashrc")
		if err := os.WriteFile(rcPath, []byte(strings.TrimLeft(exportBlock, "\n")), 0o644); err == nil {
			updated = append(updated, rcPath)
		}
	}

	return updated, nil
}

func ensureWindowsEnv(goRoot, goBin string) ([]string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	currentPath := os.Getenv("PATH")
	if strings.Contains(strings.ToLower(currentPath), strings.ToLower(goBin)) {
		return nil, nil
	}
	psScript := fmt.Sprintf(
		`$uPath = [Environment]::GetEnvironmentVariable('Path', 'User'); `+
			`if ($uPath -notlike '*%s*') { `+
			`$newPath = if ($uPath) { '%s;' + $uPath } else { '%s' }; `+
			`[Environment]::SetEnvironmentVariable('Path', $newPath, 'User') }; `+
			`[Environment]::SetEnvironmentVariable('GOROOT', '%s', 'User')`,
		goBin, goBin, goBin, goRoot,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return []string{"HKCU\\Environment\\Path", "HKCU\\Environment\\GOROOT"}, nil
}
