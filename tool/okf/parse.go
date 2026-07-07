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
	"bytes"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatterRe matches a leading YAML frontmatter block delimited by "---"
// lines. (?s) lets . span newlines; \A/\z anchor to the whole input.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---[ \t]*\r?\n?(.*)\z`)

// linkRe matches markdown links whose target ends in .md, e.g. [text](/a/b.md).
// An optional #fragment or ?query suffix after .md is tolerated (captured
// outside group 2 so the concept id stays clean).
var linkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)#?]+\.md)(?:[#?][^)]*)?\)`)

// ConceptID converts a bundle-relative file path ("tables/orders.md", or with a
// leading slash) into its concept id ("tables/orders").
func ConceptID(relPath string) string {
	return strings.TrimSuffix(path.Clean(strings.TrimPrefix(relPath, "/")), ".md")
}

// splitRaw isolates a leading YAML frontmatter block. ok is false when the input
// has no frontmatter fence.
func splitRaw(raw []byte) (yamlPart, body []byte, ok bool) {
	b := bytes.TrimPrefix(raw, []byte("\ufeff")) // strip UTF-8 BOM.
	m := frontmatterRe.FindSubmatch(b)
	if m == nil {
		return nil, raw, false
	}
	return m[1], m[2], true
}

// SplitFrontmatter parses raw into its Frontmatter and body. It is tolerant:
// missing or malformed frontmatter yields a zero Frontmatter and the original
// bytes as the body (OKF consumers MUST tolerate malformed concepts at runtime;
// use Validate for the strict producer-side check).
func SplitFrontmatter(raw []byte) (Frontmatter, []byte) {
	yamlPart, body, ok := splitRaw(raw)
	if !ok {
		return Frontmatter{}, raw
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(yamlPart, &fm); err != nil {
		return Frontmatter{}, raw // malformed YAML: tolerate, keep body verbatim.
	}
	return fm, body
}

// ExtractLinks returns the outgoing .md markdown links in body, each normalized
// to a bundle-relative concept id. conceptDir is the directory of the concept
// owning the body (used to resolve relative links). External URLs are ignored;
// a #fragment or ?query after a .md target is stripped. Broken links (targets
// that do not exist) are still returned: consumers must tolerate them.
func ExtractLinks(conceptDir string, body []byte) []Link {
	matches := linkRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	links := make([]Link, 0, len(matches))
	for _, m := range matches {
		text := string(m[1])
		target := string(m[2])
		if strings.Contains(target, "://") { // external URL.
			continue
		}
		var id string
		if strings.HasPrefix(target, "/") { // bundle-absolute.
			id = strings.TrimPrefix(target, "/")
		} else { // relative to the concept's directory.
			id = path.Join(conceptDir, target)
		}
		id = strings.TrimSuffix(path.Clean(id), ".md")
		links = append(links, Link{Target: id, Text: text})
	}
	return links
}

// ParseConcept builds a Concept from its raw file bytes. id is the concept's
// bundle-relative id (path without .md). Parsing is tolerant and never errors.
func ParseConcept(id string, raw []byte) Concept {
	fm, body := SplitFrontmatter(raw)
	return Concept{
		ID:          id,
		Frontmatter: fm,
		Body:        string(body),
		Links:       ExtractLinks(path.Dir(id), body),
	}
}
