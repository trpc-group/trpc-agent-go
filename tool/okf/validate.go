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
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
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
//     string, non-empty type (§9 rules 1 and 2);
//   - index.md and log.md follow their reserved structures (§6, §7 and §9),
//     with the root index.md frontmatter exception for okf_version from §11.
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
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if base == IndexFile {
			body := raw
			if yamlPart, parsedBody, ok := splitRaw(raw); ok {
				body = parsedBody
				if path.Dir(p) != "." {
					violations = append(violations, Violation{ConceptID(p), RuleReservedStructure, "non-root index.md must not carry frontmatter"})
				} else if e := validateRootIndexFrontmatter(yamlPart); e != "" {
					violations = append(violations, Violation{ConceptID(p), RuleReservedStructure, e})
				}
			}
			if !validIndexBody(body) {
				violations = append(violations, Violation{ConceptID(p), RuleReservedStructure, "index.md must contain one or more heading sections with linked list entries"})
			}
			return nil
		}
		if base == LogFile {
			body := raw
			if _, parsedBody, ok := splitRaw(raw); ok {
				body = parsedBody
				violations = append(violations, Violation{ConceptID(p), RuleReservedStructure, "log.md must not carry frontmatter"})
			}
			if !validLogBody(body) {
				violations = append(violations, Violation{ConceptID(p), RuleReservedStructure, "log.md must contain newest-first YYYY-MM-DD sections with list entries"})
			}
			return nil
		}
		id := ConceptID(p)
		yamlPart, _, ok := splitRaw(raw)
		if !ok {
			violations = append(violations, Violation{id, RuleMissingFrontmatter, "no leading --- YAML frontmatter block"})
			return nil
		}
		root, e := yamlRoot(yamlPart)
		if e != nil {
			violations = append(violations, Violation{id, RuleBadFrontmatter, e.Error()})
			return nil
		}
		typeNode := mappingValue(root, "type")
		if typeNode == nil || typeNode.Kind != yaml.ScalarNode || typeNode.Tag != "!!str" || strings.TrimSpace(typeNode.Value) == "" {
			violations = append(violations, Violation{id, RuleMissingType, "required 'type' field must be a non-empty string"})
		}
		return nil
	})
	return violations, err
}

func yamlRoot(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var value any
	if err := doc.Decode(&value); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return doc.Content[0], nil
}

func mappingValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

func validateRootIndexFrontmatter(data []byte) string {
	root, err := yamlRoot(data)
	if err != nil {
		return "root index.md frontmatter is not parseable YAML: " + err.Error()
	}
	version := mappingValue(root, "okf_version")
	if version == nil || version.Kind != yaml.ScalarNode || version.Tag != "!!str" || strings.TrimSpace(version.Value) == "" {
		return "root index.md frontmatter must declare a string okf_version"
	}
	return ""
}

func validIndexBody(body []byte) bool {
	doc := goldmark.DefaultParser().Parse(text.NewReader(body))
	sections := 0
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		if _, ok := node.(*ast.Heading); !ok {
			continue
		}
		sections++
		hasEntries := false
		for sibling := node.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
			if _, ok := sibling.(*ast.Heading); ok {
				break
			}
			list, ok := sibling.(*ast.List)
			if ok && list.ChildCount() > 0 && containsLink(list) {
				hasEntries = true
			}
		}
		if !hasEntries {
			return false
		}
	}
	return sections > 0
}

func validLogBody(body []byte) bool {
	doc := goldmark.DefaultParser().Parse(text.NewReader(body))
	seenTitle := false
	seenDate := false
	dateHasEntries := false
	var previous time.Time
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		switch current := node.(type) {
		case *ast.Heading:
			heading := strings.TrimSpace(string(current.Text(body)))
			switch current.Level {
			case 1:
				if seenTitle || seenDate || heading == "" {
					return false
				}
				seenTitle = true
			case 2:
				if !seenTitle || seenDate && !dateHasEntries || len(heading) != len("2006-01-02") {
					return false
				}
				date, err := time.Parse("2006-01-02", heading)
				if err != nil || date.Format("2006-01-02") != heading || !previous.IsZero() && date.After(previous) {
					return false
				}
				previous = date
				seenDate = true
				dateHasEntries = false
			default:
				return false
			}
		case *ast.List:
			if !seenDate || current.ChildCount() == 0 {
				return false
			}
			dateHasEntries = true
		}
	}
	return seenTitle && seenDate && dateHasEntries
}

func containsLink(node ast.Node) bool {
	found := false
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if _, ok := current.(*ast.Link); ok {
				found = true
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return found
}
