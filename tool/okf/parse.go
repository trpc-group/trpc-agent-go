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
	"encoding/json"
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

// conceptID converts a bundle-relative file path ("tables/orders.md", or with a
// leading slash) into its concept id ("tables/orders").
func conceptID(relPath string) string {
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

// splitFrontmatter parses raw into its Frontmatter and body. It is best-effort:
// missing or malformed frontmatter yields a zero Frontmatter and the original
// bytes as the body. Malformed frontmatter is not conformant OKF; use Validate
// for the strict producer-side check.
func splitFrontmatter(raw []byte) (Frontmatter, []byte) {
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
		default:
			var extra any
			if err := value.Decode(&extra); err == nil {
				if normalized, ok := normalizeJSONValue(extra); ok {
					if fm.Extra == nil {
						fm.Extra = make(map[string]any)
					}
					fm.Extra[key] = normalized
				}
			}
		}
	}
	return fm, nil
}

// normalizeJSONValue converts YAML maps to JSON-compatible maps. Tool results
// are JSON encoded, so preserving a YAML map[any]any verbatim would make an
// otherwise readable concept fail at the tool boundary.
func normalizeJSONValue(value any) (any, bool) {
	switch value := value.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(value))
		for key, child := range value {
			item, ok := normalizeJSONValue(child)
			if !ok {
				return nil, false
			}
			normalized[key] = item
		}
		return normalized, true
	case map[any]any:
		normalized := make(map[string]any, len(value))
		for key, child := range value {
			stringKey, ok := jsonMapKey(key)
			if !ok {
				return nil, false
			}
			if _, exists := normalized[stringKey]; exists {
				return nil, false
			}
			item, ok := normalizeJSONValue(child)
			if !ok {
				return nil, false
			}
			normalized[stringKey] = item
		}
		return normalized, true
	case []any:
		normalized := make([]any, len(value))
		for i, child := range value {
			item, ok := normalizeJSONValue(child)
			if !ok {
				return nil, false
			}
			normalized[i] = item
		}
		return normalized, true
	default:
		if _, err := json.Marshal(value); err != nil {
			return nil, false
		}
		return value, true
	}
}

func jsonMapKey(value any) (string, bool) {
	if value, ok := value.(string); ok {
		return value, true
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) == "null" || len(encoded) == 0 || encoded[0] == '{' || encoded[0] == '[' {
		return "", false
	}
	return string(encoded), true
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

// extractLinks returns the outgoing .md markdown links in body, each normalized
// to a bundle-relative concept id. conceptDir is the directory of the concept
// owning the body (used to resolve relative links). External URLs are ignored;
// a #fragment or ?query after a .md target is stripped. Broken links (targets
// that do not exist) are still returned: consumers must tolerate them.
func extractLinks(conceptDir string, body []byte) []Link {
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
		if id == "" || id == "." || id == ".." || strings.HasPrefix(id, "../") ||
			strings.ContainsRune(id, '\\') {
			return ast.WalkContinue, nil
		}
		links = append(links, Link{Target: id, Text: string(link.Text(body))})
		return ast.WalkContinue, nil
	})
	return links
}

// ParseConcept builds a Concept from its raw file bytes. id is the concept's
// bundle-relative id (path without .md). Parsing is tolerant and never errors.
func ParseConcept(id string, raw []byte) Concept {
	fm, body := splitFrontmatter(raw)
	return Concept{
		ID:          id,
		Frontmatter: fm,
		Body:        string(body),
		Links:       extractLinks(path.Dir(id), body),
	}
}
