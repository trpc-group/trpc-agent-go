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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	workspaceExecToolName       = "workspace_exec"
	reviewChecksScript          = "scripts/run-go-checks.sh"
	canonicalReviewChecksScript = "skills/code-review/" + reviewChecksScript
)

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

func (in workspaceExecInput) usesInteractiveSession() bool {
	return in.Background || in.TTY || in.PTY || in.Stdin != "" ||
		in.YieldTimeMS != nil || in.YieldMs != nil
}

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

type executionStart struct {
	original  workspaceExecInput
	executed  workspaceExecInput
	startedAt time.Time
}

type reviewRunTracker struct {
	mu                      sync.Mutex
	startedAt               time.Time
	toolCalls               int
	permissionInterceptions int
	sandboxDuration         time.Duration
	exceptions              map[string]int
	executions              map[string]executionStart
	toolInputs              map[string]executionStart
	permissionExplanations  map[string]string
	assistantExplanation    string
}

func newReviewRunTracker() *reviewRunTracker {
	return &reviewRunTracker{
		startedAt:              time.Now(),
		exceptions:             make(map[string]int),
		executions:             make(map[string]executionStart),
		toolInputs:             make(map[string]executionStart),
		permissionExplanations: make(map[string]string),
	}
}

// recordToolInput binds both representations produced by BeforeTool to the
// framework tool-call identity. Later stages use this binding instead of trying
// to reconstruct identity by parsing a rewritten shell command.
func (t *reviewRunTracker) recordToolInput(
	toolCallID string,
	original workspaceExecInput,
	executed workspaceExecInput,
) error {
	if t == nil || toolCallID == "" {
		return errors.New("workspace execution requires a non-empty tool call id")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.toolInputs[toolCallID]; exists {
		return fmt.Errorf("workspace execution tool call id %q is not unique", toolCallID)
	}
	t.toolInputs[toolCallID] = executionStart{
		original: original,
		executed: executed,
	}
	return nil
}

func (t *reviewRunTracker) toolInput(toolCallID string) (executionStart, bool) {
	if t == nil || toolCallID == "" {
		return executionStart{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	input, ok := t.toolInputs[toolCallID]
	return input, ok
}

func (t *reviewRunTracker) beginModelTurn() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.assistantExplanation = ""
	t.mu.Unlock()
}

// captureModelResponse binds only user-visible assistant text to tool calls
// emitted by that same model turn. Reasoning content is deliberately excluded:
// the approval prompt must show an explanation the user was actually given,
// and a previous tool call's explanation must never authorize a later one.
func (t *reviewRunTracker) captureModelResponse(response *model.Response) {
	if t == nil || response == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, choice := range response.Choices {
		content := choice.Message.Content
		if content == "" {
			content = choice.Delta.Content
		}
		t.assistantExplanation = mergeAssistantExplanation(
			t.assistantExplanation,
			content,
		)

		toolCalls := choice.Message.ToolCalls
		if len(toolCalls) == 0 {
			toolCalls = choice.Delta.ToolCalls
		}
		explanation := strings.TrimSpace(t.assistantExplanation)
		if explanation == "" {
			continue
		}
		for _, toolCall := range toolCalls {
			if toolCall.ID != "" {
				t.permissionExplanations[toolCall.ID] = explanation
			}
		}
	}
}

func mergeAssistantExplanation(existing, incoming string) string {
	if incoming == "" {
		return existing
	}
	if existing == "" || strings.HasPrefix(incoming, existing) {
		return incoming
	}
	if strings.HasSuffix(existing, incoming) {
		return existing
	}
	return existing + incoming
}

func (t *reviewRunTracker) permissionExplanation(toolCallID string) string {
	if t == nil || toolCallID == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(t.permissionExplanations[toolCallID])
}

func newReviewModelCallbacks(tracker *reviewRunTracker) *model.Callbacks {
	return model.NewCallbacks().
		RegisterBeforeModel(func(
			_ context.Context,
			_ *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			tracker.beginModelTurn()
			return &model.BeforeModelResult{}, nil
		}).
		RegisterAfterModel(func(
			_ context.Context,
			args *model.AfterModelArgs,
		) (*model.AfterModelResult, error) {
			if args != nil {
				tracker.captureModelResponse(args.Response)
			}
			return &model.AfterModelResult{}, nil
		})
}

func (t *reviewRunTracker) recordToolCall() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.toolCalls++
	t.mu.Unlock()
}

func (t *reviewRunTracker) recordPermissionInterception() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.permissionInterceptions++
	t.mu.Unlock()
}

// beginExecution is the allow-to-running transition. It copies the prepared
// input under the same tool_call_id so AfterTool can close the exact approved
// attempt without trusting rewritten shell text.
func (t *reviewRunTracker) beginExecution(toolCallID string) error {
	if t == nil || toolCallID == "" {
		return errors.New("workspace execution requires a non-empty tool call id")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	input, ok := t.toolInputs[toolCallID]
	if !ok {
		return fmt.Errorf("workspace execution %q was not prepared by BeforeTool", toolCallID)
	}
	if _, exists := t.executions[toolCallID]; exists {
		return fmt.Errorf("workspace execution %q already started", toolCallID)
	}
	input.startedAt = time.Now()
	t.executions[toolCallID] = input
	return nil
}

// finishExecution closes the exact approved attempt and accounts for its
// duration. Success or failure belongs to the persisted sandbox record.
func (t *reviewRunTracker) finishExecution(toolCallID string) (executionStart, time.Time, bool) {
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

func (t *reviewRunTracker) recordException(kind string) {
	if t == nil || kind == "" {
		return
	}
	t.mu.Lock()
	t.exceptions[kind]++
	t.mu.Unlock()
}

type Approver struct {
	mu          sync.Mutex
	reader      *bufio.Reader
	writer      io.Writer
	skip        bool
	terminalErr error
}

type approvalResponse struct {
	answer string
	err    error
}

var errApprovalInputUnavailable = errors.New(
	"interactive approval input is unavailable after a canceled decision",
)

func newApprover(config ApprovalConfig, skip bool) *Approver {
	var reader *bufio.Reader
	if config.Input != nil {
		reader = bufio.NewReader(config.Input)
	}
	return &Approver{
		reader: reader,
		writer: config.Output,
		skip:   skip,
	}
}

// readResponse binds one terminal read to one approval decision. A generic
// io.Reader cannot be canceled, so the private buffered channel lets a late
// response finish without blocking or becoming input for another decision.
func (a *Approver) readResponse() <-chan approvalResponse {
	responses := make(chan approvalResponse, 1)
	go func() {
		answer, err := a.reader.ReadString('\n')
		responses <- approvalResponse{answer: answer, err: err}
	}()
	return responses
}

func (a *Approver) decide(
	ctx context.Context,
	toolName string,
	command string,
	reason string,
) (tool.PermissionDecision, error) {
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	if a != nil && a.skip {
		return tool.AllowPermission(), nil
	}
	if a == nil || a.reader == nil || a.writer == nil {
		return tool.AskPermission("interactive approval is required before this tool can execute"), nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	if a.terminalErr != nil {
		return tool.PermissionDecision{}, a.terminalErr
	}
	if _, err := fmt.Fprintf(a.writer,
		"\nThe review agent requests permission to use a governed tool.\nTool: %s\nCommand: %s\nAgent explanation: %s\nApprove? [Y/n] ",
		toolName,
		command,
		reason,
	); err != nil {
		return tool.PermissionDecision{}, fmt.Errorf("write approval prompt: %w", err)
	}
	responses := a.readResponse()
	var response approvalResponse
	select {
	case <-ctx.Done():
		a.terminalErr = errApprovalInputUnavailable
		return tool.PermissionDecision{}, ctx.Err()
	case response = <-responses:
	}
	if response.err != nil && len(response.answer) == 0 {
		if response.err == io.EOF {
			return tool.AskPermission("interactive approval input ended before a decision"), nil
		}
		return tool.PermissionDecision{}, fmt.Errorf("read approval response: %w", response.err)
	}
	switch strings.ToLower(strings.TrimSpace(response.answer)) {
	case "", "y", "yes":
		return tool.AllowPermission(), nil
	case "n", "no":
		return tool.DenyPermission("user denied the requested tool execution"), nil
	default:
		return tool.AskPermission("approval response must be Y or n"), nil
	}
}

func newReviewPermissionPolicy(
	recorder *reviewRecorder,
	tracker *reviewRunTracker,
	approver *Approver,
) tool.PermissionPolicy {
	// PermissionPolicy classifies only the bundled baseline as ask. Other
	// workspace commands remain immediately usable; the Skill, not this policy,
	// decides which checks the Agent should run.
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
		var execInput workspaceExecInput
		var approvalInput workspaceExecInput
		if req.ToolName == workspaceExecToolName {
			execInput, err = decodeWorkspaceExecInput(req.Arguments)
			if err != nil {
				return tool.PermissionDecision{}, fmt.Errorf("decode workspace_exec permission arguments: %w", err)
			}
			approvalInput = execInput
			if prepared, ok := tracker.toolInput(req.ToolCallID); ok {
				approvalInput = prepared.original
			} else {
				return tool.PermissionDecision{}, fmt.Errorf(
					"workspace execution %q reached permission without BeforeTool preparation",
					req.ToolCallID,
				)
			}
		}
		_, requiresApproval := reviewChecksModule(approvalInput.Command)
		if req.ToolName == workspaceExecToolName && requiresApproval {
			tracker.recordPermissionInterception()
			reason := tracker.permissionExplanation(req.ToolCallID)
			if recorder != nil {
				reason = recorder.mask(reason)
			}
			if approvalInput.usesInteractiveSession() {
				decision = tool.DenyPermission(
					"the bundled review-checks script must run in the foreground without interactive session controls",
				)
			} else if reason == "" {
				decision = tool.AskPermission("the agent must explain why this tool call needs approval")
				if err := recordPermission(
					ctx,
					recorder,
					taskID,
					req,
					approvalInput.Command,
					decision,
				); err != nil {
					return tool.PermissionDecision{}, err
				}
				return decision, nil
			} else {
				if err := recordPermission(ctx, recorder, taskID, req, approvalInput.Command,
					tool.AskPermission(reason)); err != nil {
					return tool.PermissionDecision{}, err
				}
				decision, err = approver.decide(ctx, req.ToolName, approvalInput.Command, reason)
				if err != nil {
					return tool.PermissionDecision{}, err
				}
			}
		} else if req.ToolName == workspaceExecToolName &&
			commandAttemptsReviewChecksExecution(approvalInput.Command) {
			tracker.recordPermissionInterception()
			decision = tool.DenyPermission(
				"the bundled review-checks script execution could not be safely classified for approval",
			)
		}

		commandPreview := string(req.Arguments)
		if execInput.Command != "" {
			commandPreview = execInput.Command
		}
		if err := recordPermission(ctx, recorder, taskID, req, commandPreview, decision); err != nil {
			return tool.PermissionDecision{}, err
		}
		if decision.Action == tool.PermissionActionAllow && req.ToolName == workspaceExecToolName {
			if err := tracker.beginExecution(req.ToolCallID); err != nil {
				return tool.PermissionDecision{}, err
			}
		}
		return decision, nil
	})
}

// reviewChecksModule recognizes the single command form that is safe to put
// before a human for approval. Other ways to execute the same script are
// rejected by commandAttemptsReviewChecksExecution rather than silently
// bypassing this canonical ask path.
func reviewChecksModule(command string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 4 && fields[3] == "2>&1" {
		fields = fields[:3]
	}
	if len(fields) != 3 {
		return "", false
	}
	program := strings.Trim(fields[0], "'\"")
	if program != "sh" && program != "/bin/sh" {
		return "", false
	}
	script := strings.Trim(fields[1], "'\"")
	if script != canonicalReviewChecksScript {
		return "", false
	}
	module := path.Clean(strings.Trim(fields[2], "'\""))
	const repositoryRoot = "work/inputs/repo"
	if module != repositoryRoot &&
		!strings.HasPrefix(module, repositoryRoot+"/") {
		return "", false
	}
	return module, true
}

// commandAttemptsReviewChecksExecution separates an unrecognized execution
// form from an inert use of the exact bundled script path. Any shell pipeline,
// sequencing, or unknown program fails closed because superficial token order
// cannot prove that the script will only be read.
func commandAttemptsReviewChecksExecution(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if !mentionsBundledReviewChecksScript(command) {
		return false
	}
	return !isInertReviewChecksReference(fields)
}

func mentionsBundledReviewChecksScript(command string) bool {
	// Separate shell control characters before token comparison so compact
	// forms such as "script|sh" cannot hide the script path from governance.
	replacer := strings.NewReplacer(
		"|", " ",
		";", " ",
		"&", " ",
		"(", " ",
		")", " ",
		"<", " ",
		">", " ",
	)
	for _, field := range strings.Fields(replacer.Replace(command)) {
		candidate := strings.Trim(field, "'\"")
		candidate = strings.TrimPrefix(candidate, "./")
		if candidate == canonicalReviewChecksScript {
			return true
		}
	}
	return false
}

func isInertReviewChecksReference(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if strings.ContainsAny(field, "|;&<>`") ||
			strings.Contains(field, "$(") {
			return false
		}
	}
	program := strings.Trim(fields[0], "'\"")
	switch program {
	case "basename", "cat", "dirname", "echo", "file", "grep", "head",
		"ls", "printf", "readlink", "rg", "sed", "stat", "tail", "wc":
		return true
	default:
		return false
	}
}

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
