//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const maxStoredResults = 32

// Attr keys reserved for OpenTelemetry consumers.
const (
	// AttrDecision is the span attribute for allow/deny/ask.
	// JSONL audit uses the same suffix as the field name "decision".
	AttrDecision = "tool.safety.decision"
	// AttrRiskLevel is the span attribute for risk severity.
	AttrRiskLevel = "tool.safety.risk_level"
	// AttrRuleID is the span attribute for the winning rule id.
	AttrRuleID = "tool.safety.rule_id"
	// AttrBackend is the span attribute for workspace/host/code backend.
	AttrBackend = "tool.safety.backend"
	// AttrBlocked is true when the decision is deny or ask.
	AttrBlocked = "tool.safety.blocked"
	// AttrToolCallID is the model-issued tool call id when present.
	AttrToolCallID = "tool.safety.tool_call_id"
)

// Guard is a thin pre-execution safety policy that implements tool.PermissionPolicy.
//
// Design goals:
//   - Reuse internal/shellsafe instead of inventing a second parser.
//   - Fail closed on unparsable commands and omitted deny lists.
//   - Integrate through PermissionPolicy only (no WrapToolSet capability loss).
//   - Scan command / args / stdin / code_blocks / secret-shaped JSON keys.
//
// Guard does not replace workspace isolation, CleanEnv hardening, or sandboxes.
type Guard struct {
	policy  Policy
	audit   Auditor
	loadErr error
	extra   []Rule

	mu      sync.Mutex
	last    []Result
	reports int
}

// Option configures Guard.
type Option func(*Guard)

// WithPolicy sets an in-memory policy (already merged / defaulted).
func WithPolicy(p Policy) Option {
	return func(g *Guard) { g.policy = p }
}

// WithPolicyFile loads and overlays DefaultPolicy from path.
// Load failures are stored on the Guard and surfaced as deny decisions;
// they do not overwrite a caller-provided Auditor.
func WithPolicyFile(path string) Option {
	return func(g *Guard) {
		p, err := LoadPolicyFile(path)
		if err != nil {
			g.policy = DefaultPolicy()
			g.loadErr = err
			return
		}
		g.policy = p
		g.loadErr = nil
	}
}

// WithAuditor attaches an audit sink.
// Bare *FileAuditor values are wrapped in AsyncAuditor so CheckToolPermission
// never waits on disk I/O. MemoryAuditor stays synchronous (in-process).
// Call Close on the resulting *AsyncAuditor at process shutdown if you need
// a drained queue.
func WithAuditor(a Auditor) Option {
	return func(g *Guard) {
		if a == nil {
			return
		}
		if _, ok := a.(*FileAuditor); ok {
			a = NewAsyncAuditor(a, defaultAuditQueueSize)
		}
		g.audit = a
	}
}

// WithExtraRules registers site-specific rules applied after built-in scans.
// Extra rules can only tighten decisions (deny/ask); they never clear a deny.
func WithExtraRules(rules ...Rule) Option {
	return func(g *Guard) {
		for _, r := range rules {
			if r != nil {
				g.extra = append(g.extra, r)
			}
		}
	}
}

// NewGuard constructs a Guard. With no options it uses DefaultPolicy and a
// memory auditor suitable for tests.
func NewGuard(opts ...Option) *Guard {
	g := &Guard{
		policy: DefaultPolicy(),
		audit:  NewMemoryAuditor(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

// Policy returns the active policy snapshot.
func (g *Guard) Policy() Policy {
	if g == nil {
		return DefaultPolicy()
	}
	return g.policy
}

// LastResults returns a copy of recent scan results (bounded ring, for demos/tests).
func (g *Guard) LastResults() []Result {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Result, len(g.last))
	copy(out, g.last)
	return out
}

// Close drains a wrapped AsyncAuditor if present. Safe when audit is nil or
// synchronous. Prefer calling this at process shutdown after the last check.
func (g *Guard) Close() error {
	if g == nil || g.audit == nil {
		return nil
	}
	if c, ok := g.audit.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// CheckToolPermission implements tool.PermissionPolicy.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if g == nil {
		// A typed-nil Guard must not fail open: callers who wire safety
		// incorrectly should see a deny, not a silent allow.
		return tool.DenyPermission("safety: nil Guard"), nil
	}
	if g.loadErr != nil {
		return tool.DenyPermission("safety policy failed to load: " + g.loadErr.Error()), nil
	}

	start := time.Now()
	ex, err := Extract(req)
	if err != nil {
		return tool.DenyPermission(err.Error()), nil
	}
	// Skip the built-in Scan when there is nothing to inspect. Extra rules
	// still run so DenyToolNames / AskToolNames work on empty arg objects.
	// Secret-shaped JSON keys, path fields, and env overrides must still
	// reach Scan even for unknown / non-exec tools.
	if !needsScan(ex) && len(g.extra) == 0 {
		return tool.AllowPermission(), nil
	}

	result := Result{
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		ToolName:  ex.ToolName,
		Command:   ex.Command,
		Backend:   ex.Backend,
		Advice:    "safe to execute under current policy",
	}
	if needsScan(ex) {
		result = Scan(ex, g.policy)
	}
	for _, rule := range g.extra {
		if rule == nil {
			continue
		}
		if f, ok := rule.Check(ex, g.policy); ok {
			result = finalize(result, f)
			if result.Decision == DecisionDeny {
				break
			}
		}
	}
	result.ToolName = ex.ToolName
	if result.ToolName == "" && req != nil {
		result.ToolName = req.ToolName
	}
	toolCallID := ""
	if req != nil {
		toolCallID = req.ToolCallID
	}

	g.record(result)
	g.emitSpan(ctx, result, toolCallID)
	g.appendAudit(ctx, AuditEvent{
		Timestamp:  time.Now().UTC(),
		ToolName:   result.ToolName,
		ToolCallID: toolCallID,
		Decision:   result.Decision,
		RiskLevel:  result.RiskLevel,
		RuleID:     result.RuleID,
		Backend:    result.Backend,
		DurationMS: time.Since(start).Milliseconds(),
		Redacted:   result.Redacted,
		Blocked:    result.Blocked,
		Evidence:   result.Evidence,
	})

	switch result.Decision {
	case DecisionDeny:
		return tool.DenyPermission(formatReason(result)), nil
	case DecisionAsk:
		return tool.AskPermission(formatReason(result)), nil
	default:
		return tool.AllowPermission(), nil
	}
}

func formatReason(r Result) string {
	return fmt.Sprintf("tool safety [%s]: %s (%s)", r.RuleID, r.Evidence, r.Advice)
}

func needsScan(ex Extracted) bool {
	if strings.TrimSpace(ex.Command) != "" ||
		strings.TrimSpace(ex.Stdin) != "" ||
		strings.TrimSpace(ex.Cwd) != "" ||
		strings.TrimSpace(ex.RawText) != "" ||
		len(ex.CodeBlocks) > 0 ||
		len(ex.Paths) > 0 ||
		len(ex.Env) > 0 {
		return true
	}
	// Known exec backends still need backend-specific rules (e.g. hostexec ask)
	// even when the argument object is empty.
	switch ex.Backend {
	case BackendHost, BackendWorkspace, BackendCode:
		return true
	default:
		return false
	}
}

func (g *Guard) appendAudit(ctx context.Context, ev AuditEvent) {
	if g == nil || g.audit == nil {
		return
	}
	// Best-effort only: never let audit I/O determine the permission latency.
	// Prefer ContextAuditor (AsyncAuditor / MemoryAuditor); ignore errors.
	if ca, ok := g.audit.(ContextAuditor); ok {
		_ = ca.AppendContext(ctx, ev)
		return
	}
	_ = g.audit.Append(ev)
}

func (g *Guard) record(r Result) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = append(g.last, r)
	if len(g.last) > maxStoredResults {
		g.last = append([]Result(nil), g.last[len(g.last)-maxStoredResults:]...)
	}
	g.reports++
}

func (g *Guard) emitSpan(ctx context.Context, r Result, toolCallID string) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(AttrDecision, string(r.Decision)),
		attribute.String(AttrRiskLevel, string(r.RiskLevel)),
		attribute.String(AttrRuleID, r.RuleID),
		attribute.String(AttrBackend, string(r.Backend)),
		attribute.Bool(AttrBlocked, r.Blocked),
	}
	if toolCallID != "" {
		attrs = append(attrs, attribute.String(AttrToolCallID, toolCallID))
	}
	span.SetAttributes(attrs...)
}
