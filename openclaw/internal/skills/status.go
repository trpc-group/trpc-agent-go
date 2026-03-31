//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skills

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type StatusRequirements struct {
	OS      []string `json:"os,omitempty"`
	Bins    []string `json:"bins,omitempty"`
	AnyBins []string `json:"any_bins,omitempty"`
	Env     []string `json:"env,omitempty"`
	Config  []string `json:"config,omitempty"`
}

type StatusInstallOption struct {
	ID    string   `json:"id,omitempty"`
	Kind  string   `json:"kind,omitempty"`
	Label string   `json:"label,omitempty"`
	Bins  []string `json:"bins,omitempty"`
}

type StatusEntry struct {
	Name               string                `json:"name,omitempty"`
	Description        string                `json:"description,omitempty"`
	SkillKey           string                `json:"skill_key,omitempty"`
	ConfigKey          string                `json:"config_key,omitempty"`
	FilePath           string                `json:"file_path,omitempty"`
	BaseDir            string                `json:"base_dir,omitempty"`
	Source             string                `json:"source,omitempty"`
	Reason             string                `json:"reason,omitempty"`
	Emoji              string                `json:"emoji,omitempty"`
	Homepage           string                `json:"homepage,omitempty"`
	PrimaryEnv         string                `json:"primary_env,omitempty"`
	Bundled            bool                  `json:"bundled"`
	Always             bool                  `json:"always"`
	Disabled           bool                  `json:"disabled"`
	Eligible           bool                  `json:"eligible"`
	BlockedByAllowlist bool                  `json:"blocked_by_allowlist"`
	Requirements       StatusRequirements    `json:"requirements,omitempty"`
	Missing            StatusRequirements    `json:"missing,omitempty"`
	Install            []StatusInstallOption `json:"install,omitempty"`
}

type StatusReport struct {
	Skills []StatusEntry `json:"skills,omitempty"`
}

func BuildStatus(roots []string, opts ...Option) (StatusReport, error) {
	repo, err := NewRepository(roots, opts...)
	if err != nil {
		return StatusReport{}, err
	}
	return repo.Status(), nil
}

func (r *Repository) Status() StatusReport {
	if r == nil {
		return StatusReport{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if r == nil || r.base == nil {
		return StatusReport{}
	}

	sums := append([]skill.Summary(nil), r.base.Summaries()...)
	sort.Slice(sums, func(i, j int) bool {
		return strings.TrimSpace(sums[i].Name) < strings.TrimSpace(sums[j].Name)
	})

	out := StatusReport{
		Skills: make([]StatusEntry, 0, len(sums)),
	}
	for _, summary := range sums {
		name := strings.TrimSpace(summary.Name)
		if name == "" {
			continue
		}
		out.Skills = append(out.Skills, r.statusEntryForSummary(summary))
	}
	return out
}

func (r *Repository) statusEntryForSummary(
	summary skill.Summary,
) StatusEntry {
	name := strings.TrimSpace(summary.Name)
	skillKey := strings.TrimSpace(r.skillKey[name])
	if skillKey == "" {
		skillKey = name
	}

	baseDir := strings.TrimSpace(r.baseDirs[name])
	if baseDir == "" && r.base != nil {
		if path, err := r.base.Path(name); err == nil {
			baseDir = strings.TrimSpace(path)
		}
	}

	meta := r.metas[name]
	cfg, _ := r.resolveSkillConfig(skillKey, name)
	bundled := r.isBundledSkill(baseDir)
	disabled := cfg.Enabled != nil && !*cfg.Enabled
	blocked := false
	if bundled && len(r.allowBundled) > 0 {
		blocked = !isAllowedByAllowlist(r.allowBundled, skillKey, name)
	}
	_, eligible := r.eligible[name]

	entry := StatusEntry{
		Name:               name,
		Description:        strings.TrimSpace(summary.Description),
		SkillKey:           skillKey,
		ConfigKey:          statusConfigKey(r, skillKey, name),
		FilePath:           baseDir,
		BaseDir:            baseDir,
		Source:             resolveStatusSource(baseDir, bundled, r.roots),
		Reason:             strings.TrimSpace(r.reasons[name]),
		Bundled:            bundled,
		Disabled:           disabled,
		Eligible:           eligible,
		BlockedByAllowlist: blocked,
	}
	if meta == nil {
		return entry
	}

	entry.Always = meta.Always
	entry.PrimaryEnv = strings.TrimSpace(meta.PrimaryEnv)
	entry.Emoji = strings.TrimSpace(meta.Emoji)
	entry.Homepage = strings.TrimSpace(meta.Homepage)
	entry.Requirements = requiredStatus(meta)
	entry.Missing = missingStatus(meta, r.configKeys, cfg)
	entry.Install = normalizeStatusInstall(meta.Install)
	return entry
}

func statusConfigKey(
	r *Repository,
	skillKey string,
	name string,
) string {
	if r == nil {
		return strings.TrimSpace(skillKey)
	}
	key := strings.TrimSpace(skillKey)
	if key != "" {
		if _, ok := r.skillConfigs[key]; ok {
			return key
		}
	}
	name = strings.TrimSpace(name)
	if name != "" {
		if _, ok := r.skillConfigs[name]; ok {
			return name
		}
	}
	if key != "" {
		return key
	}
	return name
}

func requiredStatus(meta *openClawMetadata) StatusRequirements {
	if meta == nil {
		return StatusRequirements{}
	}
	return StatusRequirements{
		OS:      copyStrings(meta.OS),
		Bins:    copyStrings(meta.Requires.Bins),
		AnyBins: copyStrings(meta.Requires.AnyBins),
		Env:     copyStrings(meta.Requires.Env),
		Config:  normalizeRequirementConfig(meta.Requires.Config),
	}
}

func missingStatus(
	meta *openClawMetadata,
	configKeys map[string]struct{},
	cfg SkillConfig,
) StatusRequirements {
	if meta == nil || meta.Always {
		return StatusRequirements{}
	}
	return StatusRequirements{
		OS:      missingOS(meta.OS),
		Bins:    missingBins(meta.Requires.Bins),
		AnyBins: missingAnyBins(meta.Requires.AnyBins),
		Env:     missingEnv(meta.Requires.Env, meta.PrimaryEnv, cfg),
		Config:  missingConfig(meta.Requires.Config, configKeys),
	}
}

func missingOS(allowlist []string) []string {
	if len(allowlist) == 0 {
		return nil
	}
	goos := strings.ToLower(strings.TrimSpace(runtime.GOOS))
	for _, allowed := range allowlist {
		if normalizeOpenClawOS(allowed) == goos {
			return nil
		}
	}
	return copyStrings(allowlist)
}

func missingBins(bins []string) []string {
	var missing []string
	for _, raw := range bins {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, err := exec.LookPath(name); err == nil {
			continue
		}
		missing = append(missing, name)
	}
	return missing
}

func missingAnyBins(bins []string) []string {
	var any []string
	for _, raw := range bins {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		any = append(any, name)
		if _, err := exec.LookPath(name); err == nil {
			return nil
		}
	}
	if len(any) == 0 {
		return nil
	}
	return any
}

func missingEnv(
	names []string,
	primaryEnv string,
	cfg SkillConfig,
) []string {
	var missing []string
	for _, raw := range names {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			continue
		}
		if isBlockedSkillEnvKey(key) {
			missing = append(missing, key)
			continue
		}
		if strings.TrimSpace(cfg.Env[key]) != "" {
			continue
		}
		if strings.TrimSpace(cfg.APIKey) != "" &&
			strings.TrimSpace(primaryEnv) != "" &&
			key == strings.TrimSpace(primaryEnv) {
			continue
		}
		missing = append(missing, key)
	}
	return missing
}

func missingConfig(
	keys []string,
	available map[string]struct{},
) []string {
	var missing []string
	for _, raw := range keys {
		key := normalizeConfigKey(raw)
		if key == "" {
			continue
		}
		if hasConfigKey(available, key) {
			continue
		}
		missing = append(missing, key)
	}
	return missing
}

func normalizeRequirementConfig(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, raw := range keys {
		key := normalizeConfigKey(raw)
		if key == "" {
			continue
		}
		out = append(out, key)
	}
	return out
}

func normalizeStatusInstall(
	actions []openClawInstallEntry,
) []StatusInstallOption {
	if len(actions) == 0 {
		return nil
	}

	out := make([]StatusInstallOption, 0, len(actions))
	for i, action := range actions {
		kind := strings.TrimSpace(action.Kind)
		if kind == "" {
			continue
		}
		id := strings.TrimSpace(action.ID)
		if id == "" {
			id = fmt.Sprintf("%s-%d", kind, i)
		}
		out = append(out, StatusInstallOption{
			ID:    id,
			Kind:  kind,
			Label: installLabel(action),
			Bins:  copyStrings(action.Bins),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func installLabel(action openClawInstallEntry) string {
	if label := strings.TrimSpace(action.Label); label != "" {
		return label
	}
	switch strings.TrimSpace(action.Kind) {
	case "brew":
		if formula := strings.TrimSpace(action.Formula); formula != "" {
			return "brew install " + formula
		}
	case "go":
		if module := strings.TrimSpace(action.Module); module != "" {
			return "go install " + module
		}
	case "uv":
		if pkg := strings.TrimSpace(action.Package); pkg != "" {
			return "uv tool install " + pkg
		}
	case "node", "npm":
		if pkg := strings.TrimSpace(action.Package); pkg != "" {
			return "npm install -g " + pkg
		}
		if len(action.Packages) > 0 {
			return "npm install -g " + strings.Join(copyStrings(action.Packages), " ")
		}
	case "download":
		if rawURL := strings.TrimSpace(action.URL); rawURL != "" {
			return "download " + filepath.Base(rawURL)
		}
	}
	return "run installer"
}

func resolveStatusSource(
	baseDir string,
	bundled bool,
	roots []string,
) string {
	if bundled {
		return "bundled"
	}
	cleanBase := strings.TrimSpace(baseDir)
	if cleanBase == "" {
		return "unknown"
	}
	baseSlash := filepath.ToSlash(cleanBase)
	switch {
	case strings.Contains(baseSlash, "/.codex/skills/"):
		return "codex"
	case strings.HasSuffix(baseSlash, "/skills/local") ||
		strings.Contains(baseSlash, "/skills/local/"):
		return "local"
	case strings.HasSuffix(baseSlash, "/.agents/skills") ||
		strings.Contains(baseSlash, "/.agents/skills/"):
		return "project"
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, cleanBase)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || !strings.HasPrefix(rel, "../") {
			if strings.HasSuffix(filepath.ToSlash(root), "/skills") {
				return "workspace"
			}
			return "extra"
		}
	}
	return "custom"
}

func copyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
