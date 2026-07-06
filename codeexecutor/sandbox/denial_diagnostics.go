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
	Raw        string
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
	Denials []Denial
}

// DenialFilterScope selects which diagnostic outputs a filter rule applies to.
type DenialFilterScope string

const (
	// DenialFilterDenials applies only to Diagnostics.Denials.
	DenialFilterDenials DenialFilterScope = "denials"
	// DenialFilterAll applies to all diagnostic outputs.
	DenialFilterAll DenialFilterScope = "all"
)

// DenialTargetMatcher matches denial targets using structured fields.
type DenialTargetMatcher struct {
	Exact  string
	Prefix string
	Suffix string
	Glob   string
}

// DenialIgnoreRule ignores matching sandbox denials from diagnostic output.
type DenialIgnoreRule struct {
	Scope DenialFilterScope
	// Command, when non-empty, must be a substring of RunProgramSpec.Cmd. It
	// intentionally does not match Args because arguments may contain secrets.
	Command     string
	Operations  []string
	Targets     []DenialTargetMatcher
	RawContains []string
}

// DenialFilter configures user-defined sandbox denial filtering for diagnostic
// output. Automatic noise filtering is backend-specific.
type DenialFilter struct {
	DisableAutomatic bool
	Ignore           []DenialIgnoreRule
}

type diagnosticsKey struct{}

// WithDiagnostics asks RunProgram to collect sandbox diagnostics for this call.
// Without this context value, RunProgram keeps its normal zero-overhead
// execution path. The returned channel receives exactly one Diagnostics value.
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
