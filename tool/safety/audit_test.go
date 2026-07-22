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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestJSONLAuditSinkConcurrentWritesAreCompleteAndRedacted(t *testing.T) {
	const (
		writers         = 16
		eventsPerWriter = 25
		secret          = "audit-secret-value"
	)
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	var group sync.WaitGroup

	for writer := 0; writer < writers; writer++ {
		group.Add(1)
		go func(writer int) {
			defer group.Done()
			for index := 0; index < eventsPerWriter; index++ {
				err := sink.WriteAudit(context.Background(), AuditEvent{
					ToolName:       "workspace_exec",
					ToolCallID:     fmt.Sprintf("call-%d-%d", writer, index),
					Backend:        BackendWorkspace,
					Decision:       tool.PermissionActionDeny,
					RiskLevel:      RiskLevelCritical,
					RuleID:         "credential.read",
					Evidence:       "password=" + secret,
					Recommendation: "remove --token " + secret,
					Blocked:        true,
					DurationMS:     2,
					CommandPreview: "tool --token " + secret,
				})
				require.NoError(t, err)
			}
		}(writer)
	}
	group.Wait()

	raw := output.String()
	require.NotContains(t, raw, secret)
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	require.Len(t, lines, writers*eventsPerWriter)
	for _, line := range lines {
		require.True(t, json.Valid([]byte(line)), line)
		var event AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		require.Equal(t, auditSchemaVersion, event.SchemaVersion)
		require.False(t, event.Timestamp.IsZero())
		require.True(t, event.Blocked)
		require.True(t, event.Redacted)
		require.Positive(t, event.RedactionCount)
	}
}

func TestJSONLAuditSinkRejectsNilWriter(t *testing.T) {
	err := NewJSONLAuditSink(nil).WriteAudit(context.Background(), AuditEvent{})
	require.ErrorContains(t, err, "writer is nil")
}

func TestJSONLAuditSinkReturnsWriterFailure(t *testing.T) {
	wantErr := errors.New("disk unavailable")
	sink := NewJSONLAuditSink(failingWriter{err: wantErr})
	err := sink.WriteAudit(context.Background(), AuditEvent{ToolName: "workspace_exec"})
	require.ErrorIs(t, err, wantErr)
}

func TestSanitizeReportRedactsEveryHumanReadableFindingField(t *testing.T) {
	const secret = "report-secret-value"
	report := Report{
		Decision:       tool.PermissionActionDeny,
		RiskLevel:      RiskLevelCritical,
		RuleID:         "credential.read",
		ToolName:       "workspace_exec",
		Command:        "run --token " + secret,
		Evidence:       "password=" + secret,
		Recommendation: "remove api_key=" + secret,
		Matches: []Match{{
			Decision:       tool.PermissionActionDeny,
			RiskLevel:      RiskLevelCritical,
			RuleID:         "credential.read",
			Evidence:       "Bearer " + secret,
			Recommendation: "rotate token=" + secret,
		}},
	}

	clean := sanitizeReport(NewRedactor(), report)
	encoded, err := json.Marshal(clean)

	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
	require.True(t, clean.Redacted)
	require.Contains(t, string(encoded), RedactedValue)
}

func TestWriteGuardAuditEmitsHashAndSanitizedPreview(t *testing.T) {
	const secret = "guard-audit-secret"
	request := Request{
		ToolName:   "workspace_exec",
		ToolCallID: "call-1",
		Backend:    BackendWorkspace,
		Command:    "go test ./... --token " + secret,
	}
	report := sanitizeReport(NewRedactor(), Report{
		Decision:   tool.PermissionActionAsk,
		RiskLevel:  RiskLevelMedium,
		RuleID:     "command.review",
		Evidence:   "token=" + secret,
		ToolName:   request.ToolName,
		Command:    request.Command,
		Backend:    request.Backend,
		Blocked:    true,
		DurationMS: 7,
		Matches: []Match{
			{RuleID: "command.review"},
			{RuleID: "secret.output"},
			{RuleID: "command.review"},
		},
	})
	var got AuditEvent
	sink := AuditSinkFunc(func(_ context.Context, event AuditEvent) error {
		got = event
		return nil
	})

	err := writeGuardAudit(context.Background(), sink, NewRedactor(), request, report, "test-policy")
	encoded, marshalErr := json.Marshal(got)

	require.NoError(t, err)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), secret)
	require.Equal(t, hashRequestPayload(request), got.RequestSHA256)
	require.Equal(t, []string{"command.review", "secret.output"}, got.RuleIDs)
	require.Equal(t, "test-policy", got.PolicyID)
	require.Equal(t, int64(7), got.DurationMS)
	require.True(t, got.Redacted)
}

func TestWriteGuardAuditHashesSessionAndOversizedPayloads(t *testing.T) {
	captureHash := func(request Request) string {
		var got AuditEvent
		err := writeGuardAudit(
			context.Background(),
			AuditSinkFunc(func(_ context.Context, event AuditEvent) error {
				got = event
				return nil
			}),
			NewRedactor(),
			request,
			Report{
				ToolName:  request.ToolName,
				Backend:   request.Backend,
				Decision:  tool.PermissionActionAsk,
				RiskLevel: RiskLevelHigh,
				RuleID:    "test.review",
				Blocked:   true,
			},
			"test-policy",
		)
		require.NoError(t, err)
		return got.RequestSHA256
	}

	sessionA := Request{
		ToolName:     "skill_write_stdin",
		Backend:      BackendSkill,
		SessionInput: "first session input",
	}
	sessionB := sessionA
	sessionB.SessionInput = "second session input"
	require.NotEmpty(t, captureHash(sessionA))
	require.NotEqual(t, captureHash(sessionA), captureHash(sessionB))

	largeA := Request{
		ToolName: "execute_code",
		Backend:  BackendCode,
		Script:   strings.Repeat("a", maxReportPayloadBytes+1),
	}
	largeB := largeA
	largeB.Script = strings.Repeat("b", maxReportPayloadBytes+1)
	require.Equal(t, omittedReportPayload, requestPayload(largeA))
	require.Equal(t, omittedReportPayload, requestPayload(largeB))
	require.NotEqual(t, captureHash(largeA), captureHash(largeB))
}

func TestGuardOmitsOverlongFieldsBeforeRedaction(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\n" +
		strings.Repeat("LEAKME", maxSafetyTextRunes) +
		"\n-----END PRIVATE KEY-----"
	var got AuditEvent
	guard, err := New(
		testPolicy(),
		WithRule(RuleFunc(func(context.Context, Request, Policy) []Match {
			return []Match{{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelCritical,
				RuleID:         "organization.long_evidence",
				Evidence:       privateKey,
				Recommendation: privateKey,
			}}
		})),
		WithAuditSink(AuditSinkFunc(func(_ context.Context, event AuditEvent) error {
			got = event
			return nil
		})),
	)
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	reportJSON, err := json.Marshal(report)
	require.NoError(t, err)
	auditJSON, err := json.Marshal(got)
	require.NoError(t, err)
	for _, encoded := range [][]byte{reportJSON, auditJSON} {
		require.NotContains(t, string(encoded), "BEGIN PRIVATE KEY")
		require.NotContains(t, string(encoded), "LEAKME")
	}
	require.Equal(t, omittedSafetyText, report.Evidence)
	require.Equal(t, omittedSafetyText, got.Evidence)
	require.True(t, report.Redacted)
	require.True(t, got.Redacted)
}

func TestJSONLAuditSinkNormalizesTimestampToUTC(t *testing.T) {
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	location := time.FixedZone("test", 8*60*60)
	want := time.Date(2026, 7, 16, 12, 30, 0, 0, location).UTC()

	require.NoError(t, sink.WriteAudit(context.Background(), AuditEvent{
		Timestamp: time.Date(2026, 7, 16, 12, 30, 0, 0, location),
	}))

	var got AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &got))
	require.Equal(t, want, got.Timestamp)
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
func TestGuardSanitizesCustomAuditSinkWithConfiguredRedactor(t *testing.T) {
	const (
		secret        = "tenant-specific-secret"
		builtinSecret = "builtin-password-secret"
	)
	var got AuditEvent
	guard, err := New(
		testPolicy(),
		WithRedactor(literalRedactor{secret: secret}),
		WithAuditSink(AuditSinkFunc(func(_ context.Context, event AuditEvent) error {
			got = event
			return nil
		})),
	)
	require.NoError(t, err)

	request := commandRequest(
		BackendWorkspace,
		"echo "+secret+" password="+builtinSecret,
	)
	request.ToolCallID = "call-" + secret
	report, err := guard.Scan(context.Background(), request)

	require.NoError(t, err)
	require.NotContains(t, report.Command, secret)
	require.NotContains(t, report.Command, builtinSecret)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
	require.NotContains(t, string(encoded), builtinSecret)
	require.Contains(t, got.ToolCallID, RedactedValue)
	require.Contains(t, got.CommandPreview, RedactedValue)
	require.True(t, got.Redacted)
	require.Positive(t, got.RedactionCount)
}

type literalRedactor struct {
	secret string
}

func (r literalRedactor) RedactString(value string) (string, int) {
	count := strings.Count(value, r.secret)
	return strings.ReplaceAll(value, r.secret, RedactedValue), count
}

func (r literalRedactor) RedactBytes(value []byte) ([]byte, int) {
	redacted, count := r.RedactString(string(value))
	return []byte(redacted), count
}

func (r literalRedactor) RedactValue(value any) (any, int) {
	switch typed := value.(type) {
	case string:
		return r.RedactString(typed)
	case []byte:
		return r.RedactBytes(typed)
	default:
		return value, 0
	}
}
func TestAuditSanitizationDoesNotMutateSharedEvent(t *testing.T) {
	const (
		writers = 8
		secret  = "shared-rule-secret"
	)
	ruleIDs := []string{"token=" + secret}
	event := AuditEvent{
		PolicyID:  "policy",
		ToolName:  "workspace_exec",
		Backend:   BackendWorkspace,
		Decision:  tool.PermissionActionDeny,
		RiskLevel: RiskLevelHigh,
		RuleID:    "rule",
		RuleIDs:   ruleIDs,
		Blocked:   true,
	}
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	errorsSeen := make(chan error, writers)
	var group sync.WaitGroup
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsSeen <- sink.WriteAudit(context.Background(), event)
		}()
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		require.NoError(t, err)
	}

	require.Equal(t, "token="+secret, ruleIDs[0])
	require.NotContains(t, output.String(), secret)
	require.Len(t, strings.Split(strings.TrimSpace(output.String()), "\n"), writers)
}

func TestAuditHelpersHandleNilAndShortWriters(t *testing.T) {
	var sink AuditSinkFunc
	require.ErrorContains(
		t,
		sink.WriteAudit(context.Background(), AuditEvent{}),
		"audit sink function is nil",
	)
	require.ErrorIs(t, writeAll(noProgressWriter{}, []byte("value")), io.ErrShortWrite)
	require.ErrorIs(t, writeAll(overreportingWriter{}, []byte("value")), io.ErrShortWrite)
	require.NotEmpty(t, hashRequestPayload(Request{}))
	require.Equal(t, "unchanged", truncateRunes("unchanged", 0))
	require.Equal(t, "??", truncateRunes("????", 2))
}

type noProgressWriter struct{}

func (noProgressWriter) Write([]byte) (int, error) {
	return 0, nil
}

type overreportingWriter struct{}

func (overreportingWriter) Write(value []byte) (int, error) {
	return len(value) + 1, nil
}

type nilAuditSink struct{}

func (*nilAuditSink) WriteAudit(context.Context, AuditEvent) error {
	panic("typed-nil audit sink must not be invoked")
}

func TestTypedNilAuditSinkTriggersFailurePolicyAndHook(t *testing.T) {
	policy := testPolicy()
	policy.Actions.AuditFailure = tool.PermissionActionDeny
	var sink *nilAuditSink
	var hookErr error
	guard, err := New(
		policy,
		WithAuditSink(sink),
		WithAuditErrorHook(func(err error) { hookErr = err }),
	)
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "audit.failure"))
	require.ErrorContains(t, hookErr, "audit sink is nil")
}

type panicNilRedactor struct{}

func (*panicNilRedactor) RedactString(string) (string, int) {
	panic("typed-nil redactor must not be invoked")
}

func (*panicNilRedactor) RedactBytes([]byte) ([]byte, int) {
	panic("typed-nil redactor must not be invoked")
}

func (*panicNilRedactor) RedactValue(any) (any, int) {
	panic("typed-nil redactor must not be invoked")
}

func TestWithTypedNilRedactorKeepsBuiltInRedaction(t *testing.T) {
	const secret = "typed-nil-redactor-secret"
	var custom *panicNilRedactor
	guard, err := New(testPolicy(), WithRedactor(custom))
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "echo password="+secret),
	)

	require.NoError(t, err)
	require.NotContains(t, report.Command, secret)
	require.Contains(t, report.Command, RedactedValue)
}

func TestNilAuditSinkFuncTriggersFailurePolicyAndHook(t *testing.T) {
	policy := testPolicy()
	policy.Actions.AuditFailure = tool.PermissionActionAsk
	var sink AuditSinkFunc
	var hookErr error
	guard, err := New(
		policy,
		WithAuditSink(sink),
		WithAuditErrorHook(func(err error) { hookErr = err }),
	)
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "audit.failure"))
	require.ErrorContains(t, hookErr, "audit sink is nil")
}

type panickingAuditSink struct{}

func (panickingAuditSink) WriteAudit(context.Context, AuditEvent) error {
	panic("sink failure")
}

func TestPanickingAuditSinkTriggersFailurePolicyAndHook(t *testing.T) {
	policy := testPolicy()
	policy.Actions.AuditFailure = tool.PermissionActionDeny
	var hookErr error
	guard, err := New(
		policy,
		WithAuditSink(panickingAuditSink{}),
		WithAuditErrorHook(func(err error) { hookErr = err }),
	)
	require.NoError(t, err)

	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "audit.failure"))
	require.ErrorContains(t, hookErr, "audit sink panicked")
}

func TestHashRequestPayloadIncludesOutputDeclarations(t *testing.T) {
	base := Request{
		ToolName: "skill_run",
		Backend:  BackendSkill,
		Command:  "go test ./tool/safety",
	}
	withOutputFileA := base
	withOutputFileA.OutputFiles = []string{"reports/a.json"}
	withOutputFileB := base
	withOutputFileB.OutputFiles = []string{"reports/b.json"}
	require.NotEqual(
		t,
		hashRequestPayload(withOutputFileA),
		hashRequestPayload(withOutputFileB),
	)

	withOutputsA := base
	withOutputsA.Outputs = &OutputSpec{
		Globs:         []string{"reports/**"},
		MaxFiles:      2,
		MaxFileBytes:  1024,
		MaxTotalBytes: 2048,
		Save:          true,
		NameTemplate:  "report-{name}",
	}
	withOutputsB := withOutputsA
	outputsB := *withOutputsA.Outputs
	outputsB.MaxTotalBytes = 4096
	withOutputsB.Outputs = &outputsB
	require.NotEqual(
		t,
		hashRequestPayload(withOutputsA),
		hashRequestPayload(withOutputsB),
	)

	withArtifactOptions := base
	withArtifactOptions.SaveArtifacts = true
	withArtifactOptions.OmitInline = true
	withArtifactOptions.ArtifactPrefix = "review-output"
	require.NotEqual(
		t,
		hashRequestPayload(base),
		hashRequestPayload(withArtifactOptions),
	)
}

func TestHashRequestPayloadIncludesExecutionContext(t *testing.T) {
	base := Request{
		Command:        "go test ./tool/safety",
		CWD:            "work",
		Env:            map[string]string{"CI": "true"},
		TimeoutMS:      1000,
		MaxOutputBytes: 4096,
	}

	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "tool name", mutate: func(request *Request) {
			request.ToolName = "workspace_exec"
		}},
		{name: "backend", mutate: func(request *Request) {
			request.Backend = BackendWorkspace
		}},
		{name: "cwd", mutate: func(request *Request) {
			request.CWD = "other"
		}},
		{name: "environment value", mutate: func(request *Request) {
			request.Env["CI"] = "false"
		}},
		{name: "timeout", mutate: func(request *Request) {
			request.TimeoutMS = 2000
		}},
		{name: "output limit", mutate: func(request *Request) {
			request.MaxOutputBytes = 8192
		}},
		{name: "background", mutate: func(request *Request) {
			request.Background = true
		}},
		{name: "tty", mutate: func(request *Request) {
			request.TTY = true
		}},
		{name: "skill", mutate: func(request *Request) {
			request.Skill = "code-review"
		}},
		{name: "execution identifier", mutate: func(request *Request) {
			request.ExecutionID = "sandbox-7"
		}},
		{name: "session identifier", mutate: func(request *Request) {
			request.SessionID = "session-1"
		}},
		{name: "yield milliseconds", mutate: func(request *Request) {
			request.YieldMS = intPointer(25)
		}},
		{name: "poll lines", mutate: func(request *Request) {
			request.PollLines = 50
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneRequest(base)
			test.mutate(&changed)
			require.NotEqual(
				t,
				hashRequestPayload(base),
				hashRequestPayload(changed),
			)
		})
	}

	explicitZeroYield := cloneRequest(base)
	explicitZeroYield.YieldMS = intPointer(0)
	require.NotEqual(
		t, hashRequestPayload(base), hashRequestPayload(explicitZeroYield),
	)

	reordered := cloneRequest(base)
	reordered.Env = make(map[string]string)
	reordered.Env["SECOND"] = "2"
	reordered.Env["CI"] = "true"
	ordered := cloneRequest(base)
	ordered.Env = map[string]string{"CI": "true", "SECOND": "2"}
	require.Equal(
		t,
		hashRequestPayload(ordered),
		hashRequestPayload(reordered),
	)

	equivalentTimeout := cloneRequest(base)
	equivalentTimeout.TimeoutMS = 0
	equivalentTimeout.Timeout = time.Second
	require.Equal(
		t,
		hashRequestPayload(base),
		hashRequestPayload(equivalentTimeout),
	)
}

func TestHashRequestPayloadPreservesStructuredEntryOrder(t *testing.T) {
	base := Request{Command: "go test ./tool/safety"}
	first := cloneRequest(base)
	first.Inputs = []InputSpec{
		{},
		{From: "artifact://input.txt"},
	}
	second := cloneRequest(base)
	second.Inputs = []InputSpec{
		{From: "artifact://input.txt"},
		{},
	}
	require.NotEqual(
		t,
		hashRequestPayload(first),
		hashRequestPayload(second),
	)

	first = cloneRequest(base)
	first.OutputFiles = []string{"", "out/a.txt"}
	second = cloneRequest(base)
	second.OutputFiles = []string{"out/a.txt", ""}
	require.NotEqual(
		t,
		hashRequestPayload(first),
		hashRequestPayload(second),
	)

	first = cloneRequest(base)
	first.CodeBlocks = []CodeBlock{
		{},
		{Language: "go", Code: "package main"},
	}
	second = cloneRequest(base)
	second.CodeBlocks = []CodeBlock{
		{Language: "go", Code: "package main"},
		{},
	}
	require.NotEqual(
		t,
		hashRequestPayload(first),
		hashRequestPayload(second),
	)
}

func TestJSONLAuditSinkBoundsDirectRuleIDInput(t *testing.T) {
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	ruleIDs := make([]string, maxReportMatches+50)
	for index := range ruleIDs {
		ruleIDs[index] = fmt.Sprintf("custom.rule.%d", index)
	}
	err := sink.WriteAudit(context.Background(), AuditEvent{
		ToolName: "custom", Backend: BackendWorkspace, RuleIDs: ruleIDs,
	})
	require.NoError(t, err)
	var event AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event))
	require.Len(t, event.RuleIDs, maxReportMatches)
	require.Equal(t, "limits.rule_ids", event.RuleIDs[len(event.RuleIDs)-1])
	require.True(t, event.Redacted)
}

func TestJSONLAuditSinkNormalizesUntrustedEnumAndDigestFields(t *testing.T) {
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	err := sink.WriteAudit(context.Background(), AuditEvent{
		SchemaVersion: "token=schema-secret",
		Decision:      tool.PermissionAction("password=decision-secret"),
		RiskLevel:     RiskLevel("token=risk-secret"),
		RequestSHA256: "token=digest-secret",
		DurationMS:    -1,
	})
	require.NoError(t, err)
	encoded := output.String()
	for _, secret := range []string{
		"schema-secret", "decision-secret", "risk-secret", "digest-secret",
	} {
		require.NotContains(t, encoded, secret)
	}
	var event AuditEvent
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event))
	require.Equal(t, auditSchemaVersion, event.SchemaVersion)
	require.Equal(t, tool.PermissionActionAsk, event.Decision)
	require.Equal(t, RiskLevelHigh, event.RiskLevel)
	require.Empty(t, event.RequestSHA256)
	require.Zero(t, event.DurationMS)
	require.True(t, event.Blocked)
	require.True(t, event.Redacted)
}
