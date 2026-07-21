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
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures a Guard.
type Option func(*guardOptions) error

type guardOptions struct {
	policy        Policy
	policyPath    string
	auditPath     string
	auditWriter   *AuditWriter
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
// The writer is wrapped in an AuditWriter; the caller does not need to
// construct one first. When the caller wants to control the required or
// redaction flags, they can pass WithRequiredAudit and rely on the
// policy's audit.redact_secrets setting.
func WithAuditWriter(w io.Writer) Option {
	return func(o *guardOptions) error {
		if w == nil {
			return errors.New("audit writer is nil")
		}
		o.auditWriter = NewAuditWriterFrom(w, o.requiredAudit, true)
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
// CheckToolPermission returns a deny decision with a
// "resource.concurrency_exceeded" reason.
func WithConcurrencyPolicy(p ConcurrencyPolicy) Option {
	return func(o *guardOptions) error {
		o.concurrency = p
		return nil
	}
}

// Guard is the public Tool Execution Safety Guard. It implements
// tool.PermissionPolicy and provides post-tool redaction/audit callbacks.
//
// Construct one with NewGuard and register it as the runner's
// ToolPermissionPolicy. AttachCallbacks wires the after-tool callback
// into a tool.Callbacks so post-execution redaction and audit happen
// automatically.
//
// The guard maintains a short-lived side table that correlates the
// preflight CheckToolPermission call with the post-execution after-tool
// callback by tool call id, so the post_execute audit event can reuse
// the preflight scan id, decision, risk level, and rule ids. Entries are
// stashed only for allowed calls — deny/ask decisions never reach the
// after-tool callback — and are evicted when the after-tool callback
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
	// calls the guard has permitted. Acquired in CheckToolPermission,
	// released in the after-tool callback.
	concurrency *concurrencyLimiter

	mu         sync.Mutex
	scanEvents map[string]ScanEvent // keyed by tool call id
	releases   map[string]func()    // keyed by tool call id
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
		scanEvents:  make(map[string]ScanEvent),
		releases:    make(map[string]func()),
	}
	// Inject the guard's session tracker into the scanner so
	// ruleHost can evaluate unknown_session and residual_session
	// findings against the real session lifecycle state.
	g.scanner.sessions = g.sessions
	for _, p := range o.profiles {
		g.profiles.register(p)
		g.scanner.profiles.register(p)
	}

	if o.auditWriter != nil {
		g.audit = o.auditWriter
		// The writer's required flag is the single source of truth
		// for whether an append failure denies execution. The
		// previous implementation also checked g.closeAudit, which
		// meant an injected required writer could be bypassed when
		// closeAudit was false. WithRequiredAudit(true) is now
		// honored by promoting the writer's required flag when the
		// caller asks for it.
		if o.requiredAudit && !g.audit.required {
			g.audit.required = true
		}
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
	return g, nil
}

// Policy returns the loaded policy. Callers must not mutate it; construct
// a new Guard to apply a changed policy.
func (g *Guard) Policy() Policy {
	return g.scanner.policy
}

// Scan runs the scanner against in directly, bypassing the
// PermissionPolicy adapter. It is the entry point for batch scanning and
// the example program.
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

// CheckToolPermission implements tool.PermissionPolicy. It decodes the
// request arguments via the registered profile, scans the resulting
// ScanInput, writes a preflight audit event, projects OTel attributes,
// and returns the framework decision.
//
// The Reason field of a deny/ask decision mentions the decision, primary
// rule id, and recommendation without exposing the original command or
// any secret value.
func (g *Guard) CheckToolPermission(
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
		// Distinguish known vs unknown tools. A known tool with
		// malformed arguments is denied (fail closed). An unknown tool
		// with malformed arguments is asked (we cannot determine what
		// it would do, so human review is required).
		_, isKnown := g.profiles.lookup(req.ToolName)
		decision := DecisionDeny
		riskLevel := RiskHigh
		ruleID := "input.decode_failure"
		if !isKnown {
			decision = DecisionAsk
			riskLevel = RiskMedium
			ruleID = "input.unknown_malformed"
		}
		report := ScanReport{
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
		g.maybeAuditPreflight(report)
		if g.telemetry {
			telemetryProject(ctx, safetyAttributes(report))
		}
		if decision == DecisionDeny {
			return tool.DenyPermission(formatReason(report)), nil
		}
		return tool.AskPermission(formatReason(report)), nil
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

	report, err := g.scanner.Scan(ctx, in)
	if err != nil {
		// Scanner error: fail closed.
		report = ScanReport{
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
	report.Backend = coalesceBackend(report.Backend, in.Backend)

	// Required-audit failure must deny even an otherwise-allow decision.
	// The writer's required flag is the single source of truth; this
	// covers both file-backed writers (closeAudit=true) and injected
	// writers (closeAudit=false) that were configured with
	// required=true.
	if auditErr := g.maybeAuditPreflight(report); auditErr != nil &&
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
	}

	if g.telemetry {
		telemetryProject(ctx, safetyAttributes(report))
	}

	// Concurrency gate: acquire a slot ONLY when the decision is allow.
	// Deny/ask/error paths never acquire, so they never leak slots. The
	// release function is stashed for the after-tool callback.
	if report.Decision == DecisionAllow {
		release, concErr := g.concurrency.acquire(ctx, req.ToolName)
		if concErr != nil {
			report.Decision = DecisionDeny
			report.Intercepted = true
			report.RiskLevel = RiskHigh
			report.Findings = append(report.Findings, Finding{
				RuleID:         "resource.concurrency_exceeded",
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       redactedSnippet(concErr.Error(), 80),
				Recommendation: "Reduce concurrent tool calls or raise the concurrency policy cap",
			})
			sortFindings(report.Findings)
			if g.telemetry {
				telemetryProject(ctx, safetyAttributes(report))
			}
		} else {
			g.stashRelease(req.ToolCallID, release)
			// Stash the scan event keyed by tool call id so the
			// after-tool callback can reuse the scan id, decision,
			// risk level, and rule ids for the post_execute audit
			// event. Only the allow path stashes: deny/ask decisions
			// never reach the after-tool callback, so their entries
			// would linger until Close.
			g.stashScanEvent(req.ToolCallID, fromReport(report))
		}
	}

	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission(), nil
	case DecisionAsk:
		return tool.AskPermission(formatReason(report)), nil
	case DecisionDeny:
		return tool.DenyPermission(formatReason(report)), nil
	}
	return tool.DenyPermission(formatReason(report)), nil
}

// applyProfileDefaults fills in backend and default timeout from the
// registered profile when the decoded ScanInput did not carry them. The
// profile default timeout is capped at the policy's MaxTimeout so a
// permissive profile default cannot bypass the policy limit.
func (g *Guard) applyProfileDefaults(in ScanInput) ScanInput {
	if profile, ok := g.profiles.lookup(in.ToolProfile); ok {
		if in.Backend == "" || in.Backend == BackendUnknown {
			in.Backend = profile.Backend
		}
		if in.Timeout <= 0 {
			in.Timeout = capTimeout(profile.DefaultTimeout, g.scanner.policy.MaxTimeout)
		}
	}
	if in.Backend == "" {
		// If the tool name matches a default profile but the decoder
		// did not set ToolProfile, look up by tool name.
		if profile, ok := g.profiles.lookup(in.ToolName); ok {
			in.Backend = profile.Backend
			in.ToolProfile = profile.Name
			if in.Timeout <= 0 {
				in.Timeout = capTimeout(profile.DefaultTimeout, g.scanner.policy.MaxTimeout)
			}
		}
	}
	return in
}

// capTimeout returns d capped at max. When max is zero (unlimited), d is
// returned unchanged. When d is zero, zero is returned.
func capTimeout(d, max time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if max > 0 && d > max {
		return max
	}
	return d
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
// so the after-tool callback can correlate the two phases. The event is
// evicted by popScanEvent or by Close.
func (g *Guard) stashScanEvent(toolCallID string, ev ScanEvent) {
	if g == nil || toolCallID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scanEvents == nil {
		g.scanEvents = make(map[string]ScanEvent)
	}
	g.scanEvents[toolCallID] = ev
}

// popScanEvent returns and removes the preflight scan event for
// toolCallID. Returns a zero ScanEvent when no event was stashed.
func (g *Guard) popScanEvent(toolCallID string) ScanEvent {
	if g == nil || toolCallID == "" {
		return ScanEvent{}
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
// toolCallID so the after-tool callback can free the slot.
func (g *Guard) stashRelease(toolCallID string, release func()) {
	if g == nil || toolCallID == "" || release == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releases[toolCallID] = release
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
	ev ScanEvent,
	outputBytes int64,
	truncated bool,
	execution string,
) error {
	if g == nil || g.audit == nil {
		return nil
	}
	return g.audit.appendPostExecute(ev, outputBytes, truncated, execution)
}

// Callbacks returns a *tool.Callbacks whose AfterTool callback redacts
// secrets, applies max_output_size, and emits the post_execute audit
// event. The callback is also registered into the returned callbacks so
// callers can merge it with their own callbacks.
//
// The returned callbacks are independent of any callbacks the caller
// already has; use AttachCallbacks to merge into an existing callbacks
// value.
func (g *Guard) Callbacks() *tool.Callbacks {
	cbs := tool.NewCallbacks()
	g.attachAfterTool(cbs)
	return cbs
}

// AttachCallbacks merges the guard's after-tool callback into cbs. The
// guard's callback is appended last so an earlier caller callback cannot
// reintroduce a secret after redaction.
func (g *Guard) AttachCallbacks(cbs *tool.Callbacks) {
	if cbs == nil {
		return
	}
	g.attachAfterTool(cbs)
}

// attachAfterTool appends the guard's after-tool callback to cbs. The
// callback redacts secrets in args.Result, args.Error, and args.Meta,
// applies the global max_output_size budget, and emits the post_execute
// audit event with the real redacted/truncated state.
//
// The callback handles three surfaces:
//   - args.Result: walked via redactValue, then size-limited.
//   - args.Error: its string form is redacted and, when it contains a
//     secret, replaced with a safe structured CustomResult so the model
//     never sees the raw error text. The framework has already logged
//     the original error to the warning log before this callback runs;
//     callers that cannot tolerate that log path should suppress
//     warn-level tool-execution logs or install a redacting logger.
//   - args.Meta: each value is redacted; MCP _meta fields frequently
//     carry bearer tokens or request ids that must not leak.
func (g *Guard) attachAfterTool(cbs *tool.Callbacks) {
	cbs.RegisterAfterTool(func(
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
		defer func() {
			if r := g.popRelease(args.ToolCallID); r != nil {
				r()
			}
		}()

		// Step 1: Handle error redaction FIRST, before computing
		// safeResult. When the error contains a secret, replace
		// args.Result with a structured safe error message so the
		// model never sees the raw error text. The previous
		// implementation computed safeResult before redacting the
		// error, then returned the old safeResult.
		errorRedacted := g.redactErrorIfNeeded(args)

		// Step 2: Redact and limit the result (which may have been
		// replaced in step 1).
		safeResult, resultChanged, resultTruncated, resultSize := g.redactAndLimitTracked(args.Result)

		// Step 3: Redact Meta in place.
		metaRedacted := g.redactMetaIfNeeded(args)

		// Track host/workspace session lifecycle.
		g.trackSessionLifecycle(args.ToolName, safeResult)

		execution := "ok"
		if args.Error != nil {
			execution = "error"
		}
		redacted := resultChanged || errorRedacted || metaRedacted
		truncated := resultTruncated

		// Build the post_execute audit event.
		ev := g.postExecuteEvent(args, redacted, truncated)
		_ = g.maybeAuditPostExecute(ev, resultSize, truncated, execution)

		if !resultChanged && !errorRedacted && !metaRedacted && !resultTruncated {
			return nil, nil
		}
		return &tool.AfterToolResult{CustomResult: safeResult}, nil
	})
}

// trackSessionLifecycle registers or clears session state based on the
// tool name and the result. When exec_command or workspace_exec returns
// a session_id, the session is registered. When kill_session or
// workspace_kill_session completes, the session is marked killed.
func (g *Guard) trackSessionLifecycle(toolName string, result any) {
	if g == nil || g.sessions == nil {
		return
	}
	sessionID := extractSessionID(result)
	if sessionID == "" {
		return
	}
	switch toolName {
	case "exec_command", "workspace_exec":
		g.sessions.register(sessionID)
	case "kill_session", "workspace_kill_session":
		g.sessions.kill(sessionID)
	}
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
	return ""
}

// redactAndLimitTracked is redactAndLimit with explicit changed/truncated
// tracking so the audit event records the real redaction state. When
// truncation happens without redaction, changed is also set true so the
// callback returns the truncated value instead of the original.
func (g *Guard) redactAndLimitTracked(value any) (safe any, changed bool, truncated bool, size int64) {
	if !g.redaction {
		out, trunc, sz := limitResultBytes(value, g.scanner.policy.MaxOutputSize)
		return out, trunc, trunc, sz
	}
	redacted, c, err := redactValue(value)
	if err != nil {
		safeMarker := map[string]any{
			"status": "redacted",
			"reason": "tool result contained a secret and could not be safely returned",
		}
		out, trunc, sz := limitResultBytes(safeMarker, g.scanner.policy.MaxOutputSize)
		return out, true, trunc, sz
	}
	out, trunc, sz := limitResultBytes(redacted, g.scanner.policy.MaxOutputSize)
	// changed is true when redaction changed the value OR when
	// truncation changed the value, so the callback returns the safe
	// (truncated) value instead of the original.
	changed = c || trunc
	return out, changed, trunc, sz
}

// redactErrorIfNeeded returns true when args.Error contained a secret and
// was replaced. The redacted error text is stored in the guard's
// trackedError field so the callback can return it as a CustomResult.
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
	changed := false
	for k, v := range args.Meta {
		safe, c, err := redactValue(v)
		if err != nil || !c {
			continue
		}
		args.Meta[k] = safe
		changed = true
	}
	return changed
}

// postExecuteEvent builds a post_execute ScanEvent that reuses the
// preflight scan event when available. The guard stashes the preflight
// event in a side table keyed by (tool name, tool call id) so the after
// callback can correlate the two phases. When no preflight event is
// found (e.g. the guard was attached to a callbacks pipeline that did
// not go through CheckToolPermission), a minimal standalone event is
// produced.
func (g *Guard) postExecuteEvent(args *tool.AfterToolArgs, redacted, truncated bool) ScanEvent {
	sessionHash := g.postExecuteSessionHash(args)
	if pre := g.popScanEvent(args.ToolCallID); pre.ScanID != "" {
		pre.Redacted = redacted
		if pre.SessionHash == "" {
			pre.SessionHash = sessionHash
		}
		return pre
	}
	return ScanEvent{
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
// writer when the guard owns it. It is safe to call Close after every
// scan has completed; calling Close while a scan is in progress may
// discard pending audit writes.
func (g *Guard) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scanEvents = nil
	g.releases = nil
	if g.audit != nil && g.closeAudit {
		err := g.audit.Close()
		g.audit = nil
		return err
	}
	return nil
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
