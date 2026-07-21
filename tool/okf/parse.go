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
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

// frontmatterRe matches a leading YAML frontmatter block delimited by "---"
// lines. (?s) lets . span newlines; \A/\z anchor to the whole input.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---[ \t]*\r?\n?(.*)\z`)

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
	fm, err := decodeFrontmatter(yamlPart)
	if err != nil {
		return Frontmatter{}, raw // malformed YAML: tolerate, keep body verbatim.
	}
	return fm, body
}

// decodeFrontmatter decodes reserved fields independently so a producer's
// malformed optional field does not hide otherwise usable metadata. Unknown
// fields are preserved for consumers that round-trip frontmatter.
func decodeFrontmatter(data []byte) (Frontmatter, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Frontmatter{}, err
	}
	// Decode once into an unconstrained value to catch semantic YAML errors such
	// as duplicate mapping keys without imposing the Frontmatter Go types.
	var value any
	if err := doc.Decode(&value); err != nil {
		return Frontmatter{}, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return Frontmatter{}, nil
	}

	var fm Frontmatter
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		value := root.Content[i+1]
		switch key {
		case "type":
			_ = value.Decode(&fm.Type)
		case "title":
			_ = value.Decode(&fm.Title)
		case "description":
			_ = value.Decode(&fm.Description)
		case "resource":
			_ = value.Decode(&fm.Resource)
		case "tags":
			fm.Tags = decodeTags(value)
		case "timestamp":
			_ = value.Decode(&fm.Timestamp)
		case "okf_version":
			_ = value.Decode(&fm.OKFVersion)
		default:
			var extra any
			if err := value.Decode(&extra); err == nil {
				if fm.Extra == nil {
					fm.Extra = make(map[string]any)
				}
				fm.Extra[key] = extra
			}
		}
	}
	return fm, nil
}

func decodeTags(node *yaml.Node) []string {
	if node.Kind != yaml.SequenceNode {
		var tag string
		if err := node.Decode(&tag); err != nil {
			return nil
		}
		var tags []string
		for _, item := range strings.Split(tag, ",") {
			if item = strings.TrimSpace(item); item != "" {
				tags = append(tags, item)
			}
		}
		return tags
	}
	tags := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		var tag string
		if err := item.Decode(&tag); err == nil {
			tags = append(tags, tag)
		}
	}
	return tags
}

// ExtractLinks returns the outgoing .md markdown links in body, each normalized
// to a bundle-relative concept id. conceptDir is the directory of the concept
// owning the body (used to resolve relative links). External URLs are ignored;
// a #fragment or ?query after a .md target is stripped. Broken links (targets
// that do not exist) are still returned: consumers must tolerate them.
func ExtractLinks(conceptDir string, body []byte) []Link {
	doc := goldmark.DefaultParser().Parse(text.NewReader(body))
	var links []Link
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := node.(*ast.Link)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		target, err := url.Parse(string(link.Destination))
		if err != nil || target.Scheme != "" || target.Host != "" || !strings.HasSuffix(target.Path, ".md") {
			return ast.WalkContinue, nil
		}
		var id string
		if strings.HasPrefix(target.Path, "/") {
			id = strings.TrimPrefix(target.Path, "/")
		} else {
			id = path.Join(conceptDir, target.Path)
		}
		id = strings.TrimSuffix(path.Clean(id), ".md")
		links = append(links, Link{Target: id, Text: string(link.Text(body))})
		return ast.WalkContinue, nil
	})
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
