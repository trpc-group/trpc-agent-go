//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package app wires the example review pipeline.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// RuntimeFake selects the deterministic fake runtime.
	RuntimeFake = "fake"
	// RuntimeContainer selects the Docker container runtime.
	RuntimeContainer = "container"
	// RuntimeLocal selects local execution and is rejected unless explicitly allowed.
	RuntimeLocal = "local"
	// ModeRuleOnly disables optional model augmentation.
	ModeRuleOnly = "rule-only"
	// ModeAgent enables optional model augmentation.
	ModeAgent = "agent"
)

// Config describes one CLI invocation.
type Config struct {
	DiffFile         string
	RepoPath         string
	Fixture          string
	Runtime          string
	Mode             string
	OutDir           string
	DBPath           string
	AllowLocal       bool
	ShowTask         string
	TaskID           string
	Files            []string
	FileList         string
	PermissionAction tool.PermissionAction
	PermissionReason string
}

type runtimeFactory func(Config) (sandbox.Runtime, error)

// Run executes a full review.
func Run(cfg Config) (report.DTO, error) {
	return runWithRuntimeFactory(cfg, defaultRuntimeFactory)
}

func runWithRuntimeFactory(cfg Config, newRuntime runtimeFactory) (report.DTO, error) {
	started := time.Now()
	if cfg.Runtime == "" {
		cfg.Runtime = RuntimeContainer
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeRuleOnly
	}
	if cfg.PermissionAction == "" {
		cfg.PermissionAction = tool.PermissionActionAllow
	}
	if cfg.Mode != ModeRuleOnly && cfg.Mode != ModeAgent {
		return report.DTO{}, fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
	if cfg.OutDir == "" {
		cfg.OutDir = "out"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.OutDir, "review_audit.db")
	}
	if cfg.ShowTask != "" {
		st, err := storage.OpenSQLite(cfg.DBPath)
		if err != nil {
			return report.DTO{}, err
		}
		defer st.Close()
		return st.GetReview(cfg.ShowTask)
	}
	if cfg.Runtime == RuntimeLocal && !cfg.AllowLocal {
		return report.DTO{}, fmt.Errorf("local runtime requires --allow-local")
	}
	if cfg.Runtime == RuntimeLocal {
		return report.DTO{}, fmt.Errorf("local runtime is intentionally not implemented; use fake or container")
	}
	if cfg.Runtime != RuntimeFake && cfg.Runtime != RuntimeContainer {
		return report.DTO{}, fmt.Errorf("unsupported runtime %q", cfg.Runtime)
	}
	if err := validateConfig(cfg); err != nil {
		return report.DTO{}, err
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return report.DTO{}, err
	}
	taskID := cfg.TaskID
	if taskID == "" {
		taskID = "review-" + randomHex(8)
	}
	st, err := storage.OpenSQLite(cfg.DBPath)
	if err != nil {
		return report.DTO{}, err
	}
	defer st.Close()
	diffBytes, err := readDiff(cfg)
	if err != nil {
		_ = persistFailure(st, cfg, taskID, "input_error", err)
		return report.DTO{}, err
	}
	parsed, err := input.ParseUnifiedDiffString(string(diffBytes), input.Limits{MaxBytes: 16 << 20, MaxLines: 200000})
	if err != nil {
		_ = persistFailure(st, cfg, taskID, "parse_error", err)
		return report.DTO{}, err
	}
	engineOut := review.NewEngine(review.NewRedactor()).Review(parsed)
	status := domain.StatusCompleted
	if !parsed.Complete || len(engineOut.NeedsHumanReview) > 0 {
		status = domain.StatusNeedsHumanReview
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	snap, cleanup, err := buildSandboxSnapshot(cfg, diffBytes)
	if err != nil {
		_ = persistFailure(st, cfg, taskID, "snapshot_error", err)
		return report.DTO{}, err
	}
	defer cleanup()
	var runs []sandbox.Result
	redactor := review.NewRedactor()
	recordDecision := func(rec governance.DecisionRecord) error {
		return st.RecordDecision(ctx, taskID, storage.DecisionRecord{
			Action:     string(rec.Action),
			Reason:     redactor.Redact(rec.Reason),
			PlanDigest: rec.PlanDigest,
			CreatedAt:  time.Now().UTC(),
		})
	}
	var governanceActions []string
	permissionBlocks := 0
	agentToolCalls := 0
	moduleFiles, err := snapshotModuleFiles(snap)
	if err != nil {
		return report.DTO{}, err
	}
	plans := plannedCommands(snap, cfg.Runtime, parsed, moduleFiles)
	sandboxSkippedNoRepository := false
	if len(plans) == 0 && cfg.RepoPath == "" {
		sandboxSkippedNoRepository = true
	}
	if cfg.Mode == ModeAgent {
		if len(plans) == 0 {
			status = domain.StatusNeedsHumanReview
		} else {
			audit, agentErr := runAgentIntegration(ctx, snap.SkillPath, plans[0].Plan.Digest(), cfg.PermissionAction, func(toolName, action string) error {
				if err := st.RecordDecision(ctx, taskID, storage.DecisionRecord{
					Action: action, Reason: "framework policy for " + toolName,
					PlanDigest: plans[0].Plan.Digest(), CreatedAt: time.Now().UTC(),
				}); err != nil {
					return err
				}
				governanceActions = append(governanceActions, "agent:"+action+":"+toolName+":"+plans[0].Plan.Digest())
				agentToolCalls++
				if action != string(tool.PermissionActionAllow) {
					permissionBlocks++
				}
				return nil
			})
			if agentErr != nil || !audit.SkillLoaded || !audit.BundledContentSeen || !audit.WorkspaceCalled {
				status = domain.StatusNeedsHumanReview
			}
		}
	}
	var rt sandbox.Runtime
	defer func() {
		if rt != nil {
			_ = rt.Close()
		}
	}()
	staged := false
	var stageErr error
	for _, planned := range plans {
		plan := planned.Plan
		policy := governance.StaticPolicy{Enabled: true, Action: cfg.PermissionAction, Reason: cfg.PermissionReason, ExpectedPlanDigest: plan.Digest()}
		verify := func() error {
			return governance.VerifySources(plan, snap.SkillPath, snap.Path)
		}
		decision, gateErr := governance.Gate(ctx, policy, plan, recordDecision, verify, func(ctx context.Context) error {
			if staged {
				return nil
			}
			if stageErr != nil {
				return stageErr
			}
			if rt == nil {
				var err error
				rt, err = newRuntime(cfg)
				if err != nil {
					return err
				}
			}
			if err := rt.Stage(ctx, snap); err != nil {
				stageErr = err
				return err
			}
			staged = true
			return nil
		})
		governanceActions = append(governanceActions, string(decision.Action)+":"+plan.CommandID+":"+redactor.Redact(decision.Reason)+":"+plan.Digest())
		if decision.Action != tool.PermissionActionAllow {
			permissionBlocks++
		}
		if gateErr != nil {
			status = domain.StatusNeedsHumanReview
			continue
		}
		if decision.Action != tool.PermissionActionAllow {
			status = domain.StatusNeedsHumanReview
			continue
		}
		if err := verify(); err != nil {
			status = domain.StatusNeedsHumanReview
			continue
		}
		run, runErr := rt.Run(ctx, sandbox.Command{
			ID: plan.CommandID, Args: plan.Args, Cwd: plan.Cwd, Timeout: time.Duration(plan.CommandTimeoutMS) * time.Millisecond,
			MaxStdoutBytes: plan.StdoutLimitBytes, MaxStderrBytes: plan.StderrLimitBytes,
		})
		runs = append(runs, run)
		if runErr != nil || run.ExitCode != 0 || run.TimedOut {
			status = domain.StatusNeedsHumanReview
		}
	}
	if rt != nil {
		cleanupErr := rt.Cleanup(ctx)
		closeErr := rt.Close()
		rt = nil
		if cleanupErr != nil || closeErr != nil {
			status = domain.StatusNeedsHumanReview
		}
	}
	metrics := map[string]int{
		"files": len(parsed.Files), "findings": len(engineOut.Findings),
		"human_review": len(engineOut.NeedsHumanReview), "suppressed": engineOut.Suppressed,
		"duration_ms": int(time.Since(started).Milliseconds()), "sandbox_runs": len(runs),
		"tool_calls": len(runs) + agentToolCalls, "permission_decisions": len(governanceActions),
		"permission_blocks": permissionBlocks,
	}
	if sandboxSkippedNoRepository {
		metrics["sandbox_skipped_no_repository"] = 1
	}
	for _, finding := range append(engineOut.Findings, engineOut.NeedsHumanReview...) {
		metrics["severity_"+string(finding.Severity)]++
	}
	for _, run := range runs {
		metrics["sandbox_duration_ms"] += int(run.DurationMS)
		if run.Outcome == sandbox.OutcomeDependencyUnavailable {
			metrics["dependency_unavailable"]++
		}
	}
	dto := report.DTO{
		TaskID: taskID, Status: status, Input: inputSummary(cfg, diffBytes, parsed), Findings: engineOut.Findings, NeedsHumanReview: engineOut.NeedsHumanReview,
		SandboxRuns: runs, Governance: governanceActions, Artifacts: []string{"review_report.json", "review_report.md"},
		Metrics: metrics, Files: diffFiles(parsed), ParserWarnings: append([]string(nil), parsed.Warnings...),
	}
	dto.Stats = report.BuildStats(dto)
	js, err := report.RenderJSON(dto)
	if err != nil {
		return report.DTO{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "review_report.json"), js, 0o600); err != nil {
		return report.DTO{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "review_report.md"), []byte(report.RenderMarkdown(dto)), 0o600); err != nil {
		return report.DTO{}, err
	}
	if err := writeAcceptanceManifest(cfg.OutDir, dto); err != nil {
		return report.DTO{}, err
	}
	artifacts, err := collectReportArtifacts(cfg.OutDir)
	if err != nil {
		return report.DTO{}, err
	}
	dto.ArtifactDetails = artifacts
	dto.Stats = report.BuildStats(dto)
	js, err = report.RenderJSON(dto)
	if err != nil {
		return report.DTO{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "review_report.json"), js, 0o600); err != nil {
		return report.DTO{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "review_report.md"), []byte(report.RenderMarkdown(dto)), 0o600); err != nil {
		return report.DTO{}, err
	}
	if err := writeAcceptanceManifest(cfg.OutDir, dto); err != nil {
		return report.DTO{}, err
	}
	artifacts, err = collectReportArtifacts(cfg.OutDir)
	if err != nil {
		return report.DTO{}, err
	}
	dto.ArtifactDetails = artifacts
	dto.Stats = report.BuildStats(dto)
	if err := st.Finalize(dto); err != nil {
		return report.DTO{}, err
	}
	return dto, nil
}

func persistFailure(st storage.Store, cfg Config, taskID, metric string, cause error) error {
	if st == nil {
		return nil
	}
	dto := report.DTO{
		TaskID:         taskID,
		Status:         domain.StatusFailed,
		Input:          report.InputSummary{Kind: inputKind(cfg)},
		Metrics:        map[string]int{metric: 1},
		Artifacts:      []string{"review_report.json", "review_report.md"},
		ParserWarnings: []string{cause.Error()},
	}
	dto.Stats = report.BuildStats(dto)
	return st.Finalize(dto)
}

func collectReportArtifacts(outDir string) ([]report.Artifact, error) {
	artifacts := []report.Artifact{
		{Path: "acceptance_manifest.json", ContentType: "application/json", Durable: true},
		{Path: "review_report.json", ContentType: "application/json", Durable: true},
		{Path: "review_report.md", ContentType: "text/markdown", Durable: true},
	}
	for i := range artifacts {
		artifact, err := artifactForFile(outDir, artifacts[i])
		if err != nil {
			return nil, err
		}
		artifacts[i] = artifact
	}
	return artifacts, nil
}

func writeAcceptanceManifest(outDir string, dto report.DTO) error {
	manifest := struct {
		TaskID    string              `json:"task_id"`
		Status    domain.Status       `json:"status"`
		Input     report.InputSummary `json:"input"`
		Stats     report.Stats        `json:"stats"`
		Metrics   map[string]int      `json:"metrics"`
		Artifacts []report.Artifact   `json:"artifacts"`
		Checks    map[string]string   `json:"checks"`
	}{
		TaskID:    dto.TaskID,
		Status:    dto.Status,
		Input:     dto.Input,
		Stats:     report.BuildStats(dto),
		Metrics:   dto.Metrics,
		Artifacts: dto.ArtifactDetails,
		Checks: map[string]string{
			"sandbox":    sandboxCheck(dto),
			"redaction":  "durable_sinks_redacted",
			"governance": governanceCheck(dto),
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "acceptance_manifest.json"), data, 0o600)
}

func sandboxCheck(dto report.DTO) string {
	if len(dto.SandboxRuns) == 0 {
		return "skipped_no_repository"
	}
	if dto.Stats.Sandbox.Failed > 0 {
		return "needs_human_review"
	}
	return "completed"
}

func governanceCheck(dto report.DTO) string {
	if len(dto.Governance) == 0 {
		return "no_decisions"
	}
	return "decisions_recorded"
}

func artifactForFile(outDir string, artifact report.Artifact) (report.Artifact, error) {
	data, err := os.ReadFile(filepath.Join(outDir, artifact.Path))
	if err != nil {
		return report.Artifact{}, err
	}
	sum := sha256.Sum256(data)
	artifact.SHA256 = "sha256:" + hex.EncodeToString(sum[:])
	artifact.Bytes = int64(len(data))
	return artifact, nil
}

func inputKind(cfg Config) string {
	switch {
	case cfg.Fixture != "":
		return "fixture"
	case cfg.RepoPath != "":
		return "repo-path"
	default:
		return "diff-file"
	}
}

func diffFiles(parsed input.Diff) []string {
	files := make([]string, 0, len(parsed.Files))
	for _, file := range parsed.Files {
		path := file.NewPath
		if path == "" {
			path = file.OldPath
		}
		if path != "" {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

func inputSummary(cfg Config, diffBytes []byte, parsed input.Diff) report.InputSummary {
	kind := "diff-file"
	switch {
	case cfg.Fixture != "":
		kind = "fixture"
	case cfg.RepoPath != "":
		kind = "repo-path"
	}
	hunks := 0
	added := 0
	for _, file := range parsed.Files {
		hunks += len(file.Hunks)
		added += len(file.Added)
	}
	sum := sha256.Sum256(diffBytes)
	return report.InputSummary{
		Kind: kind, Digest: "sha256:" + hex.EncodeToString(sum[:]),
		Files: len(parsed.Files), Hunks: hunks, AddedLines: added,
	}
}

func defaultRuntimeFactory(cfg Config) (sandbox.Runtime, error) {
	if cfg.Runtime == RuntimeFake {
		rt := sandbox.NewFakeRuntime()
		if cfg.Fixture == "sandbox_failure" {
			rt.Enqueue(sandbox.Result{CommandID: governance.CommandGoTest, Stderr: "go test failed", ExitCode: 1})
		}
		return rt, nil
	}
	skillPath, err := bundledSkillPath()
	if err != nil {
		return nil, err
	}
	moduleRoot := filepath.Dir(filepath.Dir(skillPath))
	rt, err := sandbox.NewContainerRuntime(moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("container runtime unavailable: %w", err)
	}
	return rt, nil
}

func readDiff(cfg Config) ([]byte, error) {
	switch {
	case cfg.Fixture != "":
		return os.ReadFile(fixturePath(cfg.Fixture))
	case cfg.DiffFile != "":
		return os.ReadFile(cfg.DiffFile)
	default:
		files, err := selectedFiles(cfg)
		if err != nil {
			return nil, err
		}
		return gitDiff(cfg.RepoPath, files)
	}
}

func validateConfig(cfg Config) error {
	inputs := 0
	for _, present := range []bool{cfg.DiffFile != "", cfg.RepoPath != "", cfg.Fixture != ""} {
		if present {
			inputs++
		}
	}
	if inputs != 1 {
		return fmt.Errorf("exactly one of --diff-file, --repo-path, or --fixture is required")
	}
	if (len(cfg.Files) > 0 || cfg.FileList != "") && cfg.RepoPath == "" {
		return fmt.Errorf("--file and --file-list require --repo-path")
	}
	for _, file := range cfg.Files {
		if err := validateSelectedFile(file); err != nil {
			return err
		}
	}
	return nil
}

func selectedFiles(cfg Config) ([]string, error) {
	files := append([]string(nil), cfg.Files...)
	if cfg.FileList != "" {
		data, err := os.ReadFile(cfg.FileList)
		if err != nil {
			return nil, fmt.Errorf("read file list: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				files = append(files, line)
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, file := range files {
		if err := validateSelectedFile(file); err != nil {
			return nil, err
		}
		file = filepath.ToSlash(filepath.Clean(file))
		if !seen[file] {
			seen[file] = true
			out = append(out, file)
		}
	}
	return out, nil
}

func validateSelectedFile(file string) error {
	if file == "" || filepath.IsAbs(file) || strings.ContainsRune(file, 0) {
		return fmt.Errorf("unsafe selected file %q", file)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(file)))
	if clean != filepath.ToSlash(file) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("unsafe selected file %q", file)
	}
	return nil
}

func buildSandboxSnapshot(cfg Config, diffBytes []byte) (sandbox.Snapshot, func() error, error) {
	skillPath, err := bundledSkillPath()
	if err != nil {
		return sandbox.Snapshot{}, nil, err
	}
	skillDigest, err := governance.DigestTree(skillPath)
	if err != nil {
		return sandbox.Snapshot{}, nil, err
	}
	scriptDigest, err := governance.DigestFile(filepath.Join(skillPath, "scripts", "run_checks.sh"))
	if err != nil {
		return sandbox.Snapshot{}, nil, err
	}
	if cfg.RepoPath != "" {
		snap, cleanup, err := input.BuildGitSnapshot(cfg.RepoPath, 128<<20)
		if err != nil {
			return sandbox.Snapshot{}, nil, err
		}
		files, err := selectedFiles(cfg)
		if err != nil {
			_ = cleanup()
			return sandbox.Snapshot{}, nil, err
		}
		currentDiff, err := gitDiff(cfg.RepoPath, files)
		if err != nil {
			_ = cleanup()
			return sandbox.Snapshot{}, nil, err
		}
		if string(currentDiff) != string(diffBytes) {
			_ = cleanup()
			return sandbox.Snapshot{}, nil, fmt.Errorf("repository changed after diff collection")
		}
		return sandbox.Snapshot{Path: snap.Path, Digest: snap.Digest, SkillPath: skillPath, SkillDigest: skillDigest, ScriptDigest: scriptDigest}, cleanup, nil
	}
	return sandbox.Snapshot{Digest: governance.DigestString(string(diffBytes)), SkillPath: skillPath, SkillDigest: skillDigest, ScriptDigest: scriptDigest}, func() error { return nil }, nil
}

type plannedCommand struct {
	Plan governance.Plan
}

func plannedCommands(snap sandbox.Snapshot, runtime string, parsed input.Diff, moduleFiles []string) []plannedCommand {
	if snap.Path == "" {
		return nil
	}
	base := governance.Plan{
		Runtime:            runtime,
		SkillDigest:        snap.SkillDigest,
		SnapshotDigest:     snap.Digest,
		ScriptDigest:       snap.ScriptDigest,
		Env:                map[string]string{"PATH": "/usr/local/go/bin:/usr/bin:/bin", "HOME": "/tmp"},
		CommandTimeoutMS:   60_000,
		TaskTimeoutMS:      120_000,
		StdoutLimitBytes:   1 << 20,
		StderrLimitBytes:   1 << 20,
		ArtifactMaxFiles:   20,
		ArtifactFileBytes:  1 << 20,
		ArtifactTotalBytes: 8 << 20,
	}
	pkgsByModule := changedPackagesByModule(parsed.Files, moduleFiles)
	plans := make([]plannedCommand, 0, len(pkgsByModule)*3)
	for _, cmd := range []struct {
		id   string
		mode string
	}{
		{id: governance.CommandGoTest, mode: "test"},
		{id: governance.CommandGoVet, mode: "vet"},
		{id: governance.CommandStaticcheck, mode: "staticcheck"},
	} {
		for _, module := range sortedModuleRoots(pkgsByModule) {
			for _, pkg := range pkgsByModule[module] {
				p := base
				p.CommandID = cmd.id
				p.Cwd = containerModuleCWD(module)
				p.Args = []string{cmd.mode, pkg}
				plans = append(plans, plannedCommand{Plan: p})
			}
		}
	}
	return plans
}

func snapshotModuleFiles(snap sandbox.Snapshot) ([]string, error) {
	if snap.Path == "" {
		return nil, nil
	}
	var modules []string
	if err := filepath.WalkDir(snap.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(snap.Path, path)
		if err != nil {
			return err
		}
		modules = append(modules, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(modules)
	return modules, nil
}

func changedPackagesByModule(files []input.FileDiff, moduleFiles []string) map[string][]string {
	if len(moduleFiles) == 0 {
		moduleFiles = []string{"go.mod"}
	}
	moduleSet := map[string]bool{}
	for _, m := range moduleFiles {
		moduleSet[filepath.ToSlash(filepath.Dir(m))] = true
	}
	if len(moduleSet) == 0 {
		moduleSet["."] = true
	}
	pkgs := map[string]map[string]bool{}
	for _, f := range files {
		path := f.NewPath
		if path == "" {
			path = f.OldPath
		}
		if path == "" || f.Deleted || f.Binary || !strings.HasSuffix(path, ".go") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		module := nearestModuleForApp(dir, moduleSet)
		rel := strings.TrimPrefix(strings.TrimPrefix(dir, module), "/")
		pkg := "."
		if rel != "" {
			pkg = "./" + rel
		}
		if pkgs[module] == nil {
			pkgs[module] = map[string]bool{}
		}
		pkgs[module][pkg] = true
	}
	out := map[string][]string{}
	for module, set := range pkgs {
		for pkg := range set {
			out[module] = append(out[module], pkg)
		}
		sort.Slice(out[module], func(i, j int) bool {
			if out[module][i] == "." {
				return true
			}
			if out[module][j] == "." {
				return false
			}
			return out[module][i] < out[module][j]
		})
	}
	return out
}

func sortedModuleRoots(pkgs map[string][]string) []string {
	modules := make([]string, 0, len(pkgs))
	for module := range pkgs {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i] == "." {
			return true
		}
		if modules[j] == "." {
			return false
		}
		return modules[i] < modules[j]
	})
	return modules
}

func containerModuleCWD(module string) string {
	if module == "." || module == "" {
		return "work/repo"
	}
	return "work/repo/" + module
}

func nearestModuleForApp(dir string, modules map[string]bool) string {
	for {
		if modules[dir] {
			return dir
		}
		next := filepath.ToSlash(filepath.Dir(dir))
		if next == "." || next == "/" || next == dir {
			break
		}
		dir = next
	}
	return "."
}

func bundledSkillPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "skills", "code-review")
		if _, err := os.Stat(filepath.Join(candidate, "SKILL.md")); err == nil {
			return candidate, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return "", fmt.Errorf("bundled code-review skill not found")
}

func gitDiff(repoPath string, files []string) ([]byte, error) {
	args := []string{"-C", repoPath, "diff", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", "HEAD", "--"}
	for _, file := range files {
		args = append(args, ":(literal)"+file)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_EXTERNAL_DIFF=", "GIT_PAGER=cat"}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff HEAD: %w", err)
	}
	return out, nil
}

func fixturePath(name string) string {
	dir, err := os.Getwd()
	if err != nil {
		return filepath.Join("testdata", "fixtures", name+".diff")
	}
	for {
		candidate := filepath.Join(dir, "testdata", "fixtures", name+".diff")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return filepath.Join("testdata", "fixtures", name+".diff")
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
