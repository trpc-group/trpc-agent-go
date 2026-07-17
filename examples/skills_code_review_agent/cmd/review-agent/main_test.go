//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWithYAMLConfigWritesProductReports(t *testing.T) {
	outDir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "cr-agent.yaml")
	config := "input:\n  fixture: security_issue\n" +
		"output:\n  dir: " + filepath.ToSlash(outDir) + "\n  sqlite: " + filepath.ToSlash(filepath.Join(outDir, "reviews.sqlite")) + "\n" +
		"sandbox:\n  executor: fake\n  timeout: 30s\n" +
		"model:\n  rule_only: true\n  provider: fake\n"
	if err := os.WriteFile(cfgPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"--config", cfgPath}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"task_id=review-",
		"diagnostics_report=" + filepath.Join(outDir, "review_diagnostics.json"),
		"zh_report=" + filepath.Join(outDir, "review_report.zh.md"),
		"findings=1 warnings=0 needs_human_review=1",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	for _, name := range []string{"review_report.json", "review_report.md", "review_diagnostics.json", "review_report.zh.md", "reviews.sqlite"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
}

func TestRunWithoutConfigDefaultsToRuleOnly(t *testing.T) {
	outDir := t.TempDir()
	var out bytes.Buffer
	err := run([]string{
		"--fixture", "security_issue",
		"--executor", "fake",
		"--output-dir", outDir,
		"--sqlite", filepath.Join(outDir, "reviews.sqlite"),
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "findings=1 warnings=0 needs_human_review=1") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestRunPropagatesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outDir := t.TempDir()
	var out bytes.Buffer
	err := runWithContext(ctx, []string{
		"--fixture", "security_issue",
		"--executor", "fake",
		"--output-dir", outDir,
		"--sqlite", filepath.Join(outDir, "reviews.sqlite"),
	}, &out)
	if err == nil {
		t.Fatal("expected canceled context error")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no success output on canceled context, got %q", out.String())
	}
}

func TestRunYAMLConfigWithoutRuleOnlyPreservesDefault(t *testing.T) {
	outDir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "cr-agent.yaml")
	config := "input:\n  fixture: security_issue\n" +
		"output:\n  dir: " + filepath.ToSlash(outDir) + "\n  sqlite: " + filepath.ToSlash(filepath.Join(outDir, "reviews.sqlite")) + "\n" +
		"sandbox:\n  executor: fake\n  timeout: 30s\n" +
		"model:\n  provider: fake\n"
	if err := os.WriteFile(cfgPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"--config", cfgPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "findings=1 warnings=0 needs_human_review=1") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestLoadYAMLConfigAllowsExplicitRuleOnlyFalse(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cr-agent.yaml")
	if err := os.WriteFile(cfgPath, []byte("model:\n  rule_only: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadYAMLConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RuleOnly {
		t.Fatal("expected explicit model.rule_only=false to be preserved")
	}
}

func TestLoadYAMLConfigRejectsBadTimeout(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(cfgPath, []byte("sandbox:\n  timeout: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadYAMLConfig(cfgPath); err == nil {
		t.Fatal("expected bad timeout error")
	}
}
