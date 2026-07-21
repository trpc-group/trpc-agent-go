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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCovercore_SessionTrackerLifecycle exercises register, kill, clear,
// and the empty-id guards of the session tracker.
func TestCovercore_SessionTrackerLifecycle(t *testing.T) {
	tr := newSessionTracker()

	// Empty ids are ignored by every mutator and accessor.
	tr.register("")
	tr.kill("")
	tr.clear("")
	require.False(t, tr.isKnown(""))
	require.False(t, tr.isKilled(""))

	require.False(t, tr.isKnown("sess-1"))
	require.False(t, tr.isKilled("sess-1"))

	tr.register("sess-1")
	require.True(t, tr.isKnown("sess-1"))
	require.False(t, tr.isKilled("sess-1"))

	tr.kill("sess-1")
	require.True(t, tr.isKilled("sess-1"))

	tr.clear("sess-1")
	require.False(t, tr.isKnown("sess-1"))
	require.False(t, tr.isKilled("sess-1"))
}

// TestCovercore_OtelKeyHelpers verifies the int-typed attribute builders
// that safetyAttributes does not normally emit.
func TestCovercore_OtelKeyHelpers(t *testing.T) {
	kv := keyInt("k", 7)
	require.Equal(t, "k", string(kv.Key))
	require.Equal(t, int64(7), kv.Value.AsInt64())

	kv64 := keyInt64("k64", 1<<40)
	require.Equal(t, "k64", string(kv64.Key))
	require.Equal(t, int64(1<<40), kv64.Value.AsInt64())
}

// TestCovercore_SeverityHelpersUnknownValues covers the zero-rank fall
// through of the severity rankers.
func TestCovercore_SeverityHelpersUnknownValues(t *testing.T) {
	require.Equal(t, 0, ruleSeverity(RiskLevel("bogus")))
	require.Equal(t, 0, decisionSeverity(Decision("bogus")))
	require.Equal(t, 4, ruleSeverity(RiskCritical))
	require.Equal(t, 3, decisionSeverity(DecisionDeny))
}

// TestCovercore_ParseURL covers the error branches of parseURL.
func TestCovercore_ParseURL(t *testing.T) {
	// Malformed URL (control character) returns the parse error.
	_, _, _, err := parseURL("http://exa mple.com/\x7f")
	require.Error(t, err)

	// Valid URL with no host is rejected.
	_, _, scheme, err := parseURL("mailto:user@example.com")
	require.Error(t, err)
	require.Equal(t, "mailto", scheme)

	// IP-literal hosts are ambiguous for a domain allowlist.
	_, host, _, err := parseURL("http://127.0.0.1:8080/x")
	require.Error(t, err)
	require.Equal(t, "127.0.0.1", host)

	// A normal https URL parses with lowercased host and scheme.
	u, host, scheme, err := parseURL("HTTPS://ExAmple.COM/Path")
	require.NoError(t, err)
	require.NotNil(t, u)
	require.Equal(t, "example.com", host)
	require.Equal(t, "https", scheme)
}

// TestCovercore_IsAmbiguousHost enumerates the ambiguous-host classes.
func TestCovercore_IsAmbiguousHost(t *testing.T) {
	require.True(t, isAmbiguousHost(""))
	require.True(t, isAmbiguousHost("10.0.0.1"))
	require.True(t, isAmbiguousHost("::1"))
	require.True(t, isAmbiguousHost("localhost"))
	require.True(t, isAmbiguousHost("metadata.google.internal"))
	require.True(t, isAmbiguousHost("*.example.com"))
	require.True(t, isAmbiguousHost("exämple.com"))
	require.False(t, isAmbiguousHost("example.com"))
}

// TestCovercore_HostAllowedByList covers exact, wildcard, and rejection
// shapes of the domain allowlist matcher.
func TestCovercore_HostAllowedByList(t *testing.T) {
	allow := []string{"example.com", "*.wild.example", "  "}

	require.False(t, hostAllowedByList("", allow))
	require.True(t, hostAllowedByList("example.com", allow))
	require.True(t, hostAllowedByList(" Example.COM ", allow))
	require.False(t, hostAllowedByList("notexample.com", allow))

	require.True(t, hostAllowedByList("sub.wild.example", allow))
	require.True(t, hostAllowedByList("a.b.wild.example", allow))
	// The wildcard does not match the bare apex.
	require.False(t, hostAllowedByList("wild.example", allow))
	require.False(t, hostAllowedByList("evilwild.example", allow))
}

// TestCovercore_BasenameLower covers the empty-name early return.
func TestCovercore_BasenameLower(t *testing.T) {
	require.Equal(t, "", basenameLower(""))
	require.Equal(t, "rm", basenameLower("/usr/bin/RM"))
	require.Equal(t, "bash", basenameLower("bash"))
}

// TestCovercore_SleepSeconds covers the parsing branches of sleepSeconds.
func TestCovercore_SleepSeconds(t *testing.T) {
	require.Equal(t, int64(-1), sleepSeconds([]string{"sleep"}))
	require.Equal(t, int64(-1), sleepSeconds([]string{"sleep", "  "}))
	require.Equal(t, int64(5), sleepSeconds([]string{"sleep", "5"}))
	require.Equal(t, int64(0), sleepSeconds([]string{"sleep", "0.5"}))
	require.Equal(t, int64(12), sleepSeconds([]string{"sleep", "12.9"}))
	require.Equal(t, int64(-1), sleepSeconds([]string{"sleep", "abc"}))
	require.Equal(t, int64(-1), sleepSeconds([]string{"sleep", ".5"}))

	// Unbounded sleep targets map to a very large sentinel.
	for _, s := range []string{"infinity", "INF", "forever"} {
		require.Equal(t, int64(1<<62-1), sleepSeconds([]string{"sleep", s}), "s=%s", s)
	}
	// An overflowing literal is treated as unbounded, not wrapped.
	require.Equal(t, int64(1<<62-1), sleepSeconds([]string{"sleep", "99999999999999999999999"}))
}

// TestCovercore_IsInstallCommand covers the install-command families the
// default corpus does not reach.
func TestCovercore_IsInstallCommand(t *testing.T) {
	require.True(t, isInstallCommand("python", []string{"python", "-m", "pip", "install", "requests"}))
	require.True(t, isInstallCommand("python3", []string{"python3", "-m", "pip", "install", "requests"}))
	require.False(t, isInstallCommand("python", []string{"python", "-m", "pytest"}))
	require.True(t, isInstallCommand("apt-get", []string{"apt-get", "install", "curl"}))
	require.True(t, isInstallCommand("brew", []string{"brew", "install", "wget"}))
	require.True(t, isInstallCommand("cargo", []string{"cargo", "install", "ripgrep"}))
	require.False(t, isInstallCommand("apt", []string{"apt", "update"}))
}

// TestCovercore_IsOutputBomb covers the remaining output-bomb branches.
func TestCovercore_IsOutputBomb(t *testing.T) {
	require.True(t, isOutputBomb("yes", []string{"yes"}))
	require.False(t, isOutputBomb("seq", []string{"seq", "1000000"}))
	require.True(t, isOutputBomb("dd", []string{"dd", "if=/dev/zero", "of=/tmp/x"}))
	require.False(t, isOutputBomb("dd", []string{"dd", "if=/dev/zero", "of=/tmp/x", "count=1"}))
	require.True(t, isOutputBomb("tail", []string{"tail", "-f", "/var/log/syslog"}))
	require.True(t, isOutputBomb("tail", []string{"tail", "--follow", "/var/log/syslog"}))
	require.False(t, isOutputBomb("tail", []string{"tail", "-n", "5", "/var/log/syslog"}))
	require.True(t, isOutputBomb("tcpdump", []string{"tcpdump", "-i", "eth0"}))
	require.True(t, isOutputBomb("tshark", []string{"tshark"}))
	require.False(t, isOutputBomb("grep", []string{"grep", "x", "f"}))
}

// TestCovercore_HasFlagSubcommand covers the flag-followed-by-subcommand
// matcher.
func TestCovercore_HasFlagSubcommand(t *testing.T) {
	require.True(t, hasFlagSubcommand([]string{"python", "-m", "pip"}, "-m", "pip"))
	require.False(t, hasFlagSubcommand([]string{"python", "-m"}, "-m", "pip"))
	require.False(t, hasFlagSubcommand([]string{"pip", "-m", "python"}, "-m", "pip"))
	require.False(t, hasFlagSubcommand(nil, "-m", "pip"))
}

// TestCovercore_ParseDecimalInt covers the error returns and the custom
// error types.
func TestCovercore_ParseDecimalInt(t *testing.T) {
	var n int64

	consumed, err := parseDecimalInt("", &n)
	require.Error(t, err)
	require.Equal(t, 0, consumed)
	require.Equal(t, "empty number", errEmptyNumber.Error())

	consumed, err = parseDecimalInt("12a", &n)
	require.Error(t, err)
	require.Equal(t, 2, consumed)
	require.Equal(t, "non-digit byte in number", errNonDigit{c: 'a'}.Error())

	consumed, err = parseDecimalInt(" 42 ", &n)
	require.NoError(t, err)
	require.Equal(t, 2, consumed)
	require.Equal(t, int64(42), n)

	_, err = parseDecimalInt("99999999999999999999999", &n)
	require.Error(t, err)
}

// TestCovercore_CleanStringListAllBlank covers the nil-result branch.
func TestCovercore_CleanStringListAllBlank(t *testing.T) {
	require.Nil(t, cleanStringList(nil))
	require.Nil(t, cleanStringList([]string{"", "   ", "\t"}))
}

// TestCovercore_ProfileRegisterEmptyName covers the no-op register branch.
func TestCovercore_ProfileRegisterEmptyName(t *testing.T) {
	reg := newProfileRegistry()
	before := len(reg)
	reg.register(ToolProfile{Name: ""})
	require.Len(t, reg, before)
}

// TestCovercore_ConcurrencyLimiterNil covers the nil-receiver paths.
func TestCovercore_ConcurrencyLimiterNil(t *testing.T) {
	var c *concurrencyLimiter
	release, err := c.acquire(context.Background(), "tool")
	require.NoError(t, err)
	require.NotNil(t, release)
	release() // must be a safe no-op
	require.Equal(t, int64(0), c.activeCount())
}

// TestCovercore_ConcurrencyLimiterPerToolRollbackWithGlobal verifies that
// a per-tool rejection rolls back the global increment when a global cap
// is configured.
func TestCovercore_ConcurrencyLimiterPerToolRollbackWithGlobal(t *testing.T) {
	c := newConcurrencyLimiter(ConcurrencyPolicy{
		MaxActiveCalls: 5,
		PerToolLimits:  map[string]int{"tool": 1},
	})
	release, err := c.acquire(context.Background(), "tool")
	require.NoError(t, err)
	defer release()
	require.Equal(t, int64(1), c.activeCount())

	_, err = c.acquire(context.Background(), "tool")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool")
	// The failed acquire must roll the global counter back to 1.
	require.Equal(t, int64(1), c.activeCount())

	// A different tool is unaffected by the per-tool cap.
	release2, err := c.acquire(context.Background(), "other")
	require.NoError(t, err)
	release2()
}

// TestCovercore_ConcurrencyLimiterReleaseIdempotent verifies release runs
// its body once even when called twice.
func TestCovercore_ConcurrencyLimiterReleaseIdempotent(t *testing.T) {
	c := newConcurrencyLimiter(ConcurrencyPolicy{MaxActiveCalls: 2})
	release, err := c.acquire(context.Background(), "tool")
	require.NoError(t, err)
	release()
	release()
	require.Equal(t, int64(0), c.activeCount())
}

// TestCovercore_TelemetryNilContextAndIntAttributes covers the nil-context
// guard and the int/int64 attribute branches of setSpanAttribute.
func TestCovercore_TelemetryNilContextAndIntAttributes(t *testing.T) {
	require.NotPanics(t, func() {
		telemetryProject(nil, []SpanAttribute{{Key: "k", Value: "v"}})
	})

	ctx, span := newRecordingSpan()
	telemetryProject(ctx, []SpanAttribute{
		{Key: "int", Value: 3},
		{Key: "int64", Value: int64(4)},
		{Key: "skipped", Value: struct{}{}},
	})
	attrs := span.attributes()
	require.Equal(t, int64(3), attrs["int"])
	require.Equal(t, int64(4), attrs["int64"])
	require.NotContains(t, attrs, "skipped")
}

// TestCovercore_ScannerNilReceiverAndOptions covers the nil-scanner error
// and the scanner option setters.
func TestCovercore_ScannerNilReceiverAndOptions(t *testing.T) {
	var s *Scanner
	_, err := s.Scan(context.Background(), ScanInput{ToolName: "t"})
	require.Error(t, err)

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s = NewScanner(DefaultPolicy(),
		WithScannerClock(func() time.Time { return fixed }),
		WithScannerProfile(ToolProfile{
			Name:    "custom_tool",
			Backend: BackendMCP,
		}),
	)
	report, err := s.Scan(context.Background(), ScanInput{ToolName: "custom_tool"})
	require.NoError(t, err)
	require.Equal(t, fixed, report.Timestamp)

	// A nil clock option leaves the default clock in place.
	s2 := NewScanner(DefaultPolicy(), WithScannerClock(nil))
	report2, err := s2.Scan(context.Background(), ScanInput{ToolName: "workspace_exec", Command: "ls"})
	require.NoError(t, err)
	require.False(t, report2.Timestamp.IsZero())
}

// TestCovercore_ScanFillsBackendFromProfile covers the backend backfill
// when the caller populated ToolProfile but not Backend.
func TestCovercore_ScanFillsBackendFromProfile(t *testing.T) {
	s := NewScanner(DefaultPolicy())
	report, err := s.Scan(context.Background(), ScanInput{
		ToolName:    "runner",
		ToolProfile: "workspace_exec",
		Command:     "ls",
	})
	require.NoError(t, err)
	require.Equal(t, BackendWorkspaceExec, report.Backend)
}

// TestCovercore_SortFindingsTieBreakOnEvidence covers the final evidence
// comparison in the sort ordering.
func TestCovercore_SortFindingsTieBreakOnEvidence(t *testing.T) {
	findings := []Finding{
		{RuleID: "r", RiskLevel: RiskLow, Evidence: "b"},
		{RuleID: "r", RiskLevel: RiskLow, Evidence: "a"},
		{RuleID: "a", RiskLevel: RiskHigh, Evidence: "z"},
	}
	sortFindings(findings)
	require.Equal(t, []Finding{
		{RuleID: "a", RiskLevel: RiskHigh, Evidence: "z"},
		{RuleID: "r", RiskLevel: RiskLow, Evidence: "a"},
		{RuleID: "r", RiskLevel: RiskLow, Evidence: "b"},
	}, findings)
}

// TestCovercore_AnyRedactedEvidenceMarker covers the marker-based redacted
// detection branch.
func TestCovercore_AnyRedactedEvidenceMarker(t *testing.T) {
	require.True(t, anyRedacted([]Finding{{RuleID: "x", Evidence: "tok=[REDACTED:jwt]"}}))
	require.True(t, anyRedacted([]Finding{{RuleID: "secret.input_or_code"}}))
	require.False(t, anyRedacted([]Finding{{RuleID: "x", Evidence: "clean"}}))
	require.False(t, anyRedacted(nil))
}

// TestCovercore_ScanSafeInputAllows verifies a benign command produces an
// allow report with a non-nil findings list.
func TestCovercore_ScanSafeInputAllows(t *testing.T) {
	s := NewScanner(DefaultPolicy())
	report, err := s.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "ls -la",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)
	require.NotNil(t, report.Findings)
}

// TestCovercore_WithScannerProfileNilRegistry covers the lazy registry
// initialization in WithScannerProfile.
func TestCovercore_WithScannerProfileNilRegistry(t *testing.T) {
	var s Scanner
	WithScannerProfile(ToolProfile{Name: "custom", Backend: BackendMCP})(&s)
	p, ok := s.profiles.lookup("custom")
	require.True(t, ok)
	require.Equal(t, BackendMCP, p.Backend)
}

// TestCovercore_FirstStringNoMatch covers the fall-through return when no
// candidate key carries a string value.
func TestCovercore_FirstStringNoMatch(t *testing.T) {
	require.Empty(t, firstString(map[string]any{"other": "x"}, "session_id", "sessionId"))
	require.Empty(t, firstString(map[string]any{"session_id": 42}, "session_id", "sessionId"))
}
