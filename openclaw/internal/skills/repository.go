//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/deps"
)

type SkillConfig struct {
	Enabled *bool
	APIKey  string
	Env     map[string]string
}

type Repository struct {
	mu sync.RWMutex

	base  skill.Repository
	roots []string

	eligible map[string]struct{}
	reasons  map[string]string

	baseDirs map[string]string

	metas    map[string]*openClawMetadata
	skillKey map[string]string

	debug bool

	configKeys map[string]struct{}

	allowBundled map[string]struct{}
	bundledRoot  string

	skillConfigs map[string]SkillConfig
}

type Option func(*Repository)

func WithDebug(debug bool) Option {
	return func(r *Repository) {
		r.debug = debug
	}
}

func WithConfigKeys(keys []string) Option {
	return func(r *Repository) {
		r.configKeys = normalizeConfigKeys(keys)
	}
}

func WithBundledSkillsRoot(root string) Option {
	return func(r *Repository) {
		cleaned := strings.TrimSpace(root)
		if cleaned == "" {
			r.bundledRoot = ""
			return
		}
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err == nil && strings.TrimSpace(resolved) != "" {
			cleaned = resolved
		}
		r.bundledRoot = cleaned
	}
}

func WithAllowBundled(allow []string) Option {
	return func(r *Repository) {
		r.allowBundled = normalizeAllowlist(allow)
	}
}

func WithSkillConfigs(cfg map[string]SkillConfig) Option {
	return func(r *Repository) {
		r.skillConfigs = normalizeSkillConfigs(cfg)
	}
}

func NewRepository(roots []string, opts ...Option) (*Repository, error) {
	base, err := skill.NewFSRepository(roots...)
	if err != nil {
		return nil, err
	}

	r := &Repository{
		base:     base,
		roots:    append([]string(nil), roots...),
		eligible: map[string]struct{}{},
		reasons:  map[string]string{},
		baseDirs: map[string]string{},
		metas:    map[string]*openClawMetadata{},
		skillKey: map[string]string{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	r.indexLocked()
	return r, nil
}

func (r *Repository) Summaries() []skill.Summary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.base == nil {
		return nil
	}
	in := r.base.Summaries()
	out := make([]skill.Summary, 0, len(in))
	for _, s := range in {
		if _, ok := r.eligible[s.Name]; !ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (r *Repository) Get(name string) (*skill.Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if strings.TrimSpace(name) == "" {
		return nil, errors.New("empty skill name")
	}
	if _, ok := r.eligible[name]; !ok {
		if reason := r.reasons[name]; reason != "" {
			return nil, fmt.Errorf(
				"skill %q is disabled: %s",
				name,
				reason,
			)
		}
		return nil, fmt.Errorf("skill %q is disabled", name)
	}

	s, err := r.base.Get(name)
	if err != nil {
		return nil, err
	}

	baseDir := r.baseDirs[name]
	if baseDir == "" {
		if p, err := r.base.Path(name); err == nil {
			baseDir = p
		}
	}
	if baseDir == "" {
		return s, nil
	}

	s.Body = strings.ReplaceAll(
		s.Body,
		openClawBaseDirPlaceholder,
		baseDir,
	)
	for i := range s.Docs {
		s.Docs[i].Content = strings.ReplaceAll(
			s.Docs[i].Content,
			openClawBaseDirPlaceholder,
			baseDir,
		)
	}
	return s, nil
}

func (r *Repository) Path(name string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if strings.TrimSpace(name) == "" {
		return "", errors.New("empty skill name")
	}
	if _, ok := r.eligible[name]; !ok {
		if reason := r.reasons[name]; reason != "" {
			return "", fmt.Errorf(
				"skill %q is disabled: %s",
				name,
				reason,
			)
		}
		return "", fmt.Errorf("skill %q is disabled", name)
	}
	return r.base.Path(name)
}

func (r *Repository) SkillRunEnv(
	_ context.Context,
	skillName string,
) (map[string]string, error) {
	if r == nil || r.base == nil {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	name := strings.TrimSpace(skillName)
	if name == "" {
		return nil, nil
	}

	meta := r.metas[name]
	primaryEnv := ""
	if meta != nil {
		primaryEnv = strings.TrimSpace(meta.PrimaryEnv)
	}
	skillKey := strings.TrimSpace(r.skillKey[name])
	cfg, ok := r.resolveSkillConfig(skillKey, name)
	if !ok {
		return nil, nil
	}

	out := map[string]string{}
	for k, v := range cfg.Env {
		key := strings.TrimSpace(k)
		if key == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if isBlockedSkillEnvKey(key) {
			continue
		}
		out[key] = v
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if primaryEnv != "" && apiKey != "" {
		if isBlockedSkillEnvKey(primaryEnv) {
			return out, nil
		}
		if _, ok := out[primaryEnv]; !ok {
			out[primaryEnv] = apiKey
		}
	}
	return out, nil
}

func (r *Repository) DependencySources(
	names []string,
) ([]deps.Source, error) {
	if r == nil || r.base == nil {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	selected := normalizeSkillNames(names)
	summaries := r.base.Summaries()
	descriptions := make(map[string]string, len(summaries))
	for _, summary := range summaries {
		name := strings.TrimSpace(summary.Name)
		if name == "" {
			continue
		}
		descriptions[name] = strings.TrimSpace(summary.Description)
	}

	wantAll := len(selected) == 0
	out := make([]deps.Source, 0, len(descriptions))
	for name, meta := range r.metas {
		if meta == nil {
			continue
		}
		if !wantAll && !containsString(selected, name) {
			continue
		}
		out = append(out, deps.Source{
			Name:        name,
			Description: descriptions[name],
			Requires:    meta.Requires,
			Install:     meta.Install,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})

	if wantAll {
		return out, nil
	}
	if len(out) != len(selected) {
		for _, name := range selected {
			if containsSource(out, name) {
				continue
			}
			return nil, fmt.Errorf("unknown skill: %s", name)
		}
	}
	return out, nil
}

func (r *Repository) Refresh() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if refreshable, ok := r.base.(skill.RefreshableRepository); ok {
		if err := refreshable.Refresh(); err != nil {
			return err
		}
	}
	r.indexLocked()
	return nil
}

func (r *Repository) SetSkillEnabled(
	configKey string,
	enabled bool,
) error {
	if r == nil {
		return fmt.Errorf("skills repository is not available")
	}

	key := strings.TrimSpace(configKey)
	if key == "" {
		return fmt.Errorf("skill config key is required")
	}

	value := enabled

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.skillConfigs == nil {
		r.skillConfigs = map[string]SkillConfig{}
	}
	cfg := r.skillConfigs[key]
	cfg.Enabled = &value
	r.skillConfigs[key] = cfg
	r.indexLocked()
	return nil
}

func (r *Repository) indexLocked() {
	if r.base == nil {
		r.eligible = map[string]struct{}{}
		r.reasons = map[string]string{}
		r.baseDirs = map[string]string{}
		r.metas = map[string]*openClawMetadata{}
		r.skillKey = map[string]string{}
		return
	}

	eligibleSet := map[string]struct{}{}
	reasons := map[string]string{}
	baseDirs := map[string]string{}
	metas := map[string]*openClawMetadata{}
	skillKeys := map[string]string{}

	sums := r.base.Summaries()
	names := make([]string, 0, len(sums))
	for _, s := range sums {
		if strings.TrimSpace(s.Name) == "" {
			continue
		}
		names = append(names, s.Name)
	}
	sort.Strings(names)

	for _, name := range names {
		baseDir, err := r.base.Path(name)
		if err != nil {
			continue
		}
		skillMd := filepath.Join(baseDir, skillFileName)
		fm, err := parseFrontMatterFile(skillMd)
		if err != nil {
			fm = parsedFrontMatter{}
		}

		meta, ok, err := parseOpenClawMetadata(fm)
		if err != nil || !ok {
			meta = openClawMetadata{}
			ok = false
		}

		meta.SkillKey = strings.TrimSpace(meta.SkillKey)
		meta.PrimaryEnv = strings.TrimSpace(meta.PrimaryEnv)
		meta.Emoji = strings.TrimSpace(meta.Emoji)
		meta.Homepage = strings.TrimSpace(meta.Homepage)

		skillKey := name
		if meta.SkillKey != "" {
			skillKey = meta.SkillKey
		}
		skillKeys[name] = skillKey

		if ok {
			m := meta
			metas[name] = &m
		}

		cfg, _ := r.resolveSkillConfig(skillKey, name)

		isEligible, reason := evaluateSkill(
			name,
			meta,
			ok,
			r.configKeys,
			cfg,
			r.isBundledSkill(baseDir),
			r.allowBundled,
		)
		if !isEligible {
			reasons[name] = reason
			if r.debug && reason != "" {
				log.Infof(
					"skip skill %q: %s",
					name,
					reason,
				)
			}
			continue
		}

		eligibleSet[name] = struct{}{}
		baseDirs[name] = baseDir
	}

	r.eligible = eligibleSet
	r.reasons = reasons
	r.baseDirs = baseDirs
	r.metas = metas
	r.skillKey = skillKeys
}

func evaluateSkill(
	skillName string,
	meta openClawMetadata,
	hasOpenClawMeta bool,
	configKeys map[string]struct{},
	cfg SkillConfig,
	isBundled bool,
	allowBundled map[string]struct{},
) (bool, string) {
	if cfg.Enabled != nil && !*cfg.Enabled {
		return false, "disabled by config"
	}
	if isBundled && len(allowBundled) > 0 {
		key := strings.TrimSpace(meta.SkillKey)
		if key == "" {
			key = strings.TrimSpace(skillName)
		}
		if key == "" {
			return true, ""
		}
		if !isAllowedByAllowlist(allowBundled, key, skillName) {
			return false, "blocked by allow_bundled"
		}
	}
	if !hasOpenClawMeta {
		return true, ""
	}
	return evaluateOpenClawRequirements(meta, configKeys, cfg)
}

func evaluateOpenClawRequirements(
	meta openClawMetadata,
	configKeys map[string]struct{},
	cfg SkillConfig,
) (bool, string) {
	if meta.Always {
		return true, ""
	}

	osAllowed, osReason := evaluateOpenClawOS(meta.OS)
	if !osAllowed {
		return false, osReason
	}

	if reason := evaluateRequiredBins(meta.Requires.Bins); reason != "" {
		return false, reason
	}
	if reason := evaluateRequiredAnyBins(meta.Requires.AnyBins); reason != "" {
		return false, reason
	}
	if reason := evaluateRequiredEnv(
		meta.Requires.Env,
		meta.PrimaryEnv,
		cfg,
	); reason != "" {
		return false, reason
	}
	if reason := evaluateRequiredConfig(
		meta.Requires.Config,
		configKeys,
	); reason != "" {
		return false, reason
	}
	return true, ""
}

func evaluateOpenClawOS(allowlist []string) (bool, string) {
	if len(allowlist) == 0 {
		return true, ""
	}
	goos := strings.ToLower(strings.TrimSpace(runtime.GOOS))
	for _, allowed := range allowlist {
		if normalizeOpenClawOS(allowed) == goos {
			return true, ""
		}
	}
	list := strings.Join(allowlist, ", ")
	return false, fmt.Sprintf("os mismatch (allowed: %s)", list)
}

func normalizeOpenClawOS(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "win32" {
		return "windows"
	}
	return s
}

func evaluateRequiredBins(bins []string) string {
	var missing []string
	for _, bin := range bins {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"missing bins: %s",
		strings.Join(missing, ", "),
	)
}

func evaluateRequiredAnyBins(bins []string) string {
	var any []string
	for _, bin := range bins {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			continue
		}
		any = append(any, bin)
	}
	if len(any) == 0 {
		return ""
	}
	for _, bin := range any {
		if _, err := exec.LookPath(bin); err == nil {
			return ""
		}
	}
	return fmt.Sprintf(
		"missing anyBins (need one): %s",
		strings.Join(any, ", "),
	)
}

func evaluateRequiredEnv(
	names []string,
	primaryEnv string,
	cfg SkillConfig,
) string {
	var missing []string
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if v, ok := os.LookupEnv(key); ok &&
			strings.TrimSpace(v) != "" {
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
			primaryEnv != "" &&
			key == primaryEnv {
			continue
		}
		missing = append(missing, key)
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"missing env: %s",
		strings.Join(missing, ", "),
	)
}

func evaluateRequiredConfig(
	keys []string,
	available map[string]struct{},
) string {
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
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"missing config: %s",
		strings.Join(missing, ", "),
	)
}

func normalizeConfigKeys(keys []string) map[string]struct{} {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(keys))
	for _, raw := range keys {
		key := normalizeConfigKey(raw)
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func normalizeConfigKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func hasConfigKey(keys map[string]struct{}, want string) bool {
	if len(keys) == 0 {
		return false
	}
	if _, ok := keys[want]; ok {
		return true
	}
	prefix := want + "."
	for got := range keys {
		if strings.HasPrefix(got, prefix) {
			return true
		}
	}
	return false
}

func (r *Repository) resolveSkillConfig(
	skillKey string,
	skillName string,
) (SkillConfig, bool) {
	if r == nil || len(r.skillConfigs) == 0 {
		return SkillConfig{}, false
	}
	key := strings.TrimSpace(skillKey)
	if key != "" {
		if cfg, ok := r.skillConfigs[key]; ok {
			return cfg, true
		}
	}
	name := strings.TrimSpace(skillName)
	if name != "" {
		if cfg, ok := r.skillConfigs[name]; ok {
			return cfg, true
		}
	}
	return SkillConfig{}, false
}

func (r *Repository) isBundledSkill(baseDir string) bool {
	root := strings.TrimSpace(r.bundledRoot)
	if root == "" || strings.TrimSpace(baseDir) == "" {
		return false
	}
	rel, err := filepath.Rel(root, baseDir)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return false
	}
	return !strings.HasPrefix(rel, "../")
}

func normalizeAllowlist(allow []string) map[string]struct{} {
	if len(allow) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for _, raw := range allow {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func isAllowedByAllowlist(
	allow map[string]struct{},
	skillKey string,
	skillName string,
) bool {
	if len(allow) == 0 {
		return true
	}
	if _, ok := allow[skillKey]; ok {
		return true
	}
	_, ok := allow[skillName]
	return ok
}

func normalizeSkillConfigs(
	cfg map[string]SkillConfig,
) map[string]SkillConfig {
	if len(cfg) == 0 {
		return nil
	}
	out := make(map[string]SkillConfig, len(cfg))
	for rawKey, rawCfg := range cfg {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			continue
		}
		out[key] = SkillConfig{
			Enabled: rawCfg.Enabled,
			APIKey:  strings.TrimSpace(rawCfg.APIKey),
			Env:     copySkillEnv(rawCfg.Env),
		}
	}
	return out
}

func copySkillEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSource(sources []deps.Source, want string) bool {
	for _, source := range sources {
		if source.Name == want {
			return true
		}
	}
	return false
}

func normalizeSkillNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, raw := range names {
		for _, part := range strings.Split(raw, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

const (
	envLDPreload           = "LD_PRELOAD"
	envLDLibraryPath       = "LD_LIBRARY_PATH"
	envDYLDInsertLibraries = "DYLD_INSERT_LIBRARIES"
	envDYLDLibraryPath     = "DYLD_LIBRARY_PATH"
	envDYLDForceFlatNS     = "DYLD_FORCE_FLAT_NAMESPACE"
	envOpenSSLConf         = "OPENSSL_CONF"
)

func isBlockedSkillEnvKey(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case envLDPreload,
		envLDLibraryPath,
		envDYLDInsertLibraries,
		envDYLDLibraryPath,
		envDYLDForceFlatNS,
		envOpenSSLConf:
		return true
	default:
		return false
	}
}

var _ skill.Repository = (*Repository)(nil)
var _ skill.RefreshableRepository = (*Repository)(nil)
