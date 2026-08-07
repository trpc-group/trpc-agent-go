//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cragent "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/agent"
)

func TestRunAutoLoadsCurrentDirectoryConfig(t *testing.T) {
	repo := t.TempDir()
	out := filepath.Join(repo, "reports")
	dbPath := filepath.Join(repo, "audit", "review.db")
	skillsRoot := absoluteSkillsRoot(t)
	writeReviewRepo(t, repo)
	writeFile(t, filepath.Join(repo, "cr-agent.yaml"), ""+
		"mode: rule-only\n"+
		"runtime: local-fallback\n"+
		"output_dir: "+slashPath(out)+"\n"+
		"sqlite: "+slashPath(dbPath)+"\n"+
		"skills_root: "+slashPath(skillsRoot)+"\n")

	withWorkingDirectory(t, repo, func() {
		if err := Run(Options{}); err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	})
	assertFileExists(t, filepath.Join(out, "review_report.json"))
	assertFileExists(t, dbPath)
}

func TestRunLoadsExplicitConfigFile(t *testing.T) {
	repo := t.TempDir()
	cfgDir := t.TempDir()
	out := filepath.Join(repo, "explicit-reports")
	writeReviewRepo(t, repo)
	writeFile(t, filepath.Join(cfgDir, "review.yaml"), ""+
		"mode: rule-only\n"+
		"runtime: local-fallback\n"+
		"repo_path: "+slashPath(repo)+"\n"+
		"output_dir: "+slashPath(out)+"\n"+
		"skills_root: "+slashPath(absoluteSkillsRoot(t))+"\n")

	if err := Run(Options{ConfigFile: filepath.Join(cfgDir, "review.yaml")}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	assertFileExists(t, filepath.Join(out, "review_report.json"))
}

func TestProgrammaticOptionsDoNotAutoLoadLocalConfig(t *testing.T) {
	repo := t.TempDir()
	localConfigOut := filepath.Join(repo, "from-config")
	explicitOut := filepath.Join(repo, "from-options")
	writeReviewRepo(t, repo)
	writeFile(t, filepath.Join(repo, "cr-agent.yaml"), ""+
		"mode: sandbox\n"+
		"runtime: container\n"+
		"repo_path: "+slashPath(repo)+"\n"+
		"output_dir: "+slashPath(localConfigOut)+"\n"+
		"skills_root: "+slashPath(absoluteSkillsRoot(t))+"\n")

	withWorkingDirectory(t, repo, func() {
		if err := Run(Options{
			RepoPath:   repo,
			OutputDir:  explicitOut,
			Mode:       cragent.ModeRuleOnly,
			Runtime:    cragent.RuntimeLocalFallback,
			SkillsRoot: absoluteSkillsRoot(t),
		}); err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	})
	assertFileExists(t, filepath.Join(explicitOut, "review_report.json"))
	if _, err := os.Stat(filepath.Join(localConfigOut, "review_report.json")); err == nil {
		t.Fatalf("expected programmatic options to avoid auto-loading local cr-agent.yaml")
	}
}

func TestRunCLIOptionsOverrideConfig(t *testing.T) {
	repo := t.TempDir()
	configOut := filepath.Join(repo, "config-reports")
	overrideOut := filepath.Join(repo, "override-reports")
	writeReviewRepo(t, repo)
	configPath := filepath.Join(repo, "cr-agent.yaml")
	writeFile(t, configPath, ""+
		"mode: fake-model\n"+
		"runtime: local-fallback\n"+
		"repo_path: "+slashPath(repo)+"\n"+
		"output_dir: "+slashPath(configOut)+"\n"+
		"skills_root: "+slashPath(absoluteSkillsRoot(t))+"\n"+
		"model:\n"+
		"  provider: deepseek\n"+
		"  name: deepseek-chat\n"+
		"  api_key_env: CR_AGENT_TEST_MISSING_DEEPSEEK_KEY\n")

	opts, err := parseOptions([]string{
		"--config", configPath,
		"--output-dir", overrideOut,
		"--mode", cragent.ModeRuleOnly,
		"--runtime", cragent.RuntimeLocalFallback,
	})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	assertFileExists(t, filepath.Join(overrideOut, "review_report.json"))
	if _, err := os.Stat(filepath.Join(configOut, "review_report.json")); err == nil {
		t.Fatalf("expected CLI output-dir to override YAML output_dir")
	}
	data, err := os.ReadFile(filepath.Join(overrideOut, "review_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if strings.Contains(string(data), "model-provider-failed") {
		t.Fatalf("expected CLI mode=rule-only to override YAML fake-model provider: %s", data)
	}
}

func TestParseOptionsAcceptsOfficialExampleFlags(t *testing.T) {
	opts, err := parseOptions([]string{
		"-model", "gpt-4o-mini",
		"-streaming=true",
	})
	if err != nil {
		t.Fatalf("parse official example flags: %v", err)
	}
	if opts.ModelName != "gpt-4o-mini" {
		t.Fatalf("expected -model to set model name, got %q", opts.ModelName)
	}
	if !opts.Streaming {
		t.Fatalf("expected -streaming=true to be accepted")
	}
	if !opts.ExplicitFlags["model"] || !opts.ExplicitFlags["streaming"] {
		t.Fatalf("expected explicit official flags to be tracked, got %+v", opts.ExplicitFlags)
	}
}

func TestOfficialModelFlagOverridesYAMLModelName(t *testing.T) {
	repo := t.TempDir()
	configPath := filepath.Join(repo, "cr-agent.yaml")
	writeFile(t, configPath, ""+
		"model:\n"+
		"  provider: openai-compatible\n"+
		"  name: yaml-model\n")

	cli, err := parseOptions([]string{
		"--config", configPath,
		"-model", "cli-model",
	})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	opts, err := resolveOptions(cli)
	if err != nil {
		t.Fatalf("resolve options: %v", err)
	}
	if opts.ModelName != "cli-model" {
		t.Fatalf("expected -model to override YAML model.name, got %q", opts.ModelName)
	}
	if opts.ModelProvider != "openai-compatible" {
		t.Fatalf("expected YAML provider to remain, got %q", opts.ModelProvider)
	}
}

func TestConfigSupportsLocalModelAPIKey(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cr-agent.yaml")
	writeFile(t, configPath, ""+
		"mode: fake-model\n"+
		"model:\n"+
		"  provider: deepseek\n"+
		"  name: deepseek-chat\n"+
		"  api_key: sk-localyaml-1234567890abcdef\n")

	opts, err := optionsFromConfig(configPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if opts.ModelAPIKey != "sk-localyaml-1234567890abcdef" {
		t.Fatalf("expected local YAML API key to be parsed")
	}
}

func TestRunDeepSeekProviderMissingAPIKeyDoesNotAbort(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "sample.diff")
	writeFile(t, diffPath, ""+
		"diff --git a/foo.go b/foo.go\n"+
		"--- a/foo.go\n"+
		"+++ b/foo.go\n"+
		"@@ -1,1 +1,2 @@\n"+
		" package foo\n"+
		"+func Add(a, b int) int { return a + b }\n")

	err := Run(Options{
		DiffFile:       diffPath,
		OutputDir:      dir,
		Mode:           cragent.ModeFakeModel,
		Runtime:        cragent.RuntimeLocalFallback,
		SkillsRoot:     absoluteSkillsRoot(t),
		ModelProvider:  "deepseek",
		ModelName:      "deepseek-chat",
		ModelAPIKeyEnv: "CR_AGENT_TEST_MISSING_DEEPSEEK_KEY",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "review_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), "model-provider-failed") ||
		!strings.Contains(string(data), "needs_human_review") {
		t.Fatalf("expected missing DeepSeek key to be recorded as model-provider-failed: %s", data)
	}
	if strings.Contains(string(data), "CR_AGENT_TEST_MISSING_DEEPSEEK_KEY") {
		t.Fatalf("report should not leak model api key env names: %s", data)
	}
}

func TestCommittedDefaultConfigParses(t *testing.T) {
	opts, err := optionsFromConfig(filepath.Join("..", "..", "cr-agent.example.yaml"))
	if err != nil {
		t.Fatalf("parse committed cr-agent.example.yaml: %v", err)
	}
	if opts.Mode != cragent.ModeReview {
		t.Fatalf("expected committed config to keep safe default mode, got %q", opts.Mode)
	}
	if opts.ModelProvider != "" {
		t.Fatalf("expected committed config to avoid external model provider by default, got %q", opts.ModelProvider)
	}
	if opts.OutputDir != ".cr-agent/reports" || opts.SQLitePath != "" {
		t.Fatalf("unexpected committed config paths: output=%q sqlite=%q", opts.OutputDir, opts.SQLitePath)
	}
}

func TestOptionDefaultsEnableOutputScopedPersistence(t *testing.T) {
	t.Parallel()

	opts := Options{OutputDir: filepath.Join("tmp", "reports")}
	applyOptionDefaults(&opts)
	want := filepath.Join("tmp", "reports", "review.db")
	if opts.SQLitePath != want {
		t.Fatalf("default sqlite path = %q, want %q", opts.SQLitePath, want)
	}
}

func TestNoPersistSuppressesDefaultSQLitePath(t *testing.T) {
	t.Parallel()

	opts := Options{OutputDir: "reports", NoPersist: true}
	applyOptionDefaults(&opts)
	if opts.SQLitePath != "" {
		t.Fatalf("no-persist sqlite path = %q, want empty", opts.SQLitePath)
	}
}

func TestCapabilityConfigPreservesExplicitFalseAndCLIOverrides(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cr-agent.yaml")
	writeFile(t, configPath, ""+
		"mode: sandbox\n"+
		"staticcheck: true\n"+
		"sandbox:\n"+
		"  enabled: true\n"+
		"  staticcheck: true\n"+
		"model:\n"+
		"  enabled: true\n"+
		"  provider: fake\n")

	cli, err := parseOptions([]string{
		"--config", configPath,
		"--mode", cragent.ModeReview,
		"--sandbox=false",
		"--model-enabled=false",
		"--staticcheck=false",
	})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	opts, err := resolveOptions(cli)
	if err != nil {
		t.Fatalf("resolve options: %v", err)
	}
	if opts.Mode != cragent.ModeReview {
		t.Fatalf("mode = %q, want review", opts.Mode)
	}
	if opts.SandboxEnabled == nil || *opts.SandboxEnabled {
		t.Fatalf("sandbox enabled = %v, want explicit false", opts.SandboxEnabled)
	}
	if opts.ModelEnabled == nil || *opts.ModelEnabled {
		t.Fatalf("model enabled = %v, want explicit false", opts.ModelEnabled)
	}
	if opts.Staticcheck {
		t.Fatal("CLI explicit staticcheck=false must override both YAML forms")
	}
	if opts.ModelProvider != "fake" {
		t.Fatalf("provider = %q, want fake", opts.ModelProvider)
	}
}

func TestNestedSandboxStaticcheckOverridesLegacyTopLevel(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cr-agent.yaml")
	writeFile(t, configPath, "staticcheck: true\nsandbox:\n  staticcheck: false\n")
	opts, err := optionsFromConfig(configPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if opts.Staticcheck {
		t.Fatal("nested sandbox.staticcheck=false must override top-level true")
	}
}

func TestPrivateConfigIsIgnoredByGit(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(data), "cr-agent.yaml") {
		t.Fatalf("expected example cr-agent.yaml to be gitignored")
	}
}

func writeReviewRepo(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/reviewme\n")
	writeFile(t, filepath.Join(repo, "foo.go"), "package foo\n\nfunc Bad() { panic(\"boom\") }\n")
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func absoluteSkillsRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func withWorkingDirectory(t *testing.T, dir string, fn func()) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()
	fn()
}

func slashPath(path string) string {
	return filepath.ToSlash(path)
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
