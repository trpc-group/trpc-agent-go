//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
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
	AttrDecision = "tool.safety.decision"
	// AttrRiskLevel is the span attribute for risk severity.
	AttrRiskLevel = "tool.safety.risk_level"
	// AttrRuleID is the span attribute for the winning rule id.
	AttrRuleID = "tool.safety.rule_id"
	// AttrBackend is the span attribute for workspace/host/code backend.
	AttrBackend = "tool.safety.backend"
)

// Guard is a thin pre-execution safety policy that implements tool.PermissionPolicy.
//
// Design goals (relative to competing #2002 PRs):
//   - Reuse internal/shellsafe instead of inventing a second parser.
//   - Fail closed on unparsable commands and omitted deny lists.
//   - Integrate through PermissionPolicy only (no WrapToolSet capability loss).
//   - Scan code_blocks / stdin as well as command strings.
//
// Guard does not replace workspace isolation, CleanEnv hardening, or sandboxes.
type Guard struct {
	policy  Policy
	audit   Auditor
	loadErr error

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
func WithAuditor(a Auditor) Option {
	return func(g *Guard) {
		if a != nil {
			g.audit = a
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
	// Non-exec tools with empty payload: allow (permission policy may still
	// be combined with other policies by the host).
	if ex.Command == "" && ex.Stdin == "" && len(ex.CodeBlocks) == 0 &&
		ex.Backend == BackendUnknown {
		return tool.AllowPermission(), nil
	}

	result := Scan(ex, g.policy)
	result.ToolName = ex.ToolName
	if result.ToolName == "" && req != nil {
		result.ToolName = req.ToolName
	}

	g.record(result)
	g.emitSpan(ctx, result)
	if g.audit != nil {
		// Best-effort audit: permission decision must not hang on disk I/O
		// failure, but we still attempt to record. Callers who need hard
		// guarantees should use an in-memory Auditor or buffer.
		_ = g.audit.Append(AuditEvent{
			Timestamp:  time.Now().UTC(),
			ToolName:   result.ToolName,
			Decision:   result.Decision,
			RiskLevel:  result.RiskLevel,
			RuleID:     result.RuleID,
			Backend:    result.Backend,
			DurationMS: time.Since(start).Milliseconds(),
			Redacted:   result.Redacted,
			Blocked:    result.Blocked,
			Evidence:   result.Evidence,
		})
	}

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

func (g *Guard) record(r Result) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = append(g.last, r)
	if len(g.last) > maxStoredResults {
		g.last = append([]Result(nil), g.last[len(g.last)-maxStoredResults:]...)
	}
	g.reports++
}

func (g *Guard) emitSpan(ctx context.Context, r Result) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return
	}
	span.SetAttributes(
		attribute.String(AttrDecision, string(r.Decision)),
		attribute.String(AttrRiskLevel, string(r.RiskLevel)),
		attribute.String(AttrRuleID, r.RuleID),
		attribute.String(AttrBackend, string(r.Backend)),
	)
}
