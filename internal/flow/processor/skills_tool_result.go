//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	skillsLoadedContextHeader = "Loaded skill context:"

	skillToolLoad       = "skill_load"
	skillToolSelectDocs = "skill_select_docs"
)

type skillsToolResultProcessorOptions struct {
	loadMode                     string
	skipFallbackOnSessionSummary bool
}

// SkillsToolResultRequestProcessorOption configures
// SkillsToolResultRequestProcessor.
type SkillsToolResultRequestProcessorOption func(
	*skillsToolResultProcessorOptions,
)

// WithSkillsToolResultLoadMode sets how long loaded skill bodies/docs
// remain available in prompt materialization.
//
// Supported modes:
//   - SkillLoadModeTurn (default)
//   - SkillLoadModeOnce
//   - SkillLoadModeSession (legacy)
func WithSkillsToolResultLoadMode(
	mode string,
) SkillsToolResultRequestProcessorOption {
	return func(o *skillsToolResultProcessorOptions) {
		o.loadMode = mode
	}
}

// WithSkipSkillsFallbackOnSessionSummary controls whether the processor
// skips the "Loaded skill context" system-message fallback when a session
// summary is present in the request.
//
// Default: true.
func WithSkipSkillsFallbackOnSessionSummary(
	skip bool,
) SkillsToolResultRequestProcessorOption {
	return func(o *skillsToolResultProcessorOptions) {
		o.skipFallbackOnSessionSummary = skip
	}
}

// SkillsToolResultRequestProcessor materializes loaded skill content
// into tool result messages (skill_load / skill_select_docs) when
// possible.
//
// If no matching tool result message exists (for example, when history
// is suppressed but state persists), it falls back to a dedicated system
// message containing the loaded skill bodies/docs.
//
// If a session summary is present in the request and the corresponding
// option is enabled, the fallback system message is skipped.
type SkillsToolResultRequestProcessor struct {
	repo     skill.Repository
	loadMode string

	skipFallbackOnSessionSummary bool
}

// NewSkillsToolResultRequestProcessor creates a processor instance.
func NewSkillsToolResultRequestProcessor(
	repo skill.Repository,
	opts ...SkillsToolResultRequestProcessorOption,
) *SkillsToolResultRequestProcessor {
	options := skillsToolResultProcessorOptions{
		skipFallbackOnSessionSummary: true,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&options)
	}
	return &SkillsToolResultRequestProcessor{
		repo:                         repo,
		loadMode:                     normalizeSkillLoadMode(options.loadMode),
		skipFallbackOnSessionSummary: options.skipFallbackOnSessionSummary,
	}
}

// ProcessRequest implements flow.RequestProcessor.
func (p *SkillsToolResultRequestProcessor) ProcessRequest(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil || inv == nil || inv.Session == nil || p.repo == nil {
		return
	}

	loaded := p.getLoadedSkills(inv)
	if len(loaded) == 0 {
		p.removeLoadedContextMessage(req)
		return
	}
	sort.Strings(loaded) // stable prompt order

	toolCalls := indexToolCalls(req.Messages)
	lastToolMsgIdx := lastSkillToolMsgIndex(
		req.Messages,
		toolCalls,
	)

	materialized := make(map[string]struct{}, len(lastToolMsgIdx))
	for skillName, idx := range lastToolMsgIdx {
		if idx < 0 || idx >= len(req.Messages) {
			continue
		}
		msg := &req.Messages[idx]
		out, ok := p.buildToolResultContent(
			ctx,
			inv,
			skillName,
			msg.Content,
		)
		if !ok {
			continue
		}
		msg.Content = out
		materialized[skillName] = struct{}{}
	}

	fallbackContent := p.buildFallbackSystemContent(
		ctx,
		inv,
		loaded,
		materialized,
	)
	if p.skipFallbackOnSessionSummary && hasSessionSummary(inv) {
		p.removeLoadedContextMessage(req)
	} else {
		p.upsertLoadedContextMessage(req, fallbackContent)
	}

	p.maybeOffloadLoadedSkills(ctx, inv, loaded, ch)
}

func hasSessionSummary(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	raw, ok := inv.GetState(contentHasSessionSummaryStateKey)
	if !ok {
		return false
	}
	v, ok := raw.(bool)
	return ok && v
}

func (p *SkillsToolResultRequestProcessor) getLoadedSkills(
	inv *agent.Invocation,
) []string {
	state := inv.Session.SnapshotState()
	if len(state) == 0 {
		return nil
	}
	var names []string
	for k, v := range state {
		if !strings.HasPrefix(k, skill.StateKeyLoadedPrefix) {
			continue
		}
		if len(v) == 0 {
			continue
		}
		name := strings.TrimPrefix(k, skill.StateKeyLoadedPrefix)
		if strings.TrimSpace(name) == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

type toolCallIndex map[string]model.ToolCall

func indexToolCalls(msgs []model.Message) toolCallIndex {
	out := make(toolCallIndex)
	for _, m := range msgs {
		if m.Role != model.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if strings.TrimSpace(tc.ID) == "" {
				continue
			}
			out[tc.ID] = tc
		}
	}
	return out
}

func lastSkillToolMsgIndex(
	msgs []model.Message,
	calls toolCallIndex,
) map[string]int {
	out := make(map[string]int)
	for idx, m := range msgs {
		if m.Role != model.RoleTool {
			continue
		}
		if m.ToolName != skillToolLoad &&
			m.ToolName != skillToolSelectDocs {
			continue
		}
		skillName := skillNameFromToolMessage(m, calls)
		if skillName == "" {
			continue
		}
		out[skillName] = idx
	}
	return out
}

type skillNameInput struct {
	Skill string `json:"skill"`
}

func skillNameFromToolMessage(
	m model.Message,
	calls toolCallIndex,
) string {
	if m.ToolID != "" {
		if tc, ok := calls[m.ToolID]; ok {
			var in skillNameInput
			if err := json.Unmarshal(
				[]byte(tc.Function.Arguments),
				&in,
			); err == nil {
				return strings.TrimSpace(in.Skill)
			}
		}
	}
	// Fallback: parse the short tool output ("loaded: <name>") if
	// available.
	return parseLoadedSkillFromText(m.Content)
}

const loadedPrefix = "loaded:"

func parseLoadedSkillFromText(content string) string {
	s := strings.TrimSpace(content)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, loadedPrefix) {
		return ""
	}
	name := strings.TrimSpace(s[len(loadedPrefix):])
	return name
}

func isLoadedToolStub(toolOutput string, skillName string) bool {
	name := parseLoadedSkillFromText(toolOutput)
	if name == "" {
		return false
	}
	return strings.EqualFold(name, skillName)
}

func (p *SkillsToolResultRequestProcessor) buildToolResultContent(
	ctx context.Context,
	inv *agent.Invocation,
	skillName string,
	toolOutput string,
) (string, bool) {
	sk, err := p.repo.Get(skillName)
	if err != nil || sk == nil {
		log.WarnfContext(
			ctx,
			"skills: get %s failed: %v",
			skillName,
			err,
		)
		return "", false
	}

	var b strings.Builder
	base := strings.TrimSpace(toolOutput)
	if base != "" && isLoadedToolStub(base, skillName) {
		base = ""
	}
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}

	if strings.TrimSpace(sk.Body) != "" {
		b.WriteString("[Loaded] ")
		b.WriteString(skillName)
		b.WriteString("\n\n")
		b.WriteString(sk.Body)
		b.WriteString("\n")
	}

	sel := p.getDocsSelection(inv, skillName)
	b.WriteString("Docs loaded: ")
	if len(sel) == 0 {
		b.WriteString("none\n")
	} else {
		b.WriteString(strings.Join(sel, ", "))
		b.WriteString("\n")
	}

	if len(sel) > 0 {
		if docText := buildDocsText(sk, sel); docText != "" {
			b.WriteString(docText)
		}
	}
	return b.String(), true
}

func (p *SkillsToolResultRequestProcessor) getDocsSelection(
	inv *agent.Invocation,
	name string,
) []string {
	if inv == nil || inv.Session == nil {
		return nil
	}
	key := skill.StateKeyDocsPrefix + name
	v, ok := inv.Session.GetState(key)
	if !ok || len(v) == 0 {
		return nil
	}
	if string(v) == "*" {
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

func buildDocsText(sk *skill.Skill, wanted []string) string {
	if sk == nil || len(sk.Docs) == 0 {
		return ""
	}
	want := make(map[string]struct{}, len(wanted))
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
		b.WriteString("\n[Doc] ")
		b.WriteString(d.Path)
		b.WriteString("\n\n")
		b.WriteString(d.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func (p *SkillsToolResultRequestProcessor) buildFallbackSystemContent(
	ctx context.Context,
	inv *agent.Invocation,
	loaded []string,
	materialized map[string]struct{},
) string {
	var missing []string
	for _, name := range loaded {
		if _, ok := materialized[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(skillsLoadedContextHeader)
	b.WriteString("\n")
	for _, name := range missing {
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
		if strings.TrimSpace(sk.Body) != "" {
			b.WriteString("\n[Loaded] ")
			b.WriteString(name)
			b.WriteString("\n\n")
			b.WriteString(sk.Body)
			b.WriteString("\n")
		}
		sel := p.getDocsSelection(inv, name)
		b.WriteString("Docs loaded: ")
		if len(sel) == 0 {
			b.WriteString("none\n")
		} else {
			b.WriteString(strings.Join(sel, ", "))
			b.WriteString("\n")
		}
		if len(sel) > 0 {
			if docText := buildDocsText(sk, sel); docText != "" {
				b.WriteString(docText)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func (p *SkillsToolResultRequestProcessor) upsertLoadedContextMessage(
	req *model.Request,
	content string,
) {
	if req == nil {
		return
	}
	text := strings.TrimSpace(content)
	if text == "" {
		p.removeLoadedContextMessage(req)
		return
	}
	idx := findLoadedContextMessageIndex(req.Messages)
	if idx >= 0 {
		req.Messages[idx].Content = text
		return
	}
	msg := model.NewSystemMessage(text)
	insertAfterLastSystemMessage(req, msg)
}

func (p *SkillsToolResultRequestProcessor) removeLoadedContextMessage(
	req *model.Request,
) {
	if req == nil || len(req.Messages) == 0 {
		return
	}
	idx := findLoadedContextMessageIndex(req.Messages)
	if idx < 0 {
		return
	}
	req.Messages = append(req.Messages[:idx], req.Messages[idx+1:]...)
}

func findLoadedContextMessageIndex(msgs []model.Message) int {
	for i, m := range msgs {
		if m.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(m.Content, skillsLoadedContextHeader) {
			return i
		}
	}
	return -1
}

func insertAfterLastSystemMessage(
	req *model.Request,
	msg model.Message,
) {
	if req == nil {
		return
	}
	systemMsgIndex := findLastSystemMessageIndex(req.Messages)
	if systemMsgIndex >= 0 {
		req.Messages = append(
			req.Messages[:systemMsgIndex+1],
			append([]model.Message{msg},
				req.Messages[systemMsgIndex+1:]...)...,
		)
		return
	}
	req.Messages = append([]model.Message{msg}, req.Messages...)
}

func (p *SkillsToolResultRequestProcessor) maybeOffloadLoadedSkills(
	ctx context.Context,
	inv *agent.Invocation,
	loaded []string,
	ch chan<- *event.Event,
) {
	if p.loadMode != SkillLoadModeOnce ||
		inv == nil ||
		inv.Session == nil ||
		len(loaded) == 0 {
		return
	}
	delta := make(map[string][]byte, len(loaded)*2)
	for _, name := range loaded {
		loadedKey := skill.StateKeyLoadedPrefix + name
		inv.Session.SetState(loadedKey, nil)
		delta[loadedKey] = nil

		docsKey := skill.StateKeyDocsPrefix + name
		inv.Session.SetState(docsKey, nil)
		delta[docsKey] = nil
	}
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(delta),
	))
}
