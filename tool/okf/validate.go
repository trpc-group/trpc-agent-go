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
	RuleReservedStructure  = "reserved-structure"
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
// so producers can catch problems before publishing. It checks:
//   - every non-reserved .md carries a parseable YAML frontmatter block with a
//     non-empty type (§9 rules 1 and 2);
//   - a non-root index.md carries no frontmatter (§6 — only the root index.md may,
//     to hold okf_version).
//
// It does NOT validate log.md structure or which reserved keys the root index.md
// carries; those are lower-value and left to the producer.
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
		base := path.Base(p)
		if base == LogFile {
			return nil // log.md structure is not validated.
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if base == IndexFile {
			// §6: only the root index.md may carry frontmatter.
			if path.Dir(p) != "." {
				if _, _, ok := splitRaw(raw); ok {
					violations = append(violations, Violation{ConceptID(p), RuleReservedStructure, "non-root index.md must not carry frontmatter"})
				}
			}
			return nil
		}
		id := ConceptID(p)
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
