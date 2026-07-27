// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"fmt"
	"strings"
	"testing"
)

func TestAcceptanceRuleQualityCorpus(t *testing.T) {
	highRisk := []struct {
		line string
		rule string
	}{
		{`password := "correct horse battery staple"`, "go/security/hardcoded-secret"},
		{`token := "ghp_abcdefghijklmnopqrstuvwxyz123456"`, "go/security/hardcoded-secret"},
		{`exec.Command("bash", "-c", userInput)`, "go/security/dynamic-shell"},
		{`query := "SELECT * FROM users WHERE id=" + id`, "go/database/sql-concatenation"},
		{`ctx, stop := context.WithCancel(parent)`, "go/context/cancel-leak"},
		{`go func() { work() }()`, "go/concurrency/unbounded-goroutine"},
		{`f, err := os.Open(name)`, "go/resource/close"},
		{`rows, err := db.Query(query)`, "go/resource/close"},
		{`tx, err := db.Begin()`, "go/database/transaction-rollback"},
		{`value, _ := load()`, "go/error/ignored"},
	}
	detected := 0
	for index, test := range highRisk {
		raw := fmt.Sprintf("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package a\n+%s\n", test.line)
		input, err := ParseUnifiedDiff(raw)
		if err != nil {
			t.Fatal(err)
		}
		findings, _, _ := analyze(input)
		if hasRule(findings, test.rule) {
			detected++
		} else {
			t.Logf("missed corpus case %d: %s", index, test.rule)
		}
	}
	if recall := float64(detected) / float64(len(highRisk)); recall < .80 {
		t.Fatalf("high-risk recall %.1f%% is below 80%%", recall*100)
	}

	safeLines := []string{
		`token := os.Getenv("TOKEN")`, `const tokenHeader = "X-Token"`,
		`const apiKeyEnv = "API_KEY"`, `type Config struct { Token string }`,
		`json:"token"`, `header.Set("Authorization", value)`,
		`password := promptUser()`, `secret := vault.Lookup(name)`,
		`apiKey := strings.TrimSpace(input)`, `const contentType = "application/json"`,
		`ctx, cancel := context.WithCancel(parent); defer cancel()`,
		`file, err := os.Open(name); defer file.Close()`,
		`tx, err := db.Begin(); defer tx.Rollback()`,
		`exec.Command("git", "status", "--short")`,
		`go func() { select { case <-ctx.Done(): return } }()`,
	}
	highRiskRules := map[string]bool{
		"go/security/hardcoded-secret": true, "go/security/dynamic-shell": true,
		"go/database/sql-concatenation": true, "go/context/cancel-leak": true,
		"go/concurrency/unbounded-goroutine": true, "go/resource/close": true,
		"go/database/transaction-rollback": true, "go/error/ignored": true,
	}
	falsePositives := 0
	for _, line := range safeLines {
		raw := fmt.Sprintf("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package a\n+%s\n", line)
		input, err := ParseUnifiedDiff(raw)
		if err != nil {
			t.Fatal(err)
		}
		findings, warnings, human := analyze(input)
		if containsHighRiskRule(highRiskRules, findings, warnings, human) {
			falsePositives++
		}
	}
	if rate := float64(falsePositives) / float64(len(safeLines)); rate > .15 {
		t.Fatalf("false-positive rate %.1f%% exceeds 15%%", rate*100)
	}
}

func containsHighRiskRule(rules map[string]bool, buckets ...[]Finding) bool {
	for _, bucket := range buckets {
		for _, finding := range bucket {
			if rules[finding.RuleID] {
				return true
			}
		}
	}
	return false
}

func TestAcceptanceRedactionCorpus(t *testing.T) {
	secrets := []string{
		`password="correct horse battery staple"`, `token=ghp_abcdefghijklmnopqrstuvwxyz123456`,
		`api_key=sk-abcdefghijklmnopqrstuvwxyz`, `client_secret="abcdefghijklmnop"`,
		`AKIAABCDEFGHIJKLMNOP`, `ASIAABCDEFGHIJKLMNOP`,
		`Authorization: Bearer abcdefghijklmnopqrstuvwxyz`,
		`eyJabcdefghijk.abcdefghijkl.abcdefghijkl`,
		`postgres://user:pass@example.test/db`, `mysql://user:pass@example.test/db`,
		`xoxb-abcdefghijklmnopqrstuv`, `github_pat_abcdefghijklmnopqrstuvwxyz`,
		`private_key="abcdefghijklmnop"`, `passwd='abcdefghijklmnop'`,
		`token: "abcdefghijklmnop"`, `api-key: 'abcdefghijklmnop'`,
		`secret="abcdefghijklmnop"`, `client-secret=abcdefghijklmnop`,
		`sk-1234567890abcdefghijkl`, `gho_abcdefghijklmnopqrstuvwxyz`,
	}
	redacted := 0
	for _, secret := range secrets {
		output := redact(secret)
		if strings.Contains(output, "[REDACTED]") && !strings.Contains(output, secret) {
			redacted++
			continue
		}
		if !strings.Contains(output, "[REDACTED]") || strings.Contains(output, secret) {
			t.Fatalf("secret was not fully redacted: input=%q output=%q", secret, output)
		}
	}
	if rate := float64(redacted) / float64(len(secrets)); rate < .95 {
		t.Fatalf("redaction rate %.1f%% is below 95%%", rate*100)
	}
}
