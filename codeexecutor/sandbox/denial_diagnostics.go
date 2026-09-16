//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"time"
)

const (
	sandboxDenialSettleTimeout = 300 * time.Millisecond
	sandboxDenialProbeTimeout  = 500 * time.Millisecond
)

type sandboxDenialRun struct {
	enabled              bool
	runTag               string
	droppedAtStart       uint64
	defaultDenyTaggable  bool
	explicitDenyTaggable bool
	// collectGeneration is a read-only handle to the denial ring that was
	// active when this run began. On macOS it stores
	// *macosDenialCollectGeneration so collection keeps reading that ring even
	// if the log process later exits and another workspace installs a
	// replacement monitor. It does not own cancel/stop for the underlying
	// monitor process. Other platforms leave it nil.
	collectGeneration any
}

// Denial describes a sandbox denial observed during program execution.
type Denial struct {
	// Operation is a best-effort, backend-native operation string parsed from
	// the diagnostic event. It is not a stable framework vocabulary: values may
	// evolve with the backend, and callers should treat it as display text or a
	// heuristic filter key rather than a durable identifier.
	Operation string
	// Target is a best-effort, backend-native target string parsed from the
	// diagnostic event. It may be a filesystem path, a Mach service name,
	// another backend-specific value, or empty when the backend omits one. Like
	// Operation, it is not a normalized stable identifier.
	Target string
	// Raw contains the backend's original diagnostic text. It is intended for
	// human debugging, is not a stable machine-readable format, and may include
	// host paths or process names.
	Raw string
	// Timestamp is the backend-reported event time. It is zero when the backend
	// omits the timestamp or its format is not recognized.
	Timestamp time.Time
}

// Diagnostics captures sandbox-specific diagnostics for one program run.
type Diagnostics struct {
	// Denials contains sandbox denial diagnostics. The macOS backend returns at
	// most one entry for each operation and target pair. When multiple events
	// have the same pair, the first event retained after filtering supplies the
	// entry's Raw and Timestamp fields. This coalescing does not set Truncated.
	Denials []Denial
	// Truncated reports that the shared denial ring dropped one or more events
	// after this run began and before its collection snapshot. Callers must not
	// assume Denials is a complete record when Truncated is true.
	Truncated bool
}

// DenialTargetMatcher matches denial targets using structured fields.
// Within a single matcher, Exact/Prefix/Suffix/Glob are alternatives: any
// non-empty field that matches the denial target is enough for that matcher to
// hit. Empty fields are ignored. A zero-value matcher never matches.
//
// Glob uses the same doublestar dialect as sandbox filesystem Glob rules
// (for example WithNoAccessGlobs), including ** across path separators.
// Paths are compared with slash-normalized forms. A malformed Glob pattern
// does not match.
type DenialTargetMatcher struct {
	Exact  string
	Prefix string
	Suffix string
	Glob   string
}

// DenialIgnoreRule ignores matching sandbox denials from diagnostic output.
//
// Matching is conjunctive across configured constraints. A rule with every
// effective constraint empty is ignored.
type DenialIgnoreRule struct {
	// CommandContains, when non-empty, must be a substring of
	// RunProgramSpec.Cmd. It intentionally does not match Args because
	// arguments may contain secrets.
	CommandContains string
	// Operations, when non-empty, matches when it contains Denial.Operation
	// exactly. Operation values are backend-native and may evolve.
	Operations []string
	// Targets, when non-empty, matches when any matcher accepts Denial.Target.
	Targets []DenialTargetMatcher
	// RawContains, when effectively non-empty, matches when Denial.Raw contains
	// any non-empty entry. Empty entries are ignored; an all-empty list is
	// treated as unset.
	RawContains []string
}

// DenialFilter configures user-defined sandbox denial filtering for diagnostic
// output. Automatic noise filtering is backend-specific.
//
// Zero-value DenialFilter keeps automatic backend noise filters enabled and
// applies no Ignore rules. Ignore rules are disjunctive: any matching rule
// suppresses the denial. DisableAutomatic skips only the automatic filters;
// Ignore rules still apply.
type DenialFilter struct {
	DisableAutomatic bool
	Ignore           []DenialIgnoreRule
}

type diagnosticsKey struct{}

// WithDiagnostics asks RunProgram to collect sandbox diagnostics for this call.
// Without this context value, RunProgram keeps its normal zero-overhead
// execution path. The returned channel is buffered for one Diagnostics value
// and is never closed. Callers must perform exactly one receive per fresh
// diagnostics context, preferably with their own timeout or cancellation;
// ranging over the channel or waiting for it to close blocks forever.
// Callers should create a fresh diagnostics context for each RunProgram call.
// If the channel is already full, later diagnostics are dropped so RunProgram
// cannot block on diagnostic delivery.
func WithDiagnostics(ctx context.Context) (context.Context, <-chan Diagnostics) {
	ch := make(chan Diagnostics, 1)
	return context.WithValue(ctx, diagnosticsKey{}, ch), ch
}

func diagnosticsChanFromContext(ctx context.Context) chan<- Diagnostics {
	ch, _ := ctx.Value(diagnosticsKey{}).(chan Diagnostics)
	return ch
}

// DiagnosticsCapability reports runtime-detected sandbox denial diagnostic
// precision for the current host environment.
type DiagnosticsCapability struct {
	// Supported reports whether the active backend provides sandbox denial
	// diagnostics at all.
	Supported bool
	// EventStreamAvailable reports whether the backend diagnostic event stream
	// can deliver sandbox denial events on this host. It does not by itself
	// mean a production monitor is running or that denials can be tied to a
	// specific RunProgram call; see StrongCorrelation.
	EventStreamAvailable bool
	// StrongCorrelation reports whether collected denials can be strongly tied
	// to the RunProgram call that requested diagnostics. When false after a
	// completed probe, the runtime does not start production collection.
	StrongCorrelation bool
	// ProbeCompleted reports whether runtime capability probing completed
	// reliably. When false, precision fields should be treated as unknown.
	ProbeCompleted bool
	// ExplicitDenialsCollectible reports whether explicit-deny events can be
	// strongly correlated and collected when the event stream is available.
	// Backends may use different correlation mechanisms.
	ExplicitDenialsCollectible bool
	// DefaultDenialsCollectible reports whether default-deny events can be
	// strongly correlated and collected when the event stream is available.
	// Backends may use different correlation mechanisms.
	DefaultDenialsCollectible bool
}

// DiagnosticsCapability reports runtime-detected sandbox denial diagnostic
// precision for the current host environment.
func (r *Runtime) DiagnosticsCapability() DiagnosticsCapability {
	return r.diagnosticsCapabilityForPlatform()
}
