//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceExecToolName = "workspace_exec"
	reviewChecksCommand   = "run-go-checks.sh"
)

// governedCommandNames contains the list of commands that require approval
var governedCommandNames = governedCommands{
	reviewChecksCommand,
}

// governedCommands is the workspace command-approval configuration
type governedCommands []string

func (commands governedCommands) match(command string) (string, bool) {
	for _, governed := range commands {
		if strings.Contains(command, governed) {
			return governed, true
		}
	}
	return "", false
}

// workspaceExecInput is the subset of workspace_exec arguments this example
// governs. raw keeps every field from the model so callback rewrites never
// discard arguments owned by the framework or added in a future version.
type workspaceExecInput struct {
	Command       string            `json:"command"`
	CWD           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TTY           bool              `json:"tty,omitempty"`
	PTY           bool              `json:"pty,omitempty"`
	Stdin         string            `json:"stdin,omitempty"`
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	raw           map[string]json.RawMessage
}

// modifiedArguments encodes callback changes while preserving unknown fields
// from the original workspace_exec object.
func (in workspaceExecInput) modifiedArguments() ([]byte, error) {
	if in.raw == nil {
		type encodedInput workspaceExecInput
		return json.Marshal(encodedInput(in))
	}
	fields := make(map[string]json.RawMessage, len(in.raw))
	for name, value := range in.raw {
		fields[name] = value
	}
	command, err := json.Marshal(in.Command)
	if err != nil {
		return nil, err
	}
	fields["command"] = command
	if len(in.Env) == 0 {
		delete(fields, "env")
	} else {
		environment, err := json.Marshal(in.Env)
		if err != nil {
			return nil, err
		}
		fields["env"] = environment
	}
	return json.Marshal(fields)
}

// withContainerTimeout adds a process-level timeout only to non-interactive
// container calls. The framework timeout remains in place as a second bound.
func (in workspaceExecInput) withContainerTimeout() (workspaceExecInput, bool) {
	if in.usesInteractiveSession() || strings.TrimSpace(in.Command) == "" ||
		strings.HasPrefix(strings.TrimSpace(in.Command), "exec /usr/bin/timeout ") {
		return in, false
	}
	seconds := int(in.timeout() / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	in.Command = fmt.Sprintf(
		"exec /usr/bin/timeout --signal=TERM --kill-after=1s %ds sh -c %s",
		seconds,
		shellSingleQuote(in.Command),
	)
	return in, true
}

// usesInteractiveSession identifies calls whose lifecycle cannot be wrapped in
// a one-shot process timeout without changing workspace_exec semantics.
func (in workspaceExecInput) usesInteractiveSession() bool {
	return in.Background || in.TTY || in.PTY || in.Stdin != "" ||
		in.YieldTimeMS != nil || in.YieldMs != nil
}

// withAllowedEnvironment removes model-provided environment overrides except
// the one setting this example intentionally exposes.
func (in workspaceExecInput) withAllowedEnvironment() (workspaceExecInput, int) {
	filtered := 0
	for key, value := range in.Env {
		if key == "CGO_ENABLED" && (value == "0" || value == "1") {
			continue
		}
		delete(in.Env, key)
		filtered++
	}
	if len(in.Env) == 0 {
		in.Env = nil
	}
	return in, filtered
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// timeout applies the aliases accepted by workspace_exec and falls back to the
// example's five-minute execution budget.
func (in workspaceExecInput) timeout() time.Duration {
	seconds := in.Timeout
	if in.TimeoutSec != nil {
		seconds = *in.TimeoutSec
	} else if in.TimeoutSecOld != nil {
		seconds = *in.TimeoutSecOld
	}
	if seconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(seconds) * time.Second
}

func (in workspaceExecInput) envKeysJSON() string {
	keys := make([]string, 0, len(in.Env))
	for key := range in.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, _ := json.Marshal(keys)
	return string(encoded)
}

// approvalIdentity returns the run-local grant identity for one target call.
//
// Ordinary tools are authorized by tool name alone, so their identity is empty
// and grantKey.ToolName is the complete identity. workspace_exec is different:
// a simple configured command uses the configured command name, while a shell
// expression that combines operations uses its complete canonical arguments.
func approvalIdentity(toolName string, arguments []byte) (string, error) {
	// regular tool call has no identity needed, just recognized by tool name
	if toolName != workspaceExecToolName {
		return "", nil
	}
	input, err := decodeWorkspaceExecInput(arguments)
	if err != nil {
		return "", fmt.Errorf("decode workspace_exec approval arguments: %w", err)
	}

	governed, matched := governedCommandNames.match(input.Command)
	// complex shell commands use full command as identity
	if !matched || hasComplexShellStructure(input.Command) {
		identity, err := canonicalJSON(arguments)
		return string(identity), err
	}
	return governed, nil
}

// requiresApproval is the only risk-classification entry point used by the
// framework policy. Framework metadata governs ordinary tools; workspace_exec
// additionally checks its command against this example's configured commands.
func requiresApproval(
	req *tool.PermissionRequest,
	arguments []byte,
) (bool, error) {
	if req.ToolName != workspaceExecToolName {
		return req.Metadata.Destructive, nil
	}

	if req.Metadata.Destructive {
		return true, nil
	}
	input, err := decodeWorkspaceExecInput(arguments)
	if err != nil {
		return false, fmt.Errorf("decode workspace_exec approval arguments: %w", err)
	}
	_, matched := governedCommandNames.match(input.Command)
	return matched, nil
}

// canonicalJSON makes complex workspace_exec grants independent of JSON object
// key order without weakening any argument value.
func canonicalJSON(value []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("JSON value contains trailing data")
		}
		return nil, err
	}
	return json.Marshal(decoded)
}

// decodeWorkspaceExecInput retains fields that this example does not interpret.
// workspace_exec owns its public JSON schema; governance may change command or
// env, but it must not silently erase stdin, yield controls, or future fields
// while returning ModifiedArguments to the framework.
func decodeWorkspaceExecInput(arguments []byte) (workspaceExecInput, error) {
	var input workspaceExecInput
	if err := json.Unmarshal(arguments, &input); err != nil {
		return workspaceExecInput{}, err
	}
	if err := json.Unmarshal(arguments, &input.raw); err != nil {
		return workspaceExecInput{}, err
	}
	return input, nil
}

// executionStart is the workspace_exec state shared by permission handling and
// AfterTool auditing. arguments is the model's pre-rewrite JSON; original and
// executed are the decoded pre- and post-callback forms.
type executionStart struct {
	arguments []byte
	original  workspaceExecInput
	executed  workspaceExecInput
	startedAt time.Time
}

// reviewRunState coordinates permission decisions, workspace execution
// auditing, and monitoring metrics for one Review call.
type reviewRunState struct {
	mu                      sync.Mutex
	startedAt               time.Time
	toolCalls               int
	permissionInterceptions int
	sandboxDuration         time.Duration

	// exceptions counts callback-observed failures by monitoring category.
	exceptions map[string]int

	// pendingExecutions maps tool-call IDs to workspace calls prepared by
	// BeforeTool and awaiting a PermissionPolicy decision.
	pendingExecutions map[string]executionStart

	// executions maps tool-call IDs to approved workspace calls that have
	// started and are awaiting AfterTool auditing.
	executions map[string]executionStart

	// grants contains approvals reusable only within this Review call.
	grants map[grantKey]struct{}
}

// grantKey keeps tool grants separate. Identity is empty for ordinary tools,
// the configured command name for simple workspace calls, and canonical full
// arguments for complex workspace calls.
type grantKey struct {
	ToolName string
	Identity string
}

func newReviewRunState() *reviewRunState {
	return &reviewRunState{
		startedAt:         time.Now(),
		exceptions:        make(map[string]int),
		pendingExecutions: make(map[string]executionStart),
		executions:        make(map[string]executionStart),
		grants:            make(map[grantKey]struct{}),
	}
}

func (t *reviewRunState) grant(toolName, identity string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.grants[grantKey{ToolName: toolName, Identity: identity}] = struct{}{}
	t.mu.Unlock()
}

func (t *reviewRunState) hasGrant(toolName, identity string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.grants[grantKey{ToolName: toolName, Identity: identity}]
	return ok
}

// prepareWorkspaceExecution records the model-visible and executable forms
// before PermissionPolicy runs. Only workspace_exec needs this bridge because
// its BeforeTool callback may rewrite the command for container execution.
func (t *reviewRunState) prepareWorkspaceExecution(
	toolCallID string,
	arguments []byte,
	original workspaceExecInput,
	executed workspaceExecInput,
) error {
	if t == nil || toolCallID == "" {
		return errors.New("workspace execution requires a non-empty tool call id")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.pendingExecutions[toolCallID]; exists {
		return fmt.Errorf("workspace execution %q is already pending", toolCallID)
	}
	if _, exists := t.executions[toolCallID]; exists {
		return fmt.Errorf("workspace execution %q already started", toolCallID)
	}
	t.pendingExecutions[toolCallID] = executionStart{
		arguments: bytes.Clone(arguments),
		original:  original,
		executed:  executed,
	}
	return nil
}

func (t *reviewRunState) pendingWorkspaceExecution(
	toolCallID string,
) (executionStart, bool) {
	if t == nil || toolCallID == "" {
		return executionStart{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	pending, ok := t.pendingExecutions[toolCallID]
	if !ok {
		return executionStart{}, false
	}
	pending.arguments = bytes.Clone(pending.arguments)
	return pending, true
}

func (t *reviewRunState) discardPendingExecution(toolCallID string) {
	if t == nil || toolCallID == "" {
		return
	}
	t.mu.Lock()
	delete(t.pendingExecutions, toolCallID)
	t.mu.Unlock()
}

func (t *reviewRunState) recordToolCall() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.toolCalls++
	t.mu.Unlock()
}

// recordPermissionInterception counts calls that matched a governed capability,
// including calls allowed by an existing grant.
func (t *reviewRunState) recordPermissionInterception() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.permissionInterceptions++
	t.mu.Unlock()
}

// beginExecution is the PermissionPolicy Allow-to-running transition. AfterTool
// must later consume the same entry through finishExecution.
func (t *reviewRunState) beginExecution(toolCallID string) error {
	if t == nil || toolCallID == "" {
		return errors.New("workspace execution requires a non-empty tool call id")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	pending, ok := t.pendingExecutions[toolCallID]
	if !ok {
		return fmt.Errorf("workspace execution %q was not prepared by BeforeTool", toolCallID)
	}
	if _, exists := t.executions[toolCallID]; exists {
		return fmt.Errorf("workspace execution %q already started", toolCallID)
	}
	start := pending
	start.startedAt = time.Now()
	t.executions[toolCallID] = start
	delete(t.pendingExecutions, toolCallID)
	return nil
}

// finishExecution closes the exact approved attempt and accounts for its
// duration. Success or failure belongs to the persisted sandbox record.
func (t *reviewRunState) finishExecution(toolCallID string) (executionStart, time.Time, bool) {
	if t == nil {
		return executionStart{}, time.Time{}, false
	}
	finishedAt := time.Now()
	t.mu.Lock()
	start, ok := t.executions[toolCallID]
	if ok {
		delete(t.executions, toolCallID)
		t.sandboxDuration += finishedAt.Sub(start.startedAt)
	}
	t.mu.Unlock()
	return start, finishedAt, ok
}

func (t *reviewRunState) recordException(kind string) {
	if t == nil || kind == "" {
		return
	}
	t.mu.Lock()
	t.exceptions[kind]++
	t.mu.Unlock()
}

// newReviewPermissionPolicy connects the framework permission lifecycle to
// run-local grants and audit records.
//
// Ordinary tools use req.Metadata for risk and tool-name-only grants.
// workspace_exec uses its original pre-Callback arguments for classification
// and identity, but records and executes req.Arguments after callback rewrites.
func newReviewPermissionPolicy(
	recorder *reviewRecorder,
	tracker *reviewRunState,
) tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(
		ctx context.Context,
		req *tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		if req == nil {
			return tool.DenyPermission("permission request is missing"), nil
		}
		tracker.recordToolCall()
		taskID, err := reviewTaskIDFromContext(ctx)
		if err != nil {
			return tool.PermissionDecision{}, err
		}

		decision := tool.AllowPermission()
		approvalArguments := req.Arguments
		var execInput workspaceExecInput
		if req.ToolName == workspaceExecToolName {
			pending, ok := tracker.pendingWorkspaceExecution(req.ToolCallID)
			if !ok {
				return tool.PermissionDecision{}, fmt.Errorf(
					"workspace execution %q reached permission without BeforeTool preparation",
					req.ToolCallID,
				)
			}
			approvalArguments = pending.arguments
			execInput, err = decodeWorkspaceExecInput(req.Arguments)
			if err != nil {
				tracker.discardPendingExecution(req.ToolCallID)
				return tool.PermissionDecision{}, fmt.Errorf("decode workspace_exec permission arguments: %w", err)
			}
		}
		requiresApproval, err := requiresApproval(req, approvalArguments)
		if err != nil {
			tracker.discardPendingExecution(req.ToolCallID)
			return tool.PermissionDecision{}, err
		}
		if requiresApproval {
			tracker.recordPermissionInterception()
			identity, err := approvalIdentity(req.ToolName, approvalArguments)
			if err != nil {
				tracker.discardPendingExecution(req.ToolCallID)
				return tool.PermissionDecision{}, err
			}
			if tracker.hasGrant(req.ToolName, identity) {
				decision = tool.AllowPermission()
			} else {
				decision = tool.AskPermission(
					"Call request_tool_permission with this target tool and its exact arguments, then retry the target tool only if permission is granted.",
				)
			}
		}

		commandPreview := string(req.Arguments)
		if execInput.Command != "" {
			commandPreview = execInput.Command
		}
		if err := recordPermission(ctx, recorder, taskID, req, commandPreview, decision); err != nil {
			tracker.discardPendingExecution(req.ToolCallID)
			return tool.PermissionDecision{}, err
		}
		if decision.Action != tool.PermissionActionAllow {
			tracker.discardPendingExecution(req.ToolCallID)
			return decision, nil
		}
		if req.ToolName != workspaceExecToolName {
			return decision, nil
		}
		if err := tracker.beginExecution(req.ToolCallID); err != nil {
			tracker.discardPendingExecution(req.ToolCallID)
			return tool.PermissionDecision{}, err
		}
		return decision, nil
	})
}

// recordPermission persists the framework policy decision for audit and
// monitoring. User decisions made by request_tool_permission are recorded
// separately as permission_request events.
func recordPermission(
	ctx context.Context,
	recorder *reviewRecorder,
	taskID string,
	req *tool.PermissionRequest,
	command string,
	decision tool.PermissionDecision,
) error {
	if recorder == nil {
		return fmt.Errorf("review recorder is not configured")
	}
	return recorder.RecordPermissionDecision(ctx, taskID, store.PermissionDecisionRecord{
		ToolCallID: req.ToolCallID, DecisionKind: "tool_permission",
		Operation: req.ToolName, ToolName: req.ToolName, CommandPreview: command,
		Decision: string(decision.Action), Reason: decision.Reason,
	})
}

// hasComplexShellStructure reports whether a command runs multiple actions
// as one. Forms like `cmd1 && cmd2` or `a | b`, which stitch several commands
// together, count as complex. Characters inside quotes, or characters escaped
// with a backslash, are just argument data (for example, the `&&` inside
// `echo "&&"`) and don't count as composition.
//
// The one exception is `$(...)` and “ `...` “ written inside double quotes:
// even inside double quotes shell still executes them, so they still count as
// complex.
//
// Examples:
//
//	go test                → not complex
//	go test && go vet      → complex (two commands stitched together)
//	echo "a && b"          → not complex (the `&&` is just an argument inside quotes)
//	echo "$(rm -rf /)"     → complex (the `$(...)` still executes, even in double quotes)
func hasComplexShellStructure(command string) bool {
	var quote byte
	for i := 0; i < len(command); i++ {
		char := command[i]
		if char == '\\' && quote != '\'' {
			i++
			continue
		}
		if quote != 0 && char == quote {
			quote = 0
			continue
		}
		if quote == '\'' {
			continue
		}
		if quote == 0 && (char == '\'' || char == '"') {
			quote = char
			continue
		}
		if char == '`' ||
			char == '$' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}
		if quote == 0 && strings.IndexByte("&|;<>\n\r()", char) >= 0 {
			return true
		}
	}
	return false
}
