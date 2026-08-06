//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const maxCodeBlockUnwrapDepth = 3

// Guard combines a Scanner with PermissionPolicy, audit, and tracing adapters.
type Guard struct {
	scanner *Scanner
	auditor Auditor
}

// GuardOption configures Guard integrations.
type GuardOption func(*Guard)

// WithAuditor records every decision made through Guard adapters.
func WithAuditor(auditor Auditor) GuardOption {
	return func(guard *Guard) {
		guard.auditor = auditor
	}
}

// NewGuard creates reusable PermissionPolicy and wrapper adapters. A nil
// scanner uses DefaultPolicy.
func NewGuard(scanner *Scanner, options ...GuardOption) *Guard {
	if scanner == nil {
		var err error
		scanner, err = NewScanner(DefaultPolicy())
		if err != nil {
			panic(fmt.Sprintf(
				"tool safety default policy is invalid: %v", err,
			))
		}
	}
	guard := &Guard{scanner: scanner}
	for _, option := range options {
		if option != nil {
			option(guard)
		}
	}
	return guard
}

// CheckToolPermission implements tool.PermissionPolicy. It recognizes the
// argument shapes of workspaceexec, hostexec, codeexec, and command-like custom
// tools. Non-execution tools without command or script arguments are allowed so
// this policy can be safely composed at the run level.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	request *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	report := g.scanPermissionRequest(ctx, request)
	g.emitBestEffort(ctx, report)
	return permissionDecision(report), nil
}

func (g *Guard) scanPermissionRequest(
	ctx context.Context,
	request *tool.PermissionRequest,
) Report {
	if request == nil {
		return invalidRequestReport(
			"",
			BackendUnknown,
			"permission request is nil",
		)
	}
	inputs, err := permissionInputs(request)
	if err != nil {
		return invalidRequestReport(
			request.ToolName,
			resolvedBackend(request.ToolName, ""),
			fmt.Sprintf("tool arguments are not valid JSON: %v", err),
		)
	}
	if len(inputs) == 0 {
		return metadataReport(request)
	}

	reports := make([]Report, 0, len(inputs))
	for _, input := range inputs {
		reports = append(reports, g.scanner.Scan(ctx, input))
	}
	return mergeReports(reports)
}

func metadataReport(request *tool.PermissionRequest) Report {
	backend := resolvedBackend(request.ToolName, "")
	switch {
	case request.Metadata.Destructive:
		return Report{
			Decision:       DecisionAsk,
			RiskLevel:      RiskHigh,
			RuleID:         RuleMetadataDestructive,
			Evidence:       "tool metadata declares destructive external effects",
			Recommendation: "Require human review or a narrower non-destructive tool.",
			ToolName:       request.ToolName,
			Backend:        backend,
			Blocked:        true,
		}
	case request.Metadata.OpenWorld:
		return Report{
			Decision:       DecisionAsk,
			RiskLevel:      RiskMedium,
			RuleID:         RuleMetadataOpenWorld,
			Evidence:       "tool metadata declares access outside the current workspace",
			Recommendation: "Review the external target and apply a tool-specific allowlist.",
			ToolName:       request.ToolName,
			Backend:        backend,
			Blocked:        true,
		}
	default:
		return Report{
			Decision:       DecisionAllow,
			RiskLevel:      RiskLow,
			RuleID:         RuleAllow,
			Evidence:       "tool call has no executable command or script payload",
			Recommendation: "Continue applying metadata permissions and runtime isolation as appropriate.",
			ToolName:       request.ToolName,
			Backend:        backend,
		}
	}
}

func (g *Guard) emit(ctx context.Context, report Report) error {
	if ctx == nil {
		ctx = context.Background()
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String(AttrDecision, string(report.Decision)),
		attribute.String(AttrRiskLevel, string(report.RiskLevel)),
		attribute.String(AttrRuleID, report.RuleID),
		attribute.String(AttrBackend, string(report.Backend)),
	)
	if g == nil || g.auditor == nil {
		return nil
	}
	return g.auditor.Record(ctx, auditEvent(report))
}

func (g *Guard) emitBestEffort(ctx context.Context, report Report) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := g.emit(ctx, report); err != nil {
		log.WarnfContext(ctx, "Tool safety audit failed: %v", err)
	}
}

func permissionDecision(report Report) tool.PermissionDecision {
	reason := fmt.Sprintf(
		"%s: %s Recommendation: %s",
		report.RuleID,
		report.Evidence,
		report.Recommendation,
	)
	switch report.Decision {
	case DecisionDeny:
		return tool.DenyPermission(reason)
	case DecisionAsk:
		return tool.AskPermission(reason)
	default:
		return tool.AllowPermission()
	}
}

func permissionInputs(request *tool.PermissionRequest) ([]Input, error) {
	if len(request.Arguments) == 0 {
		if isExecutionTool(request.ToolName) {
			return []Input{basePermissionInput(request)}, nil
		}
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request.Arguments, &fields); err != nil {
		return nil, err
	}

	base := basePermissionInput(request)
	base.Command = firstJSONString(fields, "command", "cmd")
	base.Script = firstJSONString(fields, "script", "code")
	base.Language = firstJSONString(fields, "language")
	base.WorkingDir = firstJSONString(fields, "cwd", "workdir")
	base.Environment = jsonStringMap(fields["env"])
	base.Arguments = firstJSONStringSlice(fields, "arguments", "args")
	base.TimeoutSecond = firstJSONInt(
		fields,
		"timeout_seconds",
		"timeout_sec",
		"timeoutSec",
		"timeout",
	)
	base.Background = firstJSONBool(fields, "background")
	base.TTY = firstJSONBool(fields, "tty", "pty")
	base.Concurrency = firstJSONInt(fields, "concurrency", "workers")

	if raw, ok := fields["code_blocks"]; ok {
		blocks, err := decodeCodeBlocks(raw)
		if err != nil {
			return nil, err
		}
		inputs := make([]Input, 0, len(blocks))
		for _, block := range blocks {
			input := base
			input.Command = ""
			input.Script = block.Code
			input.Language = block.Language
			inputs = append(inputs, input)
		}
		if len(inputs) == 0 {
			return []Input{base}, nil
		}
		return inputs, nil
	}
	if chars := firstJSONString(fields, "chars"); chars != "" {
		base.Command = chars
	}
	if base.Command != "" || base.Script != "" ||
		isExecutionTool(request.ToolName) {
		return []Input{base}, nil
	}
	return nil, nil
}

func basePermissionInput(request *tool.PermissionRequest) Input {
	return Input{
		ToolName: request.ToolName,
		Backend:  resolvedBackend(request.ToolName, ""),
		Metadata: request.Metadata,
	}
}

func decodeCodeBlocks(raw json.RawMessage) ([]codeexecutor.CodeBlock, error) {
	return decodeCodeBlocksDepth(raw, 0)
}

func decodeCodeBlocksDepth(
	raw json.RawMessage,
	depth int,
) ([]codeexecutor.CodeBlock, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if encoded, ok := value.(string); ok {
		if depth >= maxCodeBlockUnwrapDepth {
			return nil, fmt.Errorf(
				"code_blocks exceeds %d nested JSON strings",
				maxCodeBlockUnwrapDepth,
			)
		}
		return decodeCodeBlocksDepth(json.RawMessage(encoded), depth+1)
	}
	switch value.(type) {
	case []any:
		var blocks []codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	case map[string]any:
		var block codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []codeexecutor.CodeBlock{block}, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("code_blocks must be an array or object")
	}
}

func firstJSONString(
	fields map[string]json.RawMessage,
	names ...string,
) string {
	for _, name := range names {
		var value string
		if raw, ok := fields[name]; ok &&
			json.Unmarshal(raw, &value) == nil {
			return value
		}
	}
	return ""
}

func firstJSONStringSlice(
	fields map[string]json.RawMessage,
	names ...string,
) []string {
	for _, name := range names {
		var value []string
		if raw, ok := fields[name]; ok &&
			json.Unmarshal(raw, &value) == nil {
			return value
		}
	}
	return nil
}

func firstJSONInt(
	fields map[string]json.RawMessage,
	names ...string,
) int {
	for _, name := range names {
		var value int
		if raw, ok := fields[name]; ok &&
			json.Unmarshal(raw, &value) == nil {
			return value
		}
	}
	return 0
}

func firstJSONBool(
	fields map[string]json.RawMessage,
	names ...string,
) bool {
	for _, name := range names {
		var value bool
		if raw, ok := fields[name]; ok &&
			json.Unmarshal(raw, &value) == nil {
			return value
		}
	}
	return false
}

func jsonStringMap(raw json.RawMessage) map[string]string {
	var value map[string]string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func isExecutionTool(name string) bool {
	switch name {
	case "workspace_exec", "exec_command", "execute_code":
		return true
	default:
		return false
	}
}

func invalidRequestReport(
	toolName string,
	backend Backend,
	evidence string,
) Report {
	return Report{
		Decision:       DecisionDeny,
		RiskLevel:      RiskHigh,
		RuleID:         RuleShellUnparsable,
		Evidence:       evidence,
		Recommendation: "Provide valid structured arguments that can be scanned before execution.",
		ToolName:       toolName,
		Backend:        backend,
		Blocked:        true,
	}
}

func mergeReports(reports []Report) Report {
	if len(reports) == 0 {
		return Report{
			Decision:       DecisionAllow,
			RiskLevel:      RiskLow,
			RuleID:         RuleAllow,
			Evidence:       "no executable inputs were present",
			Recommendation: "Continue enforcing runtime isolation.",
			Backend:        BackendUnknown,
		}
	}
	merged := reports[0]
	merged.DurationMicros = 0
	var commandParts []string
	var findings []Finding
	for _, report := range reports {
		if report.Command != "" {
			commandParts = append(commandParts, report.Command)
		}
		findings = append(findings, report.Findings...)
		merged.DurationMicros += report.DurationMicros
		merged.Redacted = merged.Redacted || report.Redacted
		candidate := Finding{
			Decision:       report.Decision,
			RiskLevel:      report.RiskLevel,
			RuleID:         report.RuleID,
			Evidence:       report.Evidence,
			Recommendation: report.Recommendation,
		}
		current := Finding{
			Decision:  merged.Decision,
			RiskLevel: merged.RiskLevel,
		}
		if findingPriority(candidate) > findingPriority(current) {
			merged.Decision = report.Decision
			merged.RiskLevel = report.RiskLevel
			merged.RuleID = report.RuleID
			merged.Evidence = report.Evidence
			merged.Recommendation = report.Recommendation
		}
	}
	merged.Command = strings.Join(commandParts, "\n")
	merged.Blocked = merged.Decision != DecisionAllow
	merged.Findings = findings
	return merged
}
