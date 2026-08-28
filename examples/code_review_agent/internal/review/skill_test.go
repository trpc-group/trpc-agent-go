//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type skillCheckPayload struct {
	Findings []Finding `json:"findings"`
	Warnings []Finding `json:"warnings"`
}

func TestSkillFilesExist(t *testing.T) {
	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	for _, rel := range []string{
		"scripts/check.sh",
		"scripts/check_rules.py",
		"scripts/check_fallback.go",
	} {
		if _, err := os.Stat(filepath.Join(skillRoot, rel)); err != nil {
			t.Fatalf("expected skill file %s: %v", rel, err)
		}
	}
}

// TestSkillCheckScriptFallsBackToGoWhenPythonUnavailable 固定 Go 回退路径。
func TestSkillCheckScriptFallsBackToGoWhenPythonUnavailable(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	repoRoot := filepath.Dir(filepath.Dir(skillRoot))
	diff, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "fixtures", "secret.diff"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	bashPath := mustLookPath(t, "bash")
	tempBin := t.TempDir()
	linkTool(t, tempBin, "go")
	linkTool(t, tempBin, "mktemp")
	linkTool(t, tempBin, "cat")
	linkTool(t, tempBin, "rm")

	cmd := exec.Command(bashPath, filepath.Join(skillRoot, "scripts", "check.sh"))
	cmd.Stdin = bytes.NewReader(diff)
	cmd.Env = append(os.Environ(),
		"PATH="+tempBin,
		"GOCACHE="+filepath.Join(t.TempDir(), "gocache"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check.sh fallback failed: %v\n%s", err, out)
	}

	var payload struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal check output: %v\n%s", err, out)
	}
	if len(payload.Findings) != 1 || payload.Findings[0].RuleID != "secret-leak" {
		t.Fatalf("expected secret-leak finding from Go fallback, got %+v", payload.Findings)
	}
}

// TestSkillCheckScriptGoFallbackScansPastLongLines ensures an accepted input
// cannot be reported as successfully reviewed after Scanner stops early.
func TestSkillCheckScriptGoFallbackScansPastLongLines(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	diff := "diff --git a/sample.go b/sample.go\n" +
		"--- a/sample.go\n" +
		"+++ b/sample.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+" + strings.Repeat("x", 1024*1024) + "\n" +
		"@@ -0,0 +2 @@\n" +
		"+panic(\"after long line\")\n"

	payload := runSkillCheck(t, skillRoot, diff, fallbackScriptEnv(t))
	if got := countSkillRule(payload.Findings, "panic-direct"); got != 1 {
		t.Fatalf("expected finding after oversized line, got %d: %+v", got, payload.Findings)
	}
}

// TestSkillCheckScriptDetectsSecretShapes 固定 Skill 规则的密钥覆盖面。
func TestSkillCheckScriptDetectsSecretShapes(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	repoRoot := filepath.Dir(filepath.Dir(skillRoot))
	diff, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "fixtures", "secret-shapes.diff"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	cmd := exec.Command(mustLookPath(t, "bash"), filepath.Join(skillRoot, "scripts", "check.sh"))
	cmd.Stdin = bytes.NewReader(diff)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check.sh failed: %v\n%s", err, out)
	}

	var payload struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal check output: %v\n%s", err, out)
	}
	if got := countSkillRule(payload.Findings, "secret-leak"); got != 6 {
		t.Fatalf("expected six high-confidence secret findings, got %d: %+v", got, payload.Findings)
	}
	for _, raw := range []string{
		"sk-proj-1234567890abcdef",
		"llm-live-1234567890abcdef",
		"sk-1234567890abcdef",
		"github_pat_1234567890abcdef1234567890abcdef",
		"abc.def.ghi",
		"plain-password",
		"dummy",
	} {
		if strings.Contains(string(out), raw) {
			t.Fatalf("check.sh output leaked or overreported raw value %q: %s", raw, out)
		}
	}
}

func TestSkillCheckScriptDoesNotTreatFixtureMarkersSpecial(t *testing.T) {
	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatal(err)
	}
	baseline := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -0,0 +1 @@\n+// ordinary comment\n"
	marked := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -0,0 +1 @@\n+// sandbox-timeout fixture sandbox-fail fixture\n"
	for _, env := range [][]string{nil, fallbackScriptEnv(t)} {
		if got, want := runSkillCheck(t, skillRoot, marked, env), runSkillCheck(t, skillRoot, baseline, env); !reflect.DeepEqual(got, want) {
			t.Fatalf("fixture markers changed skill output: got %+v want %+v", got, want)
		}
	}
}

func TestSkillCheckScriptRedactsShortDeclarationSecretsInBothEngines(t *testing.T) {
	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatal(err)
	}
	const secret = "opaque-short-declaration-credential"
	diff := "diff --git a/config.go b/config.go\n" +
		"--- a/config.go\n" +
		"+++ b/config.go\n" +
		"@@ -0,0 +1 @@\n" +
		"+apiKey := \"" + secret + "\"\n"

	for _, env := range [][]string{nil, fallbackScriptEnv(t)} {
		payload := runSkillCheck(t, skillRoot, diff, env)
		if got := countSkillRule(payload.Findings, "secret-leak"); got != 1 {
			t.Fatalf("expected one secret finding, got %d: %+v", got, payload.Findings)
		}
		for _, finding := range payload.Findings {
			if strings.Contains(finding.Evidence, secret) {
				t.Fatalf("skill output leaked short-declaration secret: %+v", finding)
			}
			if finding.RuleID == "secret-leak" && !strings.Contains(finding.Evidence, "apiKey=[REDACTED]") {
				t.Fatalf("secret finding did not preserve redaction marker: %+v", finding)
			}
		}
	}
}

func TestSkillCheckScriptTreatsHeaderLikeHunkContentAsAddedLines(t *testing.T) {
	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatal(err)
	}
	diff := "--- a/notes.md\n" +
		"+++ b/notes.md\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+++ not-a-file-header\n" +
		"+TODO(follow-up): retain line numbers\n"
	for _, env := range [][]string{nil, fallbackScriptEnv(t)} {
		payload := runSkillCheck(t, skillRoot, diff, env)
		if got := countSkillRule(payload.Findings, "todo-marker"); got != 1 {
			t.Fatalf("expected TODO after header-like content, got %d findings: %+v", got, payload.Findings)
		}
		for _, finding := range payload.Findings {
			if finding.RuleID == "todo-marker" && (finding.File != "notes.md" || finding.Line != 2) {
				t.Fatalf("todo finding anchor = %+v, want notes.md:2", finding)
			}
		}
	}
}

func TestSkillCheckScriptReportsMissingTestHintForAnyGoFile(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	diff := "diff --git a/internal/handler.go b/internal/handler.go\n" +
		"--- a/internal/handler.go\n+++ b/internal/handler.go\n" +
		"@@ -0,0 +1,3 @@\n+package internal\n+\n+func Handle() {}\n"
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{name: "python"},
		{name: "go fallback", env: fallbackScriptEnv(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(mustLookPath(t, "bash"), filepath.Join(skillRoot, "scripts", "check.sh"))
			cmd.Stdin = strings.NewReader(diff)
			if tc.env != nil {
				cmd.Env = tc.env
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("check.sh failed: %v\n%s", err, out)
			}
			var payload struct {
				Warnings []Finding `json:"warnings"`
			}
			if err := json.Unmarshal(out, &payload); err != nil {
				t.Fatalf("unmarshal check output: %v\n%s", err, out)
			}
			if got := countSkillRule(payload.Warnings, "missing-test-hint"); got != 1 {
				t.Fatalf("expected missing-test-hint for a non-fixture Go file, got %d: %+v", got, payload.Warnings)
			}
		})
	}
}

func TestSkillRulesKeepDifferentLinesAndHonorFollowingCleanup(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	diff := "diff --git a/sample.go b/sample.go\n--- a/sample.go\n+++ b/sample.go\n@@ -1 +1,12 @@\n package sample\n" +
		"+func sample(ctx context.Context) {\n" +
		"+  _, stop := context.WithCancel(ctx)\n" +
		"+  defer stop()\n" +
		"+  first, _ := os.Open(\"first\")\n" +
		"+  defer first.Close()\n" +
		"+  second, _ := os.Open(\"second\")\n" +
		"+  panic(\"one\")\n" +
		"+  panic(\"two\")\n" +
		"+  exec.Command(\"git\", args...)\n" +
		"+  exec.Command(\"git\", \"branch-\"+args[0])\n" +
		"+}\n"
	cmd := exec.Command(mustLookPath(t, "bash"), filepath.Join(skillRoot, "scripts", "check.sh"))
	cmd.Stdin = strings.NewReader(diff)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check.sh failed: %v\n%s", err, out)
	}
	var payload struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	if got := countSkillRule(payload.Findings, "panic-direct"); got != 2 {
		t.Fatalf("expected two panic findings, got %d: %+v", got, payload.Findings)
	}
	if got := countSkillRule(payload.Findings, "resource-leak"); got != 1 {
		t.Fatalf("expected only second file to leak, got %d: %+v", got, payload.Findings)
	}
	for _, ruleID := range []string{"context-leak", "command-injection"} {
		if got := countSkillRule(payload.Findings, ruleID); got != 0 {
			t.Fatalf("unexpected %s finding: %+v", ruleID, payload.Findings)
		}
	}
}

func TestSkillRulesDecodeQuotedPathsAndSkipGoOnlyChecksForMarkdown(t *testing.T) {
	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatal(err)
	}
	diff := "diff --git \"a/docs/\\346\\261\\211\\t.md\" \"b/docs/\\346\\261\\211\\t.md\"\n" +
		"--- \"a/docs/\\346\\261\\211\\t.md\"\n" +
		"+++ \"b/docs/\\346\\261\\211\\t.md\"\n" +
		"@@ -0,0 +1,3 @@\n" +
		"+TODO(example): document rollout\n" +
		"+const apiKey = \"sk-1234567890abcdef\"\n" +
		"+panic(\"documentation only\")\n"
	for _, env := range [][]string{nil, fallbackScriptEnv(t)} {
		payload := runSkillCheck(t, skillRoot, diff, env)
		if countSkillRule(payload.Findings, "todo-marker") != 1 || countSkillRule(payload.Findings, "secret-leak") != 1 {
			t.Fatalf("expected generic findings, got %+v", payload.Findings)
		}
		if countSkillRule(payload.Findings, "panic-direct") != 0 {
			t.Fatalf("markdown must not trigger Go-only rules: %+v", payload.Findings)
		}
		for _, finding := range payload.Findings {
			if finding.RuleID == "todo-marker" && finding.File != "docs/汉\t.md" {
				t.Fatalf("quoted file path = %q, want decoded path", finding.File)
			}
		}
	}
}

func TestSkillRulesParseTimestampedUnifiedDiffHeaders(t *testing.T) {
	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(skillRoot))
	diff, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "fixtures", "timestamped-unified.diff"))
	if err != nil {
		t.Fatalf("read timestamped unified diff fixture: %v", err)
	}

	for _, env := range [][]string{nil, fallbackScriptEnv(t)} {
		payload := runSkillCheck(t, skillRoot, string(diff), env)
		if got := countSkillRule(payload.Findings, "panic-direct"); got != 1 {
			t.Fatalf("expected Go rule for timestamped header, got %d: %+v", got, payload.Findings)
		}
		for _, finding := range payload.Findings {
			if finding.RuleID == "panic-direct" && (finding.File != "service.go" || finding.Line != 3) {
				t.Fatalf("timestamped finding anchor = %+v, want service.go:3", finding)
			}
		}
	}
}

func TestSkillCheckScriptFallbackParityForDuplicateRuleLines(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	diff := "diff --git a/sample.go b/sample.go\n" +
		"--- a/sample.go\n+++ b/sample.go\n" +
		"@@ -0,0 +1,11 @@\n" +
		"+package sample\n" +
		"+\n" +
		"+func sample(parts []string) {\n" +
		"+  msg := \"\"\n" +
		"+  for _, part := range parts {\n" +
		"+    msg += part\n" +
		"+    msg += \",\"\n" +
		"+  }\n" +
		"+  panic(\"one\")\n" +
		"+  panic(\"two\")\n" +
		"+}\n"

	pythonPayload := runSkillCheck(t, skillRoot, diff, nil)
	fallbackPayload := runSkillCheck(t, skillRoot, diff, fallbackScriptEnv(t))
	assertSkillOutputParity(t, pythonPayload, fallbackPayload)

	if got := countSkillRule(fallbackPayload.Findings, "panic-direct"); got != 2 {
		t.Fatalf("expected two panic findings from fallback, got %d: %+v", got, fallbackPayload.Findings)
	}
	if got := countSkillRule(fallbackPayload.Warnings, "string-concat-loop"); got != 2 {
		t.Fatalf("expected two string-concat-loop warnings from fallback, got %d: %+v", got, fallbackPayload.Warnings)
	}
}

func TestSkillCheckScriptPreservesRepositoryPathsStartingWithSidePrefixes(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	for _, path := range []string{"a/handler.go", "b/handler.go"} {
		diff := "diff --git a/" + path + " b/" + path + "\n" +
			"--- a/" + path + "\n+++ b/" + path + "\n" +
			"@@ -0,0 +1 @@\n+// TODO(example): verify path normalization\n"
		pythonPayload := runSkillCheck(t, skillRoot, diff, nil)
		fallbackPayload := runSkillCheck(t, skillRoot, diff, fallbackScriptEnv(t))
		assertSkillOutputParity(t, pythonPayload, fallbackPayload)
		if len(pythonPayload.Findings) != 1 || pythonPayload.Findings[0].File != path {
			t.Errorf("Skill finding path = %+v, want %q", pythonPayload.Findings, path)
		}
	}
}

func TestSkillCheckScriptFallbackParityForFollowingCleanup(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	diff := "diff --git a/sample.go b/sample.go\n" +
		"--- a/sample.go\n+++ b/sample.go\n" +
		"@@ -0,0 +1,13 @@\n" +
		"+package sample\n" +
		"+\n" +
		"+func sample(ctx context.Context, dsn string) {\n" +
		"+  workerCtx, stop := context.WithCancel(ctx)\n" +
		"+  defer stop()\n" +
		"+  file, _ := os.Open(\"sample\")\n" +
		"+  defer file.Close()\n" +
		"+  conn, _ := sql.Open(\"sqlite\", dsn)\n" +
		"+  defer conn.Close()\n" +
		"+  var wg sync.WaitGroup\n" +
		"+  wg.Add(1)\n" +
		"+  go func() { defer wg.Done(); <-workerCtx.Done() }()\n" +
		"+}\n"

	pythonPayload := runSkillCheck(t, skillRoot, diff, nil)
	fallbackPayload := runSkillCheck(t, skillRoot, diff, fallbackScriptEnv(t))
	assertSkillOutputParity(t, pythonPayload, fallbackPayload)

	for _, ruleID := range []string{"context-leak", "resource-leak", "db-lifecycle", "goroutine-leak"} {
		if got := countSkillRule(fallbackPayload.Findings, ruleID); got != 0 {
			t.Fatalf("expected fallback cleanup parity for %s, got %+v", ruleID, fallbackPayload.Findings)
		}
	}
}

func TestSkillCheckScriptFallbackParityForCommandInjectionBoundaries(t *testing.T) {
	t.Parallel()

	skillRoot, err := SkillRoot()
	if err != nil {
		t.Fatalf("SkillRoot returned error: %v", err)
	}
	diff := "diff --git a/sample.go b/sample.go\n" +
		"--- a/sample.go\n+++ b/sample.go\n" +
		"@@ -0,0 +1,9 @@\n" +
		"+package sample\n" +
		"+\n" +
		"+func sample(ctx context.Context, name, cmd string, args []string) {\n" +
		"+  exec.Command(\"git\", args...)\n" +
		"+  exec.Command(\"git\", \"branch-\"+args[0])\n" +
		"+  exec.Command(\"sh\", \"-c\", name)\n" +
		"+  exec.Command(cmd, \"status\")\n" +
		"+  exec.CommandContext(ctx, cmd, \"status\")\n" +
		"+}\n"

	pythonPayload := runSkillCheck(t, skillRoot, diff, nil)
	fallbackPayload := runSkillCheck(t, skillRoot, diff, fallbackScriptEnv(t))
	assertSkillOutputParity(t, pythonPayload, fallbackPayload)

	if got := countSkillRule(fallbackPayload.Findings, "command-injection"); got != 3 {
		t.Fatalf("expected three command-injection findings from fallback, got %d: %+v", got, fallbackPayload.Findings)
	}
	for _, finding := range fallbackPayload.Findings {
		if finding.RuleID != "command-injection" {
			continue
		}
		if finding.Line == 4 || finding.Line == 5 {
			t.Fatalf("unexpected command-injection finding for fixed executable: %+v", finding)
		}
	}
}

func countSkillRule(findings []Finding, ruleID string) int {
	total := 0
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			total++
		}
	}
	return total
}

// mustLookPath 查找测试工具。
func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not available: %v", name, err)
	}
	return path
}

// linkTool 构造受控 PATH。
func linkTool(t *testing.T, dir string, name string) {
	t.Helper()
	target := mustLookPath(t, name)
	link := filepath.Join(dir, name)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

func fallbackScriptEnv(t *testing.T) []string {
	t.Helper()
	tempBin := t.TempDir()
	for _, name := range []string{"go", "mktemp", "cat", "rm"} {
		linkTool(t, tempBin, name)
	}
	return append(os.Environ(),
		"PATH="+tempBin,
		"GOCACHE="+filepath.Join(t.TempDir(), "gocache"),
	)
}

func runSkillCheck(t *testing.T, skillRoot string, diff string, env []string) skillCheckPayload {
	t.Helper()
	if env == nil {
		mustLookPath(t, "python3")
	}

	cmd := exec.Command(mustLookPath(t, "bash"), filepath.Join(skillRoot, "scripts", "check.sh"))
	cmd.Stdin = strings.NewReader(diff)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check.sh failed: %v\n%s", err, out)
	}

	var payload skillCheckPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal check output: %v\n%s", err, out)
	}
	return payload
}

func assertSkillOutputParity(t *testing.T, want skillCheckPayload, got skillCheckPayload) {
	t.Helper()

	wantFindings := skillFindingSignatures(want.Findings)
	gotFindings := skillFindingSignatures(got.Findings)
	if !equalStringSlices(wantFindings, gotFindings) {
		t.Fatalf("findings parity mismatch\nwant: %v\ngot: %v", wantFindings, gotFindings)
	}

	wantWarnings := skillFindingSignatures(want.Warnings)
	gotWarnings := skillFindingSignatures(got.Warnings)
	if !equalStringSlices(wantWarnings, gotWarnings) {
		t.Fatalf("warnings parity mismatch\nwant: %v\ngot: %v", wantWarnings, gotWarnings)
	}
}

func skillFindingSignatures(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s|%s|%s|%s|%s",
			finding.Severity,
			finding.Category,
			finding.File,
			finding.Line,
			finding.Title,
			finding.Evidence,
			finding.Recommendation,
			finding.Confidence,
			finding.Source,
			finding.RuleID,
			finding.Status,
		))
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
