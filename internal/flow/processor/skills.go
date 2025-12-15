//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	repo skill.Repository
}

// NewSkillsRequestProcessor creates a processor instance.
func NewSkillsRequestProcessor(
	repo skill.Repository,
) *SkillsRequestProcessor {
	return &SkillsRequestProcessor{repo: repo}
}

// ProcessRequest implements flow.RequestProcessor.
func (p *SkillsRequestProcessor) ProcessRequest(
	ctx context.Context, inv *agent.Invocation, req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil || inv == nil || inv.Session == nil || p.repo == nil {
		return
	}

	// 1) Always inject overview (names + descriptions) into system
	//    message. Merge into existing system message if present.
	p.injectOverview(req)

	// 2) Loaded skills full content (merge into existing system message).
	loaded := p.getLoadedSkills(inv)
	sort.Strings(loaded) // stable prompt order

	var lb strings.Builder
	for _, name := range loaded {
		sk, err := p.repo.Get(name)
		if err != nil || sk == nil {
			log.WarnfContext(
				ctx,
				"skills: get %s failed: %v",
				name,
				err,
			)
			continue
		}
		if sk.Body != "" {
			lb.WriteString("\n[Loaded] ")
			lb.WriteString(name)
			lb.WriteString("\n\n")
			lb.WriteString(sk.Body)
			lb.WriteString("\n")
		}
		// Docs
		sel := p.getDocsSelection(inv, name)
		// Summary line to make selected docs explicit.
		lb.WriteString("Docs loaded: ")
		if len(sel) == 0 {
			lb.WriteString("none\n")
		} else {
			lb.WriteString(strings.Join(sel, ", "))
			lb.WriteString("\n")
		}
		if len(sel) > 0 {
			if docText := p.buildDocsText(sk, sel); docText != "" {
				lb.WriteString(docText)
			}
		}
	}
	if s := lb.String(); s != "" {
		p.mergeIntoSystem(req, s)
	}

	// Send a preprocessing trace event even when only overview is
	// injected, for consistent trace semantics.
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID, inv.AgentName,
		event.WithObject(model.ObjectTypePreprocessingInstruction),
	))
}

func (p *SkillsRequestProcessor) injectOverview(req *model.Request) {
	const header = "Available skills:"
	sums := p.repo.Summaries()
	if len(sums) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	for _, s := range sums {
		line := fmt.Sprintf("- %s: %s\n", s.Name, s.Description)
		b.WriteString(line)
	}
	// Add concise guidance for tool and workspace usage. Text is kept
	// generic so it applies across executors.
	b.WriteString("\nTooling and workspace guidance:\n")
	b.WriteString("- Skills run inside an isolated workspace; you see ")
	b.WriteString("only files that are in the workspace or have been ")
	b.WriteString("staged there by tools.\n")
	b.WriteString("- Prefer $SKILLS_DIR, $WORK_DIR, $OUTPUT_DIR, ")
	b.WriteString("$RUN_DIR, and $WORKSPACE_DIR over hard-coded ")
	b.WriteString("paths when forming commands.\n")
	b.WriteString("- Treat $WORK_DIR/inputs (and a skill's inputs/ ")
	b.WriteString("directory) as the place where tools stage user or ")
	b.WriteString("host input files. Avoid overwriting or mutating ")
	b.WriteString("these inputs directly.\n")
	b.WriteString("- When the user mentions external files, ")
	b.WriteString("directories, artifacts, or URLs, decide whether to ")
	b.WriteString("stage them into $WORK_DIR/inputs via available ")
	b.WriteString("tools before reading.\n")
	b.WriteString("- Prefer writing new files under $OUTPUT_DIR or a ")
	b.WriteString("skill's out/ directory and include output_files ")
	b.WriteString("globs (or an outputs spec) so files can be ")
	b.WriteString("collected or saved as artifacts.\n")
	b.WriteString("- When chaining multiple skills, read previous ")
	b.WriteString("results from $OUTPUT_DIR (or a skill's out/ ")
	b.WriteString("directory) instead of copying them back into ")
	b.WriteString("inputs directories.\n")
	b.WriteString("- If a skill is not loaded, call skill_load; you ")
	b.WriteString("may pass docs or include_all_docs.\n")
	b.WriteString("- If the body is loaded but docs are missing, call ")
	b.WriteString("skill_select_docs or call skill_load again to add ")
	b.WriteString("docs.\n")
	b.WriteString("- When body and needed docs are present, call ")
	b.WriteString("skill_run to execute commands.\n")
	overview := b.String()

	idx := findSystemMessageIndex(req.Messages)
	if idx >= 0 {
		sys := &req.Messages[idx]
		if !strings.Contains(sys.Content, header) {
			if sys.Content != "" {
				sys.Content += "\n\n" + overview
			} else {
				sys.Content = overview
			}
		}
		return
	}
	// No system message yet: create one at the front.
	msg := model.NewSystemMessage(overview)
	req.Messages = append([]model.Message{msg}, req.Messages...)
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

// mergeIntoSystem appends content into the existing system message when
// available; otherwise, it creates a new system message at the front.
func (p *SkillsRequestProcessor) mergeIntoSystem(
	req *model.Request, content string,
) {
	if req == nil || content == "" {
		return
	}
	idx := findSystemMessageIndex(req.Messages)
	if idx >= 0 {
		if req.Messages[idx].Content != "" {
			req.Messages[idx].Content += "\n\n" + content
		} else {
			req.Messages[idx].Content = content
		}
		return
	}
	msg := model.NewSystemMessage(content)
	req.Messages = append([]model.Message{msg}, req.Messages...)
}
