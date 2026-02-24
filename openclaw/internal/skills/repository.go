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
}

type Option func(*Repository)

func WithDebug(debug bool) Option {
	return func(r *Repository) {
		r.debug = debug
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

		eligible, reason := evaluateSkill(skillMd)
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

func evaluateSkill(skillMdPath string) (bool, string) {
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
	return evaluateOpenClawRequirements(meta)
}

func evaluateOpenClawRequirements(meta openClawMetadata) (bool, string) {
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

var _ skill.Repository = (*Repository)(nil)
