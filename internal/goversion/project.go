package goversion

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectVersion holds a Go version requirement discovered in a project directory.
type ProjectVersion struct {
	Version    string `json:"version"`
	SourceFile string `json:"source_file"`
}

// DetectProjectVersion walks up from startDir looking for `.go-version`, `go.work`, or `go.mod`
// and extracts the target Go version (preferring `toolchain go1.x.y` if present and non-"default",
// otherwise using the `go 1.x.y` directive).
func DetectProjectVersion(startDir string) (*ProjectVersion, error) {
	if strings.TrimSpace(startDir) == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("determine working directory: %w", err)
		}
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, err
	}

	for {
		// 1. Check .go-version
		goVerFile := filepath.Join(dir, ".go-version")
		if v, err := parseDotGoVersion(goVerFile); err == nil && v != "" {
			return &ProjectVersion{Version: v, SourceFile: goVerFile}, nil
		}

		// 2. Check go.work
		goWorkFile := filepath.Join(dir, "go.work")
		if v, err := parseGoModOrWork(goWorkFile); err == nil && v != "" {
			return &ProjectVersion{Version: v, SourceFile: goWorkFile}, nil
		}

		// 3. Check go.mod
		goModFile := filepath.Join(dir, "go.mod")
		if v, err := parseGoModOrWork(goModFile); err == nil && v != "" {
			return &ProjectVersion{Version: v, SourceFile: goModFile}, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return nil, errors.New("no go.mod, go.work, or .go-version file found in current or parent directories")
}

func parseDotGoVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line != "" {
			return Normalize(line), nil
		}
	}
	return "", nil
}

func parseGoModOrWork(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var goDirective, toolchainDirective string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			switch fields[0] {
			case "go":
				goDirective = Normalize(fields[1])
			case "toolchain":
				tc := strings.TrimSpace(fields[1])
				if tc != "default" {
					// Strip any suffix like "+auto" (e.g., "go1.27.3+auto")
					if plusIdx := strings.Index(tc, "+"); plusIdx >= 0 {
						tc = tc[:plusIdx]
					}
					toolchainDirective = Normalize(tc)
				}
			}
		}
	}

	if toolchainDirective != "" {
		return toolchainDirective, nil
	}
	return goDirective, nil
}
