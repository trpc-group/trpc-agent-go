//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package skill provides skill-related tools (function calls)
// for loading skills on demand.
package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stateDeltaProvider is consumed by the flow to attach state delta
// on tool.response events.
type stateDeltaProvider interface {
	StateDelta(toolCallID string, args []byte, resultJSON []byte) map[string][]byte
}

// LoadTool enables loading a skill into session state.
// It produces deltas under prefixes defined by skill package.
type LoadTool struct {
	repo        skill.Repository
	description string
}

const defaultLoadToolDescription = "Load a skill body and optional docs. " +
	"Prefer progressive disclosure: load SKILL.md first, " +
	"then load only needed docs. " +
	"Safe to call multiple times to add or replace docs. " +
	"Do not call this to list skills; names and descriptions " +
	"are already in context. Use when a task needs a skill's " +
	"SKILL.md body and selected docs in context."

type loadToolOptions struct {
	description string
}

// LoadToolOption configures LoadTool.
type LoadToolOption func(*loadToolOptions)

// WithLoadToolDescription overrides the skill_load tool description.
func WithLoadToolDescription(
	description string,
) LoadToolOption {
	return func(o *loadToolOptions) {
		o.description = description
	}
}

// NewLoadTool creates a new LoadTool.
func NewLoadTool(repo skill.Repository) *LoadTool {
	return NewLoadToolWithOptions(repo)
}

// NewLoadToolWithOptions creates a new LoadTool with optional overrides.
func NewLoadToolWithOptions(
	repo skill.Repository,
	opts ...LoadToolOption,
) *LoadTool {
	options := loadToolOptions{
		description: defaultLoadToolDescription,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return &LoadTool{
		repo:        repo,
		description: options.description,
	}
}

// loadInput is the schema for skill_load.
type loadInput struct {
	Skill          string   `json:"skill"`
	Docs           []string `json:"docs,omitempty"`
	IncludeAllDocs bool     `json:"include_all_docs,omitempty"`
}

type invokeSkillRequestDetail struct {
	SkillName  string `json:"skill_name"`
	SkillID    string `json:"skill_id"`
	SafePath   string `json:"safe_path,omitempty"`
	PathSHA256 string `json:"path_sha256,omitempty"`
	PathBytes  int    `json:"path_bytes,omitempty"`
}

type invokeSkillResponseDetail struct {
	SkillName      string `json:"skill_name"`
	SkillID        string `json:"skill_id"`
	ContentSHA256  string `json:"content_sha256,omitempty"`
	ContentBytes   int    `json:"content_bytes,omitempty"`
	ContentPreview string `json:"content_preview,omitempty"`
}

// Declaration implements tool.Tool.
func (t *LoadTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "skill_load",
		Description: t.description,
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Load skill input",
			Required:    []string{"skill"},
			Properties: map[string]*tool.Schema{
				"skill": skillNameSchema(
					t.repo, "Skill name to load",
				),
				"docs": {
					Type: "array",
					Items: &tool.Schema{
						Type: "string",
					},
					Description: "Optional doc names to include (prefer few)",
				},
				"include_all_docs": {
					Type:        "boolean",
					Description: "Include all docs if true (use sparingly)",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "string",
			Description: "Status message",
		},
	}
}

// Call validates and returns a message for user feedback.
func (t *LoadTool) Call(ctx context.Context, args []byte) (result any, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var invocation *agent.Invocation
	if inv, ok := agent.InvocationFromContext(ctx); ok {
		invocation = inv
	}
	var in loadInput
	if err := json.Unmarshal(args, &in); err != nil {
		t.traceInvokeSkill(ctx, invocation, "", nil, "", "", time.Now(), err)
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	in.Skill = strings.TrimSpace(in.Skill)
	tracker := t.startInvokeSkill(ctx, invocation, in.Skill)
	if in.Skill == "" {
		err := fmt.Errorf("skill is required")
		tracker.finish(nil, "", "", err)
		return nil, fmt.Errorf("skill is required")
	}
	var loadedSkill *skill.Skill
	if t.repo != nil {
		// validate existence
		loadedSkill, err = skill.GetForContext(ctx, t.repo, in.Skill)
		if err != nil {
			err = fmt.Errorf("unknown skill: %s", in.Skill)
			tracker.finish(nil, "", "", err)
			return nil, err
		}
	}
	skillPath, skillContent := t.skillMaterial(ctx, in.Skill, loadedSkill)
	tracker.finish(loadedSkill, skillPath, skillContent, nil)
	return fmt.Sprintf("loaded: %s", in.Skill), nil
}

type invokeSkillTracker struct {
	ctx        context.Context
	invocation *agent.Invocation
	repo       skill.Repository
	span       trace.Span
	started    bool
	start      time.Time
	skillName  string
	skillID    string
}

func (t *LoadTool) startInvokeSkill(
	ctx context.Context,
	invocation *agent.Invocation,
	skillName string,
) *invokeSkillTracker {
	skillName = strings.TrimSpace(skillName)
	skillID := stableSkillID(t.repo, skillName)
	spanName := itelemetry.NewInvokeSkillSpanName(skillName)
	_, span, started := itrace.StartSpan(ctx, invocation, spanName)
	return &invokeSkillTracker{
		ctx:        ctx,
		invocation: invocation,
		repo:       t.repo,
		span:       span,
		started:    started,
		start:      time.Now(),
		skillName:  skillName,
		skillID:    skillID,
	}
}

func (t *LoadTool) traceInvokeSkill(
	ctx context.Context,
	invocation *agent.Invocation,
	skillName string,
	loadedSkill *skill.Skill,
	skillPath string,
	skillContent string,
	start time.Time,
	err error,
) {
	tracker := t.startInvokeSkill(ctx, invocation, skillName)
	tracker.start = start
	tracker.finish(loadedSkill, skillPath, skillContent, err)
}

func (t *invokeSkillTracker) finish(
	loadedSkill *skill.Skill,
	skillPath string,
	skillContent string,
	err error,
) {
	if t == nil {
		return
	}
	skillName := t.skillName
	description := ""
	if loadedSkill != nil {
		if loadedSkill.Summary.Name != "" {
			skillName = loadedSkill.Summary.Name
		}
		description = loadedSkill.Summary.Description
	}
	if skillName == "" {
		skillName = t.skillName
	}
	if skillName == "" {
		skillName = "_unknown"
	}
	if t.started {
		itelemetry.TraceInvokeSkill(t.span, &itelemetry.InvokeSkillAttributes{
			Invocation:       t.invocation,
			SkillName:        skillName,
			SkillID:          t.skillID,
			SkillDescription: description,
			Phase:            "materialize",
			Request:          invokeSkillRequestJSON(skillName, t.skillID, skillPath),
			Response:         invokeSkillResponseJSON(skillName, t.skillID, skillContent),
			Error:            err,
		})
		t.span.End()
	}
	itelemetry.ReportInvokeSkillMetrics(t.ctx, itelemetry.InvokeSkillMetricAttributes{
		SkillName: skillName,
		SkillID:   t.skillID,
		UserID:    invocationUserID(t.invocation),
		AgentID:   invocationAgentID(t.invocation),
		AgentName: invocationAgentName(t.invocation),
		Error:     err,
	}, time.Since(t.start))
}

func (t *LoadTool) skillMaterial(
	ctx context.Context,
	skillName string,
	loadedSkill *skill.Skill,
) (string, string) {
	var content string
	if loadedSkill != nil {
		content = loadedSkill.Body
	}
	if t.repo == nil {
		return "", content
	}
	dir, err := skill.PathForContext(ctx, t.repo, skillName)
	if err != nil || dir == "" {
		return "", content
	}
	path := filepath.Join(dir, skill.SkillFile)
	b, err := os.ReadFile(path)
	if err != nil {
		log.WarnfContext(ctx, "skill_load telemetry read failed for %s: %v", skillName, err)
		return path, content
	}
	return path, string(b)
}

func invokeSkillRequestJSON(skillName, skillID, path string) string {
	detail := invokeSkillRequestDetail{
		SkillName: skillName,
		SkillID:   skillID,
	}
	if path != "" {
		detail.SafePath = safeSkillPath(path)
		detail.PathSHA256 = sha256Hex(path)
		detail.PathBytes = len(path)
	}
	return mustJSON(detail)
}

func invokeSkillResponseJSON(skillName, skillID, content string) string {
	detail := invokeSkillResponseDetail{
		SkillName: skillName,
		SkillID:   skillID,
	}
	if content != "" {
		detail.ContentSHA256 = sha256Hex(content)
		detail.ContentBytes = len(content)
		detail.ContentPreview = truncateUTF8(content, 1024)
	}
	return mustJSON(detail)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func safeSkillPath(path string) string {
	file := filepath.Base(path)
	dir := filepath.Base(filepath.Dir(path))
	if dir == "." || dir == string(filepath.Separator) || dir == "" {
		return file
	}
	return filepath.Join(dir, file)
}

func stableSkillID(repo skill.Repository, skillName string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(fmt.Sprintf("%T\n", repo)))
	if rooted, ok := repo.(skill.RootedRepository); ok {
		roots := rooted.Roots()
		sort.Strings(roots)
		for _, root := range roots {
			_, _ = h.Write([]byte(sha256Hex(root)))
			_, _ = h.Write([]byte{'\n'})
		}
	}
	_, _ = h.Write([]byte(strings.TrimSpace(skillName)))
	sum := h.Sum(nil)
	return "skill_" + hex.EncodeToString(sum[:8])
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func invocationUserID(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	return inv.Session.UserID
}

func invocationAgentName(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	return inv.AgentName
}

func invocationAgentID(inv *agent.Invocation) string {
	return invocationAgentName(inv)
}

// StateDelta builds delta keys to mark loaded skill and doc selection.
func (t *LoadTool) StateDelta(_ string, args []byte, _ []byte) map[string][]byte {
	delta, _ := t.stateDelta("", args)
	return delta
}

// StateDeltaForInvocation writes agent-scoped state for the invocation.
func (t *LoadTool) StateDeltaForInvocation(
	inv *agent.Invocation,
	toolCallID string,
	args []byte,
	resultJSON []byte,
) map[string][]byte {
	_ = toolCallID
	_ = resultJSON

	var agentName string
	if inv != nil {
		agentName = inv.AgentName
	}
	delta, skillName := t.stateDelta(agentName, args)
	return appendLoadedOrderStateDelta(
		inv,
		agentName,
		delta,
		skillName,
	)
}

func (t *LoadTool) stateDelta(
	agentName string,
	args []byte,
) (map[string][]byte, string) {
	var in loadInput
	if err := json.Unmarshal(args, &in); err != nil {
		log.Warnf("skill_load state parse failed: %v", err)
		return nil, ""
	}
	in.Skill = strings.TrimSpace(in.Skill)
	if in.Skill == "" {
		return nil, ""
	}
	delta := make(map[string][]byte)
	// Mark as loaded.
	k := skill.LoadedKey(agentName, in.Skill)
	delta[k] = []byte("1")
	// Docs selection
	if in.IncludeAllDocs {
		dk := skill.DocsKey(agentName, in.Skill)
		delta[dk] = []byte("*")
	} else if len(in.Docs) > 0 {
		dk := skill.DocsKey(agentName, in.Skill)
		b, err := json.Marshal(in.Docs)
		if err == nil {
			delta[dk] = b
		}
	}
	return delta, in.Skill
}

var _ tool.Tool = (*LoadTool)(nil)
var _ tool.CallableTool = (*LoadTool)(nil)
var _ stateDeltaProvider = (*LoadTool)(nil)
