package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*
var embeddedTemplates embed.FS

// SkillSubdirName is the folder name created inside a skills root directory.
const SkillSubdirName = "g"

// ResolveSkillsBaseDir resolves the base skills root directory according to:
// 1. customPath (--path) if non-empty
// 2. ide (--ide) shortcut if non-empty (e.g. "antigravity" -> ~/.gemini/config/skills)
// 3. default -> ~/.agents/skills
func ResolveSkillsBaseDir(homeDir, customPath, ide string) (string, error) {
	if strings.TrimSpace(customPath) != "" {
		p := strings.TrimSpace(customPath)
		if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
			if homeDir == "" {
				return "", errors.New("cannot expand '~' without a home directory")
			}
			p = filepath.Join(homeDir, p[2:])
		}
		return filepath.Clean(p), nil
	}

	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil || homeDir == "" {
			return "", fmt.Errorf("resolve user home dir: %w", err)
		}
	}

	ideKey := strings.TrimSpace(strings.ToLower(ide))
	switch ideKey {
	case "":
		return filepath.Join(homeDir, ".agents", "skills"), nil
	case "antigravity", "agy", "gemini":
		return filepath.Join(homeDir, ".gemini", "config", "skills"), nil
	case "claude", "claude-code":
		return filepath.Join(homeDir, ".claude", "skills"), nil
	case "cursor":
		return filepath.Join(homeDir, ".cursor", "skills"), nil
	case "codex":
		return filepath.Join(homeDir, ".codex", "skills"), nil
	case "opencode":
		return filepath.Join(homeDir, ".config", "opencode", "skills"), nil
	case "windsurf":
		return filepath.Join(homeDir, ".codeium", "windsurf", "skills"), nil
	case "copilot", "vscode":
		return filepath.Join(homeDir, ".copilot", "skills"), nil
	case "agents", "default":
		return filepath.Join(homeDir, ".agents", "skills"), nil
	default:
		return "", fmt.Errorf("unsupported --ide %q (supported: antigravity, claude, cursor, codex, opencode, windsurf, copilot, agents)", ide)
	}
}

// ResolveSkillTargetDir returns the final skill folder where SKILL.md will be written.
// If baseDir already ends with "/g" or contains "SKILL.md" directly when specified via --path, we place it cleanly inside <baseDir>/g unless baseDir is already named "g".
func ResolveSkillTargetDir(homeDir, customPath, ide string) (string, error) {
	baseDir, err := ResolveSkillsBaseDir(homeDir, customPath, ide)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(filepath.Base(baseDir), SkillSubdirName) {
		return baseDir, nil
	}
	return filepath.Join(baseDir, SkillSubdirName), nil
}

// Install installs the embedded skill files into targetDir and returns the list of written file paths.
func Install(homeDir, customPath, ide string) (string, []string, error) {
	targetDir, err := ResolveSkillTargetDir(homeDir, customPath, ide)
	if err != nil {
		return "", nil, err
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create skills directory %s: %w", targetDir, err)
	}

	var written []string
	err = fs.WalkDir(embeddedTemplates, "templates", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "templates" {
			return nil
		}
		rel := strings.TrimPrefix(path, "templates/")
		outPath := filepath.Join(targetDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}
		data, err := embeddedTemplates.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded template %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return fmt.Errorf("write skill file %s: %w", outPath, err)
		}
		written = append(written, outPath)
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return targetDir, written, nil
}
