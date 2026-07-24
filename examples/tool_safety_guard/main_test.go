// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/testify/v2/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

var requiredScenarioNames = []string{
	"safe go test",
	"dangerous delete",
	"credential read",
	"denied network egress",
	"allowed network request",
	"shell wrapper bypass",
	"pipeline command",
	"dependency install",
	"long runtime",
	"oversized output",
	"host long session",
	"explicit human review",
}

func TestSamplesCoverRequiredScenariosAndExpectedDecisions(t *testing.T) {
	samples, err := loadSamples("samples.json")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(samples), 12)

	byName := make(map[string]sample, len(samples))
	for _, current := range samples {
		byName[current.Name] = current
	}
	for _, name := range requiredScenarioNames {
		require.Contains(t, byName, name)
	}

	policy, err := safety.LoadPolicy("tool_safety_policy.yaml")
	require.NoError(t, err)
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)
	for _, current := range samples {
		report := guard.Scan(current.Request)
		require.Equal(t, current.ExpectedDecision, report.Decision, current.Name)
		assertCompleteReport(t, report)
	}
}

func TestGenerateMatchesNormalizedGoldenFixtures(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	auditPath := filepath.Join(dir, "audit.jsonl")
	require.NoError(t, generate(
		"tool_safety_policy.yaml", "samples.json", reportPath, auditPath,
	))

	require.Equal(t, readFile(t, "tool_safety_report.json"), normalizeReport(t, reportPath))
	require.Equal(t, readFile(t, "tool_safety_audit.jsonl"), normalizeAudit(t, auditPath))
	require.True(t, strings.HasSuffix(readFile(t, reportPath), "\n"))
	require.True(t, strings.HasSuffix(readFile(t, auditPath), "\n"))

	if runtime.GOOS != "windows" {
		for _, path := range []string{reportPath, auditPath} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		}
	}
}

func TestAuditFixtureCorrelatesAndContainsOnlyContractKeys(t *testing.T) {
	var reports []safety.Report
	require.NoError(t, json.Unmarshal([]byte(readFile(t, "tool_safety_report.json")), &reports))
	lines := strings.Split(strings.TrimSpace(readFile(t, "tool_safety_audit.jsonl")), "\n")
	require.Len(t, lines, len(reports))
	allowed := map[string]bool{
		"schema_version": true, "timestamp": true, "scan_id": true,
		"stage": true, "tool_name": true, "backend": true,
		"decision": true, "risk_level": true, "rule_id": true,
		"duration_ms": true, "redacted": true, "intercepted": true,
		"execution_status": true,
	}
	for index, line := range lines {
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(line), &raw))
		for key := range raw {
			require.True(t, allowed[key], key)
		}
		var event safety.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		require.Equal(t, reports[index].ScanID, event.ScanID)
		require.Equal(t, reports[index].Decision, event.Decision)
		require.Equal(t, reports[index].RuleID, event.RuleID)
		require.Equal(
			t, reports[index].Decision != safety.DecisionAllow,
			event.Intercepted,
		)
	}
	contents := readFile(t, "tool_safety_audit.jsonl")
	for _, forbidden := range []string{
		`"command":`, `"evidence":`, `"env":`, `"result":`,
		"private-key-value",
	} {
		require.NotContains(t, contents, forbidden)
	}
}

func TestGeneratorRejectsSymlinkTargetsWithoutChangingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o600))
	link := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.Symlink(target, link))
	err := generate("tool_safety_policy.yaml", "samples.json", link, filepath.Join(t.TempDir(), "audit.jsonl"))
	require.Error(t, err)
	require.Equal(t, "unchanged", readFile(t, target))

	realParent := t.TempDir()
	linkedParent := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.Symlink(realParent, linkedParent))
	err = generate(
		"tool_safety_policy.yaml", "samples.json",
		filepath.Join(linkedParent, "report.json"),
		filepath.Join(t.TempDir(), "audit.jsonl"),
	)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(realParent, "report.json"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestStrictInputsFailWithoutLeakingSecretValues(t *testing.T) {
	dir := t.TempDir()
	secret := "private-key-value"
	badSamples := filepath.Join(dir, "samples.json")
	require.NoError(t, os.WriteFile(badSamples, []byte(`[{"name":"bad","expected_decision":"deny","request":{"backend":"bogus","command":"`+secret+`"},"unknown":true}]`), 0o600))
	err := generate("tool_safety_policy.yaml", badSamples, filepath.Join(dir, "report"), filepath.Join(dir, "audit"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)

	badPolicy := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(badPolicy, []byte("unknown_field: "+secret+"\n"), 0o600))
	err = generate(badPolicy, "samples.json", filepath.Join(dir, "report2"), filepath.Join(dir, "audit2"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
}

func TestSampleValidationRejectsEmptyAndUnusableRequests(t *testing.T) {
	for name, contents := range map[string]string{
		"empty name":       `[{"name":"","expected_decision":"allow","request":{"command":"go test ./..."}}]`,
		"bad decision":     `[{"name":"bad","expected_decision":"maybe","request":{"command":"go test ./..."}}]`,
		"bad backend":      `[{"name":"bad","expected_decision":"allow","request":{"backend":"other","command":"go test ./..."}}]`,
		"unusable request": `[{"name":"bad","expected_decision":"allow","request":{"backend":"workspaceexec"}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "samples.json")
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			_, err := loadSamples(path)
			require.Error(t, err)
		})
	}
}

func TestBufferedOutputPropagatesWriteFlushAndCloseErrors(t *testing.T) {
	wantWrite := errors.New("write failure")
	err := writeBufferedAndClose(&failingWriteCloser{writeErr: wantWrite}, func(w io.Writer) error {
		_, currentErr := io.WriteString(w, strings.Repeat("x", 8192))
		return currentErr
	})
	require.ErrorIs(t, err, wantWrite)
	err = writeBufferedAndClose(&failingWriteCloser{writeErr: wantWrite}, func(w io.Writer) error {
		_, currentErr := io.WriteString(w, "buffered")
		return currentErr
	})
	require.ErrorIs(t, err, wantWrite)

	wantClose := errors.New("close failure")
	err = writeBufferedAndClose(&failingWriteCloser{closeErr: wantClose}, func(w io.Writer) error {
		_, currentErr := io.WriteString(w, "ok")
		return currentErr
	})
	require.ErrorIs(t, err, wantClose)
}

func TestScanOnlyPathHasNoExecutor(t *testing.T) {
	samples, err := loadSamples("samples.json")
	require.NoError(t, err)
	policy, err := safety.LoadPolicy("tool_safety_policy.yaml")
	require.NoError(t, err)
	var audit bytes.Buffer
	reports, err := scanSamples(samples, policy, safety.NewJSONLAuditSink(&audit), fixedNow)
	require.NoError(t, err)
	require.Len(t, reports, len(samples))
	source := readFile(t, "main.go")
	require.NotContains(t, source, `"os/exec"`)
	require.NotContains(t, source, "exec.Command")
}

func assertCompleteReport(t *testing.T, report safety.Report) {
	t.Helper()
	require.NotEmpty(t, report.ScanID)
	require.NotEmpty(t, report.Decision)
	require.NotEmpty(t, report.RiskLevel)
	require.NotEmpty(t, report.RuleID)
	require.NotEmpty(t, report.Evidence)
	require.NotEmpty(t, report.Recommendation)
	require.NotEmpty(t, report.ToolName)
	require.NotEmpty(t, report.Backend)
}

func normalizeReport(t *testing.T, path string) string {
	t.Helper()
	var reports []safety.Report
	require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &reports))
	for index := range reports {
		reports[index].ScanID = scanID(index)
		reports[index].DurationMillis = 0
	}
	encoded, err := json.MarshalIndent(reports, "", "  ")
	require.NoError(t, err)
	return string(encoded) + "\n"
}

func normalizeAudit(t *testing.T, path string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(readFile(t, path)), "\n")
	var output strings.Builder
	for index, line := range lines {
		var event safety.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		event.ScanID = scanID(index)
		event.Timestamp = fixedNow()
		event.DurationMillis = 0
		encoded, err := json.Marshal(event)
		require.NoError(t, err)
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return output.String()
}

func fixedNow() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func scanID(index int) string {
	return fmt.Sprintf("scan-%d", index+1)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(contents)
}

type failingWriteCloser struct {
	writeErr error
	closeErr error
}

func (w *failingWriteCloser) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}

func (w *failingWriteCloser) Close() error {
	return w.closeErr
}
