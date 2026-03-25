//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import "context"

// This file defines a draft sandbox API surface for future integration.
//
// The intent is to keep the design additive for now:
//   - existing WorkspacePolicy and RunProgramSpec still work unchanged
//   - current local/container runtimes do not need to implement these types yet
//   - future work can wire policy resolution, approval, and backend selection
//     into workspace_exec, localexec, and openclaw host tools incrementally

// ExecutionIntent identifies why code execution is being requested.
type ExecutionIntent string

const (
	// ExecutionIntentCodeBlocks is used for direct code block execution.
	ExecutionIntentCodeBlocks ExecutionIntent = "code_blocks"
	// ExecutionIntentWorkspaceExec is used by the workspace_exec tool.
	ExecutionIntentWorkspaceExec ExecutionIntent = "workspace_exec"
	// ExecutionIntentLocalExec is used by local code execution in llmagent.
	ExecutionIntentLocalExec ExecutionIntent = "localexec"
	// ExecutionIntentOpenClawExecCommand is used by the OpenClaw exec_command tool.
	ExecutionIntentOpenClawExecCommand ExecutionIntent = "openclaw_exec_command"
	// ExecutionIntentOpenClawLocalExec is used when OpenClaw enables localexec.
	ExecutionIntentOpenClawLocalExec ExecutionIntent = "openclaw_local_exec"
	// ExecutionIntentOpenClawCron is used for scheduled OpenClaw executions.
	ExecutionIntentOpenClawCron ExecutionIntent = "openclaw_cron"
	// ExecutionIntentOpenClawBackgroundSession is used for long-lived
	// interactive or background OpenClaw sessions.
	ExecutionIntentOpenClawBackgroundSession ExecutionIntent = "openclaw_background_session"
)

// FileSystemAccessMode describes the baseline filesystem posture.
type FileSystemAccessMode string

const (
	// FileSystemAccessNone denies all filesystem access except what an
	// implementation must expose internally to start the command.
	FileSystemAccessNone FileSystemAccessMode = "none"
	// FileSystemAccessReadOnly allows read-only filesystem access.
	FileSystemAccessReadOnly FileSystemAccessMode = "read_only"
	// FileSystemAccessWorkspaceWrite allows writes only to explicit writable roots.
	FileSystemAccessWorkspaceWrite FileSystemAccessMode = "workspace_write"
	// FileSystemAccessFull grants unrestricted filesystem access.
	FileSystemAccessFull FileSystemAccessMode = "full"
)

// FileSystemPolicy describes the desired filesystem view for a command.
//
// Paths are expected to be absolute host paths or resolved workspace-relative
// paths after policy resolution. Backends may normalize or reject unsupported
// paths before execution.
type FileSystemPolicy struct {
	Mode FileSystemAccessMode
	// ReadOnlyRoots are roots that should be visible as read-only.
	ReadOnlyRoots []string
	// WritableRoots are roots that may be written.
	WritableRoots []string
	// ProtectedRoots stay read-only even when an ancestor appears in WritableRoots.
	ProtectedRoots []string
	// IncludePlatformDefaults exposes system-level read-only paths needed for
	// common runtimes, dynamic loaders, and interpreters.
	IncludePlatformDefaults bool
}

// NetworkAccessMode describes the outbound network posture for a command.
type NetworkAccessMode string

const (
	// NetworkAccessNone denies outbound network access.
	NetworkAccessNone NetworkAccessMode = "none"
	// NetworkAccessRestricted allows limited outbound access according to allowlists.
	NetworkAccessRestricted NetworkAccessMode = "restricted"
	// NetworkAccessProxyOnly requires outbound traffic to flow through a managed proxy.
	NetworkAccessProxyOnly NetworkAccessMode = "proxy_only"
	// NetworkAccessFull grants unrestricted outbound network access.
	NetworkAccessFull NetworkAccessMode = "full"
)

// NetworkPolicy describes desired outbound network permissions.
type NetworkPolicy struct {
	Mode NetworkAccessMode
	// AllowDomains is an optional domain allowlist for restricted mode.
	AllowDomains []string
	// AllowCIDRs is an optional CIDR allowlist for restricted mode.
	AllowCIDRs []string
	// AllowLoopback controls whether loopback addresses remain reachable.
	AllowLoopback bool
	// ProxyURL identifies the managed proxy endpoint when proxy-only mode is used.
	ProxyURL string
}

// EnvironmentInheritanceMode controls how much host environment is inherited.
type EnvironmentInheritanceMode string

const (
	// EnvironmentInheritanceNone starts from an empty environment.
	EnvironmentInheritanceNone EnvironmentInheritanceMode = "none"
	// EnvironmentInheritanceMinimal starts from a small implementation-defined baseline.
	EnvironmentInheritanceMinimal EnvironmentInheritanceMode = "minimal"
	// EnvironmentInheritanceHost starts from the host process environment.
	EnvironmentInheritanceHost EnvironmentInheritanceMode = "host"
)

// EnvironmentPolicy describes environment inheritance and explicit mutation.
type EnvironmentPolicy struct {
	Inheritance EnvironmentInheritanceMode
	// Allow is an optional allowlist of inherited variables.
	Allow []string
	// Deny is an optional denylist applied after Allow.
	Deny []string
	// Set injects or overrides environment values for the child process.
	Set map[string]string
	// Unset removes variables after inheritance and Set are applied.
	Unset []string
}

// ApprovalAction is the control-plane outcome for a command request.
type ApprovalAction string

const (
	// ApprovalActionAllow permits execution to continue.
	ApprovalActionAllow ApprovalAction = "allow"
	// ApprovalActionPrompt requires human or service-level approval.
	ApprovalActionPrompt ApprovalAction = "prompt"
	// ApprovalActionDeny blocks execution.
	ApprovalActionDeny ApprovalAction = "deny"
)

// ApprovalRule matches a request and yields an ApprovalAction.
//
// Matcher fields intentionally stay generic in this draft. Implementations may
// interpret entries as exact values, prefixes, globs, or regexes depending on
// the integration layer.
type ApprovalRule struct {
	Name string
	// Intents narrows the rule to a subset of execution intents.
	Intents []ExecutionIntent
	// Commands narrows the rule to command matcher expressions.
	Commands []string
	// Paths narrows the rule to filesystem path matcher expressions.
	Paths []string
	// Domains narrows the rule to network destination matcher expressions.
	Domains []string
	Action  ApprovalAction
}

// ApprovalPolicy describes how a request should be gated before execution.
type ApprovalPolicy struct {
	DefaultAction ApprovalAction
	// RequireApprover forces prompt requests to fail closed when no approver exists.
	RequireApprover bool
	Rules           []ApprovalRule
}

// ExecutionPolicy is the intended successor to the legacy WorkspacePolicy.
//
// It combines execution intent with split filesystem, network, environment,
// resource, and approval controls in one explicit policy object.
type ExecutionPolicy struct {
	Intent      ExecutionIntent
	FileSystem  FileSystemPolicy
	Network     NetworkPolicy
	Environment EnvironmentPolicy
	Limits      ResourceLimits
	Approval    ApprovalPolicy
}

// AdditionalPermissions describes temporary, request-scoped policy expansion.
//
// This is intended for cases where a command needs narrowly scoped extra
// privileges on top of a stable baseline policy.
type AdditionalPermissions struct {
	Reason string

	ReadOnlyRoots  []string
	WritableRoots  []string
	ProtectedRoots []string

	AllowDomains []string
	AllowCIDRs   []string

	SetEnv   map[string]string
	UnsetEnv []string
}

// PolicyResolveRequest is the input to a SandboxPolicyResolver.
type PolicyResolveRequest struct {
	Intent    ExecutionIntent
	Workspace Workspace
	Spec      RunProgramSpec
	Metadata  map[string]string
}

// ApprovalRequest is the input to an ApprovalDecider.
type ApprovalRequest struct {
	Workspace Workspace
	Spec      RunProgramSpec
	Policy    ExecutionPolicy
	Metadata  map[string]string
}

// ApprovalResult captures the approval outcome for one request.
type ApprovalResult struct {
	Action ApprovalAction
	Rule   string
	Reason string
}

// SandboxRunRequest is the high-level request accepted by a coordinator.
type SandboxRunRequest struct {
	Intent                ExecutionIntent
	Workspace             Workspace
	Spec                  RunProgramSpec
	AdditionalPermissions AdditionalPermissions
	Metadata              map[string]string
}

// SandboxStartProgramRequest is the high-level interactive request accepted by
// a coordinator.
type SandboxStartProgramRequest struct {
	Intent                ExecutionIntent
	Workspace             Workspace
	Spec                  InteractiveProgramSpec
	AdditionalPermissions AdditionalPermissions
	Metadata              map[string]string
}

// SandboxRequest is the final backend-facing execution request.
type SandboxRequest struct {
	Workspace             Workspace
	Spec                  RunProgramSpec
	Policy                ExecutionPolicy
	AdditionalPermissions AdditionalPermissions
	Metadata              map[string]string
}

// SandboxInteractiveRequest is the final backend-facing interactive request.
type SandboxInteractiveRequest struct {
	Workspace             Workspace
	Spec                  InteractiveProgramSpec
	Policy                ExecutionPolicy
	AdditionalPermissions AdditionalPermissions
	Metadata              map[string]string
}

// SandboxBackendCapabilities describes optional backend features.
type SandboxBackendCapabilities struct {
	ProcessIsolation    bool
	ContainerIsolation  bool
	NetworkIsolation    bool
	ProtectedSubpaths   bool
	InteractiveSessions bool
	PermissionOverlays  bool
}

// SandboxPolicyResolver turns a high-level request into an execution policy.
type SandboxPolicyResolver interface {
	ResolveSandboxPolicy(
		ctx context.Context,
		req PolicyResolveRequest,
	) (ExecutionPolicy, error)
}

// ApprovalDecider decides whether a request may proceed.
type ApprovalDecider interface {
	DecideSandboxApproval(
		ctx context.Context,
		req ApprovalRequest,
	) (ApprovalResult, error)
}

// SandboxBackend applies a concrete isolation backend to a resolved request.
type SandboxBackend interface {
	Name() string
	Capabilities() SandboxBackendCapabilities
	CanApply(req SandboxRequest) bool
	RunProgram(ctx context.Context, req SandboxRequest) (RunResult, error)
}

// SandboxInteractiveBackend applies a concrete isolation backend to an
// interactive request.
type SandboxInteractiveBackend interface {
	SandboxBackend
	CanStartProgram(req SandboxInteractiveRequest) bool
	StartProgram(
		ctx context.Context,
		req SandboxInteractiveRequest,
	) (ProgramSession, error)
}

// SandboxBackendSelector chooses the best backend for a request.
type SandboxBackendSelector interface {
	SelectSandboxBackend(
		ctx context.Context,
		req SandboxRequest,
		backends []SandboxBackend,
	) (SandboxBackend, error)
}

// SandboxInteractiveBackendSelector chooses the best backend for an
// interactive request.
type SandboxInteractiveBackendSelector interface {
	SelectSandboxInteractiveBackend(
		ctx context.Context,
		req SandboxInteractiveRequest,
		backends []SandboxBackend,
	) (SandboxInteractiveBackend, error)
}
