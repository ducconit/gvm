package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvConfig holds resolved cross-platform paths and host metadata.
type EnvConfig struct {
	HomeDir        string `json:"home_dir"`
	GVMHome        string `json:"gvm_home"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	ExecutablePath string `json:"executable_path"`
}

// Detect resolves the current platform, user home directory, and GVM_HOME.
func Detect() (EnvConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		homeDir = os.Getenv("HOME")
		if homeDir == "" {
			homeDir = os.Getenv("USERPROFILE")
		}
		if homeDir == "" {
			return EnvConfig{}, fmt.Errorf("detect user home directory: %w", err)
		}
	}

	gvmHome := strings.TrimSpace(os.Getenv("GVM_HOME"))
	if gvmHome == "" {
		gvmHome = filepath.Join(homeDir, ".gvm")
	}
	absGVMHome, err := filepath.Abs(gvmHome)
	if err == nil {
		gvmHome = absGVMHome
	}

	goos := strings.TrimSpace(os.Getenv("GVM_GOOS"))
	if goos == "" {
		goos = runtime.GOOS
	}

	goarch := strings.TrimSpace(os.Getenv("GVM_GOARCH"))
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	exePath, _ := os.Executable()
	if exePath != "" {
		if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
			exePath = resolved
		}
	}

	return EnvConfig{
		HomeDir:        homeDir,
		GVMHome:        gvmHome,
		GOOS:           goos,
		GOARCH:         goarch,
		ExecutablePath: exePath,
	}, nil
}

// VersionsDir returns $GVM_HOME/versions.
func (c EnvConfig) VersionsDir() string {
	return filepath.Join(c.GVMHome, "versions")
}

// VersionDir returns $GVM_HOME/versions/<version>.
func (c EnvConfig) VersionDir(v string) string {
	return filepath.Join(c.VersionsDir(), v)
}

// CurrentDir returns $GVM_HOME/current (symlink/junction to active Go root).
func (c EnvConfig) CurrentDir() string {
	return filepath.Join(c.GVMHome, "current")
}

// CurrentBinDir returns $GVM_HOME/current/bin.
func (c EnvConfig) CurrentBinDir() string {
	return filepath.Join(c.CurrentDir(), "bin")
}

// GoBinaryName returns "go.exe" on Windows or "go" on Unix.
func GoBinaryName(goos string) string {
	if goos == "windows" {
		return "go.exe"
	}
	return "go"
}

// GoBinaryPath returns the path to the go executable inside a version directory.
func (c EnvConfig) GoBinaryPath(versionDir string) string {
	return filepath.Join(versionDir, "bin", GoBinaryName(c.GOOS))
}

// ActiveGoBinaryPath returns the path to the currently active go binary.
func (c EnvConfig) ActiveGoBinaryPath() string {
	return filepath.Join(c.CurrentBinDir(), GoBinaryName(c.GOOS))
}

// TempDir returns $GVM_HOME/tmp for staging downloads and extractions.
func (c EnvConfig) TempDir() string {
	return filepath.Join(c.GVMHome, "tmp")
}

// AliasesFile returns $GVM_HOME/aliases.json.
func (c EnvConfig) AliasesFile() string {
	return filepath.Join(c.GVMHome, "aliases.json")
}

// CurrentVersionFile returns $GVM_HOME/current_version.
func (c EnvConfig) CurrentVersionFile() string {
	return filepath.Join(c.GVMHome, "current_version")
}

// ComputedEnv holds the key environment variables managed or exposed by g.
type ComputedEnv struct {
	GVMHome       string `json:"GVM_HOME"`
	GOROOT        string `json:"GOROOT"`
	GOPATH        string `json:"GOPATH"`
	GOBIN         string `json:"GOBIN"`
	GOPROXY       string `json:"GOPROXY"`
	GOOS          string `json:"GOOS"`
	GOARCH        string `json:"GOARCH"`
	ActiveVersion string `json:"ACTIVE_VERSION"`
	PathEntry     string `json:"PATH_ENTRY"`
}

// ComputeEnv computes the Go environment variables for the current platform and active version.
func (c EnvConfig) ComputeEnv(activeVersion string) ComputedEnv {
	gopath := strings.TrimSpace(os.Getenv("GOPATH"))
	if gopath == "" {
		gopath = filepath.Join(c.HomeDir, "go")
	}
	gobin := strings.TrimSpace(os.Getenv("GOBIN"))
	if gobin == "" {
		gobin = filepath.Join(gopath, "bin")
	}
	goproxy := strings.TrimSpace(os.Getenv("GOPROXY"))
	if goproxy == "" {
		goproxy = "https://proxy.golang.org,direct"
	}
	return ComputedEnv{
		GVMHome:       c.GVMHome,
		GOROOT:        c.CurrentDir(),
		GOPATH:        gopath,
		GOBIN:         gobin,
		GOPROXY:       goproxy,
		GOOS:          c.GOOS,
		GOARCH:        c.GOARCH,
		ActiveVersion: activeVersion,
		PathEntry:     c.CurrentBinDir(),
	}
}

// FormatEnvExport formats shell setup instructions for the detected OS.
func FormatEnvExport(cfg EnvConfig, activeVersion string) string {
	env := cfg.ComputeEnv(activeVersion)
	var b strings.Builder
	b.Grow(512)

	fmt.Fprintf(&b, "GVM_HOME=%s\n", env.GVMHome)
	fmt.Fprintf(&b, "GOROOT=%s\n", env.GOROOT)
	fmt.Fprintf(&b, "GOPATH=%s\n", env.GOPATH)
	fmt.Fprintf(&b, "GOBIN=%s\n", env.GOBIN)
	fmt.Fprintf(&b, "GOPROXY=%s\n", env.GOPROXY)
	fmt.Fprintf(&b, "GOOS=%s\n", env.GOOS)
	fmt.Fprintf(&b, "GOARCH=%s\n", env.GOARCH)
	if env.ActiveVersion != "" {
		fmt.Fprintf(&b, "ACTIVE_VERSION=%s\n", env.ActiveVersion)
	} else {
		fmt.Fprintf(&b, "ACTIVE_VERSION=(none)\n")
	}
	fmt.Fprintf(&b, "PATH_ENTRY=%s\n", env.PathEntry)
	b.WriteString("\n")

	if cfg.GOOS == "windows" {
		b.WriteString("# PowerShell setup:\n")
		fmt.Fprintf(&b, "$env:GOROOT = %q\n", env.GOROOT)
		fmt.Fprintf(&b, "$env:GOPATH = %q\n", env.GOPATH)
		fmt.Fprintf(&b, "$env:PATH = %q + \";\" + %q + \";\" + $env:PATH\n", env.PathEntry, env.GOBIN)
	} else {
		b.WriteString("# Bash / Zsh setup (add to ~/.bashrc or ~/.zshrc):\n")
		fmt.Fprintf(&b, "export GOROOT=%q\n", env.GOROOT)
		fmt.Fprintf(&b, "export GOPATH=%q\n", env.GOPATH)
		fmt.Fprintf(&b, "export PATH=\"%s:%s:$PATH\"\n", env.PathEntry, env.GOBIN)
	}
	return b.String()
}
