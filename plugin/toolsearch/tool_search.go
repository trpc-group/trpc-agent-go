//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolsearch provides a Tool Search plugin.
package toolsearch

import (
	"context"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolSearch uses an LLM to select relevant tools before the main
// model call by mutating `args.Request.Tools` in a BeforeModel callback.
type ToolSearch struct {
	name          string
	searcher      searcher
	maxTools      int
	alwaysInclude []string
	failOpen      bool
}

const defaultToolSearchPluginName = "tool_search"

// New creates a new ToolSearch.
func New(m model.Model, opts ...Option) (*ToolSearch, error) {
	if m == nil {
		return nil, fmt.Errorf("newing tool search: model is nil")
	}
	cfg := &Config{Model: m}
	for _, opt := range opts {
		opt(cfg)
	}

	s := &ToolSearch{
		name:          cfg.Name,
		maxTools:      cfg.MaxTools,
		alwaysInclude: append([]string(nil), cfg.AlwaysInclude...),
		failOpen:      cfg.FailOpen,
	}
	if s.maxTools <= 0 {
		s.maxTools = defaultMaxTools
	}
	if s.name == "" {
		s.name = defaultToolSearchPluginName
	}

	if cfg.toolKnowledge != nil {
		s.searcher = newKnowledgeSearcher(cfg.Model, cfg.SystemPrompt, cfg.toolKnowledge)
	} else {
		s.searcher = newLlmSearch(cfg.Model, cfg.SystemPrompt)
	}
	return s, nil
}

// Name implements plugin.Plugin.
func (s *ToolSearch) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

// Register implements plugin.Plugin.
func (s *ToolSearch) Register(r *plugin.Registry) {
	if s == nil || r == nil {
		return
	}
	r.BeforeModel(s.Callback())
}

// Callback returns a BeforeModel callback that performs tool selection.
func (s *ToolSearch) Callback() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (res *model.BeforeModelResult, err error) {
		defer func() {
			if err != nil && s.failOpen {
				// Fallback to original full tool set (do not mutate req.Tools).
				err = nil
			}
		}()
		req := requestFromBeforeModelArgs(args)
		if req == nil {
			return nil, nil
		}
		if len(req.Tools) == 0 {
			return nil, nil
		}

		baseTools := req.Tools
		if err := s.validateAlwaysIncludeToolsExist(baseTools); err != nil {
			return nil, err
		}

		candidateTools := s.buildCandidateTools(baseTools)
		if len(candidateTools) == 0 {
			// If no tools are available for selection, nothing to do.
			return nil, nil
		}

		lastUser, err := lastUserMessage(req.Messages)
		if err != nil {
			return nil, err
		}

		ctx, selectedTools, err := s.searcher.Search(ctx, candidateTools, lastUser.Content, s.maxTools)
		if err != nil {
			return nil, err
		}

		// Rebuild request tools map.
		req.Tools = buildSelectedTools(baseTools, selectedTools, s.alwaysInclude)
		return &model.BeforeModelResult{Context: ctx}, nil
	}
}

func requestFromBeforeModelArgs(args *model.BeforeModelArgs) *model.Request {
	if args == nil || args.Request == nil {
		return nil
	}
	return args.Request
}

func (s *ToolSearch) validateAlwaysIncludeToolsExist(baseTools map[string]tool.Tool) error {
	if len(s.alwaysInclude) == 0 {
		return nil
	}

	missing := make([]string, 0)
	for _, name := range s.alwaysInclude {
		if _, ok := baseTools[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	available := sortedToolNames(baseTools)
	return fmt.Errorf(
		"validating always include tools: tools in always_include not found in request: %v; available tools: %v",
		missing, available,
	)
}

func sortedToolNames(tools map[string]tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *ToolSearch) buildCandidateTools(baseTools map[string]tool.Tool) map[string]tool.Tool {
	// Prepare candidate tools for selection (exclude always-include).
	candidateTools := make(map[string]tool.Tool, len(baseTools))

	always := make(map[string]bool, len(s.alwaysInclude))
	for _, name := range s.alwaysInclude {
		always[name] = true
	}

	for name, t := range baseTools {
		if always[name] {
			continue
		}
		if t == nil || t.Declaration() == nil {
			continue
		}
		candidateTools[name] = t
	}
	return candidateTools
}

func lastUserMessage(messages []model.Message) (model.Message, error) {
	lastUser, ok := findLastUserMessage(messages)
	if !ok {
		return model.Message{}, fmt.Errorf("finding last user message: no user message found in request messages")
	}
	return lastUser, nil
}

func buildSelectedTools(
	baseTools map[string]tool.Tool,
	selected []string,
	alwaysInclude []string,
) map[string]tool.Tool {
	newTools := make(map[string]tool.Tool, len(selected)+len(alwaysInclude))
	for _, name := range selected {
		newTools[name] = baseTools[name]
	}
	for _, name := range alwaysInclude {
		if _, ok := newTools[name]; !ok {
			newTools[name] = baseTools[name]
		}
	}
	return newTools
}
