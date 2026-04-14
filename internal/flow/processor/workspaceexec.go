//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	toolworkspaceexec "trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

const (
	workspaceExecGuidanceHeader = "Executor workspace guidance:"
)

type workspaceExecRequestProcessorOptions struct {
	sessionTools     bool
	hasSkillsRepo    bool
	repoResolver     func(*agent.Invocation) skill.Repository
	enabledResolver  func(*agent.Invocation) bool
	sessionsResolver func(*agent.Invocation) bool
}

// WorkspaceExecRequestProcessorOption configures
// WorkspaceExecRequestProcessor.
type WorkspaceExecRequestProcessorOption func(*workspaceExecRequestProcessorOptions)

// WithWorkspaceExecSessionsEnabled tells the processor that the
// workspace_exec companion session tools are registered.
func WithWorkspaceExecSessionsEnabled() WorkspaceExecRequestProcessorOption {
	return func(o *workspaceExecRequestProcessorOptions) {
		o.sessionTools = true
	}
}

// WithWorkspaceExecEnabledResolver sets an invocation-aware workspace_exec
// capability resolver.
func WithWorkspaceExecEnabledResolver(
	resolver func(*agent.Invocation) bool,
) WorkspaceExecRequestProcessorOption {
	return func(o *workspaceExecRequestProcessorOptions) {
		o.enabledResolver = resolver
	}
}

// WithWorkspaceExecSessionsResolver sets an invocation-aware resolver for
// workspace_exec session helper capability.
func WithWorkspaceExecSessionsResolver(
	resolver func(*agent.Invocation) bool,
) WorkspaceExecRequestProcessorOption {
	return func(o *workspaceExecRequestProcessorOptions) {
		o.sessionsResolver = resolver
	}
}

// WithWorkspaceExecSkillsRepo indicates that skills are configured, so the
// workspace guidance can mention existing paths under skills/.
func WithWorkspaceExecSkillsRepo() WorkspaceExecRequestProcessorOption {
	return func(o *workspaceExecRequestProcessorOptions) {
		o.hasSkillsRepo = true
	}
}

// WithWorkspaceExecSkillsRepositoryResolver sets an invocation-aware skills repository resolver.
func WithWorkspaceExecSkillsRepositoryResolver(
	resolver func(*agent.Invocation) skill.Repository,
) WorkspaceExecRequestProcessorOption {
	return func(o *workspaceExecRequestProcessorOptions) {
		o.repoResolver = resolver
	}
}

// WorkspaceExecRequestProcessor injects system guidance for executor-side
// workspace_exec tools independently of skills repo wiring.
type WorkspaceExecRequestProcessor struct {
	sessionTools     bool
	staticSkillsRepo bool
	repoResolver     func(*agent.Invocation) skill.Repository
	enabledResolver  func(*agent.Invocation) bool
	sessionsResolver func(*agent.Invocation) bool
}

// NewWorkspaceExecRequestProcessor creates a new
// WorkspaceExecRequestProcessor.
func NewWorkspaceExecRequestProcessor(
	opts ...WorkspaceExecRequestProcessorOption,
) *WorkspaceExecRequestProcessor {
	var options workspaceExecRequestProcessorOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&options)
	}
	return &WorkspaceExecRequestProcessor{
		sessionTools:     options.sessionTools,
		staticSkillsRepo: options.hasSkillsRepo,
		repoResolver:     options.repoResolver,
		enabledResolver:  options.enabledResolver,
		sessionsResolver: options.sessionsResolver,
	}
}

// ProcessRequest implements flow.RequestProcessor.
func (p *WorkspaceExecRequestProcessor) ProcessRequest(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		return
	}

	guidance := p.guidanceText(inv)
	if guidance == "" {
		return
	}

	idx := findSystemMessageIndex(req.Messages)
	if idx >= 0 {
		sys := &req.Messages[idx]
		if !strings.Contains(sys.Content, workspaceExecGuidanceHeader) {
			if sys.Content != "" {
				sys.Content += "\n\n" + guidance
			} else {
				sys.Content = guidance
			}
		}
	} else {
		req.Messages = append([]model.Message{
			model.NewSystemMessage(guidance),
		}, req.Messages...)
	}

	if inv == nil {
		return
	}
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypePreprocessingInstruction),
	))
}

func (p *WorkspaceExecRequestProcessor) guidanceText(
	inv *agent.Invocation,
) string {
	if !p.enabledForInvocation(inv) {
		return ""
	}
	var b strings.Builder
	b.WriteString(workspaceExecGuidanceHeader)
	b.WriteString("\n")
	b.WriteString("- Treat workspace_exec as the default general shell ")
	b.WriteString("runner for shared executor-side work. It runs inside ")
	b.WriteString("the current executor workspace, not on the agent ")
	b.WriteString("host; workspace is its scope, not its capability ")
	b.WriteString("limit.\n")
	b.WriteString("- workspace_exec starts at the workspace root by ")
	b.WriteString("default. Prefer work/, out/, and runs/ for shared ")
	b.WriteString("executor-side work, and treat cwd as a ")
	b.WriteString("workspace-relative path.\n")
	b.WriteString("- Network access depends on the current executor ")
	b.WriteString("environment. If you need a network command such as ")
	b.WriteString("curl, use a small bounded command to verify whether ")
	b.WriteString("that environment allows it.\n")
	b.WriteString("- When a limitation depends on the executor ")
	b.WriteString("environment and a small bounded command can verify ")
	b.WriteString("it, verify first before claiming the limitation. ")
	b.WriteString("This applies to checks such as command ")
	b.WriteString("availability, file presence, or access to a known ")
	b.WriteString("URL.\n")
	if toolworkspaceexec.SupportsArtifactSave(inv) {
		b.WriteString("- Use workspace_save_artifact only when you ")
		b.WriteString("need a stable artifact reference for an already ")
		b.WriteString("existing file in work/, out/, or runs/. ")
		b.WriteString("Intermediate files usually stay in the workspace.\n")
	}
	if p.hasSkillsRepo(inv) {
		b.WriteString("- Paths under skills/ are only useful when some ")
		b.WriteString("other tool has already placed content there. ")
		b.WriteString("workspace_exec does not stage skills ")
		b.WriteString("automatically.\n")
	}
	if p.sessionToolsForInvocation(inv) {
		b.WriteString("- When workspace_exec starts a command that keeps ")
		b.WriteString("running or waits for stdin, continue with ")
		b.WriteString("workspace_write_stdin. When chars is empty, ")
		b.WriteString("workspace_write_stdin acts like a poll. Use ")
		b.WriteString("workspace_kill_session to stop a running ")
		b.WriteString("workspace_exec session.\n")
		b.WriteString("- Interactive workspace_exec sessions are only ")
		b.WriteString("guaranteed within the current invocation. Do not ")
		b.WriteString("assume a later user message can resume the same ")
		b.WriteString("session.\n")
	}
	return b.String()
}

func (p *WorkspaceExecRequestProcessor) enabledForInvocation(
	inv *agent.Invocation,
) bool {
	if p.enabledResolver != nil {
		return p.enabledResolver(inv)
	}
	return true
}

func (p *WorkspaceExecRequestProcessor) sessionToolsForInvocation(
	inv *agent.Invocation,
) bool {
	if p.sessionsResolver != nil {
		return p.sessionsResolver(inv)
	}
	return p.sessionTools
}

func (p *WorkspaceExecRequestProcessor) hasSkillsRepo(
	inv *agent.Invocation,
) bool {
	if p.repoResolver != nil {
		return p.repoResolver(inv) != nil
	}
	return p.staticSkillsRepo
}
