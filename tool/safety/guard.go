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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures a Guard.
type Option func(*guardOptions) error

type guardOptions struct {
	policy        Policy
	policyPath    string
	auditPath     string
	auditWriter   io.Writer
	telemetry     bool
	redaction     bool
	profiles      []ToolProfile
	requiredAudit bool
	concurrency   ConcurrencyPolicy
}

// WithPolicy uses an in-memory policy. The policy is validated; an
// invalid policy causes NewGuard to fail.
func WithPolicy(policy Policy) Option {
	return func(o *guardOptions) error {
		o.policy = policy
		return nil
	}
}

// WithPolicyFile loads the policy from path. If path is empty, the
// default policy is used.
func WithPolicyFile(path string) Option {
	return func(o *guardOptions) error {
		o.policyPath = path
		return nil
	}
}

// WithAuditPath writes audit events to path. The file is opened with
// 0600 permissions.
func WithAuditPath(path string) Option {
	return func(o *guardOptions) error {
		o.auditPath = path
		return nil
	}
}

// WithAuditWriter uses an existing io.Writer as the audit destination.
// The raw writer is stored and wrapped in an AuditWriter only after the
// final policy is resolved, so the policy's audit.required and
// audit.redact_secrets settings apply to injected writers the same way
// they apply to file-backed writers. WithRequiredAudit remains an
// explicit override that can promote the required flag. The caller does
// not need to construct an AuditWriter first.
func WithAuditWriter(w io.Writer) Option {
	return func(o *guardOptions) error {
		if w == nil {
			return errors.New("audit writer is nil")
		}
		o.auditWriter = w
		return nil
	}
}

// WithTelemetry enables OTel span attribute projection.
func WithTelemetry(enabled bool) Option {
	return func(o *guardOptions) error {
		o.telemetry = enabled
		return nil
	}
}

// WithRedaction enables secret redaction in tool results and artifacts.
// Redaction is on by default; pass false to disable.
func WithRedaction(enabled bool) Option {
	return func(o *guardOptions) error {
		o.redaction = enabled
		return nil
	}
}

// WithToolProfile registers an additional tool profile. Default profiles
// remain registered; a custom profile with the same Name replaces the
// default for that name.
func WithToolProfile(profile ToolProfile) Option {
	return func(o *guardOptions) error {
		o.profiles = append(o.profiles, profile)
		return nil
	}
}

// WithRequiredAudit makes an audit-write failure deny execution.
func WithRequiredAudit(enabled bool) Option {
	return func(o *guardOptions) error {
		o.requiredAudit = enabled
		return nil
	}
}

// WithConcurrencyPolicy configures the global and per-tool active-call
// caps. When MaxActiveCalls or PerToolLimits is exceeded,
// the wrapped call returns a deny decision with a
// "resource.concurrency_exceeded" reason.
func WithConcurrencyPolicy(p ConcurrencyPolicy) Option {
	return func(o *guardOptions) error {
		if p.MaxActiveCalls < 0 {
			return errors.New("max active calls must be non-negative")
		}
		for name, limit := range p.PerToolLimits {
			if limit < 0 {
				return fmt.Errorf(
					"per-tool concurrency limit for %q must be non-negative",
					name,
				)
			}
		}
		o.concurrency = p
		return nil
	}
}

// Guard is the public Tool Execution Safety Guard. Use WrapTool for
// package-owned preflight, execution, redaction, audit, and cleanup.
//
// The guard maintains a short-lived side table that correlates the
// wrapper preflight with post-execution completion by tool call id, so
// the post_execute audit event can reuse
// the preflight scan id, decision, risk level, and rule ids. Entries are
// stashed only for allowed calls and evicted when wrapper completion
// runs or when the guard is closed.
type Guard struct {
	scanner    *Scanner
	audit      *AuditWriter
	telemetry  bool
	redaction  bool
	profiles   profileRegistry
	sessions   *sessionTracker
	closeAudit bool

	// concurrencyLimiter caps the number of concurrent in-flight tool
	// calls the guard has permitted. Acquired during wrapper preflight
	// and released during wrapper completion.
	concurrency *concurrencyLimiter

	mu          sync.Mutex
	cond        *sync.Cond
	inFlight    int
	closing     bool
	closed      bool
	closeErr    error
	scanEvents  map[string]scanEvent // keyed by tool call id
	releases    map[string]func()    // keyed by tool call id
	activeCalls map[string]struct{}  // model tool call ids currently in flight
}

// NewGuard constructs a Guard from the given options. If no policy is
// supplied, DefaultPolicy is used. If a policy file path is supplied, it
// is loaded and overrides any in-memory policy.
func NewGuard(opts ...Option) (*Guard, error) {
	o := guardOptions{
		policy:    DefaultPolicy(),
		redaction: true,
	}
	for _, opt := range opts {
		if opt != nil {
			if err := opt(&o); err != nil {
				return nil, err
			}
		}
	}
	if o.policyPath != "" {
		loaded, err := LoadPolicy(o.policyPath)
		if err != nil {
			return nil, err
		}
		o.policy = loaded
	} else if err := o.policy.Validate(); err != nil {
		return nil, err
	}

	g := &Guard{
		scanner:     NewScanner(o.policy, withScannerSessions(nil)),
		telemetry:   o.telemetry,
		redaction:   o.redaction,
		profiles:    newProfileRegistry(),
		sessions:    newSessionTracker(),
		concurrency: newConcurrencyLimiter(o.concurrency),
		scanEvents:  make(map[string]scanEvent),
		releases:    make(map[string]func()),
		activeCalls: make(map[string]struct{}),
	}
	g.cond = sync.NewCond(&g.mu)
	// Inject the guard's session tracker into the scanner so
	// ruleHost can evaluate unknown_session and residual_session
	// findings against the real session lifecycle state.
	g.scanner.sessions = g.sessions
	for _, p := range o.profiles {
		g.profiles.register(p)
		g.scanner.profiles.register(p)
	}

	if o.auditWriter != nil {
		// The writer is wrapped only now, after the final policy has
		// been resolved, so the policy's audit.required and
		// audit.redact_secrets settings apply. The writer's required
		// flag is the single source of truth for whether an append
		// failure denies execution; WithRequiredAudit(true) is an
		// explicit override that promotes it.
		g.audit = NewAuditWriterFrom(
			o.auditWriter,
			o.policy.Audit.Required || o.requiredAudit,
			o.policy.Audit.RedactSecrets,
		)
	} else if o.auditPath != "" || o.policy.Audit.Path != "" {
		path := o.auditPath
		if path == "" {
			path = o.policy.Audit.Path
		}
		w, err := NewAuditWriter(path, o.policy.Audit.Required || o.requiredAudit, o.policy.Audit.RedactSecrets)
		if err != nil {
			return nil, err
		}
		g.audit = w
		g.closeAudit = true
	}
	if g.audit == nil && (o.policy.Audit.Required || o.requiredAudit) {
		// A required audit with no writable destination would silently
		// produce no audit trail at all; fail construction instead.
		return nil, errors.New(
			"audit is required but no audit writer is configured (no audit path and no injected writer)")
	}
	return g, nil
}

// Policy returns the loaded policy. Callers must not mutate it; construct
// a new Guard to apply a changed policy.
func (g *Guard) Policy() Policy {
	if g == nil || g.scanner == nil {
		return Policy{}
	}
	return clonePolicy(g.scanner.policy)
}

func (g *Guard) beginWrappedCall() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing || g.closed {
		return errors.New("safety guard is closed")
	}
	g.inFlight++
	return nil
}

func (g *Guard) endWrappedCall() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight > 0 {
		g.inFlight--
	}
	if g.inFlight == 0 && g.cond != nil {
		g.cond.Broadcast()
	}
}

// Scan runs the scanner against in directly. It is the entry point for
// standalone and batch analysis that does not execute a tool.
func (g *Guard) Scan(ctx context.Context, in ScanInput) (ScanReport, error) {
	if g == nil {
		return ScanReport{}, errors.New("guard is nil")
	}
	return g.scanner.Scan(ctx, in)
}

// ScanBatch scans every input using the guard's policy and scanner.
func (g *Guard) ScanBatch(ctx context.Context, inputs []ScanInput) (BatchReport, error) {
	if g == nil {
		return BatchReport{}, errors.New("guard is nil")
	}
	return g.scanner.ScanBatch(ctx, inputs)
}

// checkToolCall decodes the request arguments via the registered
// profile, scans the resulting ScanInput, writes a preflight audit
// event, acquires lifecycle state, and returns the safety decision. The
// callable wrapper must invoke finalizeCall for every allowed decision.
//
// The Reason field of a deny/ask decision mentions the decision, primary
// rule id, and recommendation without exposing the original command or
// any secret value.
func (g *Guard) checkToolCall(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if g == nil {
		return tool.DenyPermission("safety guard is nil"), nil
	}
	if req == nil {
		return tool.DenyPermission("permission request is nil"), nil
	}
	in, decodeErr := decodeRequest(req.ToolName, req.Arguments, g.profiles)
	if decodeErr != nil {
		return g.permissionForDecodeFailure(ctx, req, decodeErr), nil
	}

	// Apply metadata defaults from the registered profile when the
	// decoded ScanInput did not carry them.
	in = g.applyProfileDefaults(in)
	// Map tool.Metadata into ScanInput so rules can use it.
	in.Metadata = ToolMetadata{
		ReadOnly:        req.Metadata.ReadOnly,
		Destructive:     req.Metadata.Destructive,
		ConcurrencySafe: req.Metadata.ConcurrencySafe,
		SearchOrRead:    req.Metadata.SearchOrRead,
		OpenWorld:       req.Metadata.OpenWorld,
		MaxResultSize:   req.Metadata.MaxResultSize,
	}
	// Map tool.Metadata.MaxResultSize into OutputSizeHint so the
	// resource rule can compare it against max_output_size.
	if in.OutputSizeHint == 0 && req.Metadata.MaxResultSize > 0 {
		in.OutputSizeHint = int64(req.Metadata.MaxResultSize)
	}

	report := g.scanPermission(ctx, req, in)

	// Concurrency gate first, preflight audit second, so the persisted
	// record carries the final decision. A denied call never executes, so
	// a slot acquired by the gate is released here instead of in the
	// completion handler.
	release := g.gateConcurrency(ctx, req, &report)
	release, reserved := g.reserveAllowedCall(req, &report, release)
	transferred := false
	defer func() {
		if transferred {
			return
		}
		if release != nil {
			release()
		}
		if reserved {
			g.releaseToolCallID(req.ToolCallID)
		}
	}()
	if denied := g.auditPreflightOrDeny(&report); denied && release != nil {
		release()
		release = nil
	}
	if report.Decision != DecisionAllow && reserved {
		g.releaseToolCallID(req.ToolCallID)
		reserved = false
	}

	if g.telemetry {
		telemetryProject(ctx, safetyAttributes(report))
	}

	if report.Decision == DecisionAllow {
		g.stashRelease(req.ToolCallID, release)
		// Stash the scan event so wrapper completion can reuse the scan
		// id, decision, risk level, and rule ids.
		g.stashScanEvent(req.ToolCallID, fromReport(report))
		transferred = true
	}

	return permissionDecisionForReport(report), nil
}

func (g *Guard) permissionForDecodeFailure(
	ctx context.Context,
	req *tool.PermissionRequest,
	decodeErr error,
) tool.PermissionDecision {
	report := g.decodeFailureReport(req, decodeErr)
	g.auditPreflightOrDeny(&report)
	if g.telemetry {
		telemetryProject(ctx, safetyAttributes(report))
	}
	return permissionDecisionForReport(report)
}

func (g *Guard) scanPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
	in ScanInput,
) ScanReport {
	report, err := g.scanner.Scan(ctx, in)
	if err == nil {
		report.Backend = coalesceBackend(report.Backend, in.Backend)
		return report
	}
	return ScanReport{
		SchemaVersion: "1",
		ScanID:        newScanID(),
		Timestamp:     g.scanner.clock(),
		ToolName:      req.ToolName,
		Backend:       in.Backend,
		Decision:      DecisionDeny,
		RiskLevel:     RiskHigh,
		Findings: []Finding{{
			RuleID:         "scanner.error",
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       redactedSnippet(err.Error(), 80),
			Recommendation: "Refuse the call; the safety scanner reported an internal error",
		}},
		Intercepted: true,
	}
}

func (g *Guard) reserveAllowedCall(
	req *tool.PermissionRequest,
	report *ScanReport,
	release func(),
) (func(), bool) {
	if report.Decision != DecisionAllow || req.ToolCallID == "" {
		return release, false
	}
	if g.reserveToolCallID(req.ToolCallID) {
		return release, true
	}
	if release != nil {
		release()
		release = nil
	}
	report.Decision = DecisionDeny
	report.Intercepted = true
	report.RiskLevel = RiskHigh
	report.Findings = append(report.Findings, Finding{
		RuleID:         "input.duplicate_tool_call_id",
		RiskLevel:      RiskHigh,
		Decision:       DecisionDeny,
		Evidence:       "tool call id is already active",
		Recommendation: "Generate a unique tool call id for every concurrent execution",
	})
	sortFindings(report.Findings)
	return release, false
}

func permissionDecisionForReport(
	report ScanReport,
) tool.PermissionDecision {
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission()
	case DecisionAsk:
		return tool.AskPermission(formatReason(report))
	default:
		return tool.DenyPermission(formatReason(report))
	}
}

// decodeFailureReport builds the fail-closed report for undecodable
// arguments. A known tool with malformed arguments is denied (fail
// closed). An unknown tool with malformed arguments is asked (we cannot
// determine what it would do, so human review is required).
func (g *Guard) decodeFailureReport(req *tool.PermissionRequest, decodeErr error) ScanReport {
	_, isKnown := g.profiles.lookup(req.ToolName)
	decision := DecisionDeny
	riskLevel := RiskHigh
	ruleID := "input.decode_failure"
	if !isKnown {
		decision = DecisionAsk
		riskLevel = RiskMedium
		ruleID = "input.unknown_malformed"
	}
	return ScanReport{
		SchemaVersion: "1",
		ScanID:        newScanID(),
		Timestamp:     g.scanner.clock(),
		ToolName:      req.ToolName,
		Backend:       BackendUnknown,
		Decision:      decision,
		RiskLevel:     riskLevel,
		Findings: []Finding{{
			RuleID:         ruleID,
			RiskLevel:      riskLevel,
			Decision:       decision,
			Evidence:       redactedSnippet(decodeErr.Error(), 80),
			Recommendation: "Refuse the call; the tool arguments could not be decoded safely",
		}},
		Intercepted: true,
	}
}

// gateConcurrency acquires a concurrency slot for an allow decision and
// returns the release function the caller must stash for wrapper
// completion. Deny/ask/error decisions never acquire a slot, so they never
// leak. When the cap is exceeded the report is downgraded to a deny with
// a resource.concurrency_exceeded finding and no slot is held.
//
// A request without a tool call id cannot be correlated with the
// completion hook that releases the slot, so it is denied whenever a
// concurrency limit is configured.
func (g *Guard) gateConcurrency(
	ctx context.Context,
	req *tool.PermissionRequest,
	report *ScanReport,
) (release func()) {
	if report.Decision != DecisionAllow {
		return nil
	}
	if req.ToolCallID == "" && g.concurrency.enabled() {
		report.Decision = DecisionDeny
		report.Intercepted = true
		report.RiskLevel = RiskHigh
		report.Findings = append(report.Findings, Finding{
			RuleID:         "resource.concurrency_id_required",
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       "concurrency limits require a non-empty tool call id",
			Recommendation: "Generate a unique tool call id before executing the tool",
		})
		sortFindings(report.Findings)
		return nil
	}
	release, err := g.concurrency.acquire(ctx, req.ToolName)
	if err != nil {
		report.Decision = DecisionDeny
		report.Intercepted = true
		report.RiskLevel = RiskHigh
		report.Findings = append(report.Findings, Finding{
			RuleID:         "resource.concurrency_exceeded",
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       redactedSnippet(err.Error(), 80),
			Recommendation: "Reduce concurrent tool calls or raise the concurrency policy cap",
		})
		sortFindings(report.Findings)
		return nil
	}
	return release
}

// auditPreflightOrDeny appends the preflight audit event. When the
// writer is required and rejects the event, the report is downgraded to
// a deny with an audit.write_failure finding and true is returned so the
// caller can release any concurrency slot it already holds. The writer's
// required flag is the single source of truth; this covers both
// file-backed writers (closeAudit=true) and injected writers
// (closeAudit=false) that were configured with required=true.
func (g *Guard) auditPreflightOrDeny(report *ScanReport) bool {
	if auditErr := g.maybeAuditPreflight(*report); auditErr != nil &&
		g.audit != nil && g.audit.required {
		report.Decision = DecisionDeny
		report.Intercepted = true
		report.Findings = append(report.Findings, Finding{
			RuleID:         "audit.write_failure",
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       "required audit writer rejected the preflight event",
			Recommendation: "Restore audit storage or relax audit.required in the policy",
		})
		sortFindings(report.Findings)
		return true
	}
	return false
}

// applyProfileDefaults fills in backend and default timeout from the
// registered profile when the decoded ScanInput did not carry them. The
// profile default timeout is applied WITHOUT capping it at the policy's
// MaxTimeout: when the request omits a timeout, the backend really runs
// with its own default, so the scanner must evaluate that effective
// duration. When the backend default exceeds MaxTimeout the
// resource.timeout_exceeded rule fires and the call cannot be allowed
// as-is; the caller must supply an explicit bounded timeout (or the
// operator must raise max_timeout).
func (g *Guard) applyProfileDefaults(in ScanInput) ScanInput {
	if profile, ok := g.profiles.lookup(in.ToolProfile); ok {
		if in.Backend == "" || in.Backend == BackendUnknown {
			in.Backend = profile.Backend
		}
		if in.Timeout <= 0 {
			in.Timeout = profile.DefaultTimeout
		}
	}
	if in.Backend == "" {
		// If the tool name matches a default profile but the decoder
		// did not set ToolProfile, look up by tool name.
		if profile, ok := g.profiles.lookup(in.ToolName); ok {
			in.Backend = profile.Backend
			in.ToolProfile = profile.Name
			if in.Timeout <= 0 {
				in.Timeout = profile.DefaultTimeout
			}
		}
	}
	return in
}

// maybeAuditPreflight appends a preflight audit event when the guard has
// an audit writer.
func (g *Guard) maybeAuditPreflight(report ScanReport) error {
	if g == nil || g.audit == nil {
		return nil
	}
	return g.audit.appendPreflight(report)
}

// stashScanEvent records the preflight scan event keyed by tool call id
// so wrapper completion can correlate the two phases. The event is
// evicted by popScanEvent or by Close.
func (g *Guard) stashScanEvent(toolCallID string, ev scanEvent) {
	if g == nil || toolCallID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scanEvents == nil {
		g.scanEvents = make(map[string]scanEvent)
	}
	g.scanEvents[toolCallID] = ev
}

// popScanEvent returns and removes the preflight scan event for
// toolCallID. Returns a zero scanEvent when no event was stashed.
func (g *Guard) popScanEvent(toolCallID string) scanEvent {
	if g == nil || toolCallID == "" {
		return scanEvent{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ev, ok := g.scanEvents[toolCallID]
	if ok {
		delete(g.scanEvents, toolCallID)
	}
	return ev
}

// stashRelease records the concurrency-limiter release function for
// toolCallID so wrapper completion can free the slot. When an entry
// for toolCallID already exists it is superseded: the previous release
// is invoked before the new one is stored, so a re-stash never leaks the
// earlier slot.
func (g *Guard) stashRelease(toolCallID string, release func()) {
	if g == nil || toolCallID == "" || release == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.releases == nil {
		g.releases = make(map[string]func())
	}
	if prev := g.releases[toolCallID]; prev != nil {
		delete(g.releases, toolCallID)
		prev()
	}
	g.releases[toolCallID] = release
}

// reserveToolCallID reserves id for one active execution. It returns
// false when another execution is already using the same model-issued
// id, because correlation and concurrency state would otherwise be
// ambiguous.
func (g *Guard) reserveToolCallID(id string) bool {
	if g == nil || id == "" {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeCalls == nil {
		g.activeCalls = make(map[string]struct{})
	}
	if _, exists := g.activeCalls[id]; exists {
		return false
	}
	g.activeCalls[id] = struct{}{}
	return true
}

// releaseToolCallID releases an active execution id.
func (g *Guard) releaseToolCallID(id string) {
	if g == nil || id == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.activeCalls, id)
}

// finishCall releases concurrency and correlation state for one
// completed tool call.
func (g *Guard) finishCall(id string) {
	if r := g.popRelease(id); r != nil {
		r()
	}
	g.popScanEvent(id)
	g.releaseToolCallID(id)
}

// popRelease returns and removes the release function for toolCallID.
func (g *Guard) popRelease(toolCallID string) func() {
	if g == nil || toolCallID == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.releases[toolCallID]
	if ok {
		delete(g.releases, toolCallID)
	}
	return r
}

// maybeAuditPostExecute appends a post_execute audit event.
func (g *Guard) maybeAuditPostExecute(
	ev scanEvent,
	outputBytes int64,
	truncated bool,
	execution string,
) error {
	if g == nil || g.audit == nil {
		return nil
	}
	return g.audit.appendPostExecute(ev, outputBytes, truncated, execution)
}

// finalizeCall handles three completion surfaces:
//   - args.Result: walked via redactValue, then size-limited.
//   - args.Error: its string form is redacted and, when it contains a
//     secret, replaced with a safe structured CustomResult so the model
//     never sees the raw error text.
//   - args.Meta: each value is redacted; MCP _meta fields frequently
//     carry bearer tokens or request ids that must not leak.
//
// finalizeCall performs result redaction, output limiting, post-execute
// audit, session tracking, and concurrency release.
func (g *Guard) finalizeCall(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if g == nil || args == nil {
		return nil, nil
	}
	// Always release the concurrency slot and pop the preflight
	// scan event, even when redaction is disabled, so the limiter
	// does not leak slots and the side table does not grow without
	// bound.
	defer g.finishCall(args.ToolCallID)
	originalResult := args.Result

	// Step 1: Handle error redaction FIRST, before computing
	// safeResult. When the error contains a secret, replace
	// args.Result with a structured safe error message so the
	// model never sees the raw error text.
	errorRedacted := g.redactErrorIfNeeded(args)

	// Step 2: Redact and limit the result (which may have been
	// replaced in step 1).
	safeResult, resultRedacted, resultChanged,
		resultTruncated, resultSize :=
		g.redactAndLimitTracked(args.Result)

		// Step 3: Redact Meta in place.
	metaRedacted := g.redactMetaIfNeeded(args)

	// Track host/workspace session lifecycle only for successful
	// calls: a failed exec/kill call carrying a session id must not
	// register or clear session state.
	if args.Error == nil {
		g.trackSessionLifecycle(
			args.ToolName, args.Arguments, originalResult,
		)
	}

	execution := "ok"
	if args.Error != nil {
		execution = "error"
	}
	redacted := resultRedacted || errorRedacted || metaRedacted
	truncated := resultTruncated

	// Build the post_execute audit event. Execution already
	// happened, so an audit failure cannot deny anything; with a
	// required writer it is surfaced as a warning instead.
	ev := g.postExecuteEvent(args, redacted, truncated)
	if err := g.maybeAuditPostExecute(
		ev, resultSize, truncated, execution,
	); err != nil {
		log.WarnfContext(ctx,
			"tool_safety: post-execute audit append failed for tool %q: %v",
			args.ToolName, err)
	}

	if !resultChanged && !errorRedacted && !metaRedacted &&
		!resultTruncated {
		return nil, nil
	}
	return &tool.AfterToolResult{CustomResult: safeResult}, nil
}

// trackSessionLifecycle registers or clears session state based on the
// tool name and the result. When exec_command or workspace_exec returns
// a session_id, the session is registered. When kill_session or
// workspace_kill_session completes, the session is marked killed. The
// caller invokes this only for successful calls; a failed call must not
// mutate lifecycle state.
func (g *Guard) trackSessionLifecycle(
	toolName string,
	arguments []byte,
	result any,
) {
	if g == nil || g.sessions == nil {
		return
	}
	switch toolName {
	case "write_stdin", "workspace_write_stdin":
		if !isTerminalSessionStatus(extractResultStatus(result)) {
			return
		}
		if in, err := decodeRequest(
			toolName, arguments, g.profiles,
		); err == nil {
			g.sessions.kill(in.SessionID)
		}
	case "exec_command", "workspace_exec":
		g.sessions.register(extractSessionID(result))
	case "kill_session", "workspace_kill_session":
		g.sessions.kill(extractSessionID(result))
	}
}

func extractResultStatus(result any) string {
	raw, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return ""
	}
	value, ok := decoded.(map[string]any)
	if !ok {
		return ""
	}
	status, _ := value["status"].(string)
	return status
}

func isTerminalSessionStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "exited", "killed", "completed", "closed", "done":
		return true
	}
	return false
}

// extractSessionID returns the session_id field from a structured tool
// result. Returns "" when the result is nil or has no session_id.
func extractSessionID(result any) string {
	if result == nil {
		return ""
	}
	switch v := result.(type) {
	case map[string]any:
		if s, ok := v["session_id"].(string); ok {
			return s
		}
		if s, ok := v["sessionId"].(string); ok {
			return s
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return ""
	}
	if value, ok := decoded.(map[string]any); ok {
		if sessionID, ok := value["session_id"].(string); ok {
			return sessionID
		}
		if sessionID, ok := value["sessionId"].(string); ok {
			return sessionID
		}
	}
	return ""
}

// redactAndLimitTracked is redactAndLimit with explicit changed/truncated
// tracking so the audit event records the real redaction state. When
// truncation happens without redaction, changed is also set true so the
// completion returns the truncated value instead of the original.
func (g *Guard) redactAndLimitTracked(
	value any,
) (
	safe any,
	redacted bool,
	changed bool,
	truncated bool,
	size int64,
) {
	if !g.redaction {
		out, trunc, sz := limitResultBytes(value, g.scanner.policy.MaxOutputSize)
		return out, false, trunc, trunc, sz
	}
	redactedValue, c, err := redactValue(value)
	if err != nil {
		safeMarker := map[string]any{
			"status": "redacted",
			"reason": "tool result contained a secret and could not be safely returned",
		}
		out, trunc, sz := limitResultBytes(safeMarker, g.scanner.policy.MaxOutputSize)
		return out, true, true, trunc, sz
	}
	out, trunc, sz := limitResultBytes(
		redactedValue, g.scanner.policy.MaxOutputSize,
	)
	// changed is true when redaction changed the value OR when
	// truncation changed the value, so completion returns the safe
	// (truncated) value instead of the original.
	changed = c || trunc
	return out, c, changed, trunc, sz
}

// redactErrorIfNeeded returns true when args.Error contained a secret.
// In that case args.Result is replaced with a safe structured error so
// completion returns the redacted message as a CustomResult and the
// model never sees the raw error text.
func (g *Guard) redactErrorIfNeeded(args *tool.AfterToolArgs) bool {
	if args.Error == nil || !g.redaction {
		return false
	}
	errStr := args.Error.Error()
	if !hasSecret(errStr) {
		return false
	}
	redacted, _ := redactString(errStr)
	// Replace the result with a safe structured error so the model
	// sees a redacted message instead of the raw error text.
	args.Result = map[string]any{
		"status":  "error_redacted",
		"message": redacted,
	}
	return true
}

// redactMetaIfNeeded redacts secret-bearing values in args.Meta in place.
// Returns true when any redaction was applied.
func (g *Guard) redactMetaIfNeeded(args *tool.AfterToolArgs) bool {
	if !g.redaction || len(args.Meta) == 0 {
		return false
	}
	safe, changed, err := redactValue(args.Meta)
	if err != nil {
		replaceMetadata(args.Meta, map[string]any{
			"status": "redacted",
			"reason": "tool metadata could not be redacted safely",
		})
		return true
	}
	safeMeta, ok := safe.(map[string]any)
	if !ok {
		replaceMetadata(args.Meta, map[string]any{
			"status": "redacted",
			"reason": "tool metadata had an unsupported shape",
		})
		return true
	}
	if changed {
		replaceMetadata(args.Meta, safeMeta)
	}
	return changed
}

func replaceMetadata(
	target map[string]any,
	safe map[string]any,
) {
	for key := range target {
		delete(target, key)
	}
	for key, value := range safe {
		target[key] = value
	}
}

// postExecuteEvent builds a post_execute scanEvent that reuses the
// preflight scan event when available. The guard stashes the preflight
// event in a side table keyed by tool call id so completion can
// correlate the two phases. When no preflight event is found, a minimal
// standalone event is produced.
func (g *Guard) postExecuteEvent(args *tool.AfterToolArgs, redacted, truncated bool) scanEvent {
	sessionHash := g.postExecuteSessionHash(args)
	if pre := g.popScanEvent(args.ToolCallID); pre.ScanID != "" {
		pre.Redacted = redacted
		if pre.SessionHash == "" {
			pre.SessionHash = sessionHash
		}
		return pre
	}
	return scanEvent{
		ScanID:      newScanID(),
		ToolName:    args.ToolName,
		Backend:     g.backendFor(args.ToolName),
		Decision:    DecisionAllow,
		RiskLevel:   RiskLow,
		Redacted:    redacted,
		SessionHash: sessionHash,
	}
}

// postExecuteSessionHash returns the hashed session id for a
// post-execute event so the audit record carries a non-empty
// session_hash. The session id is decoded from the tool arguments
// (write_stdin/kill_session); when absent, a session_id in the tool
// result (exec_command/workspace_exec) is used. Returns "" when the
// call has no session id.
func (g *Guard) postExecuteSessionHash(args *tool.AfterToolArgs) string {
	if g == nil || args == nil {
		return ""
	}
	if in, err := decodeRequest(args.ToolName, args.Arguments, g.profiles); err == nil &&
		in.SessionID != "" {
		return hashSessionID(in.SessionID)
	}
	return hashSessionID(extractSessionID(args.Result))
}

// backendFor returns the registered backend for toolName, or
// BackendUnknown when no profile matches.
func (g *Guard) backendFor(toolName string) Backend {
	if profile, ok := g.profiles.lookup(toolName); ok {
		return profile.Backend
	}
	return BackendUnknown
}

// RedactString redacts secrets in a single string value.
func (g *Guard) RedactString(value string) (string, bool) {
	if !g.redaction {
		return value, false
	}
	return redactString(value)
}

// RedactValue redacts secrets in any JSON-compatible value. Unknown types
// that contain a secret are replaced with a safe structured marker.
func (g *Guard) RedactValue(value any) (any, bool, error) {
	if !g.redaction {
		return value, false, nil
	}
	return redactValue(value)
}

// LimitResult truncates the result to max_output_size bytes after
// redaction. Returns the truncated value, whether truncation happened,
// and the total byte size.
func (g *Guard) LimitResult(value any) (any, bool, int64) {
	return limitResultBytes(value, g.scanner.policy.MaxOutputSize)
}

// RedactArtifact redacts a single artifact. For text artifacts, secrets
// are replaced with [REDACTED:...]. For binary artifacts containing a
// secret, an error is returned so the caller can refuse the save.
func (g *Guard) RedactArtifact(in *artifact.Artifact) (*artifact.Artifact, bool, error) {
	if !g.redaction {
		return in, false, nil
	}
	return redactArtifact(in)
}

// WrapArtifactService returns an artifact.Service that redacts or refuses
// secret-bearing artifacts on SaveArtifact and LoadArtifact.
func (g *Guard) WrapArtifactService(service artifact.Service) artifact.Service {
	if !g.redaction {
		return service
	}
	return newArtifactServiceWrapper(service)
}

// Close releases the guard's resources. It flushes and closes the audit
// writer when the guard owns it and resets the session tracker so its
// state does not grow without bound over the guard's lifetime. It is
// safe to call Close after every scan has completed; calling Close while
// a scan is in progress may discard pending audit writes.
func (g *Guard) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.closed {
		err := g.closeErr
		g.mu.Unlock()
		return err
	}
	if g.closing {
		for !g.closed {
			g.cond.Wait()
		}
		err := g.closeErr
		g.mu.Unlock()
		return err
	}
	g.closing = true
	for g.inFlight > 0 {
		g.cond.Wait()
	}
	releases := make([]func(), 0, len(g.releases))
	for _, release := range g.releases {
		if release != nil {
			releases = append(releases, release)
		}
	}
	g.scanEvents = nil
	g.releases = nil
	g.activeCalls = nil
	audit := g.audit
	closeAudit := g.closeAudit
	g.audit = nil
	g.mu.Unlock()
	for _, release := range releases {
		release()
	}
	if g.sessions != nil {
		g.sessions.reset()
	}
	var closeErr error
	if audit != nil && closeAudit {
		closeErr = audit.Close()
	}
	g.mu.Lock()
	g.closeErr = closeErr
	g.closed = true
	g.cond.Broadcast()
	g.mu.Unlock()
	return closeErr
}

// formatReason builds the Reason string for a deny/ask decision. It
// includes the decision, the primary rule id, and the recommendation.
// It never includes the original command or any secret value.
func formatReason(report ScanReport) string {
	primary := ""
	if len(report.Findings) > 0 {
		primary = report.Findings[0].RuleID
	}
	if primary == "" {
		primary = "unknown"
	}
	recommendation := ""
	if len(report.Findings) > 0 {
		recommendation = report.Findings[0].Recommendation
	}
	return fmt.Sprintf(
		"safety %s: rule=%s risk=%s backend=%s recommendation=%s",
		report.Decision, primary, report.RiskLevel,
		report.Backend, recommendation,
	)
}

// coalesceBackend returns the first non-empty, non-unknown backend.
func coalesceBackend(a, b Backend) Backend {
	if a != "" && a != BackendUnknown {
		return a
	}
	if b != "" && b != BackendUnknown {
		return b
	}
	if a != "" {
		return a
	}
	return b
}
