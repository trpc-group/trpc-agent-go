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
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionPolicy_MapsNeedsHumanReviewToAsk(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "custom",
				Backend:        BackendUnknown,
				Decision:       DecisionNeedsHumanReview,
				RiskLevel:      RiskHigh,
				RuleID:         "unknown.requires_review",
				Recommendation: "review unknown tool",
				Blocked:        true,
			}, nil
		}),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom",
		Arguments: []byte(`{"text":"download"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "decision=needs_human_review")
	require.Equal(t, DecisionNeedsHumanReview, observed.Decision)
}

func TestPermissionPolicy_UnsupportedBackendResolverFailsClosed(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		MustDefaultScanner(Policy{}),
		WithBackendResolver(func(*tool.PermissionRequest) Backend {
			return Backend("HOST")
		}),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"python -i","background":true,"tty":true}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, BackendUnknown, observed.Backend)
	require.Equal(t, DecisionDeny, observed.Decision)
	require.Equal(t, "backend.unsupported", observed.RuleID)
}

func TestPermissionPolicy_AuditBestEffortKeepsDenyDecision(t *testing.T) {
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspace,
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "command.dangerous_delete",
				Recommendation: "avoid deletion",
				Blocked:        true,
			}, nil
		}),
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /tmp/x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
}

func TestPermissionPolicy_AuditStrictFailsAllowDecision(t *testing.T) {
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspace,
				Decision:       DecisionAllow,
				RiskLevel:      RiskLow,
				Recommendation: "safe",
			}, nil
		}),
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
		WithAuditFailureMode(AuditFailureModeStrict),
	)
	_, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.ErrorContains(t, err, "disk full")
}

func TestPermissionPolicy_InheritsAuditFailureModeFromDefaultScanner(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		AuditFailureMode: AuditFailureModeStrict,
	})
	policy := NewPermissionPolicy(
		scanner,
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
	)
	_, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.ErrorContains(t, err, "disk full")
}

func TestPermissionPolicy_ExplicitAuditFailureModeOverridesScannerPolicy(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		AuditFailureMode: AuditFailureModeStrict,
	})
	policy := NewPermissionPolicy(
		scanner,
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
		WithAuditFailureMode(AuditFailureModeBestEffort),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestPermissionPolicy_InvalidAuditFailureModeKeepsInheritedStrict(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		AuditFailureMode: AuditFailureModeStrict,
	})
	policy := NewPermissionPolicy(
		scanner,
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
		WithAuditFailureMode(AuditFailureMode("strict ")),
	)
	_, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	require.ErrorContains(t, err, "disk full")
}

func TestPermissionPolicy_NormalizesCustomReportBlocked(t *testing.T) {
	var observed Report
	var audit bytes.Buffer
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "custom",
				Backend:        BackendUnknown,
				Decision:       DecisionDeny,
				RiskLevel:      RiskHigh,
				RuleID:         "custom.deny",
				Recommendation: "do not execute",
				Blocked:        false,
			}, nil
		}),
		WithAuditWriter(NewJSONLAuditWriter(&audit)),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom",
		Arguments: []byte(`{"value":"ok"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.True(t, observed.Blocked)
	require.Contains(t, audit.String(), `"blocked":true`)
}

func TestPermissionPolicy_CodeExecInvalidArgumentsAsk(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		MustDefaultScanner(Policy{}),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":"not-json"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Equal(t, DecisionAsk, observed.Decision)
	require.Equal(t, "tool.arguments_invalid", observed.RuleID)
}

func TestPermissionPolicy_ScannerErrorStillAuditsAndObserves(t *testing.T) {
	var observed Report
	var audit bytes.Buffer
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{}, errors.New("scanner unavailable")
		}),
		WithAuditWriter(NewJSONLAuditWriter(&audit)),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, "scanner.error", observed.RuleID)
	require.Contains(t, audit.String(), `"rule_id":"scanner.error"`)
}

func TestPermissionPolicy_ZeroValueScannerDecisionFailsClosed(t *testing.T) {
	var observed Report
	var audit bytes.Buffer
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{}, nil
		}),
		WithAuditWriter(NewJSONLAuditWriter(&audit)),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, DecisionDeny, observed.Decision)
	require.Equal(t, "scanner.invalid_decision", observed.RuleID)
	require.Equal(t, RiskHigh, observed.RiskLevel)
	require.True(t, observed.Blocked)
	require.Contains(t, observed.Evidence, `scanner returned invalid decision ""`)
	require.Contains(t, audit.String(), `"rule_id":"scanner.invalid_decision"`)
	require.Contains(t, audit.String(), `"decision":"deny"`)
}

func TestPermissionPolicy_UnsupportedScannerDecisionFailsClosed(t *testing.T) {
	var observed Report
	var audit bytes.Buffer
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{Decision: Decision("unexpected")}, nil
		}),
		WithAuditWriter(NewJSONLAuditWriter(&audit)),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, DecisionDeny, observed.Decision)
	require.Equal(t, "scanner.invalid_decision", observed.RuleID)
	require.Equal(t, RiskHigh, observed.RiskLevel)
	require.True(t, observed.Blocked)
	require.Contains(t, observed.Evidence, `scanner returned invalid decision "unexpected"`)
	require.Contains(t, audit.String(), `"rule_id":"scanner.invalid_decision"`)
	require.Contains(t, audit.String(), `"decision":"deny"`)
}

func TestPermissionPolicy_InvalidLaterScannerDecisionPreservesEarlierStricterReport(t *testing.T) {
	var observed Report
	var audit bytes.Buffer
	var scans int
	policy := NewPermissionPolicy(
		ScannerFunc(func(_ context.Context, req ScanRequest) (Report, error) {
			scans++
			switch scans {
			case 1:
				return Report{
					ToolName:       req.ToolName,
					ToolCallID:     req.ToolCallID,
					Backend:        req.Backend,
					Decision:       DecisionDeny,
					RiskLevel:      RiskCritical,
					RuleID:         "first.critical_deny",
					Recommendation: "block the first code block",
					Blocked:        true,
				}, nil
			case 2:
				return Report{}, nil
			default:
				t.Fatalf("unexpected extra scan %d", scans)
				return Report{}, nil
			}
		}),
		WithAuditWriter(NewJSONLAuditWriter(&audit)),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "execute_code",
		Arguments: []byte(`{"code_blocks":[` +
			`{"language":"python","code":"print('first')" },` +
			`{"language":"python","code":"print('second')"}` +
			`]}`),
	})
	require.NoError(t, err)
	require.Equal(t, 2, scans)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, DecisionDeny, observed.Decision)
	require.Equal(t, "first.critical_deny", observed.RuleID)
	require.Equal(t, RiskCritical, observed.RiskLevel)
	require.True(t, observed.Blocked)
	require.Contains(t, decision.Reason, "rule=first.critical_deny")
	require.Contains(t, decision.Reason, "recommendation=block the first code block")
	require.Contains(t, audit.String(), `"rule_id":"first.critical_deny"`)
	require.Contains(t, audit.String(), `"decision":"deny"`)
	require.NotContains(t, audit.String(), `"rule_id":"scanner.invalid_decision"`)
	require.NotContains(t, decision.Reason, "scanner.invalid_decision")
}

func TestPermissionPolicy_UsesBackendResolverAndNilFallbacks(t *testing.T) {
	allowPolicy := NewPermissionPolicy(nil)
	decision, err := allowPolicy.CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
	decision, err = allowPolicy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)

	var observed Report
	policy := NewPermissionPolicy(
		ScannerFunc(func(_ context.Context, req ScanRequest) (Report, error) {
			return Report{
				ToolName:       req.ToolName,
				Backend:        req.Backend,
				Decision:       DecisionAsk,
				RiskLevel:      RiskMedium,
				RuleID:         "test.ask",
				Recommendation: "review",
				Blocked:        true,
			}, nil
		}),
		WithBackendResolver(func(*tool.PermissionRequest) Backend {
			return BackendSandbox
		}),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err = policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom",
		Arguments: []byte(`{"x":1}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Equal(t, BackendSandbox, observed.Backend)
}

func TestPermissionHelpers(t *testing.T) {
	require.Equal(t, tool.PermissionActionAllow, permissionDecisionForReport(Report{Decision: DecisionAllow}, nil).Action)
	require.Equal(t, tool.PermissionActionDeny, permissionDecisionForReport(Report{Decision: DecisionDeny}, nil).Action)
	require.Equal(t, tool.PermissionActionAsk, permissionDecisionForReport(Report{Decision: DecisionNeedsHumanReview}, nil).Action)
	require.Equal(t, tool.PermissionActionDeny, permissionDecisionForReport(Report{}, nil).Action)
	require.Equal(t, tool.PermissionActionDeny, permissionDecisionForReport(Report{Decision: Decision("unexpected")}, nil).Action)
	require.Equal(t, string(tool.PermissionActionDeny), permissionAction(DecisionDeny))
	require.Equal(t, string(tool.PermissionActionAsk), permissionAction(DecisionNeedsHumanReview))
	require.Equal(t, string(tool.PermissionActionAllow), permissionAction(DecisionAllow))
	require.Equal(t, BackendUnknown, defaultBackendResolver(nil))
	require.Empty(t, PermissionReason(Report{Decision: DecisionAllow}))
	require.NotEmpty(t, PermissionReason(Report{
		Decision:       DecisionAsk,
		RiskLevel:      RiskMedium,
		RuleID:         "x",
		Backend:        BackendHost,
		Recommendation: "token=abc123\nretry",
	}))
	report := Report{
		Decision:       DecisionAsk,
		RiskLevel:      RiskHigh,
		RuleID:         "x",
		Backend:        BackendHost,
		Recommendation: "review /etc/passwd and token=abc123",
	}
	require.NotContains(t, PermissionReason(report), "/etc/passwd")
	require.Contains(t, PermissionReason(report), "<redacted>")
	require.NotContains(
		t,
		permissionDecisionForReport(report, []string{"/etc/passwd"}).Reason,
		"/etc/passwd",
	)
}

func TestJSONLAuditWriter_NilCancelledAndShortWrite(t *testing.T) {
	require.NoError(t, (*JSONLAuditWriter)(nil).WriteAuditEvent(context.Background(), AuditEvent{}))
	require.NoError(t, NewJSONLAuditWriter(nil).WriteAuditEvent(context.Background(), AuditEvent{}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, NewJSONLAuditWriter(&bytes.Buffer{}).WriteAuditEvent(ctx, AuditEvent{}), context.Canceled)

	err := NewJSONLAuditWriter(shortWriter{}).WriteAuditEvent(context.Background(), AuditEvent{})
	require.Error(t, err)
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}

func TestPermissionPolicy_AuditStrictKeepsBlockedDecision(t *testing.T) {
	var observed Report
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspace,
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "command.dangerous_delete",
				Recommendation: "avoid deletion",
				Blocked:        true,
			}, nil
		}),
		WithAuditWriter(failingAuditWriter{err: errors.New("disk full")}),
		WithAuditFailureMode(AuditFailureModeStrict),
		WithReportObserver(func(_ context.Context, report Report) {
			observed = report
		}),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /tmp/x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, "disk full", observed.AuditError)
}

func TestJSONLAuditWriter_WritesRedactedEvent(t *testing.T) {
	var buf bytes.Buffer
	writer := NewJSONLAuditWriter(&buf)
	err := writer.WriteAuditEvent(context.Background(), AuditEvent{
		ToolName:         "workspace_exec",
		Backend:          BackendWorkspace,
		Decision:         DecisionDeny,
		PermissionAction: "deny",
		RiskLevel:        RiskCritical,
		RuleID:           "path.secret_file",
		Blocked:          true,
		Redacted:         true,
	})
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"tool_name":"workspace_exec"`)
	require.Contains(t, buf.String(), `"redacted":true`)
}

func TestPermissionPolicy_AuditWriterRedactsScannerRecommendation(t *testing.T) {
	var audit bytes.Buffer
	sensitivePath := "/etc/passwd"
	recommendation := "remove password=hunter2 from " +
		sensitivePath + " before retry"
	policy := NewPermissionPolicy(
		ScannerFunc(func(context.Context, ScanRequest) (Report, error) {
			return Report{
				ToolName:       "custom",
				Backend:        BackendUnknown,
				Decision:       DecisionAsk,
				RiskLevel:      RiskHigh,
				RuleID:         "custom.sensitive",
				Recommendation: recommendation,
				Blocked:        true,
			}, nil
		}),
		WithAuditWriter(NewJSONLAuditWriter(&audit)),
	)
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom",
		Arguments: []byte(`{"text":"download"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.NotContains(t, audit.String(), "hunter2")
	require.NotContains(t, audit.String(), sensitivePath)
	require.NotContains(t, decision.Reason, sensitivePath)
	require.Contains(t, audit.String(), `"recommendation":"remove \u003credacted\u003e from \u003credacted\u003e before retry"`)
	require.Contains(t, audit.String(), `"redacted":true`)
}

func TestAuditEventFromReport_DefaultScannerExplicitEmptyDeniedPathsStayEmpty(t *testing.T) {
	scanner := MustDefaultScanner(Policy{
		DisableDefaultDenies: true,
		DeniedPaths:          []string{},
	})
	event := auditEventFromReport(Report{
		ToolName:       "workspace_exec",
		Backend:        BackendWorkspace,
		Decision:       DecisionAsk,
		RiskLevel:      RiskHigh,
		Recommendation: "remove password=hunter2 from /etc/passwd before retry",
		Blocked:        true,
	}, auditDeniedPathsForScanner(scanner))
	require.NotContains(t, event.Recommendation, "hunter2")
	require.Contains(t, event.Recommendation, "/etc/passwd")
	require.True(t, event.Redacted)
	require.Empty(t, auditDeniedPathsForScanner(scanner))
}

func TestPermissionPolicy_AuditRedactionDoesNotMutateSharedDeniedPaths(t *testing.T) {
	policy := NewPermissionPolicy(
		MustDefaultScanner(Policy{
			DeniedPaths: []string{"secret", "/etc/passwd", "/very/long/sensitive/path"},
		}),
		WithAuditWriter(NewJSONLAuditWriter(&bytes.Buffer{})),
	)
	p, ok := policy.(*permissionPolicy)
	require.True(t, ok)
	require.Equal(t, []string{"secret", "/etc/passwd", "/very/long/sensitive/path"}, p.auditDeniedPaths)

	const workers = 16
	type workerResult struct {
		decision tool.PermissionDecision
		err      error
	}

	var wg sync.WaitGroup
	results := make(chan workerResult, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName:  "workspace_exec",
				Arguments: []byte(`{"command":"echo ok"}`),
			})
			results <- workerResult{
				decision: decision,
				err:      err,
			}
		}()
	}
	wg.Wait()
	close(results)

	for result := range results {
		require.NoError(t, result.err)
		require.Equal(t, tool.PermissionActionAllow, result.decision.Action)
	}

	require.Equal(t, []string{"secret", "/etc/passwd", "/very/long/sensitive/path"}, p.auditDeniedPaths)
}
