package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// SwitchCurrentLink updates $GVM_HOME/current to point to targetVersionDir.
// On Unix (darwin, linux), it performs an atomic symlink swap.
// On Windows, it tries os.Symlink first and falls back to `cmd /c mklink /J` (directory junction, which requires no admin privileges on Windows).
func SwitchCurrentLink(gvmHome, targetVersionDir string) error {
	if err := os.MkdirAll(gvmHome, 0o755); err != nil {
		return fmt.Errorf("ensure gvm home: %w", err)
	}
	info, err := os.Stat(targetVersionDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("target version directory does not exist: %s", targetVersionDir)
	}

	currentLink := filepath.Join(gvmHome, "current")

	if runtime.GOOS == "windows" {
		_ = RemoveCurrentLink(gvmHome)
		if err := os.Symlink(targetVersionDir, currentLink); err == nil {
			return nil
		}
		// Fallback to Windows Directory Junction (works without Developer Mode or Administrator privileges)
		cmd := exec.Command("cmd", "/c", "mklink", "/J", currentLink, targetVersionDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create windows directory junction %s -> %s: %w (%s)", currentLink, targetVersionDir, err, string(out))
		}
		return nil
	}

	// Unix atomic symlink replacement via temporary symlink + os.Rename
	tmpLink := filepath.Join(gvmHome, fmt.Sprintf(".current.tmp.%d", os.Getpid()))
	_ = os.Remove(tmpLink)
	if err := os.Symlink(targetVersionDir, tmpLink); err != nil {
		return fmt.Errorf("create symlink %s -> %s: %w", tmpLink, targetVersionDir, err)
	}
	if err := os.Rename(tmpLink, currentLink); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("replace current symlink: %w", err)
	}
	return nil
}

// RemoveCurrentLink removes $GVM_HOME/current if it exists.
func RemoveCurrentLink(gvmHome string) error {
	currentLink := filepath.Join(gvmHome, "current")
	if _, err := os.Lstat(currentLink); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(currentLink)
}
