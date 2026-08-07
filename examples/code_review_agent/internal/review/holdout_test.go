//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
)

func TestIndependentHoldoutRecallAndFalsePositiveRatio(t *testing.T) {
	positives := []struct {
		line     string
		category string
	}{
		{`func run(v string) { exec.Command("sh", "-c", v) }`, "security"},
		{`func invoke(v string) { exec.Command("bash", "-c", v) }`, "security"},
		{`rows, err := db.Query("SELECT * FROM t WHERE id=" + id)`, "security"},
		{`go func() { work() }()`, "concurrency"},
		{`go func(v int) { consume(v) }(value)`, "concurrency"},
		{`f, err := os.Open(name)`, "resources"},
		{`resp, err := http.Get(endpoint)`, "resources"},
		{`value, _ := risky()`, "errors"},
		{`result, _ = risky()`, "errors"},
		{`rows, err := db.Query(query, id)`, "database"},
	}
	safe := []string{
		`exec.Command(binary, args...)`,
		`rows, err := db.Query("SELECT * FROM t WHERE id=?", id); defer rows.Close()`,
		`func start(ctx context.Context) { go func() { <-ctx.Done() }() }`,
		`f, err := os.Open(name); if err != nil { return err }; defer f.Close()`,
		`value, err := risky(); if err != nil { return fmt.Errorf("risky: %w", err) }`,
		`db, err := sql.Open("sqlite", dsn); if err != nil { return err }`,
	}
	tp := 0
	for i, tc := range positives {
		out := reviewHoldoutLine(tc.line)
		if hasCategory(out, tc.category) {
			tp++
		} else {
			t.Logf("false negative %d: %s", i, tc.line)
		}
	}
	fp := 0
	for i, line := range safe {
		out := reviewHoldoutLine(line)
		if len(out.Findings) != 0 {
			fp += len(out.Findings)
			t.Logf("false positive %d: %s => %#v", i, line, out.Findings)
		}
	}
	recall := float64(tp) / float64(len(positives))
	fpr := float64(fp) / float64(tp+fp)
	if recall < 0.80 {
		t.Fatalf("holdout recall = %.3f, want >= 0.80 (tp=%d total=%d)", recall, tp, len(positives))
	}
	if fpr > 0.15 {
		t.Fatalf("holdout FP/(TP+FP) = %.3f, want <= 0.15 (tp=%d fp=%d)", fpr, tp, fp)
	}
}

func TestIndependentHoldoutSecretRedactionScore(t *testing.T) {
	r := NewRedactor()
	secrets := []string{
		"fixture-secret-value-aws-key", "fixture-secret-value-aws-key", "fixture-secret-value-aws-key", "fixture-secret-value-aws-key",
		"fixture-secret-value-github-token", "fixture-secret-value-github-token",
		`password="holdout-one"`, `password='holdout-two'`, `passwd="holdout-three"`, `token="holdout-four"`,
		`secret='holdout-five'`, `TOKEN = "holdout-six"`, `Password: "holdout-seven"`,
		"https://alice:holdout@example.com/repo", "http://bob:holdout@example.net/path",
		"-----BEGIN PRIVATE KEY-----\nholdout-a\n-----END PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----\nholdout-b\n-----END RSA PRIVATE KEY-----",
		`api_token="holdout-eight"`, `client_secret='holdout-nine'`, `PASSWORD="holdout-ten"`,
	}
	detected := 0
	for _, secret := range secrets {
		if r.Detect(secret) && r.Redact(secret) != secret {
			detected++
		}
	}
	score := float64(detected) / float64(len(secrets))
	if score < 0.95 {
		t.Fatalf("holdout secret detection = %.3f, want >= 0.95 (%d/%d)", score, detected, len(secrets))
	}
}

func reviewHoldoutLine(line string) Output {
	diff := input.Diff{Complete: true, Files: []input.FileDiff{
		{NewPath: "holdout.go", Added: []input.AddedLine{{Line: 10, Text: line}}},
		{NewPath: "holdout_test.go", Added: []input.AddedLine{{Line: 1, Text: "func TestHoldout(t *testing.T) {}"}}},
	}}
	return NewEngine(NewRedactor()).Review(diff)
}

func hasCategory(out Output, category string) bool {
	for _, finding := range out.Findings {
		if finding.Category == category {
			return true
		}
	}
	return false
}
