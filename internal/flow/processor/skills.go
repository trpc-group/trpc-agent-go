//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// SkillsRequestProcessor injects skill overviews and loaded contents.
//
// Behavior:
//   - Overview: injects names + descriptions (cheap).
//   - Loaded skills: inject full SKILL.md body.
//   - Docs: inject doc texts selected via state keys.
//
// State keys used (per turn, ephemeral):
//   - skill.StateKeyLoadedPrefix+name -> "1"
//   - skill.StateKeyDocsPrefix+name ->
//     "*" or JSON array of file names.
type SkillsRequestProcessor struct {
	repo         skill.Repository
	showOverview bool
}

// NewSkillsRequestProcessor creates a processor instance.
func NewSkillsRequestProcessor(
	repo skill.Repository, showOverview bool,
) *SkillsRequestProcessor {
	return &SkillsRequestProcessor{
		repo:         repo,
		showOverview: showOverview,
	}
}

// ProcessRequest implements flow.RequestProcessor.
func (p *SkillsRequestProcessor) ProcessRequest(
	ctx context.Context, inv *agent.Invocation, req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil || inv == nil || inv.Session == nil || p.repo == nil {
		return
	}

	// 1) Overview (names + descriptions)
	if p.showOverview {
		p.injectOverview(req)
	}

	// 2) Loaded skills full content
	loaded := p.getLoadedSkills(inv)
	if len(loaded) == 0 {
		return
	}

	// Deterministic order for stable prompts
	sort.Strings(loaded)

	for _, name := range loaded {
		sk, err := p.repo.Get(name)
		if err != nil || sk == nil {
			log.Warnf("skills: get %s failed: %v", name, err)
			continue
		}
		if sk.Body != "" {
			msg := model.NewSystemMessage(sk.Body)
			req.Messages = append(req.Messages, msg)
		}
		// Docs
		docNames := p.getDocsSelection(inv, name)
		if len(docNames) == 0 {
			continue
		}
		docText := p.buildDocsText(sk, docNames)
		if docText != "" {
			msg := model.NewSystemMessage(docText)
			req.Messages = append(req.Messages, msg)
		}
	}

	// Send a preprocessing trace event.
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID, inv.AgentName,
		event.WithObject(model.ObjectTypePreprocessingInstruction),
	))
}

func (p *SkillsRequestProcessor) injectOverview(req *model.Request) {
	sums := p.repo.Summaries()
	if len(sums) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("Available skills:\n")
	for _, s := range sums {
		line := fmt.Sprintf("- %s: %s\n", s.Name, s.Description)
		b.WriteString(line)
	}
	req.Messages = append(req.Messages,
		model.NewSystemMessage(b.String()))
}

func (p *SkillsRequestProcessor) getLoadedSkills(
	inv *agent.Invocation,
) []string {
	var names []string
	for k, v := range inv.Session.State {
		if !strings.HasPrefix(k, skill.StateKeyLoadedPrefix) {
			continue
		}
		if len(v) == 0 {
			continue
		}
		name := strings.TrimPrefix(k, skill.StateKeyLoadedPrefix)
		names = append(names, name)
	}
	return names
}

func (p *SkillsRequestProcessor) getDocsSelection(
	inv *agent.Invocation, name string,
) []string {
	key := skill.StateKeyDocsPrefix + name
	v, ok := inv.Session.State[key]
	if !ok || len(v) == 0 {
		return nil
	}
	if string(v) == "*" {
		// Select all doc files present.
		sk, err := p.repo.Get(name)
		if err != nil || sk == nil {
			return nil
		}
		var all []string
		for _, d := range sk.Docs {
			all = append(all, d.Path)
		}
		return all
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err != nil {
		return nil
	}
	return arr
}

func (p *SkillsRequestProcessor) buildDocsText(
	sk *skill.Skill, wanted []string,
) string {
	if sk == nil || len(sk.Docs) == 0 {
		return ""
	}
	// Build a map for quick lookup of requested docs.
	want := map[string]struct{}{}
	for _, n := range wanted {
		want[n] = struct{}{}
	}
	var b strings.Builder
	for _, d := range sk.Docs {
		if _, ok := want[d.Path]; !ok {
			continue
		}
		if d.Content == "" {
			continue
		}
		// Separate docs with a marker title.
		b.WriteString("\n[Doc] ")
		b.WriteString(d.Path)
		b.WriteString("\n\n")
		b.WriteString(d.Content)
		b.WriteString("\n")
	}
	return b.String()
}
