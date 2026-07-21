package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

type Skill struct {
	Name        string
	Path        string
	Instruction string
	RulesDoc    string
	Scripts     []string
}

func LoadSkill(path string) (Skill, error) {
	skill := Skill{Name: "code-review", Path: path}
	body, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil {
		return skill, fmt.Errorf("load SKILL.md: %w", err)
	}
	skill.Instruction = string(body)

	rulesPath := filepath.Join(path, "rules", "go-code-review-rules.md")
	if body, err := os.ReadFile(rulesPath); err == nil {
		skill.RulesDoc = string(body)
	}

	scriptsDir := filepath.Join(path, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				skill.Scripts = append(skill.Scripts, filepath.Join(scriptsDir, entry.Name()))
			}
		}
	}
	return skill, nil
}
