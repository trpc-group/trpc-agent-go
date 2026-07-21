//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type policySnapshot struct{ policy Policy }

// Guard scans tool calls, emits audit events, and redacts tool output.
type Guard struct {
	policy   atomic.Pointer[policySnapshot]
	sink     AuditSink
	previous tool.PermissionPolicy
}

var (
	_ tool.PermissionPolicy    = (*Guard)(nil)
	_ tool.ToolResultSanitizer = (*Guard)(nil)
	_ tool.ToolErrorSanitizer  = (*Guard)(nil)
)

// Option configures a Guard at construction time.
type Option func(*Guard) error

// WithAuditSink configures the mandatory sink for each evaluated call. If the
// sink fails, Guard fails closed.
func WithAuditSink(sink AuditSink) Option {
	return func(g *Guard) error {
		if sink == nil {
			return errors.New("safety: audit sink cannot be nil")
		}
		g.sink = sink
		return nil
	}
}

// WithPermissionPolicy composes an existing policy with the Guard. The
// strongest decision wins (deny > ask > allow).
func WithPermissionPolicy(policy tool.PermissionPolicy) Option {
	return func(g *Guard) error {
		if policy == nil {
			return errors.New("safety: composed permission policy cannot be nil")
		}
		g.previous = policy
		return nil
	}
}

// NewGuard constructs a Guard from a validated policy.
func NewGuard(policy Policy, opts ...Option) (*Guard, error) {
	if err := ValidatePolicy(policy); err != nil {
		return nil, err
	}
	g := &Guard{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	g.policy.Store(&policySnapshot{policy: clonePolicy(policy)})
	return g, nil
}

// NewDefaultGuard constructs a Guard with all built-in rules enabled.
func NewDefaultGuard(opts ...Option) (*Guard, error) {
	return NewGuard(DefaultPolicy(), opts...)
}

// Policy returns a defensive copy of the active policy.
func (g *Guard) Policy() Policy {
	if g == nil || g.policy.Load() == nil {
		return Policy{}
	}
	return clonePolicy(g.policy.Load().policy)
}

// ToolProfile returns a defensive copy of the effective profile for a
// model-visible tool name, including namespaced-tool and wildcard fallback.
func (g *Guard) ToolProfile(toolName string) ToolProfile {
	if g == nil || g.policy.Load() == nil {
		return ToolProfile{}
	}
	profile := profileFor(g.policy.Load().policy, toolName)
	profile.AllowedDomains = append([]string(nil), profile.AllowedDomains...)
	profile.DeniedCommands = append([]string(nil), profile.DeniedCommands...)
	profile.AllowedCommands = append([]string(nil), profile.AllowedCommands...)
	profile.ForbiddenPaths = append([]string(nil), profile.ForbiddenPaths...)
	profile.AllowedEnv = append([]string(nil), profile.AllowedEnv...)
	return profile
}

// Reload strictly parses and atomically installs a policy. On error, the old
// policy remains active.
func (g *Guard) Reload(data []byte, format string) error {
	policy, err := ParsePolicy(data, format)
	if err != nil {
		return err
	}
	return g.ReloadPolicy(policy)
}

// ReloadPolicy validates and atomically installs a programmatic policy.
func (g *Guard) ReloadPolicy(policy Policy) error {
	if g == nil {
		return errors.New("safety: nil guard")
	}
	if err := ValidatePolicy(policy); err != nil {
		return err
	}
	g.policy.Store(&policySnapshot{policy: clonePolicy(policy)})
	return nil
}

// Scan evaluates a normalized request and writes its audit event.
func (g *Guard) Scan(ctx context.Context, req ScanRequest) (Report, error) {
	if g == nil || g.policy.Load() == nil {
		return Report{}, errors.New("safety: guard is not initialized")
	}
	report := scanRequest(g.policy.Load().policy, req)
	if err := g.record(ctx, req, report); err != nil {
		report.Decision = tool.PermissionActionDeny
		report.Reason = "safety audit failed; execution blocked"
		report.Findings = append(report.Findings, Finding{
			RuleID: "audit_failure", Severity: SeverityCritical,
			Action: tool.PermissionActionDeny, Message: report.Reason,
		})
		finalizeReport(&report)
		setOTelAttributes(ctx, req, report)
		return report, err
	}
	setOTelAttributes(ctx, req, report)
	return report, nil
}

// CheckToolPermission implements tool.PermissionPolicy.
func (g *Guard) CheckToolPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if g == nil || g.policy.Load() == nil {
		return tool.DenyPermission("safety: guard is not initialized"), nil
	}
	if req == nil {
		return tool.DenyPermission("safety: nil permission request"), nil
	}
	scanReq, parseErr := scanRequestFromPermission(req)
	if parseErr != nil {
		scanReq.RawFields = map[string]any{"unparsed_arguments": string(req.Arguments)}
	}
	report := scanRequest(g.policy.Load().policy, scanReq)
	if parseErr != nil && actionRank(report.Decision) < actionRank(tool.PermissionActionAsk) {
		report.Decision = tool.PermissionActionAsk
		report.Reason = "tool arguments could not be parsed safely"
		report.Findings = append(report.Findings, Finding{
			RuleID: "unparsable_input", Severity: SeverityHigh,
			Action: tool.PermissionActionAsk, Message: report.Reason,
		})
	}
	var composedErr error
	if g.previous != nil {
		decision, err := g.previous.CheckToolPermission(ctx, req)
		if err != nil {
			composedErr = fmt.Errorf("composed permission policy failed: %w", err)
			report.Decision = tool.PermissionActionDeny
			report.Reason = "composed permission policy failed; execution blocked"
			report.Findings = append(report.Findings, Finding{
				RuleID: "composed_permission_policy", Severity: SeverityCritical,
				Action: tool.PermissionActionDeny, Message: report.Reason,
			})
		} else if decision, err = tool.NormalizePermissionDecision(decision); err != nil {
			composedErr = fmt.Errorf("composed permission policy returned an invalid decision: %w", err)
			report.Decision = tool.PermissionActionDeny
			report.Reason = "composed permission policy returned an invalid decision; execution blocked"
			report.Findings = append(report.Findings, Finding{
				RuleID: "composed_permission_policy", Severity: SeverityCritical,
				Action: tool.PermissionActionDeny, Message: report.Reason,
			})
		} else if stronger(decision.Action, report.Decision) {
			report.Decision = decision.Action
			report.Reason = redactReason(decision.Reason)
			report.Findings = append(report.Findings, Finding{
				RuleID: "composed_permission_policy", Severity: SeverityHigh,
				Action: decision.Action, Message: report.Reason,
			})
		}
	}
	finalizeReport(&report)
	if err := g.record(ctx, scanReq, report); err != nil {
		report.Decision = tool.PermissionActionDeny
		report.Reason = "safety audit failed; execution blocked"
		report.Findings = append(report.Findings, Finding{
			RuleID: "audit_failure", Severity: SeverityCritical,
			Action: tool.PermissionActionDeny, Message: report.Reason,
		})
		finalizeReport(&report)
		setOTelAttributes(ctx, scanReq, report)
		return tool.DenyPermission("safety audit failed; execution blocked"), err
	}
	setOTelAttributes(ctx, scanReq, report)
	if composedErr != nil {
		return tool.DenyPermission(report.Reason), composedErr
	}
	switch report.Decision {
	case tool.PermissionActionDeny:
		return tool.DenyPermission(report.Reason), nil
	case tool.PermissionActionAsk:
		return tool.AskPermission(report.Reason), nil
	default:
		return tool.AllowPermission(), nil
	}
}

// AfterToolCallbackStructured redacts secrets in post-execution tool results.
// It can be registered directly with tool.Callbacks.RegisterAfterTool.
func (g *Guard) AfterToolCallbackStructured(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
	if args == nil {
		return nil, errors.New("safety: nil after-tool arguments")
	}
	redactedResult, changed := RedactValue(args.Result)
	if !changed {
		return &tool.AfterToolResult{}, nil
	}
	event := AuditEvent{
		Timestamp: time.Now().UTC(), ToolName: RedactString(args.ToolName), ToolCallID: RedactString(args.ToolCallID),
		Decision: tool.PermissionActionAllow, Reason: "secret material redacted from tool output",
		RuleIDs: []string{"secret_exposure"}, RequestID: digestBytes(args.Arguments),
		RiskLevel: SeverityCritical, Recommendation: "Remove secret material from tool output.",
		Blocked: false, Redacted: true,
	}
	if g != nil && g.sink != nil {
		if err := g.sink.WriteAudit(ctx, event); err != nil {
			return nil, fmt.Errorf("safety: write output-redaction audit: %w", err)
		}
	}
	return &tool.AfterToolResult{CustomResult: redactedResult}, nil
}

// SanitizeToolResult implements tool.ToolResultSanitizer. Framework runners
// invoke it after all ordinary callbacks, so callback replacement values cannot
// bypass output redaction.
func (g *Guard) SanitizeToolResult(ctx context.Context, args *tool.AfterToolArgs) (any, error) {
	result, err := g.AfterToolCallbackStructured(ctx, args)
	if err != nil {
		return nil, err
	}
	if result == nil || result.CustomResult == nil {
		return args.Result, nil
	}
	return result.CustomResult, nil
}

// SanitizeToolError redacts secret material from final error text. The
// original error remains private so callers cannot recover secrets with
// errors.Unwrap or errors.As.
func (g *Guard) SanitizeToolError(ctx context.Context, args *tool.AfterToolArgs) (error, error) {
	if args == nil {
		return nil, errors.New("safety: nil after-tool arguments")
	}
	if args.Error == nil {
		return nil, nil
	}
	message := RedactString(args.Error.Error())
	if message == args.Error.Error() {
		return args.Error, nil
	}
	event := AuditEvent{
		Timestamp: time.Now().UTC(), ToolName: RedactString(args.ToolName), ToolCallID: RedactString(args.ToolCallID),
		Decision: tool.PermissionActionAllow, Reason: "secret material redacted from tool error",
		RuleIDs: []string{"secret_exposure"}, RequestID: digestBytes(args.Arguments),
		RiskLevel: SeverityCritical, Recommendation: "Remove secret material from tool errors.",
		Blocked: false, Redacted: true,
	}
	if g != nil && g.sink != nil {
		if err := g.sink.WriteAudit(ctx, event); err != nil {
			return nil, fmt.Errorf("safety: write error-redaction audit: %w", err)
		}
	}
	return sanitizedToolError{message: message}, nil
}

type sanitizedToolError struct {
	message string
}

func (e sanitizedToolError) Error() string { return e.message }

func (g *Guard) record(ctx context.Context, req ScanRequest, report Report) error {
	if g.sink == nil {
		return nil
	}
	event := AuditEvent{
		Timestamp: time.Now().UTC(), ToolName: RedactString(req.ToolName), ToolCallID: RedactString(req.ToolCallID),
		Backend: RedactString(req.Backend), Decision: report.Decision, Reason: redactReason(report.Reason),
		RuleIDs: safetyRuleIDs(report.Findings), RequestID: report.RequestID, DurationUS: report.DurationUS,
		RiskLevel: report.RiskLevel, Recommendation: report.Recommendation,
		Blocked: report.Blocked, Redacted: report.Redacted,
	}
	if err := g.sink.WriteAudit(ctx, event); err != nil {
		return fmt.Errorf("safety: write audit event: %w", err)
	}
	return nil
}

func scanRequestFromPermission(req *tool.PermissionRequest) (ScanRequest, error) {
	scanReq := ScanRequest{ToolName: req.ToolName, ToolCallID: req.ToolCallID}
	if len(bytes.TrimSpace(req.Arguments)) == 0 {
		return scanReq, nil
	}
	if err := validateJSONNoDuplicateKeys(req.Arguments); err != nil {
		return scanReq, fmt.Errorf("validate tool arguments: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(req.Arguments))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return scanReq, fmt.Errorf("decode tool arguments: %w", err)
	}
	if fields == nil {
		return scanReq, errors.New("tool arguments must be a JSON object")
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return scanReq, errors.New("trailing JSON value")
		}
		return scanReq, fmt.Errorf("decode trailing tool arguments: %w", err)
	}
	scanReq.RawFields = fields
	seen := make(map[string]string)
	for key, value := range fields {
		canonical := canonicalPermissionField(key)
		if canonical == "" {
			continue
		}
		if previous, exists := seen[canonical]; exists {
			return scanReq, fmt.Errorf(
				"conflicting tool argument aliases %q and %q", previous, key,
			)
		}
		seen[canonical] = key
		if err := applyPermissionField(&scanReq, canonical, value); err != nil {
			return scanReq, fmt.Errorf("normalize tool argument %q: %w", key, err)
		}
	}
	return scanReq, nil
}

func canonicalPermissionField(key string) string {
	switch strings.ToLower(key) {
	case "command", "cmd", "script":
		return "command"
	case "args", "argv", "command_args":
		return "args"
	case "cwd", "working_dir", "working_directory", "workdir":
		return "working_dir"
	case "env", "environment", "env_vars":
		return "env"
	case "stdin", "input", "chars":
		return "stdin"
	case "code", "source":
		return "code"
	case "language", "lang":
		return "language"
	case "backend", "executor":
		return "backend"
	case "pty", "use_pty":
		return "pty"
	case "background", "detached", "run_in_background":
		return "background"
	case "timeout":
		return "timeout"
	case "timeout_ms":
		return "timeout_ms"
	case "timeout_sec", "timeout_seconds":
		return "timeout_sec"
	case "max_output_bytes", "output_limit", "max_output":
		return "max_output_bytes"
	default:
		return ""
	}
}

func applyPermissionField(scanReq *ScanRequest, canonical string, value any) error {
	switch canonical {
	case "command", "working_dir", "stdin", "code", "language", "backend":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a string, got %T", value)
		}
		applyPermissionStringField(scanReq, canonical, text)
		return nil
	case "args":
		return applyPermissionArgs(scanReq, value)
	case "env":
		return applyPermissionEnv(scanReq, value)
	case "pty", "background":
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("must be a boolean, got %T", value)
		}
		if canonical == "pty" {
			scanReq.PTY = flag
		} else {
			scanReq.Background = flag
		}
		return nil
	case "timeout":
		duration, err := strictDurationValue(value)
		if err != nil {
			return err
		}
		scanReq.Timeout = duration
		return nil
	case "timeout_ms", "timeout_sec":
		return applyPermissionScaledTimeout(scanReq, canonical, value)
	case "max_output_bytes":
		integer, err := strictInt64Value(value)
		if err != nil {
			return err
		}
		if integer < 0 {
			return errors.New("cannot be negative")
		}
		scanReq.MaxOutputBytes = integer
		return nil
	default:
		return fmt.Errorf("unsupported canonical field %q", canonical)
	}
}

func applyPermissionArgs(scanReq *ScanRequest, value any) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("must be an array of strings, got %T", value)
	}
	scanReq.Args = make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			return fmt.Errorf("item %d must be a string, got %T", i, item)
		}
		scanReq.Args[i] = text
	}
	return nil
}

func applyPermissionEnv(scanReq *ScanRequest, value any) error {
	items, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("must be an object of string values, got %T", value)
	}
	scanReq.Env = make(map[string]string, len(items))
	for key, item := range items {
		text, ok := item.(string)
		if !ok {
			return fmt.Errorf("environment %q must be a string, got %T", key, item)
		}
		scanReq.Env[key] = text
	}
	return nil
}

func applyPermissionScaledTimeout(scanReq *ScanRequest, canonical string, value any) error {
	integer, err := strictInt64Value(value)
	if err != nil {
		return err
	}
	unit := time.Millisecond
	if canonical == "timeout_sec" {
		unit = time.Second
	}
	duration, err := checkedDuration(integer, unit)
	if err != nil {
		return err
	}
	scanReq.Timeout = duration
	return nil
}

func applyPermissionStringField(scanReq *ScanRequest, canonical, value string) {
	switch canonical {
	case "command":
		scanReq.Command = value
	case "working_dir":
		scanReq.WorkingDir = value
	case "stdin":
		scanReq.Stdin = value
	case "code":
		scanReq.Code = value
	case "language":
		scanReq.Language = value
	case "backend":
		scanReq.Backend = value
	}
}

func strictDurationValue(value any) (time.Duration, error) {
	if text, ok := value.(string); ok {
		duration, err := time.ParseDuration(text)
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %w", err)
		}
		if duration < 0 {
			return 0, errors.New("duration cannot be negative")
		}
		return duration, nil
	}
	seconds, err := strictInt64Value(value)
	if err != nil {
		return 0, fmt.Errorf("must be a duration string or integer seconds: %w", err)
	}
	return checkedDuration(seconds, time.Second)
}

func strictInt64Value(value any) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("must be an integer, got %T", value)
	}
	result, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer: %w", err)
	}
	return result, nil
}

func checkedDuration(value int64, unit time.Duration) (time.Duration, error) {
	if value < 0 {
		return 0, errors.New("duration cannot be negative")
	}
	if value > math.MaxInt64/int64(unit) {
		return 0, errors.New("duration overflows time.Duration")
	}
	return time.Duration(value) * unit, nil
}

func setOTelAttributes(ctx context.Context, req ScanRequest, report Report) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	rule := ""
	risk := "none"
	if len(report.Findings) > 0 {
		rule = report.Findings[0].RuleID
		risk = string(report.Findings[0].Severity)
	}
	rules := safetyRuleIDs(report.Findings)
	span.SetAttributes(
		attribute.String("tool.safety.decision", string(report.Decision)),
		attribute.String("tool.safety.risk", risk),
		attribute.String("tool.safety.rule", rule),
		attribute.StringSlice("tool.safety.rules", rules),
		attribute.Bool("tool.safety.blocked", report.Blocked),
		attribute.String("tool.safety.backend", RedactString(req.Backend)),
		attribute.String("tool.safety.request_sha256", report.RequestID),
		attribute.Int64("tool.safety.duration_us", report.Duration.Microseconds()),
	)
}

const maxTelemetryRuleIDs = 32

func safetyRuleIDs(findings []Finding) []string {
	seen := make(map[string]struct{}, len(findings))
	rules := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding.RuleID == "" {
			continue
		}
		if _, exists := seen[finding.RuleID]; exists {
			continue
		}
		seen[finding.RuleID] = struct{}{}
		rules = append(rules, finding.RuleID)
	}
	sort.Strings(rules)
	if len(rules) > maxTelemetryRuleIDs {
		rules = rules[:maxTelemetryRuleIDs]
	}
	return rules
}

func digestBytes(data []byte) string {
	return requestDigest(ScanRequest{RawFields: map[string]any{"digest_input": string(data)}})
}
