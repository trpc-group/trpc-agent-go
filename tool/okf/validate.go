//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package okf

import (
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Conformance rule ids reported by Validate.
const (
	RuleMissingFrontmatter = "missing-frontmatter"
	RuleBadFrontmatter     = "bad-frontmatter"
	RuleMissingType        = "missing-type"
)

// Violation is one OKF conformance problem found by Validate.
type Violation struct {
	Concept string // Concept id (bundle-relative path minus .md).
	Rule    string // One of the Rule* ids.
	Detail  string // Human-readable explanation.
}

// Validate lints a bundle for OKF producer-side conformance and returns the
// violations found (empty == conformant).
//
// This is the strict, generation/CI-time gate — the counterpart to the runtime
// tolerance a consumer must have. Per the OKF spec a consumer MUST NOT reject a
// bundle for missing/unknown fields or broken links; Validate does the opposite,
// so producers can catch problems before publishing: every non-reserved .md must
// carry a parseable YAML frontmatter block with a non-empty type.
//
// Pass os.DirFS(bundleRoot) as fsys.
func Validate(fsys fs.FS) ([]Violation, error) {
	var violations []Violation
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		if base := path.Base(p); base == IndexFile || base == LogFile {
			return nil // reserved files are not concepts.
		}
		id := ConceptID(p)
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		yamlPart, _, ok := splitRaw(raw)
		if !ok {
			violations = append(violations, Violation{id, RuleMissingFrontmatter, "no leading --- YAML frontmatter block"})
			return nil
		}
		var fm Frontmatter
		if e := yaml.Unmarshal(yamlPart, &fm); e != nil {
			violations = append(violations, Violation{id, RuleBadFrontmatter, e.Error()})
			return nil
		}
		if strings.TrimSpace(fm.Type) == "" {
			violations = append(violations, Violation{id, RuleMissingType, "required 'type' field is empty"})
		}
		return nil
	})
	return violations, err
}
