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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type Repository struct {
	base skill.Repository

	eligible map[string]struct{}
	reasons  map[string]string

	baseDirs map[string]string

	debug bool

	configKeys map[string]struct{}
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

func NewRepository(roots []string, opts ...Option) (*Repository, error) {
	base, err := skill.NewFSRepository(roots...)
	if err != nil {
		return nil, err
	}

	r := &Repository{
		base:     base,
		eligible: map[string]struct{}{},
		reasons:  map[string]string{},
		baseDirs: map[string]string{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	r.index()
	return r, nil
}

func (r *Repository) Summaries() []skill.Summary {
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

func (r *Repository) index() {
	if r.base == nil {
		return
	}

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

		eligible, reason := evaluateSkill(skillMd, r.configKeys)
		if !eligible {
			r.reasons[name] = reason
			if r.debug && reason != "" {
				log.Infof(
					"skip skill %q: %s",
					name,
					reason,
				)
			}
			continue
		}

		r.eligible[name] = struct{}{}
		r.baseDirs[name] = baseDir
	}
}

func evaluateSkill(
	skillMdPath string,
	configKeys map[string]struct{},
) (bool, string) {
	fm, err := parseFrontMatterFile(skillMdPath)
	if err != nil {
		if errors.Is(err, errNoFrontMatter) {
			return true, ""
		}
		return true, ""
	}

	meta, ok, err := parseOpenClawMetadata(fm)
	if err != nil || !ok {
		return true, ""
	}
	return evaluateOpenClawRequirements(meta, configKeys)
}

func evaluateOpenClawRequirements(
	meta openClawMetadata,
	configKeys map[string]struct{},
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
	if reason := evaluateRequiredEnv(meta.Requires.Env); reason != "" {
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

func evaluateRequiredEnv(names []string) string {
	var missing []string
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := os.LookupEnv(key); ok {
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

var _ skill.Repository = (*Repository)(nil)
