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
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	workspaceExecToolName = "workspace_exec"
	reviewChecksCommand   = "run-go-checks.sh"

	workspaceTimeoutBudget = 5 * time.Minute

	denyEmptyCommand                  = "workspace_exec command must be a non-empty string"
	denyEnvKey                        = "workspace_exec env key is not allowed"
	denyEnvValueType                  = "workspace_exec env value must be a string"
	denyEnvCGOValue                   = "workspace_exec CGO_ENABLED must be 0 or 1"
	denyTimeoutNegative               = "workspace_exec timeout must not be negative"
	denyTimeoutBudget                 = "workspace_exec timeout exceeds the five-minute budget"
	denyArgsNotObject                 = "workspace_exec arguments must be a JSON object"
	denyArgsTrailingData              = "workspace_exec arguments contain trailing data"
	denyArgsInvalidJSON               = "workspace_exec arguments must be valid JSON"
	askPermissionReason               = "Call request_tool_permission with this target tool and its exact arguments, then retry the target tool only if permission is granted."
	defaultToolOutputLimitBytes int64 = 16 * 1024
)

// riskMarkers is the single configurable entry point for workspace command
// approval. Matching uses strings.Contains against the full command string.
var riskMarkers = []string{
	reviewChecksCommand,
}

// governedExecution owns run-local risk classification, exact grants, the
// permission Tool, PermissionPolicy, and sandbox audit callbacks for one Review.
type governedExecution struct {
	mu sync.Mutex

	markers          []string
	grants           map[grantKey]struct{}
	started          map[string]time.Time
	recorder         *reviewRecorder
	sanitizer        *redact.Sanitizer
	approver         *Approver
	backend          string
	outputLimitBytes int64
}

type grantKey struct {
	ToolName string
	Identity string
}

func newGovernedExecution(
	recorder *reviewRecorder,
	sanitizer *redact.Sanitizer,
	approver *Approver,
	backend string,
) *governedExecution {
	if backend == "" {
		backend = "container"
	}
	return &governedExecution{
		markers:          append([]string(nil), riskMarkers...),
		grants:           make(map[grantKey]struct{}),
		started:          make(map[string]time.Time),
		recorder:         recorder,
		sanitizer:        sanitizer,
		approver:         approver,
		backend:          backend,
		outputLimitBytes: defaultToolOutputLimitBytes,
	}
}

func (g *governedExecution) grant(toolName, identity string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.grants[grantKey{ToolName: toolName, Identity: identity}] = struct{}{}
	g.mu.Unlock()
}

func (g *governedExecution) hasGrant(toolName, identity string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.grants[grantKey{ToolName: toolName, Identity: identity}]
	return ok
}

func (g *governedExecution) beginExecution(toolCallID string) error {
	if g == nil || toolCallID == "" {
		return errors.New("workspace execution requires a non-empty tool call id")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.started[toolCallID]; exists {
		return fmt.Errorf("workspace execution %q already started", toolCallID)
	}
	g.started[toolCallID] = time.Now()
	return nil
}

func (g *governedExecution) finishExecution(toolCallID string) (time.Time, time.Time, bool) {
	finishedAt := time.Now()
	if g == nil {
		return time.Time{}, finishedAt, false
	}
	g.mu.Lock()
	startedAt, ok := g.started[toolCallID]
	if ok {
		delete(g.started, toolCallID)
	}
	g.mu.Unlock()
	return startedAt, finishedAt, ok
}

// PermissionPolicy connects the framework permission lifecycle to run-local
// grants and durable audit records. Example policy validation runs before any
// approval or sandbox execution.
func (g *governedExecution) PermissionPolicy() tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(
		ctx context.Context,
		req *tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		if req == nil {
			return tool.DenyPermission("permission request is missing"), nil
		}
		if g == nil || g.recorder == nil {
			return tool.PermissionDecision{}, errors.New("governed execution is not configured")
		}
		taskID, err := reviewTaskIDFromContext(ctx)
		if err != nil {
			return tool.PermissionDecision{}, err
		}

		decision := tool.AllowPermission()
		commandPreview := string(req.Arguments)
		if req.ToolName == workspaceExecToolName {
			fields, denyReason, err := validateWorkspacePolicy(req.Arguments)
			if err != nil {
				return tool.PermissionDecision{}, err
			}
			if denyReason != "" {
				decision = tool.DenyPermission(denyReason)
				if err := g.recordToolPermission(ctx, taskID, req, commandPreview, decision); err != nil {
					return tool.PermissionDecision{}, err
				}
				return decision, nil
			}
			if fields.Command != "" {
				commandPreview = fields.Command
			}
		}

		needsApproval, err := requiresApproval(req, req.Arguments, g.markers)
		if err != nil {
			return tool.PermissionDecision{}, err
		}
		if needsApproval {
			identity, err := approvalIdentity(req.ToolName, req.Arguments)
			if err != nil {
				return tool.PermissionDecision{}, err
			}
			if g.hasGrant(req.ToolName, identity) {
				decision = tool.AllowPermission()
			} else {
				decision = tool.AskPermission(askPermissionReason)
			}
		}

		if err := g.recordToolPermission(ctx, taskID, req, commandPreview, decision); err != nil {
			return tool.PermissionDecision{}, err
		}
		if decision.Action != tool.PermissionActionAllow {
			return decision, nil
		}
		if req.ToolName == workspaceExecToolName {
			if err := g.beginExecution(req.ToolCallID); err != nil {
				return tool.PermissionDecision{}, err
			}
		}
		return decision, nil
	})
}

func (g *governedExecution) recordToolPermission(
	ctx context.Context,
	taskID string,
	req *tool.PermissionRequest,
	command string,
	decision tool.PermissionDecision,
) error {
	return g.recorder.RecordPermissionDecision(ctx, taskID, store.PermissionDecisionRecord{
		ToolCallID:     req.ToolCallID,
		DecisionKind:   "tool_permission",
		Operation:      req.ToolName,
		ToolName:       req.ToolName,
		CommandPreview: command,
		Decision:       string(decision.Action),
		Reason:         decision.Reason,
	})
}

// PermissionTool returns the request_tool_permission Function Tool for this run.
func (g *governedExecution) PermissionTool() tool.Tool {
	return function.NewFunctionTool(
		g.requestToolPermission,
		function.WithName(requestToolPermissionName),
		function.WithDescription("Request user permission for a specific target tool call. Pass the exact target tool name and complete argument object from an approval_required call, plus a concise Reason. The result echoes target_arguments; if granted, retry the real target tool by copying that complete object without dropping or changing fields."),
		function.WithInputSchema(requestToolPermissionInputSchema()),
		function.WithOutputSchema(requestToolPermissionOutputSchema()),
	)
}

// Callbacks returns AfterTool adapters that redacts generic Tool output and
// records bounded sandbox evidence for allowed workspace_exec calls.
func (g *governedExecution) Callbacks() *tool.Callbacks {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		ctx context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		if args == nil {
			return nil, nil
		}
		if args.ToolName != workspaceExecToolName {
			return redactGenericToolResult(g.sanitizer, args)
		}
		return g.governWorkspaceExecResult(ctx, args)
	})
	return callbacks
}

// requiresApproval is the only risk-classification entry point. Ordinary tools
// use framework metadata; workspace_exec additionally checks configured markers.
func requiresApproval(
	req *tool.PermissionRequest,
	arguments []byte,
	markers []string,
) (bool, error) {
	if req.ToolName != workspaceExecToolName {
		return req.Metadata.Destructive, nil
	}
	if req.Metadata.Destructive {
		return true, nil
	}
	fields, denyReason, err := validateWorkspacePolicy(arguments)
	if err != nil {
		return false, err
	}
	if denyReason != "" {
		// Policy denials are handled before risk classification.
		return false, nil
	}
	return matchesRiskMarker(fields.Command, markers), nil
}

func matchesRiskMarker(command string, markers []string) bool {
	for _, marker := range markers {
		if marker != "" && strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

// approvalIdentity returns the run-local grant identity for one target call.
// Ordinary tools are authorized by tool name alone (empty identity). workspace
// calls use tool name plus the complete canonical argument object.
func approvalIdentity(toolName string, arguments []byte) (string, error) {
	if toolName != workspaceExecToolName {
		return "", nil
	}
	identity, err := canonicalJSON(arguments)
	if err != nil {
		return "", fmt.Errorf("canonical workspace_exec identity: %w", err)
	}
	return string(identity), nil
}

// workspacePolicyFields is the minimal subset this example validates. Full raw
// JSON remains the identity and audit source; unknown fields are never dropped.
type workspacePolicyFields struct {
	Command string
	CWD     string
}

// validateWorkspacePolicy enforces this example's non-overridable workspace
// constraints without rewriting arguments or shadowing the full framework schema.
func validateWorkspacePolicy(arguments []byte) (workspacePolicyFields, string, error) {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return workspacePolicyFields{}, denyArgsNotObject, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return workspacePolicyFields{}, "", fmt.Errorf("%s: %w", denyArgsInvalidJSON, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return workspacePolicyFields{}, denyArgsTrailingData, nil
		}
		return workspacePolicyFields{}, "", fmt.Errorf("%s: %w", denyArgsInvalidJSON, err)
	}

	var fields workspacePolicyFields
	if commandRaw, ok := raw["command"]; ok {
		if err := json.Unmarshal(commandRaw, &fields.Command); err != nil {
			return workspacePolicyFields{}, denyEmptyCommand, nil
		}
	}
	if strings.TrimSpace(fields.Command) == "" {
		return workspacePolicyFields{}, denyEmptyCommand, nil
	}
	if cwdRaw, ok := raw["cwd"]; ok && len(cwdRaw) > 0 && string(cwdRaw) != "null" {
		if err := json.Unmarshal(cwdRaw, &fields.CWD); err != nil {
			return workspacePolicyFields{}, denyEmptyCommand, nil
		}
	}
	if envRaw, ok := raw["env"]; ok && len(envRaw) > 0 && string(envRaw) != "null" {
		if reason := validateWorkspaceEnv(envRaw); reason != "" {
			return fields, reason, nil
		}
	}
	if reason := validateWorkspaceTimeout(raw); reason != "" {
		return fields, reason, nil
	}
	return fields, "", nil
}

func validateWorkspaceEnv(envRaw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(envRaw))
	decoder.UseNumber()
	var env map[string]json.RawMessage
	if err := decoder.Decode(&env); err != nil {
		return denyEnvValueType
	}
	for key, valueRaw := range env {
		var value string
		if err := json.Unmarshal(valueRaw, &value); err != nil {
			return denyEnvValueType
		}
		if key != "CGO_ENABLED" {
			return denyEnvKey
		}
		if value != "0" && value != "1" {
			return denyEnvCGOValue
		}
	}
	return ""
}

func validateWorkspaceTimeout(raw map[string]json.RawMessage) string {
	seconds, present, err := decodeTimeoutSeconds(raw)
	if err != nil {
		return denyTimeoutNegative
	}
	if !present || seconds == 0 {
		return ""
	}
	if seconds < 0 {
		return denyTimeoutNegative
	}
	if time.Duration(seconds)*time.Second > workspaceTimeoutBudget {
		return denyTimeoutBudget
	}
	return ""
}

func decodeTimeoutSeconds(raw map[string]json.RawMessage) (int, bool, error) {
	for _, name := range []string{"timeout_sec", "timeoutSec", "timeout"} {
		valueRaw, ok := raw[name]
		if !ok || len(valueRaw) == 0 || string(valueRaw) == "null" {
			continue
		}
		var number json.Number
		if err := json.Unmarshal(valueRaw, &number); err == nil {
			parsed, err := number.Int64()
			if err != nil {
				return 0, true, err
			}
			return int(parsed), true, nil
		}
		var asInt int
		if err := json.Unmarshal(valueRaw, &asInt); err != nil {
			return 0, true, err
		}
		return asInt, true, nil
	}
	return 0, false, nil
}

// canonicalJSON makes workspace grants independent of JSON object key order
// without weakening any argument value.
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
	if _, ok := decoded.(map[string]any); !ok {
		return nil, errors.New("JSON value must be an object")
	}
	return json.Marshal(decoded)
}
