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
}

// Denial describes a sandbox denial observed during program execution.
type Denial struct {
	Operation string
	Target    string
	// Raw contains the backend's original diagnostic text. It is intended for
	// human debugging, is not a stable machine-readable format, and may include
	// host paths or process names.
	Raw string
	// Timestamp is the backend-reported event time. It is zero when the backend
	// omits the timestamp or its format is not recognized.
	Timestamp  time.Time
	Source     DenialSource
	Confidence DenialConfidence
}

// DenialSource identifies where a denial diagnostic came from.
type DenialSource string

const (
	// DenialSourceMacOSUnifiedLog reports denials parsed from the macOS unified log.
	DenialSourceMacOSUnifiedLog DenialSource = "macos-unified-log"
)

// DenialConfidence reports the correlation strength for a denial diagnostic.
type DenialConfidence string

const (
	// DenialConfidenceStrong reports a denial strongly correlated to the run.
	DenialConfidenceStrong DenialConfidence = "strong"
)

// Diagnostics captures sandbox-specific diagnostics for one program run.
type Diagnostics struct {
	// Denials contains sandbox denial diagnostics. The macOS backend returns at
	// most one entry for each operation and target pair. When multiple events
	// have the same pair, the first event retained after filtering supplies the
	// entry's Raw, Timestamp, Source, and Confidence fields. This coalescing
	// does not set Truncated.
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
type DenialTargetMatcher struct {
	Exact  string
	Prefix string
	Suffix string
	Glob   string
}

// DenialIgnoreRule ignores matching sandbox denials from diagnostic output.
//
// Matching is conjunctive across configured constraints: a non-empty Command
// must be a substring of RunProgramSpec.Cmd, Operations must contain the denial
// operation when set, Targets must match via any listed matcher when set, and
// RawContains must find at least one non-empty substring in Denial.Raw when set.
// Empty RawContains entries are ignored, and an all-empty list is treated as
// unset. A rule with every effective constraint empty is ignored.
type DenialIgnoreRule struct {
	// Command, when non-empty, must be a substring of RunProgramSpec.Cmd. It
	// intentionally does not match Args because arguments may contain secrets.
	Command     string
	Operations  []string
	Targets     []DenialTargetMatcher
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
// execution path. The returned channel is buffered for one Diagnostics value;
// callers should create a fresh diagnostics context for each RunProgram call.
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
	// can be used to collect sandbox denial events on this host.
	EventStreamAvailable bool
	// StrongCorrelation reports whether collected denials can be strongly tied
	// to the RunProgram call that requested diagnostics.
	StrongCorrelation bool
	// ProbeCompleted reports whether runtime capability probing completed
	// reliably. When false, precision fields should be treated as unknown.
	ProbeCompleted bool
	// ExplicitDenyTaggable reports whether explicit deny rules can carry runTag.
	ExplicitDenyTaggable bool
	// DefaultDenyTaggable reports whether default-deny events can carry runTag.
	DefaultDenyTaggable bool
}

// DiagnosticsCapability reports runtime-detected sandbox denial diagnostic
// precision for the current host environment.
func (r *Runtime) DiagnosticsCapability() DiagnosticsCapability {
	return r.diagnosticsCapabilityForPlatform()
}
