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

const defaultName = "okf"

// toolSet adapts a Store into a tool.ToolSet.
type toolSet struct {
	store        Store
	name         string
	namePrefix   string
	maxBodyBytes int
	tools        []tool.Tool
}

// NewToolSet adapts a Store into a tool.ToolSet. Mount it on an agent with
// llmagent.WithToolSets([]tool.ToolSet{ts}).
func NewToolSet(store Store, opts ...Option) (tool.ToolSet, error) {
	if store == nil {
		return nil, errors.New("okf: NewToolSet requires a non-nil Store")
	}
	t := &toolSet{
		store: store,
		name:  defaultName,
	}
	for _, opt := range opts {
		opt(t)
	}
	if t.namePrefix != "" {
		for _, r := range t.namePrefix {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
				(r < '0' || r > '9') && r != '_' && r != '-' {
				return nil, fmt.Errorf("okf: invalid tool name prefix %q: use only letters, numbers, underscores, and hyphens", t.namePrefix)
			}
		}
		if len(t.toolName("okf_read")) > 64 {
			return nil, fmt.Errorf("okf: tool name prefix %q produces a name longer than 64 characters", t.namePrefix)
		}
	}
	if t.maxBodyBytes < 0 {
		return nil, fmt.Errorf("okf: max body bytes must not be negative")
	}
	t.tools = []tool.Tool{t.listTool(), t.readTool()}
	return t, nil
}

// Tools implements tool.ToolSet.
func (t *toolSet) Tools(context.Context) []tool.Tool { return t.tools }

// Close implements tool.ToolSet. The ToolSet does not own the Store lifecycle.
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
					msg := fmt.Sprintf("concept %q not found — call %s to browse the bundle",
						a.ConceptID, t.toolName("okf_list"))
					return Concept{}, errors.New(msg)
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
