//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	capabilityKindTool  = "tool"
	capabilityKindSkill = "skill"

	capabilitySelectPrefix = "select:"

	bm25K1 = 1.2
	bm25B  = 0.75
)

type capabilitySearchItem struct {
	kind        string
	Name        string
	Description string
	Aliases     []string
	SearchText  string
}

type capabilitySearchIndex struct {
	items   []capabilitySearchItem
	docs    []capabilitySearchDoc
	docFreq map[string]int
	avgLen  float64
}

type capabilitySearchDoc struct {
	terms  map[string]int
	length int
}

type scoredCapabilityItem struct {
	item  capabilitySearchItem
	score float64
}

func newCapabilitySearchIndex(
	items []capabilitySearchItem,
) *capabilitySearchIndex {
	copied := append([]capabilitySearchItem(nil), items...)
	index := &capabilitySearchIndex{
		items:   copied,
		docs:    make([]capabilitySearchDoc, 0, len(copied)),
		docFreq: map[string]int{},
	}
	totalLen := 0
	for _, item := range copied {
		doc := newCapabilitySearchDoc(item.SearchText)
		index.docs = append(index.docs, doc)
		totalLen += doc.length
		seen := map[string]bool{}
		for term := range doc.terms {
			if seen[term] {
				continue
			}
			seen[term] = true
			index.docFreq[term]++
		}
	}
	if len(index.docs) > 0 {
		index.avgLen = float64(totalLen) / float64(len(index.docs))
	}
	return index
}

func newCapabilitySearchDoc(text string) capabilitySearchDoc {
	tokens := capabilitySearchTokens(text)
	terms := make(map[string]int, len(tokens))
	for _, token := range tokens {
		terms[token]++
	}
	return capabilitySearchDoc{
		terms:  terms,
		length: len(tokens),
	}
}

func (i *capabilitySearchIndex) search(query string) []capabilitySearchItem {
	if i == nil || len(i.items) == 0 {
		return nil
	}
	queryTerms := dedupeCapabilityTerms(capabilitySearchTokens(query))
	if len(queryTerms) == 0 {
		return append([]capabilitySearchItem(nil), i.items...)
	}
	scored := make([]scoredCapabilityItem, 0, len(i.items))
	for idx, item := range i.items {
		score := i.scoreDoc(i.docs[idx], queryTerms)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredCapabilityItem{
			item:  item,
			score: score,
		})
	}
	sort.SliceStable(scored, func(a, b int) bool {
		if scored[a].score == scored[b].score {
			return compareCapabilityItems(
				scored[a].item,
				scored[b].item,
			) < 0
		}
		return scored[a].score > scored[b].score
	})
	out := make([]capabilitySearchItem, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.item)
	}
	return out
}

func (i *capabilitySearchIndex) scoreDoc(
	doc capabilitySearchDoc,
	queryTerms []string,
) float64 {
	if doc.length == 0 || len(i.docs) == 0 {
		return 0
	}
	score := 0.0
	for _, term := range queryTerms {
		tf := doc.terms[term]
		if tf == 0 {
			continue
		}
		df := i.docFreq[term]
		idf := math.Log(
			1 + (float64(len(i.docs)-df)+0.5)/(float64(df)+0.5),
		)
		tfFloat := float64(tf)
		denominator := tfFloat + bm25K1*(1-bm25B+
			bm25B*float64(doc.length)/i.avgLen)
		score += idf * (tfFloat * (bm25K1 + 1)) / denominator
	}
	return score
}

func capabilityToolSearchText(decl *tool.Declaration, aliases []string) string {
	if decl == nil {
		return ""
	}
	var parts []string
	appendSearchPart(&parts, decl.Name)
	appendSearchPart(&parts, splitIdentifier(decl.Name))
	for _, alias := range aliases {
		appendSearchPart(&parts, alias)
		appendSearchPart(&parts, splitIdentifier(alias))
	}
	appendSearchPart(&parts, decl.Description)
	appendSchemaSearchText(&parts, decl.InputSchema, map[*tool.Schema]bool{})
	return strings.Join(parts, " ")
}

func capabilitySkillSearchText(name string, description string) string {
	var parts []string
	appendSearchPart(&parts, name)
	appendSearchPart(&parts, splitIdentifier(name))
	appendSearchPart(&parts, description)
	return strings.Join(parts, " ")
}

func appendSchemaSearchText(
	parts *[]string,
	schema *tool.Schema,
	seen map[*tool.Schema]bool,
) {
	if schema == nil || seen[schema] {
		return
	}
	seen[schema] = true
	appendSearchPart(parts, schema.Type)
	appendSearchPart(parts, schema.Description)
	appendSearchPart(parts, schema.Pattern)
	appendSearchPart(parts, schema.Ref)
	for _, value := range schema.Enum {
		appendSearchPart(parts, fmt.Sprint(value))
	}
	propertyNames := sortedSchemaNames(schema.Properties)
	for _, name := range propertyNames {
		appendSearchPart(parts, name)
		appendSearchPart(parts, splitIdentifier(name))
		appendSchemaSearchText(parts, schema.Properties[name], seen)
	}
	appendSchemaSearchText(parts, schema.Items, seen)
	defNames := sortedSchemaNames(schema.Defs)
	for _, name := range defNames {
		appendSearchPart(parts, name)
		appendSearchPart(parts, splitIdentifier(name))
		appendSchemaSearchText(parts, schema.Defs[name], seen)
	}
	if additional, ok := schema.AdditionalProperties.(*tool.Schema); ok {
		appendSchemaSearchText(parts, additional, seen)
	}
}

func sortedSchemaNames(values map[string]*tool.Schema) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendSearchPart(parts *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*parts = append(*parts, value)
}

func capabilitySearchTokens(text string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(current.String()))
		current.Reset()
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func dedupeCapabilityTerms(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	seen := map[string]bool{}
	for _, token := range tokens {
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

func splitIdentifier(value string) string {
	var parts []string
	var current strings.Builder
	var previous rune
	flush := func() {
		if current.Len() == 0 {
			return
		}
		parts = append(parts, current.String())
		current.Reset()
	}
	for _, r := range value {
		if r == '_' || r == '-' || r == '.' || r == '/' || unicode.IsSpace(r) {
			flush()
			previous = 0
			continue
		}
		if previous != 0 && unicode.IsLower(previous) && unicode.IsUpper(r) {
			flush()
		}
		current.WriteRune(r)
		previous = r
	}
	flush()
	return strings.Join(parts, " ")
}

func parseCapabilitySelectQuery(query string) ([]string, bool) {
	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToLower(trimmed), capabilitySelectPrefix) {
		return nil, false
	}
	raw := strings.TrimSpace(trimmed[len(capabilitySelectPrefix):])
	if raw == "" {
		return nil, true
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		name := strings.TrimSpace(field)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, true
}

func selectCapabilityItems(
	items []capabilitySearchItem,
	names []string,
) ([]capabilitySearchItem, []string) {
	byName := map[string]capabilitySearchItem{}
	for _, item := range items {
		if existing, ok := byName[item.Name]; ok &&
			kindRank(existing.kind) <= kindRank(item.kind) {
			continue
		}
		byName[item.Name] = item
		for _, alias := range item.Aliases {
			if existing, ok := byName[alias]; ok &&
				kindRank(existing.kind) <= kindRank(item.kind) {
				continue
			}
			byName[alias] = item
		}
	}
	selected := make([]capabilitySearchItem, 0, len(names))
	missing := make([]string, 0)
	selectedNames := make(map[string]bool, len(names))
	for _, name := range names {
		item, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if selectedNames[item.kind+"\x00"+item.Name] {
			continue
		}
		selectedNames[item.kind+"\x00"+item.Name] = true
		selected = append(selected, item)
	}
	return selected, missing
}

func capabilitySummaries(
	items []capabilitySearchItem,
) ([]CapabilityToolSummary, []CapabilitySkillSummary) {
	tools := make([]CapabilityToolSummary, 0)
	skills := make([]CapabilitySkillSummary, 0)
	for _, item := range items {
		switch item.kind {
		case capabilityKindSkill:
			skills = append(skills, CapabilitySkillSummary{
				Name:        item.Name,
				Description: item.Description,
			})
		default:
			tools = append(tools, CapabilityToolSummary{
				Name:        item.Name,
				Description: item.Description,
			})
		}
	}
	return tools, skills
}

func capabilityNameGroups(
	items []capabilitySearchItem,
) []CapabilityNameGroup {
	var toolNames []string
	var skillNames []string
	for _, item := range items {
		switch item.kind {
		case capabilityKindSkill:
			skillNames = append(skillNames, item.Name)
		default:
			toolNames = append(toolNames, item.Name)
		}
	}
	groups := make([]CapabilityNameGroup, 0, 2)
	if len(toolNames) > 0 {
		groups = append(groups, CapabilityNameGroup{
			Kind:  "tools",
			Names: toolNames,
		})
	}
	if len(skillNames) > 0 {
		groups = append(groups, CapabilityNameGroup{
			Kind:  "skills",
			Names: skillNames,
		})
	}
	return groups
}

func sortCapabilityItems(items []capabilitySearchItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return compareCapabilityItems(items[i], items[j]) < 0
	})
}

func compareCapabilityItems(
	a capabilitySearchItem,
	b capabilitySearchItem,
) int {
	if kindRank(a.kind) != kindRank(b.kind) {
		return kindRank(a.kind) - kindRank(b.kind)
	}
	if a.Name < b.Name {
		return -1
	}
	if a.Name > b.Name {
		return 1
	}
	return 0
}

func kindRank(kind string) int {
	switch kind {
	case capabilityKindTool:
		return 0
	case capabilityKindSkill:
		return 1
	default:
		return 2
	}
}

func capabilityItemsFingerprint(items []capabilitySearchItem) string {
	hash := sha256.New()
	for _, item := range items {
		fmt.Fprintf(
			hash,
			"%s\x00%s\x00%s\x00%s\x00%s\x00",
			item.kind,
			item.Name,
			item.Description,
			strings.Join(item.Aliases, "\x00"),
			item.SearchText,
		)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
