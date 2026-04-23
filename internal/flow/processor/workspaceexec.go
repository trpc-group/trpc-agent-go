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
	sessionTools      bool
	hasSkillsRepo     bool
	repoResolver      func(*agent.Invocation) skill.Repository
	enabledResolver   func(*agent.Invocation) bool
	sessionsResolver  func(*agent.Invocation) bool
	fileToolsResolver func(*agent.Invocation) bool
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

// WithWorkspaceFileToolsEnabledResolver sets an invocation-aware
// resolver that reports whether the workspace file tools
// (workspace_read_file, workspace_list_dir, workspace_search_file,
// workspace_search_content, workspace_write_file,
// workspace_replace_content) are exposed for this invocation. When
// it returns true the processor appends a file-tool guidance block
// to the workspace system prompt so the model prefers the dedicated
// tools over running raw shell commands via workspace_exec. The
// resolver is independent of WithWorkspaceExecEnabledResolver so it
// works in the "file-tools-only" configuration described by
// llmagent.WithWorkspaceFileToolsEnabled.
func WithWorkspaceFileToolsEnabledResolver(
	resolver func(*agent.Invocation) bool,
) WorkspaceExecRequestProcessorOption {
	return func(o *workspaceExecRequestProcessorOptions) {
		o.fileToolsResolver = resolver
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
// workspace_exec and workspace file tools independently of skills
// repo wiring. The processor emits guidance when either surface is
// active for the invocation; the shared preamble steers the model
// toward workspace-aware tools regardless of which half is on.
type WorkspaceExecRequestProcessor struct {
	sessionTools      bool
	staticSkillsRepo  bool
	repoResolver      func(*agent.Invocation) skill.Repository
	enabledResolver   func(*agent.Invocation) bool
	sessionsResolver  func(*agent.Invocation) bool
	fileToolsResolver func(*agent.Invocation) bool
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
		sessionTools:      options.sessionTools,
		staticSkillsRepo:  options.hasSkillsRepo,
		repoResolver:      options.repoResolver,
		enabledResolver:   options.enabledResolver,
		sessionsResolver:  options.sessionsResolver,
		fileToolsResolver: options.fileToolsResolver,
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
	execOn := p.execEnabledForInvocation(inv)
	fileToolsOn := p.fileToolsEnabledForInvocation(inv)
	if !execOn && !fileToolsOn {
		return ""
	}
	var b strings.Builder
	b.WriteString(workspaceExecGuidanceHeader)
	b.WriteString("\n")
	b.WriteString("- The executor workspace is the shared working ")
	b.WriteString("directory for this invocation. Every workspace_* ")
	b.WriteString("tool operates inside that workspace, not on the ")
	b.WriteString("agent host; workspace is their scope, not their ")
	b.WriteString("capability limit.\n")
	b.WriteString("- Prefer work/, out/, and runs/ for shared ")
	b.WriteString("executor-side work, and treat any workspace-tool ")
	b.WriteString("path or cwd as a workspace-relative path rooted at ")
	b.WriteString("the workspace root.\n")
	b.WriteString("- Conversation file inputs that are attached to the ")
	b.WriteString("current request or visible session are staged ")
	b.WriteString("automatically under work/inputs before any ")
	b.WriteString("workspace tool runs; do not recreate them.\n")
	if execOn {
		b.WriteString("- Treat workspace_exec as the default general ")
		b.WriteString("shell runner for shared executor-side work. It ")
		b.WriteString("starts at the workspace root by default.\n")
		b.WriteString("- Network access depends on the current executor ")
		b.WriteString("environment. If you need a network command such ")
		b.WriteString("as curl, use a small bounded command to verify ")
		b.WriteString("whether that environment allows it.\n")
		b.WriteString("- When a limitation depends on the executor ")
		b.WriteString("environment and a small bounded command can ")
		b.WriteString("verify it, verify first before claiming the ")
		b.WriteString("limitation. This applies to checks such as ")
		b.WriteString("command availability, file presence, or access ")
		b.WriteString("to a known URL.\n")
	}
	if fileToolsOn {
		if execOn {
			b.WriteString("- Prefer the dedicated workspace file tools ")
			b.WriteString("over ad-hoc shell commands when the task is ")
			b.WriteString("a plain file operation: workspace_read_file ")
			b.WriteString("to read text, workspace_list_dir to list a ")
			b.WriteString("directory, workspace_search_file to find ")
			b.WriteString("files by glob, and workspace_search_content ")
			b.WriteString("to grep file contents. Reserve workspace_exec ")
			b.WriteString("for commands that need a real shell.\n")
		} else {
			b.WriteString("- Use workspace_read_file, ")
			b.WriteString("workspace_list_dir, workspace_search_file, ")
			b.WriteString("and workspace_search_content to explore ")
			b.WriteString("and read files inside the workspace.\n")
		}
		b.WriteString("- Use workspace_write_file to create or ")
		b.WriteString("overwrite files, and workspace_replace_content ")
		b.WriteString("to edit existing files in place. Both refuse ")
		b.WriteString("to modify framework-managed paths: ")
		b.WriteString("work/inputs/** (staged conversation inputs), ")
		b.WriteString("skills/** (skill working copies), and any ")
		b.WriteString("paths declared as bootstrap file targets. ")
		b.WriteString("Reach those indirectly (for example copy to ")
		b.WriteString("work/ first) instead of writing them.\n")
		b.WriteString("- Respect the truncated / files_partial flags ")
		b.WriteString("on workspace_search_* and workspace_read_file ")
		b.WriteString("results: when they are set the result is ")
		b.WriteString("incomplete, so narrow the scope or raise the ")
		b.WriteString("limits (max_bytes, max_results, max_matches) ")
		b.WriteString("before concluding.\n")
	}
	if execOn && toolworkspaceexec.SupportsArtifactSave(inv) {
		b.WriteString("- Use workspace_save_artifact only when you ")
		b.WriteString("need a stable artifact reference for an already ")
		b.WriteString("existing file in work/, out/, or runs/. ")
		b.WriteString("Intermediate files usually stay in the workspace.\n")
	}
	if p.hasSkillsRepo(inv) {
		b.WriteString("- Paths under skills/<name> become populated ")
		b.WriteString("only after skill_load <name>: that call ")
		b.WriteString("materializes the writable skill working copy ")
		b.WriteString("and turns skills/<name>/ into a real directory ")
		b.WriteString("you can run commands from.")
		if execOn {
			b.WriteString(" After loading, set cwd to skills/<name> ")
			b.WriteString("for workspace_exec and run the scripts ")
			b.WriteString("referenced in that SKILL.md (for example, ")
			b.WriteString("the Examples section).")
		}
		b.WriteString(" Without a prior skill_load, paths under ")
		b.WriteString("skills/ are not staged and workspace tools will ")
		b.WriteString("not see them.\n")
	}
	if execOn && p.sessionToolsForInvocation(inv) {
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

func (p *WorkspaceExecRequestProcessor) execEnabledForInvocation(
	inv *agent.Invocation,
) bool {
	if p.enabledResolver != nil {
		return p.enabledResolver(inv)
	}
	return true
}

func (p *WorkspaceExecRequestProcessor) fileToolsEnabledForInvocation(
	inv *agent.Invocation,
) bool {
	if p.fileToolsResolver != nil {
		return p.fileToolsResolver(inv)
	}
	return false
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
