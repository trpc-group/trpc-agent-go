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
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultName      = "okf"
	defaultFindLimit = 10
)

// toolSet adapts a Store into a tool.ToolSet exposing okf_list/okf_read/okf_find.
type toolSet struct {
	store        Store
	name         string
	namePrefix   string
	listEnabled  bool
	readEnabled  bool
	findEnabled  bool
	maxBodyBytes int
	findLimit    int
	tools        []tool.Tool
}

// NewToolSet adapts a Store into a tool.ToolSet. Mount it on an agent with
// llmagent.WithToolSets([]tool.ToolSet{ts}).
func NewToolSet(store Store, opts ...Option) (tool.ToolSet, error) {
	if store == nil {
		return nil, errors.New("okf: NewToolSet requires a non-nil Store")
	}
	t := &toolSet{
		store:       store,
		name:        defaultName,
		listEnabled: true,
		readEnabled: true,
		findEnabled: true,
		findLimit:   defaultFindLimit,
	}
	for _, opt := range opts {
		opt(t)
	}
	var tools []tool.Tool
	if t.listEnabled {
		tools = append(tools, t.listTool())
	}
	if t.readEnabled {
		tools = append(tools, t.readTool())
	}
	if t.findEnabled {
		tools = append(tools, t.findTool())
	}
	t.tools = tools
	return t, nil
}

// Tools implements tool.ToolSet.
func (t *toolSet) Tools(context.Context) []tool.Tool { return t.tools }

// Close implements tool.ToolSet. A local store holds no resources.
func (t *toolSet) Close() error { return nil }

// Name implements tool.ToolSet. It reflects WithNamePrefix so several bundles
// mounted on one agent get distinct set names.
func (t *toolSet) Name() string {
	if t.namePrefix != "" {
		return t.namePrefix + "_" + t.name
	}
	return t.name
}

func (t *toolSet) toolName(base string) string {
	if t.namePrefix != "" {
		return t.namePrefix + "_" + base
	}
	return base
}

// truncateUTF8 returns s cut to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// --- okf_list ---

type listArgs struct {
	// Dir is a pointer so that omitting it (bundle root) is not "required".
	Dir *string `json:"dir,omitempty" jsonschema:"description=Bundle-relative directory to list; omit for the bundle root"`
}

func (t *toolSet) listTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, a listArgs) (Listing, error) {
			dir := ""
			if a.Dir != nil {
				dir = *a.Dir
			}
			return t.store.List(ctx, dir)
		},
		function.WithName(t.toolName("okf_list")),
		function.WithDescription("List an OKF knowledge-bundle directory (progressive disclosure). "+
			"Returns index.md content plus the concepts and sub-directories directly under 'dir', "+
			"so you can decide what to read next. Start here with 'dir' omitted to see the bundle "+
			"root index and top-level concepts."),
	)
}

// --- okf_read ---

type readArgs struct {
	ConceptID string `json:"concept_id" jsonschema:"description=Concept id = bundle-relative path without the .md extension,required"`
}

func (t *toolSet) readTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, a readArgs) (Concept, error) {
			c, err := t.store.Read(ctx, a.ConceptID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return Concept{}, fmt.Errorf("concept %q not found — call okf_list to browse the bundle or okf_find to search by keyword", a.ConceptID)
				}
				return Concept{}, err
			}
			if t.maxBodyBytes > 0 && len(c.Body) > t.maxBodyBytes {
				c.Body = truncateUTF8(c.Body, t.maxBodyBytes)
				c.Truncated = true
			}
			return c, nil
		},
		function.WithName(t.toolName("okf_read")),
		function.WithDescription("Read one OKF concept by id. Returns its structured frontmatter "+
			"(type/title/description/resource/tags/timestamp), the markdown body, and outgoing links "+
			"to related concepts so you can navigate the knowledge graph."),
	)
}

// --- okf_find ---

type findArgs struct {
	Query string   `json:"query" jsonschema:"description=Free-text query matched against concept title/description and body,required"`
	Type  *string  `json:"type,omitempty" jsonschema:"description=Optional exact frontmatter 'type' filter"`
	Tags  []string `json:"tags,omitempty" jsonschema:"description=Optional tags; a concept must carry all of them"`
	Limit *int     `json:"limit,omitempty" jsonschema:"description=Maximum number of results"`
}

type findResult struct {
	Hits []Hit  `json:"hits"`
	Note string `json:"note,omitempty"` // Guidance to the model when Hits is empty.
}

func (t *toolSet) findTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, a findArgs) (findResult, error) {
			q := Query{Text: a.Query, Tags: a.Tags}
			if a.Type != nil {
				q.Type = *a.Type
			}
			if a.Limit != nil {
				q.Limit = *a.Limit
			} else {
				q.Limit = t.findLimit
			}
			hits, err := t.store.Find(ctx, q)
			if err != nil {
				return findResult{}, err
			}
			if hits == nil {
				hits = []Hit{} // serialize [] not null, so the model reads "no matches" clearly.
			}
			res := findResult{Hits: hits}
			if len(hits) == 0 {
				res.Note = "no concepts matched; try broader terms, drop the type/tags filter, or call okf_list to browse the bundle root"
			}
			return res, nil
		},
		function.WithName(t.toolName("okf_find")),
		function.WithDescription("Search the OKF bundle for concepts matching a free-text query, "+
			"optionally filtered by frontmatter type and tags. Returns concept ids with title/description; "+
			"then use okf_read to read the full content and follow links."),
	)
}
