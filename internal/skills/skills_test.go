package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkills_ResolveAndInstall(t *testing.T) {
	homeDir := t.TempDir()

	// 1. Default -> ~/.agents/skills/g
	targetDefault, filesDefault, err := Install(homeDir, "", "")
	if err != nil {
		t.Fatalf("Install default: %v", err)
	}
	wantDefault := filepath.Join(homeDir, ".agents", "skills", "g")
	if targetDefault != wantDefault || len(filesDefault) == 0 {
		t.Fatalf("expected target=%s, got %s (files=%v)", wantDefault, targetDefault, filesDefault)
	}
	skillContent, err := os.ReadFile(filepath.Join(targetDefault, "SKILL.md"))
	if err != nil || !strings.Contains(string(skillContent), "name: g") {
		t.Fatalf("unexpected SKILL.md content: %v", err)
	}

	// 2. --ide antigravity -> ~/.gemini/config/skills/g
	targetAgy, _, err := Install(homeDir, "", "antigravity")
	if err != nil {
		t.Fatalf("Install --ide antigravity: %v", err)
	}
	wantAgy := filepath.Join(homeDir, ".gemini", "config", "skills", "g")
	if targetAgy != wantAgy {
		t.Fatalf("expected %s, got %s", wantAgy, targetAgy)
	}

	// 3. --path custom directory overrides --ide
	customDir := filepath.Join(homeDir, "my-custom-skills")
	targetCustom, _, err := Install(homeDir, customDir, "antigravity")
	if err != nil {
		t.Fatalf("Install --path: %v", err)
	}
	wantCustom := filepath.Join(customDir, "g")
	if targetCustom != wantCustom {
		t.Fatalf("expected %s, got %s", wantCustom, targetCustom)
	}

	// 4. Unsupported --ide returns clear error
	if _, _, err := Install(homeDir, "", "unknown-ide"); err == nil {
		t.Fatalf("expected error for unknown --ide")
	}
}
