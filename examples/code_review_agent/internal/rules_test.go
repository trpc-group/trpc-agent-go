//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuleEngine_SQLInjection(t *testing.T) {
	diff := `diff --git a/auth/handler.go b/auth/handler.go
--- a/auth/handler.go
+++ b/auth/handler.go
@@ -1,3 +1,6 @@
 package auth
+func login(name string) {
+	q := "SELECT * FROM users WHERE name = '" + name + "'"
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	found := false
	for _, f := range findings {
		if f.RuleID == "SQL_INJECTION" {
			found = true
			require.Equal(t, SeverityCritical, f.Severity)
		}
	}
	require.True(t, found, "expected SQL_INJECTION finding")
}

func TestRuleEngine_HardcodedSecret(t *testing.T) {
	diff := `diff --git a/config.go b/config.go
--- a/config.go
+++ b/config.go
@@ -1,3 +1,5 @@
 package config
+const APIKey = "sk-1234567890abcdef1234567890"
+const token = "tok_1234567890abcdef1234567890abcd"
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	hasSecret := false
	for _, f := range findings {
		if f.RuleID == "HARDCODED_SECRET" {
			hasSecret = true
		}
	}
	require.True(t, hasSecret, "expected HARDCODED_SECRET finding")
}

func TestRuleEngine_GoroutineLeak(t *testing.T) {
	diff := `diff --git a/worker.go b/worker.go
--- a/worker.go
+++ b/worker.go
@@ -1,3 +1,6 @@
 package worker
+func startWorker() {
+	go func() {
+		process()
+	}()
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	found := false
	for _, f := range findings {
		if f.RuleID == "GOROUTINE_LEAK" {
			found = true
		}
	}
	require.True(t, found, "expected GOROUTINE_LEAK finding")
}

func TestRuleEngine_UnclosedResource(t *testing.T) {
	diff := `diff --git a/io.go b/io.go
--- a/io.go
+++ b/io.go
@@ -1,3 +1,7 @@
 package io
+func readConfig(path string) {
+	f, _ := os.Open(path)
+	buf := make([]byte, 1024)
+	f.Read(buf)
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	found := false
	for _, f := range findings {
		if f.RuleID == "UNCLOSED_RESOURCE" {
			found = true
		}
	}
	require.True(t, found, "expected UNCLOSED_RESOURCE finding")
}

func TestRuleEngine_DBConnectionLeak(t *testing.T) {
	diff := `diff --git a/db.go b/db.go
--- a/db.go
+++ b/db.go
@@ -1,3 +1,6 @@
 package db
+func newDB() {
+	db, _ := sql.Open("postgres", dsn)
+	db.SetMaxOpenConns(10)
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	found := false
	for _, f := range findings {
		if f.RuleID == "DB_CONNECTION_LEAK" {
			found = true
		}
	}
	require.True(t, found, "expected DB_CONNECTION_LEAK finding")
}

func TestRuleEngine_CleanDiff_NoFindings(t *testing.T) {
	diff := `diff --git a/clean.go b/clean.go
--- a/clean.go
+++ b/clean.go
@@ -1,3 +1,6 @@
 package clean
+func HandleRequest(ctx context.Context, req *Request) (*Response, error) {
+	resp, err := process(ctx, req)
+	return resp, err
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	// Should have no critical/high findings (may have low-confidence test_missing).
	highFindings := 0
	for _, f := range findings {
		if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
			highFindings++
		}
	}
	require.Equal(t, 0, highFindings, "expected no critical/high findings")
}

func TestRuleEngine_IgnoredError(t *testing.T) {
	diff := `diff --git a/handler.go b/handler.go
--- a/handler.go
+++ b/handler.go
@@ -1,3 +1,5 @@
 package handler
+func saveData(data []byte) {
+	_ = os.WriteFile("out.txt", data, 0644)
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	found := false
	for _, f := range findings {
		if f.RuleID == "IGNORED_ERROR" {
			found = true
		}
	}
	require.True(t, found, "expected IGNORED_ERROR finding")
}

func TestRuleEngine_SensitiveInfoInLog(t *testing.T) {
	diff := `diff --git a/log.go b/log.go
--- a/log.go
+++ b/log.go
@@ -1,3 +1,5 @@
 package log
+func logConn(pwd string) {
+	log.Printf("password: %s", pwd)
+}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)

	engine := DefaultRuleEngine()
	findings := engine.Run(files)
	require.NotEmpty(t, findings)

	found := false
	for _, f := range findings {
		if f.RuleID == "SENSITIVE_INFO_IN_LOG" {
			found = true
		}
	}
	require.True(t, found, "expected SENSITIVE_INFO_IN_LOG finding")
}

func TestRuleEngine_AllFixtures(t *testing.T) {
	fixtures := []string{
		"../fixtures/01_clean.diff",
		"../fixtures/02_security.diff",
		"../fixtures/03_goroutine_leak.diff",
		"../fixtures/04_resource_unclosed.diff",
		"../fixtures/05_db_lifecycle.diff",
		"../fixtures/06_test_missing.diff",
		"../fixtures/07_duplicate.diff",
		"../fixtures/08_sensitive_info.diff",
	}

	for _, fx := range fixtures {
		t.Run(filepath.Base(fx), func(t *testing.T) {
			data, err := os.ReadFile(fx)
			require.NoError(t, err, "failed to read fixture: %s", fx)

			files, err := ParseDiffString(string(data))
			require.NoError(t, err, "failed to parse fixture: %s", fx)
			require.NotEmpty(t, files, "expected at least one file in: %s", fx)

			engine := DefaultRuleEngine()
			findings := engine.Run(files)
			t.Logf("  %s: %d findings", filepath.Base(fx), len(findings))
		})
	}
}
