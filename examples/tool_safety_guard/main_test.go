//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		if current.Name == "host long session" {
			require.Equal(t, safety.DecisionDeny, report.Decision)
			require.Equal(t, "resource.timeout", report.RuleID)
			for _, rule := range []string{
				"resource.timeout", "host.background", "host.tty",
			} {
				require.True(t, reportHasFinding(report, rule), rule)
			}
		}
	}
}

func TestGenerateMatchesNormalizedGoldenFixtures(t *testing.T) {
	dir := canonicalTempDir(t)
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
	target := filepath.Join(canonicalTempDir(t), "target")
	require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o600))
	link := filepath.Join(canonicalTempDir(t), "report.json")
	require.NoError(t, os.Symlink(target, link))
	err := generate("tool_safety_policy.yaml", "samples.json", link, filepath.Join(canonicalTempDir(t), "audit.jsonl"))
	require.Error(t, err)
	require.Equal(t, "unchanged", readFile(t, target))

	realParent := canonicalTempDir(t)
	linkedParent := filepath.Join(canonicalTempDir(t), "linked")
	require.NoError(t, os.Symlink(realParent, linkedParent))
	err = generate(
		"tool_safety_policy.yaml", "samples.json",
		filepath.Join(linkedParent, "report.json"),
		filepath.Join(canonicalTempDir(t), "audit.jsonl"),
	)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(realParent, "report.json"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestStrictInputsFailWithoutLeakingSecretValues(t *testing.T) {
	dir := canonicalTempDir(t)
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
		"empty name":                     `[{"name":"","expected_decision":"allow","request":{"tool_name":"workspace_exec","backend":"workspaceexec","command":"go test ./..."}}]`,
		"bad decision":                   `[{"name":"bad","expected_decision":"maybe","request":{"tool_name":"workspace_exec","backend":"workspaceexec","command":"go test ./..."}}]`,
		"bad backend":                    `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"workspace_exec","backend":"other","command":"go test ./..."}}]`,
		"unusable request":               `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"workspace_exec","backend":"workspaceexec"}}]`,
		"empty code blocks":              `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"code_exec","backend":"codeexec","code_blocks":[]}}]`,
		"empty code blocks with command": `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"code_exec","backend":"codeexec","command":"go test ./...","code_blocks":[]}}]`,
		"empty code block":               `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"code_exec","backend":"codeexec","code_blocks":[{"code":"","language":""}]}}]`,
		"blank code":                     `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"code_exec","backend":"codeexec","code_blocks":[{"code":"  ","language":"go"}]}}]`,
		"blank language":                 `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"code_exec","backend":"codeexec","code_blocks":[{"code":"package main","language":"  "}]}}]`,
		"blank argv zero":                `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"workspace_exec","backend":"workspaceexec","args":["  ","test"]}}]`,
		"raw null":                       `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"mcp_exec","backend":"unknown","raw_arguments":null}}]`,
		"raw empty object":               `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"mcp_exec","backend":"unknown","raw_arguments":{}}}]`,
		"raw empty array":                `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"mcp_exec","backend":"unknown","raw_arguments":[]}}]`,
		"raw blank string":               `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"mcp_exec","backend":"unknown","raw_arguments":"  "}}]`,
		"raw unscannable object":         `[{"name":"bad","expected_decision":"allow","request":{"tool_name":"mcp_exec","backend":"unknown","raw_arguments":{"note":"private-key-value"}}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(canonicalTempDir(t), "samples.json")
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			_, err := loadSamples(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-key-value")
		})
	}
}

func TestSampleValidationPreservesOpenWorldExecutionInputs(t *testing.T) {
	for name, raw := range map[string]string{
		"nested command":   `{"payload":{"command":"go test ./tool/safety"}}`,
		"nested code":      `{"payload":{"code":"package main","language":"go"}}`,
		"encoded command":  `"rm -rf /"`,
		"network endpoint": `{"endpoint":"https://api.github.com"}`,
	} {
		t.Run(name, func(t *testing.T) {
			contents := `[{"name":"valid","expected_decision":"allow","request":{"tool_name":"mcp_exec","backend":"unknown","raw_arguments":` + raw + `}}]`
			path := filepath.Join(canonicalTempDir(t), "samples.json")
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			_, err := loadSamples(path)
			require.NoError(t, err)
		})
	}
}

func TestGeneratorRejectsOutputAliasesBeforeWriting(t *testing.T) {
	dir := canonicalTempDir(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o700))
	absolute := filepath.Join(dir, "output.json")
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	relative, err := filepath.Rel(workingDirectory, absolute)
	require.NoError(t, err)

	for name, paths := range map[string][2]string{
		"relative and absolute": {relative, absolute},
		"cleaned aliases": {
			filepath.Join(dir, "nested", "..", "output.json"), absolute,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := generate(
				"tool_safety_policy.yaml", "samples.json", paths[0], paths[1],
			)
			require.Error(t, err)
			_, statErr := os.Stat(absolute)
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestGeneratorRejectsExistingHardLinkAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard-link permissions vary on Windows")
	}
	dir := canonicalTempDir(t)
	reportPath := filepath.Join(dir, "report.json")
	auditPath := filepath.Join(dir, "audit.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte("unchanged"), 0o600))
	require.NoError(t, os.Link(reportPath, auditPath))

	err := generate(
		"tool_safety_policy.yaml", "samples.json", reportPath, auditPath,
	)
	require.Error(t, err)
	require.Equal(t, "unchanged", readFile(t, reportPath))
	require.Equal(t, "unchanged", readFile(t, auditPath))
}

func TestGeneratorCanonicalizesMacOSTempParentAliases(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS has the controlled /tmp alias covered by this test")
	}
	dir, err := os.MkdirTemp("/tmp", "tool-safety-alias-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	reportPath := filepath.Join(dir, "output.json")
	auditPath := filepath.Join("/private/tmp", filepath.Base(dir), "output.json")

	err = generate(
		"tool_safety_policy.yaml", "samples.json", reportPath, auditPath,
	)
	require.Error(t, err)
	_, statErr := os.Stat(reportPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestGeneratorRejectsNestedAncestorSymlinkWithoutWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	realParent := canonicalTempDir(t)
	require.NoError(t, os.Mkdir(filepath.Join(realParent, "nested"), 0o700))
	target := filepath.Join(realParent, "nested", "report.json")
	require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o600))
	outer := canonicalTempDir(t)
	link := filepath.Join(outer, "alias")
	require.NoError(t, os.Symlink(realParent, link))

	err := generate(
		"tool_safety_policy.yaml", "samples.json",
		filepath.Join(link, "nested", "report.json"),
		filepath.Join(canonicalTempDir(t), "audit.jsonl"),
	)
	require.Error(t, err)
	require.Equal(t, "unchanged", readFile(t, target))
}

func TestCLIRejectsMalformedSampleWithoutLeakingSecret(t *testing.T) {
	dir := canonicalTempDir(t)
	secret := "private-key-value"
	samplesPath := filepath.Join(dir, "samples.json")
	require.NoError(t, os.WriteFile(
		samplesPath,
		[]byte(`[{"name":"bad","expected_decision":"deny","request":{"tool_name":"workspace_exec","backend":"workspaceexec","command":"`+secret+`"},"unknown":true}]`),
		0o600,
	))
	command := exec.Command(
		os.Args[0], "-test.run=TestCLIHelperProcess", "--",
		"-policy", "tool_safety_policy.yaml", "-samples", samplesPath,
		"-report", filepath.Join(dir, "report.json"),
		"-audit", filepath.Join(dir, "audit.jsonl"),
	)
	command.Env = append(os.Environ(), "GO_WANT_TOOL_SAFETY_HELPER=1")
	output, err := command.CombinedOutput()
	require.Error(t, err)
	require.NotContains(t, string(output), secret)
	_, statErr := os.Stat(filepath.Join(dir, "report.json"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TOOL_SAFETY_HELPER") != "1" {
		return
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index + 1
			break
		}
	}
	os.Args = append([]string{"tool_safety_guard"}, os.Args[separator:]...)
	main()
}

func TestREADMEEnforcesDecisionBeforeExecution(t *testing.T) {
	readme := readFile(t, "README.md")
	switchAt := strings.Index(readme, "switch preflight.Decision")
	executeAt := strings.Index(readme, "rawResult, executionErr := execute(ctx)")
	require.GreaterOrEqual(t, switchAt, 0)
	require.Greater(t, executeAt, switchAt)
	for _, decisionCase := range []string{
		"case safety.DecisionAllow:",
		"case safety.DecisionDeny:",
		"case safety.DecisionAsk, safety.DecisionNeedsHumanReview:",
	} {
		require.Contains(t, readme, decisionCase)
	}
	require.Contains(t, readme, "guard == nil || processor == nil || execute == nil")
	require.Contains(t, readme, "authorize == nil")
}

func TestDocumentationUsesDurableExternalExampleLinks(t *testing.T) {
	for _, path := range []string{
		"../../docs/mkdocs/en/tool-safety.md",
		"../../docs/mkdocs/zh/tool-safety.md",
	} {
		contents := readFile(t, path)
		require.NotContains(t, contents, "../../../examples/")
		require.Contains(
			t, contents,
			"https://github.com/trpc-group/trpc-agent-go/tree/main/examples/tool_safety_guard",
		)
	}
}

func TestGoFilesUseCurrentCopyrightHeader(t *testing.T) {
	header := "//\n" +
		"// Tencent is pleased to support the open source community by making trpc-agent-go available.\n" +
		"//\n// Copyright (C) 2026 Tencent.  All rights reserved.\n" +
		"//\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n"
	for _, path := range []string{"main.go", "main_test.go"} {
		require.True(t, strings.HasPrefix(readFile(t, path), header), path)
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

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return resolved
}

func reportHasFinding(report safety.Report, ruleID string) bool {
	for _, finding := range report.Findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
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
