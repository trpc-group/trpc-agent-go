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
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestQuotedSensitiveValuePattern(t *testing.T) {
	pattern := regexp.MustCompile("^" + quotedSensitiveValuePattern(1) + "$")
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "double quoted", value: `"a"`, want: true},
		{name: "even backslashes allow closing quote", value: `"a\\"`, want: true},
		{name: "odd backslashes escape inner quote", value: `"a\\\"b"`, want: true},
		{name: "single quoted escaped quote", value: `'a\'b'`, want: true},
		{name: "escaped newline text", value: `"a\nb"`, want: true},
		{name: "escaped tab text", value: `"a\tb"`, want: true},
		{name: "hex escape text", value: `"a\x41b"`, want: true},
		{name: "escaped terminal quote is unclosed", value: `"a\"`, want: false},
		{name: "real newline is rejected", value: "\"a\nb\"", want: false},
		{name: "real carriage return is rejected", value: "\"a\rb\"", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pattern.MatchString(tt.value); got != tt.want {
				t.Fatalf("pattern.MatchString(%q) = %t, want %t", tt.value, got, tt.want)
			}
		})
	}
}

func TestRedactTextQuotedAssignments(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantType  string
		fragments []string
		wantCount int
	}{
		{
			name:      "double quoted spaces",
			input:     `password = "violet mesa lantern"`,
			wantType:  "password",
			fragments: []string{"violet mesa", "lantern"},
			wantCount: 1,
		},
		{
			name:      "single quoted spaces",
			input:     `serviceToken := 'a b c d'`,
			wantType:  "token",
			fragments: []string{"a b c d"},
			wantCount: 1,
		},
		{
			name:      "quoted punctuation",
			input:     `secret: "violet, mesa; lantern"`,
			wantType:  "secret",
			fragments: []string{"violet", "mesa", "lantern"},
			wantCount: 1,
		},
		{
			name:      "escaped double quotes",
			input:     `password = "violet \"mesa\" lantern"`,
			wantType:  "password",
			fragments: []string{"violet", "mesa", "lantern"},
			wantCount: 1,
		},
		{
			name:      "escaped single quotes",
			input:     `token = 'violet \'mesa\' lantern'`,
			wantType:  "token",
			fragments: []string{"violet", "mesa", "lantern"},
			wantCount: 1,
		},
		{
			name:      "x api key quoted spaces",
			input:     `X-API-Key: "violet mesa lantern"`,
			wantType:  "api-key",
			fragments: []string{"violet mesa", "lantern"},
			wantCount: 1,
		},
		{
			name:      "authorization quoted spaces",
			input:     `Authorization: "Bearer violet mesa lantern"`,
			wantType:  "authorization",
			fragments: []string{"violet mesa", "lantern"},
			wantCount: 1,
		},
		{
			name:      "six character value",
			input:     `secret = "abcdef"`,
			wantType:  "secret",
			fragments: []string{"abcdef"},
			wantCount: 1,
		},
		{
			name:      "phrase like value is intentionally redacted",
			input:     `token: "hello world"`,
			wantType:  "token",
			fragments: []string{"hello world"},
			wantCount: 1,
		},
		{
			name:      "five character value remains unchanged",
			input:     `secret = "abcde"`,
			wantCount: 0,
		},
		{
			name:      "unquoted phrase remains unchanged",
			input:     `token: some words here`,
			wantCount: 0,
		},
		{
			name:      "unclosed quote is not partially redacted",
			input:     `password = "secret phrase`,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted := redactText(tt.input)
			if redacted.Count != tt.wantCount {
				t.Fatalf("redaction count = %d, want %d: %+v", redacted.Count, tt.wantCount, redacted)
			}
			if tt.wantCount == 0 {
				if redacted.Text != tt.input || len(redacted.Types) != 0 {
					t.Fatalf("redacted = %+v, want unchanged input", redacted)
				}
				return
			}
			if !containsString(redacted.Types, tt.wantType) {
				t.Fatalf("redaction types = %#v, want %q", redacted.Types, tt.wantType)
			}
			assertP1NoSecretFragments(t, redacted.Text, tt.fragments...)
			again := redactText(redacted.Text)
			if again.Count != 0 || again.Text != redacted.Text {
				t.Fatalf("redaction is not idempotent: first=%+v second=%+v", redacted, again)
			}
		})
	}
}

func TestDetectorRedactorCoverage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fragment string
		wantType string
	}{
		{
			name:     "quoted assignment",
			input:    `password = "violet mesa lantern"`,
			fragment: "violet mesa",
			wantType: "password",
		},
		{
			name:     "escaped quoted assignment",
			input:    `token = "abcd\"efgh"`,
			fragment: "abcd",
			wantType: "token",
		},
		{
			name:     "aws access key",
			input:    "AKIAIOSFODNN7EXAMPLE",
			fragment: "AKIAIOSFODNN7EXAMPLE",
			wantType: "aws-key",
		},
		{
			name:     "github token",
			input:    "ghp_TEST_ONLY_NOT_A_REAL_TOKEN_123456",
			fragment: "ghp_TEST_ONLY_NOT_A_REAL_TOKEN_123456",
			wantType: "github-token",
		},
		{
			name:     "openai token",
			input:    "sk-test_only_not_a_real_token_123456",
			fragment: "sk-test_only_not_a_real_token_123456",
			wantType: "openai-token",
		},
		{
			name:     "bearer token",
			input:    "Bearer abcdefghijklmnopqrstuvwxyz",
			fragment: "abcdefghijklmnopqrstuvwxyz",
			wantType: "bearer-token",
		},
		{
			name: "pem private key",
			input: "-----BEGIN PRIVATE KEY-----\n" +
				"TEST_ONLY_NOT_A_REAL_PRIVATE_KEY\n" +
				"-----END PRIVATE KEY-----",
			fragment: "TEST_ONLY_NOT_A_REAL_PRIVATE_KEY",
			wantType: "private-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !isHardcodedSecret(tt.input) {
				t.Fatalf("isHardcodedSecret(%q) = false", tt.input)
			}
			redacted := redactText(tt.input)
			if redacted.Count == 0 || !containsString(redacted.Types, tt.wantType) {
				t.Fatalf("redacted = %+v, want type %q", redacted, tt.wantType)
			}
			assertP1NoSecretFragments(t, redacted.Text, tt.fragment)
		})
	}
}

func TestSecretFindingRedactsQuotedEvidence(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		fragments []string
	}{
		{
			name:      "spaced and punctuated",
			line:      `const password = "violet mesa, lantern; quartz"`,
			fragments: []string{"violet mesa", "lantern", "quartz"},
		},
		{
			name:      "escaped quote",
			line:      `const token = "abcd\"efgh"`,
			fragments: []string{"abcd", "efgh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := candidateLine{File: "config.go", Line: 7, Text: tt.line}
			matches := securityRuleMatches(candidate, strings.TrimSpace(candidate.Text))
			if len(matches) != 1 || matches[0].RuleID != ruleSecretHardcoded {
				t.Fatalf("matches = %+v, want one hardcoded-secret match", matches)
			}
			finalized := finalizeRuleMatches(matches)
			if len(finalized.Findings) != 1 || finalized.Redactions != 1 {
				t.Fatalf("finalized = %+v, want one redacted finding", finalized)
			}
			assertP1NoSecretFragments(t, finalized.Findings[0].Evidence, tt.fragments...)
			if !strings.Contains(finalized.Findings[0].Evidence, "<redacted:") {
				t.Fatalf("evidence = %q, want redaction marker", finalized.Findings[0].Evidence)
			}
		})
	}
}

func TestSandboxRunRedactsQuotedSecrets(t *testing.T) {
	const secret = `password = "violet mesa lantern"`
	run, redactions := sanitizeSandboxRun(sandboxRun{
		Stdout:   secret,
		Stderr:   "stderr: " + secret,
		Error:    "error: " + secret,
		Warnings: []string{"warning: " + secret},
	})
	if redactions != 4 {
		t.Fatalf("redactions = %d, want 4: %+v", redactions, run)
	}
	for _, value := range append([]string{run.Stdout, run.Stderr, run.Error}, run.Warnings...) {
		assertP1NoSecretFragments(t, value, "violet mesa", "lantern")
		if !strings.Contains(value, "<redacted:password>") {
			t.Fatalf("sanitized value = %q, want password marker", value)
		}
	}
}

func TestSecretPersistenceAcrossOutputs(t *testing.T) {
	requireSQLiteDriver(t)

	const (
		taskID = "task-spaced-secret"
		line   = `const password = "violet mesa, lantern; quartz"`
	)
	fragments := []string{"violet mesa", "lantern", "quartz"}
	diff := strings.Join([]string{
		"diff --git a/config.go b/config.go",
		"index 1111111..2222222 100644",
		"--- a/config.go",
		"+++ b/config.go",
		"@@ -1 +1,3 @@",
		" package config",
		"+",
		"+" + line,
	}, "\n")
	tempDir := t.TempDir()
	diffPath := filepath.Join(tempDir, "change.diff")
	if err := os.WriteFile(diffPath, []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(tempDir, "reviews.db")
	outputDir := filepath.Join(tempDir, "output")
	code, stdout, stderr := runRawForTest(t, []string{
		"--diff-file", diffPath,
		"--dry-run",
		"--db-path", dbPath,
		"--output-dir", outputDir,
	}, nil, nil, runtimeHooks{taskID: taskID})
	if code != 0 {
		t.Fatalf("review exit code = %d, want 0; stderr: %s", code, stderr)
	}
	assertP1NoSecretFragments(t, stdout, fragments...)
	assertP1NoSecretFragments(t, stderr, fragments...)

	var summary reviewSummary
	mustUnmarshalSummary(t, stdout, &summary)
	if summary.Findings != 1 || summary.Redactions != 1 {
		t.Fatalf("summary = %+v, want one finding and one redaction", summary)
	}
	jsonPath := filepath.Join(outputDir, filepath.FromSlash(summary.ReportPaths.JSON))
	jsonBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	assertP1NoSecretFragments(t, string(jsonBytes), fragments...)
	var report reviewReport
	if err := json.Unmarshal(jsonBytes, &report); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, jsonBytes)
	}
	assertP1RedactedReport(t, report, fragments...)

	markdownPath := filepath.Join(outputDir, filepath.FromSlash(summary.ReportPaths.Markdown))
	markdownBytes, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	assertP1NoSecretFragments(t, string(markdownBytes), fragments...)

	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var evidence string
	err = db.QueryRow(`
SELECT evidence
FROM findings
WHERE task_id = ? AND disposition = 'finding'
ORDER BY ordinal
LIMIT 1`, taskID).Scan(&evidence)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	assertP1NoSecretFragments(t, evidence, fragments...)
	if !strings.Contains(evidence, "<redacted:password>") {
		t.Fatalf("persisted evidence = %q, want password marker", evidence)
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	assertP1NoSecretFragments(t, string(dbBytes), fragments...)

	code, queryOut, stderr := runRawForTest(t, []string{
		"--show-task", taskID,
		"--db-path", dbPath,
	}, nil, nil, runtimeHooks{})
	if code != 0 {
		t.Fatalf("show-task exit code = %d, want 0; stderr: %s", code, stderr)
	}
	assertP1NoSecretFragments(t, queryOut, fragments...)
	var replayed reviewReport
	if err := json.Unmarshal([]byte(queryOut), &replayed); err != nil {
		t.Fatalf("unmarshal show-task report: %v\n%s", err, queryOut)
	}
	assertP1RedactedReport(t, replayed, fragments...)
}

func assertP1RedactedReport(t *testing.T, report reviewReport, fragments ...string) {
	t.Helper()
	if len(report.Findings) != 1 || report.Findings[0].RuleID != ruleSecretHardcoded {
		t.Fatalf("findings = %+v, want one hardcoded-secret finding", report.Findings)
	}
	if report.Metrics.Redactions != 1 {
		t.Fatalf("redactions = %d, want 1", report.Metrics.Redactions)
	}
	assertP1NoSecretFragments(t, report.Findings[0].Evidence, fragments...)
	if !strings.Contains(report.Findings[0].Evidence, "<redacted:password>") {
		t.Fatalf("evidence = %q, want password marker", report.Findings[0].Evidence)
	}
}

func assertP1NoSecretFragments(t *testing.T, value string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			t.Fatalf("value retained secret fragment %q: %q", fragment, value)
		}
	}
}
