//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the entry point for the code review agent example.
//
// main wires the deterministic review pipeline together: it parses CLI flags,
// loads review input from one of four sources, runs the rule engine, executes
// sandboxed static checks (go vet / staticcheck, plus go test when not in
// dry-run), persists the full TaskReport to SQLite, and writes JSON + Markdown
// reports. The pipeline is split into small helpers (loadInput, runRules,
// runSandboxChecks, buildAndPersistReport, printSummary) so that each function
// stays well under the cyclomatic-complexity budget. Interrupts (Ctrl-C) cancel
// the context threaded through every long-running operation; deferred Close
// calls release the store and sandbox workspace orderly.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/astrules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diagparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/fakellm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/inputsource"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/permission"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// cliFlags holds the resolved command-line flags for the code review agent.
type cliFlags struct {
	diffFile    string
	repoPath    string
	fileList    string
	fixtureDir  string
	outDir      string
	dbPath      string
	executor    string
	unsafeLocal bool
	// unsafeAllowE2BNetwork opts in to the e2b backend despite it having
	// network access (the SDK exposes no option to disable it). Fail-closed
	// by default so reviewers do not accidentally run untrusted `go test`
	// against a network-enabled sandbox.
	unsafeAllowE2BNetwork bool
	dryRun                bool
	model                 string
	// PR metadata embedded in the report header so CI-generated reports
	// carry the context reviewers need without opening the diff. Borrowed
	// from competitor PR #2090.
	prTitle  string
	prAuthor string
	prBranch string
	// containerBaseImage overrides the sandbox base image. Useful when the
	// default golang image is unreachable (e.g. China CI behind a mirror).
	// Borrowed from competitor PR #2243.
	containerBaseImage string
}

// parseFlags builds a FlagSet from the provided args and returns the resolved
// configuration. It uses flag.ContinueOnError so callers can control the exit
// behavior on parse failures.
func parseFlags(args []string) (*cliFlags, error) {
	fs := flag.NewFlagSet("code-review-agent", flag.ContinueOnError)
	f := &cliFlags{}
	fs.StringVar(&f.diffFile, "diff-file", "", "path to a unified diff file to review")
	fs.StringVar(&f.repoPath, "repo-path", "", "path to the repository under review")
	fs.StringVar(&f.fileList, "file-list", "", "path to a newline-separated list of files to review")
	fs.StringVar(&f.fixtureDir, "fixture-dir", "", "path to a fixture directory used for dry-run inputs")
	fs.StringVar(&f.outDir, "out-dir", "./out", "directory where review artifacts are written")
	fs.StringVar(&f.dbPath, "db-path", "./review.db", "path to the SQLite database used for persistence")
	fs.StringVar(&f.executor, "executor", "container", "sandbox executor backend: container|e2b|local")
	fs.BoolVar(&f.unsafeLocal, "unsafe-local", false, "allow the unsafe local executor (fail-closed by default)")
	fs.BoolVar(&f.unsafeAllowE2BNetwork, "unsafe-allow-e2b-network", false, "allow the e2b backend despite it having network access (fail-closed by default)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "parse inputs and plan the review without executing sandboxed tools")
	fs.StringVar(&f.model, "model", "deepseek-v4-flash", "LLM model identifier: 'fake' uses a deterministic API-key-free fake model suitable for CI; the default runs rule-based review only")
	// PR metadata (borrowed from competitor PR #2090): embedded in the
	// report header so CI-generated reports carry reviewer context.
	fs.StringVar(&f.prTitle, "pr-title", "", "PR title to embed in the report header (optional, CI metadata)")
	fs.StringVar(&f.prAuthor, "pr-author", "", "PR author to embed in the report header (optional, CI metadata)")
	fs.StringVar(&f.prBranch, "pr-branch", "", "PR branch name to embed in the report header (optional, CI metadata)")
	// Container base image override (borrowed from competitor PR #2243):
	// lets reviewers behind a Docker mirror use a regional image.
	fs.StringVar(&f.containerBaseImage, "container-base-image", "", "override the sandbox container base image (e.g. docker.m.daocloud.io/library/golang:1.23-bookworm)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return f, nil
}

// pipelineOpts bundles the resolved CLI flags with run-derived configuration
// (the pipeline start time) and is threaded through the pipeline helpers.
type pipelineOpts struct {
	cliFlags
	startTime time.Time
}

// sandboxRunRecord pairs a sandbox.RunResult with the command that produced it.
// RunResult does not carry the originating command, so this pairing is needed
// to persist store.SandboxRun rows (whose command column is NOT NULL).
type sandboxRunRecord struct {
	command string
	result  sandbox.RunResult
}

func main() {
	// Construct a cancellable context that is cancelled on os.Interrupt (Ctrl-C).
	// The pipeline threads this context through the input, sandbox and storage
	// layers so interrupts trigger orderly cleanup via deferred Close calls.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	f, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to parse flags:", err)
		os.Exit(2)
	}

	opts := &pipelineOpts{cliFlags: *f, startTime: time.Now()}
	if err := runPipeline(ctx, opts); err != nil {
		log.Printf("pipeline failed: %v", err)
		os.Exit(1)
	}
}

// runPipeline drives the full code review pipeline. It initialises the store,
// loads input, runs rules, executes sandboxed static checks, builds the report,
// and persists everything to the database. On any step failure it logs the
// error, attempts to persist a partial report with conclusion
// needs_human_review via a deferred handler, and returns the error.
func runPipeline(ctx context.Context, opts *pipelineOpts) (retErr error) {
	st := store.New(opts.dbPath)
	defer st.Close()
	// Ensure the database file's parent directory exists so a fresh out-dir
	// (e.g. db-path under a not-yet-created --out-dir) does not cause the
	// SQLite open to fail with SQLITE_CANTOPEN.
	if parent := filepath.Dir(opts.dbPath); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create db dir: %w", err)
		}
	}
	if err := st.Init(ctx); err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	taskID := store.NewTaskID(opts.repoPath)
	log.Printf("task %s starting (dry-run=%v, executor=%s, model=%s)", taskID, opts.dryRun, opts.executor, opts.model)
	// The --model flag selects the LLM augmentation strategy:
	//   "" | "deepseek-v4-flash"  — rule-based only (default; no LLM call)
	//   "fake"                    — deterministic fake LLM (no API keys; CI)
	//   any other value           — reserved for real LLM integration (Phase 3+)
	if opts.model != "" && opts.model != "deepseek-v4-flash" && opts.model != "fake" {
		log.Printf("warning: --model %q is not yet wired into the pipeline; review is rule-based only", opts.model)
	}

	metrics := telemetry.New()

	// Accumulated pipeline state, consumed by the deferred partial-report
	// handler when a step fails. Each step assigns its result here as it
	// completes so the handler can persist whatever was collected.
	var (
		input         *inputsource.Input
		rev           *review.Report
		runRecords    []sandboxRunRecord
		permDecisions []store.PermissionDecision
	)
	defer func() {
		if retErr == nil {
			return
		}
		log.Printf("persisting partial report for task %s after failure: %v", taskID, retErr)
		// Use a non-cancelled context so SaveTaskReport can complete even
		// after the pipeline ctx was cancelled (e.g. by Ctrl-C).
		persistCtx := context.WithoutCancel(ctx)
		if _, _, _, perr := buildAndPersistReport(
			persistCtx, st, opts, taskID, input, rev,
			runRecords, permDecisions, metrics,
			string(report.ConclusionNeedsReview),
		); perr != nil {
			log.Printf("partial report persist failed: %v", perr)
		}
	}()

	in, err := loadInput(ctx, opts)
	if err != nil {
		return fmt.Errorf("load input: %w", err)
	}
	input = in
	log.Printf("loaded %d diff file(s) from %s", len(input.Files), input.Source)

	// Run rules / fake LLM on the original parsed content so secret-detection
	// rules (SI-001) and LLM heuristics (LLM-001) still fire. Immediately scrub
	// finding Evidence/Recommendation, then redact DiffText so no later sink
	// retains plaintext secrets.
	ruleFindings, revReport := runRules(taskID, input, metrics, opts.model)
	scrubReviewSecrets(revReport)
	for i := range ruleFindings {
		e, r, _ := redact.FindingFields(ruleFindings[i].Evidence, ruleFindings[i].Recommendation)
		ruleFindings[i].Evidence = e
		ruleFindings[i].Recommendation = r
	}
	rev = revReport
	if redacted, n := redact.DiffText(input.DiffText); n > 0 {
		input.DiffText = redacted
		log.Printf("redacted %d sensitive occurrence(s) in diff text", n)
	}
	log.Printf("rules: %d confirmed finding(s), %d warning(s), %d need human review",
		len(rev.Findings), len(rev.Warnings), len(rev.NeedsHumanReview))

	// Load the code-review Skill so that sandbox commands are sourced from
	// the skill's scripts rather than hardcoded in the pipeline. The skill
	// repository scans the skills/ directory; Get returns the SKILL.md body
	// and Docs (rule metadata). The scripts themselves are executed inside
	// the sandbox via the skill directory path.
	skillsRoot := resolveSkillsDir()
	skillRepo, err := skill.NewFSRepository(skillsRoot)
	if err != nil {
		log.Printf("warning: failed to load skill repository from %q: %v (falling back to built-in commands)", skillsRoot, err)
	}
	var skillDir string
	if skillRepo != nil {
		sk, gerr := skillRepo.Get("code-review")
		if gerr != nil || sk == nil {
			log.Printf("warning: code-review skill not found: %v (falling back to built-in commands)", gerr)
		} else {
			log.Printf("loaded skill %q: %s", sk.Summary.Name, sk.Summary.Description)
			skillDir, _ = skillRepo.Path("code-review")
		}
	}

	policy := permission.NewPolicy(nil)
	runs, perms, err := runSandboxChecks(ctx, opts, taskID, policy, metrics, skillDir)
	if err != nil {
		return err
	}
	runRecords = runs
	permDecisions = perms

	// Phase 3.3: parse go vet / staticcheck output from sandbox runs into
	// structured findings. The diag findings are merged with the rule
	// findings and the report is re-aggregated so the diagnostics appear
	// as first-class findings (DIAG-001 / DIAG-002) in the report.
	// Borrowed from competitor PR #2243.
	if diagFindings := parseDiagFindings(runRecords); len(diagFindings) > 0 {
		for _, f := range diagFindings {
			metrics.IncFinding(f.Severity)
		}
		merged := make([]rules.Finding, 0, len(ruleFindings)+len(diagFindings))
		merged = append(merged, ruleFindings...)
		merged = append(merged, diagFindings...)
		rev = review.Build(taskID, merged)
		log.Printf("diag: %d diagnostic finding(s) merged (total: %d confirmed, %d warning, %d need human review)",
			len(diagFindings), len(rev.Findings), len(rev.Warnings), len(rev.NeedsHumanReview))
	}

	rd, jsonPath, mdPath, err := buildAndPersistReport(
		ctx, st, opts, taskID, input, rev,
		runRecords, permDecisions, metrics, "")
	if err != nil {
		return err
	}
	printSummary(rd, jsonPath, mdPath)
	return nil
}

// loadInput selects the input source from the resolved flags and loads it.
// Exactly one of --fixture-dir, --diff-file, --file-list or --repo-path must
// be set; passing multiple is rejected so a stale CLI invocation never
// silently picks one source and ignores the others.
//
// --repo-path is overloaded: it is a standalone source AND the path anchor
// for --file-list. When --file-list is set, --repo-path is consumed as its
// anchor and does not count as a separate source, so the combination
// `--file-list=x --repo-path=/r` is valid (exactly one source: file-list).
// Every other combination of two or more sources is rejected.
func loadInput(ctx context.Context, opts *pipelineOpts) (*inputsource.Input, error) {
	fileListSet := opts.fileList != ""
	repoPathSet := opts.repoPath != ""

	var sources []string
	if opts.fixtureDir != "" {
		sources = append(sources, "--fixture-dir")
	}
	if opts.diffFile != "" {
		sources = append(sources, "--diff-file")
	}
	if fileListSet {
		sources = append(sources, "--file-list")
	}
	// When --file-list is set, --repo-path is its anchor, not a separate
	// source; only count it as a source when there is no --file-list.
	if repoPathSet && !fileListSet {
		sources = append(sources, "--repo-path")
	}

	switch len(sources) {
	case 0:
		return nil, errors.New("no input source specified (use --fixture-dir, --diff-file, --file-list or --repo-path)")
	case 1:
		// exactly one source — ok
	default:
		return nil, fmt.Errorf("conflicting input sources: %s (specify exactly one; --repo-path may anchor --file-list)",
			strings.Join(sources, ", "))
	}

	// --file-list requires --repo-path as its anchor (not counted above).
	if fileListSet && !repoPathSet {
		return nil, errors.New("--file-list requires --repo-path to anchor file paths")
	}

	switch {
	case opts.fixtureDir != "":
		return inputsource.Load(ctx, inputsource.SourceFixtureDir, opts.fixtureDir)
	case opts.diffFile != "":
		return inputsource.Load(ctx, inputsource.SourceDiffFile, opts.diffFile)
	case opts.fileList != "":
		return inputsource.Load(ctx, inputsource.SourceFileList, opts.fileList, opts.repoPath)
	default:
		return inputsource.Load(ctx, inputsource.SourceRepoPath, opts.repoPath)
	}
}

// runRules executes the regex rule engine and the AST rule engine against the
// parsed files, records per-finding telemetry, and aggregates the findings
// into a review.Report. AST rules run on newly added files only (OldPath ==
// "/dev/null") because that is the only case where the added lines form a
// complete, parseable Go source file.
//
// When modelFlag is "fake", the deterministic fake LLM (fakellm.FakeModel)
// is invoked with the diff text and its findings are merged with the rule
// findings before report aggregation. This exercises the LLM integration
// path end-to-end without requiring API keys. Borrowed from competitor
// PR #2243's --fake-model flag.
//
// The function returns both the raw rules.Finding slice and the aggregated
// review.Report. The raw findings are returned so the caller can merge in
// diagnostic findings from sandbox output (Phase 3.3) and re-aggregate the
// report via review.Build without losing the rule findings.
func runRules(taskID string, input *inputsource.Input, metrics *telemetry.Metrics, modelFlag string) ([]rules.Finding, *review.Report) {
	engine := rules.NewEngine()
	ruleFindings := engine.Run(input.Files)
	astEngine := astrules.NewEngine()
	astFindings := astEngine.Run(input.Files)
	ruleFindings = append(ruleFindings, astFindings...)

	if modelFlag == "fake" {
		llmFindings := runFakeLLM(input)
		ruleFindings = append(ruleFindings, llmFindings...)
	}

	for _, f := range ruleFindings {
		metrics.IncFinding(f.Severity)
	}
	return ruleFindings, review.Build(taskID, ruleFindings)
}

// runFakeLLM drives the deterministic fake LLM against the diff text and
// converts its JSON response into rules.Finding values tagged with
// Source="llm:<RuleID>". The fake model is API-key-free and produces
// deterministic output, so this function is safe to call from CI. Errors
// from the fake model are logged and swallowed: the fake never fails in
// practice, and a failure here should not break the rule-based review.
func runFakeLLM(input *inputsource.Input) []rules.Finding {
	m := fakellm.New()
	req := model.NewRequest([]model.Message{
		model.NewUserMessage(input.DiffText),
	})
	ch, err := m.GenerateContent(context.Background(), req)
	if err != nil {
		log.Printf("fake llm: generate failed: %v (skipping LLM findings)", err)
		return nil
	}
	var content string
	for r := range ch {
		if r == nil || len(r.Choices) == 0 {
			continue
		}
		content += r.Choices[0].Message.Content
	}
	parsed := fakellm.ParseFindings(content)
	out := make([]rules.Finding, 0, len(parsed))
	for _, f := range parsed {
		out = append(out, rules.Finding{
			RuleID:         f.RuleID,
			Severity:       f.Severity,
			Category:       f.Category,
			File:           f.File,
			Line:           f.Line,
			Title:          f.Title,
			Evidence:       f.Evidence,
			Recommendation: f.Recommendation,
			Confidence:     f.Confidence,
			Source:         "llm:" + f.RuleID,
		})
	}
	log.Printf("fake llm: %d finding(s) emitted", len(out))
	return out
}

// parseDiagFindings converts the stdout/stderr of sandboxed go vet and
// staticcheck runs into structured rules.Finding values via the
// diagparse package. Skipped and empty runs produce no findings. The
// returned findings are tagged with Source="diag:DIAG-xxx" and merged
// with rule findings in the pipeline so tool diagnostics surface as
// first-class findings in the report. Borrowed from competitor PR #2243.
func parseDiagFindings(records []sandboxRunRecord) []rules.Finding {
	if len(records) == 0 {
		return nil
	}
	inputs := make([]diagparse.RunInput, 0, len(records))
	for _, r := range records {
		// Skipped runs (no repo staged) and empty-output runs contribute
		// nothing — skip them to avoid handing the parser empty bytes.
		if r.result.Status == sandbox.StatusSkipped {
			continue
		}
		inputs = append(inputs, diagparse.RunInput{
			Command: r.command,
			Stdout:  r.result.Stdout,
			Stderr:  r.result.Stderr,
		})
	}
	if len(inputs) == 0 {
		return nil
	}
	return diagparse.FromRuns(inputs)
}

// resolveSkillsDir resolves the skills/ directory path. It checks the
// SKILLS_ROOT env var first, then falls back to ./skills relative to the
// current working directory. Returns "" if not found (the pipeline falls
// back to built-in commands).
func resolveSkillsDir() string {
	if root := os.Getenv("SKILLS_ROOT"); root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			if abs, err := filepath.Abs(root); err == nil {
				return abs
			}
			return root
		}
	}
	if info, err := os.Stat("skills"); err == nil && info.IsDir() {
		if abs, err := filepath.Abs("skills"); err == nil {
			return abs
		}
		return "skills"
	}
	return ""
}

// runSandboxChecks initialises the sandbox, plans the static-check commands,
// applies the permission policy to each, and executes the allowed ones. In
// dry-run mode it keeps `go vet` and `staticcheck` but skips `go test`.
//
// When skillDir is non-empty, the skill's shell scripts (run_go_vet.sh,
// run_staticcheck.sh, run_go_unit.sh) are staged read-only into the workspace
// at sandbox.SkillStageDir and executed instead of bare `go vet`/`staticcheck`/
// `go test`, so the example exercises the Skill + sandbox integration requested
// by the issue.
//
// The workspace lifecycle (create + close) is owned by this function so the
// workspace never outlives the sandbox checks. Sandbox construction failures
// are fail-closed in normal mode and best-effort skipped in dry-run mode.
//
// When opts.repoPath is empty (diff-only / fixture / file-list mode) the
// static checks are skipped entirely: go vet, staticcheck and go test all
// require a staged Go repository, and running them against an empty
// workspace only produces noise (failed runs that force the conclusion to
// needs_human_review without surfacing any real issue). A single skipped
// record is returned so the report is transparent about why sandbox checks
// did not run.
func runSandboxChecks(ctx context.Context, opts *pipelineOpts, taskID string, policy *permission.Policy, metrics *telemetry.Metrics, skillDir string) ([]sandboxRunRecord, []store.PermissionDecision, error) {
	if opts.repoPath == "" {
		log.Printf("sandbox checks skipped: no repo staged (diff-only/fixture/file-list mode)")
		return []sandboxRunRecord{{
			command: "go-vet+staticcheck+go-test",
			result:  sandbox.RunResult{Status: sandbox.StatusSkipped, ExitCode: 0, Stdout: nil, Stderr: nil},
		}}, nil, nil
	}

	sbCfg := sandbox.Config{
		Backend:               backendFromFlag(opts.executor),
		UnsafeLocal:           opts.unsafeLocal,
		UnsafeAllowE2BNetwork: opts.unsafeAllowE2BNetwork,
		RepoPath:              opts.repoPath,
		Timeout:               120 * time.Second,
		MaxStdoutBytes:        1 << 20,
		MaxStderrBytes:        1 << 20,
		ContainerBaseImage:    opts.containerBaseImage,
	}
	sb, err := sandbox.New(sbCfg)
	if err != nil {
		return handleSandboxInitFailure(opts, err)
	}

	ws, err := sb.CreateWorkspace(ctx)
	if err != nil {
		return handleSandboxInitFailure(opts, fmt.Errorf("create workspace: %w", err))
	}
	defer sb.Close(context.WithoutCancel(ctx), ws)

	// Stage the skill scripts into the workspace so they are visible inside
	// the sandbox filesystem (container/e2b backends do not share the host
	// filesystem). Staging is read-only to prevent sandbox commands from
	// modifying the skill definition.
	//
	// Skill scripts are only executed when a repo is staged: the scripts
	// cd into $WORKSPACE_DIR/repo, which only exists when RepoPath is set.
	// In diff-only/fixture mode there is no repo to vet, so we fall back
	// to built-in commands (which are harmless no-ops without a repo Cwd).
	useSkillScripts := false
	if skillDir != "" && opts.repoPath != "" {
		if serr := sb.StageDirectory(ctx, ws, skillDir, sandbox.SkillStageDir, true); serr != nil {
			return handleSandboxInitFailure(opts, fmt.Errorf("stage skill dir: %w", serr))
		}
		useSkillScripts = true
	}

	return executeSandboxCommands(ctx, sb, ws, opts, taskID, policy, metrics, useSkillScripts)
}

// handleSandboxInitFailure applies the dry-run vs fail-closed policy when the
// sandbox cannot be constructed or a workspace cannot be created. In dry-run
// mode a single StatusSkipped sandbox_run is recorded and a nil error is
// returned so the pipeline can still produce a report without forcing
// needs_human_review on an otherwise clean dry-run; otherwise the error is
// returned (fail-closed).
func handleSandboxInitFailure(opts *pipelineOpts, err error) ([]sandboxRunRecord, []store.PermissionDecision, error) {
	if opts.dryRun {
		log.Printf("warning: sandbox unavailable in dry-run, skipping: %v", err)
		records := []sandboxRunRecord{{
			command: "sandbox-init",
			result: sandbox.RunResult{
				Status:   sandbox.StatusSkipped,
				ExitCode: 0,
				Stderr:   []byte(err.Error()),
			},
		}}
		return records, nil, nil
	}
	return nil, nil, err
}

// executeSandboxCommands runs each planned command through the permission
// policy and sandbox executor, recording permission decisions, run results,
// and telemetry. Blocked commands are skipped; allowed commands are executed.
// When useSkillScripts is true, commands are sourced from the skill's scripts
// which have been staged into the workspace.
func executeSandboxCommands(
	ctx context.Context,
	sb *sandbox.Executor,
	ws codeexecutor.Workspace,
	opts *pipelineOpts,
	taskID string,
	policy *permission.Policy,
	metrics *telemetry.Metrics,
	useSkillScripts bool,
) ([]sandboxRunRecord, []store.PermissionDecision, error) {
	var records []sandboxRunRecord
	var perms []store.PermissionDecision
	var totalDuration time.Duration

	for _, spec := range planSandboxCommands(opts, useSkillScripts) {
		cmd := spec.Cmd + " " + strings.Join(spec.Args, " ")
		dec, reason := policy.CheckNonInteractive(cmd)
		// Redact command text before persistence so any accidental secret in
		// a planned argv never lands in SQLite / reports.
		safeCmd, _ := redact.CommandText(cmd)
		perms = append(perms, store.PermissionDecision{
			TaskID:    taskID,
			Command:   safeCmd,
			Action:    string(dec.Action),
			Reason:    reason,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if dec.Action != "allow" {
			metrics.IncPermissionBlocked()
			log.Printf("permission blocked %q: %s", safeCmd, reason)
			continue
		}
		result, runErr := sb.Run(ctx, ws, spec)
		if runErr != nil {
			result = sandbox.RunResult{Status: sandbox.StatusFailed, ExitCode: -1, Stderr: []byte(runErr.Error())}
		}
		records = append(records, sandboxRunRecord{command: safeCmd, result: result})
		metrics.IncToolCalls()
		totalDuration += result.Duration
		if result.Status == sandbox.StatusTimeout {
			metrics.IncException("sandbox_timeout")
		} else if result.Status == sandbox.StatusFailed {
			metrics.IncException("sandbox_failed")
		}
		log.Printf("sandbox %q -> status=%s exit=%d duration=%s",
			cmd, result.Status, result.ExitCode, result.Duration)
	}
	metrics.RecordSandboxDuration(totalDuration)
	return records, perms, nil
}

// planSandboxCommands returns the sandbox commands to run. When useSkillScripts
// is true, the skill's POSIX shell scripts (already staged into the workspace
// at sandbox.SkillStageDir) are used instead of bare `go vet`/`staticcheck`/
// `go test`, so the example exercises the Skill + sandbox integration.
// `go vet` and `staticcheck` scripts always run; the `go test` script is
// skipped in dry-run mode to keep the run under two minutes.
//
// Skill scripts run with Cwd="" (workspace root) because the repo is staged
// read-only — the scripts use $WORKSPACE_DIR/repo to cd into the repo and
// $WORKSPACE_DIR/out for writable output. Non-skill commands use Cwd="repo"
// directly.
func planSandboxCommands(opts *pipelineOpts, useSkillScripts bool) []sandbox.RunSpec {
	repoCwd := ""
	if opts.repoPath != "" {
		repoCwd = "repo"
	}
	if useSkillScripts {
		scriptRel := sandbox.SkillStageDir + "/scripts"
		specs := []sandbox.RunSpec{
			{Cmd: "sh", Args: []string{scriptRel + "/run_go_vet.sh"}, Cwd: ""},
			{Cmd: "sh", Args: []string{scriptRel + "/run_staticcheck.sh"}, Cwd: ""},
		}
		if !opts.dryRun {
			specs = append(specs, sandbox.RunSpec{Cmd: "sh", Args: []string{scriptRel + "/run_go_unit.sh"}, Cwd: ""})
		}
		return specs
	}
	specs := []sandbox.RunSpec{
		{Cmd: "go", Args: []string{"vet", "./..."}, Cwd: repoCwd},
		{Cmd: "staticcheck", Args: []string{"./..."}, Cwd: repoCwd},
	}
	if !opts.dryRun {
		specs = append(specs, sandbox.RunSpec{Cmd: "go", Args: []string{"test", "-count=1", "./..."}, Cwd: repoCwd})
	}
	return specs
}

// backendFromFlag maps the --executor flag value to a sandbox.Backend. Unknown
// values default to the container backend.
func backendFromFlag(s string) sandbox.Backend {
	switch s {
	case "container":
		return sandbox.BackendContainer
	case "e2b":
		return sandbox.BackendE2B
	case "local":
		return sandbox.BackendLocal
	default:
		return sandbox.BackendContainer
	}
}

// buildAndPersistReport aggregates the pipeline state into a ReportData, writes
// the JSON and Markdown reports, and persists a TaskReport to the store. When
// conclusionOverride is non-empty it forces the conclusion (used for partial
// reports after a step failure) and marks the task status as "failed" so the
// database distinguishes partial reports from completed ones. It returns the
// ReportData and report paths.
func buildAndPersistReport(
	ctx context.Context,
	st store.Store,
	opts *pipelineOpts,
	taskID string,
	input *inputsource.Input,
	rev *review.Report,
	runRecords []sandboxRunRecord,
	permDecisions []store.PermissionDecision,
	metrics *telemetry.Metrics,
	conclusionOverride string,
) (*report.ReportData, string, string, error) {
	metrics.RecordTotalDuration(time.Since(opts.startTime))
	summary := metrics.GetSummary()

	// report.Build panics on a nil review, so substitute an empty report when
	// the rules step never ran (partial report after an early failure).
	revReport := rev
	if revReport == nil {
		revReport = &review.Report{TaskID: taskID}
	}
	rd := report.Build(taskID, revReport, toRunResults(runRecords), permDecisions, nil, summary, report.PRMetadata{
		Title:  opts.prTitle,
		Author: opts.prAuthor,
		Branch: opts.prBranch,
	})
	if conclusionOverride != "" {
		rd.Conclusion = report.Conclusion(conclusionOverride)
	}

	// Write reports first, then build Artifacts with real SizeBytes and
	// rewrite so both on-disk JSON/MD and the DB agree (no SizeBytes:0 lie).
	now := time.Now().UTC().Format(time.RFC3339)
	// Temporary empty artifacts so WriteAll can create the files.
	rd.Artifacts = nil
	jsonPath, mdPath, err := rd.WriteAll(opts.outDir)
	if err != nil {
		return nil, "", "", fmt.Errorf("write reports: %w", err)
	}
	artifacts := buildArtifacts(taskID, jsonPath, mdPath, now)
	rd.Artifacts = artifacts
	// Rewrite with real artifact sizes so on-disk reports match DB.
	if _, _, err := rd.WriteAll(opts.outDir); err != nil {
		return nil, "", "", fmt.Errorf("rewrite reports with artifact sizes: %w", err)
	}
	// Sizes may have grown by a few bytes after embedding Artifacts; refresh.
	artifacts = buildArtifacts(taskID, jsonPath, mdPath, now)
	rd.Artifacts = artifacts

	// Derive the task status: a non-empty conclusionOverride means this is a
	// partial report written after a step failure, so the task is "failed"
	// rather than "completed". This keeps the database honest about which
	// runs actually finished the full pipeline.
	taskStatus := "completed"
	if conclusionOverride != "" {
		taskStatus = "failed"
	}
	taskReport := buildTaskReport(taskID, opts, input, rd, runRecords, permDecisions, artifacts, summary, jsonPath, mdPath, now, taskStatus)
	if err := st.SaveTaskReport(ctx, taskReport); err != nil {
		return rd, jsonPath, mdPath, fmt.Errorf("save task report: %w", err)
	}
	return rd, jsonPath, mdPath, nil
}

// buildTaskReport assembles the store.TaskReport aggregate from the pipeline
// outputs. now is the shared creation timestamp for all child rows. status
// is "completed" for a full run or "failed" for a partial report written
// after a step failure.
func buildTaskReport(
	taskID string,
	opts *pipelineOpts,
	input *inputsource.Input,
	rd *report.ReportData,
	runRecords []sandboxRunRecord,
	permDecisions []store.PermissionDecision,
	artifacts []store.Artifact,
	summary telemetry.Summary,
	jsonPath, mdPath, now, status string,
) store.TaskReport {
	diffSource := ""
	if input != nil {
		diffSource = string(input.Source)
	}
	// Persist confirmed + warnings + needs-human-review so audits can
	// reconstruct why a conclusion became needs_human_review from DB alone.
	allFindings := make([]review.Finding, 0,
		len(rd.Review.Findings)+len(rd.Review.Warnings)+len(rd.Review.NeedsHumanReview))
	allFindings = append(allFindings, rd.Review.Findings...)
	for _, w := range rd.Review.Warnings {
		allFindings = append(allFindings, w.Finding)
	}
	allFindings = append(allFindings, rd.Review.NeedsHumanReview...)

	return store.TaskReport{
		Task: store.ReviewTask{
			TaskID:            taskID,
			CreatedAt:         now,
			RepoPath:          opts.repoPath,
			DiffSource:        diffSource,
			Status:            status,
			Conclusion:        string(rd.Conclusion),
			TotalDurationMs:   int64(summary.TotalDuration.Milliseconds()),
			SandboxDurationMs: int64(summary.SandboxDuration.Milliseconds()),
		},
		Findings:    toStoreFindings(allFindings, taskID, now),
		SandboxRuns: toStoreSandboxRuns(runRecords, taskID, now),
		Permissions: permDecisions,
		Artifacts:   artifacts,
		Report: store.ReportRow{
			TaskID:       taskID,
			JSONPath:     jsonPath,
			MarkdownPath: mdPath,
			CreatedAt:    now,
		},
		Metrics: toStoreMetrics(taskID, summary, now),
	}
}

// toRunResults extracts the bare RunResult slice from the paired records.
func toRunResults(records []sandboxRunRecord) []sandbox.RunResult {
	out := make([]sandbox.RunResult, 0, len(records))
	for _, r := range records {
		out = append(out, r.result)
	}
	return out
}

// toStoreFindings converts review findings to the persistence model.
func toStoreFindings(findings []review.Finding, taskID, now string) []store.Finding {
	out := make([]store.Finding, 0, len(findings))
	for _, f := range findings {
		out = append(out, store.Finding{
			TaskID:         taskID,
			Severity:       f.Severity,
			Category:       f.Category,
			File:           f.File,
			Line:           f.Line,
			Title:          f.Title,
			Evidence:       f.Evidence,
			Recommendation: f.Recommendation,
			Confidence:     f.Confidence,
			Source:         f.Source,
			RuleID:         f.RuleID,
			Fingerprint:    f.Fingerprint,
			CreatedAt:      now,
		})
	}
	return out
}

// toStoreSandboxRuns converts the paired run records to the persistence model,
// preserving NULL semantics for exit code and output streams.
func toStoreSandboxRuns(records []sandboxRunRecord, taskID, now string) []store.SandboxRun {
	out := make([]store.SandboxRun, 0, len(records))
	for _, r := range records {
		out = append(out, toStoreSandboxRun(r, taskID, now))
	}
	return out
}

// toStoreSandboxRun converts a single record. ExitCode is NULL when the
// sandbox reported an infrastructure error (sentinel -1); stdout/stderr are
// NULL when empty, mirroring the nullable columns in schema.sql.
func toStoreSandboxRun(r sandboxRunRecord, taskID, now string) store.SandboxRun {
	sr := store.SandboxRun{
		TaskID:     taskID,
		Command:    r.command,
		Status:     r.result.Status,
		DurationMs: r.result.Duration.Milliseconds(),
		TimedOut:   r.result.TimedOut,
		Truncated:  r.result.Truncated,
		CreatedAt:  now,
	}
	if r.result.ExitCode >= 0 {
		sr.ExitCode = sql.NullInt64{Int64: int64(r.result.ExitCode), Valid: true}
	} else {
		sr.ExitCode = sql.NullInt64{Valid: false}
	}
	if len(r.result.Stdout) > 0 {
		sr.Stdout = sql.NullString{String: string(r.result.Stdout), Valid: true}
	}
	if len(r.result.Stderr) > 0 {
		sr.Stderr = sql.NullString{String: string(r.result.Stderr), Valid: true}
	}
	return sr
}

// buildArtifacts constructs the two report-file artifact rows (JSON + Markdown).
// The artifact Name is derived from the actual file path's base so it matches
// the per-task filename produced by report.ReportData.reportFileName.
func buildArtifacts(taskID, jsonPath, mdPath, now string) []store.Artifact {
	return []store.Artifact{
		{TaskID: taskID, Name: filepath.Base(jsonPath), Path: jsonPath, SizeBytes: fileSize(jsonPath), CreatedAt: now},
		{TaskID: taskID, Name: filepath.Base(mdPath), Path: mdPath, SizeBytes: fileSize(mdPath), CreatedAt: now},
	}
}

// fileSize returns the size of the file at path, or 0 if it cannot be stat'd.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// toStoreMetrics maps the telemetry summary to the persistence model.
func toStoreMetrics(taskID string, summary telemetry.Summary, now string) store.TelemetryMetrics {
	return store.TelemetryMetrics{
		TaskID:                 taskID,
		TotalDurationMs:        int64(summary.TotalDuration.Milliseconds()),
		SandboxDurationMs:      int64(summary.SandboxDuration.Milliseconds()),
		ToolCalls:              summary.ToolCalls,
		PermissionBlockedCount: summary.PermissionBlocked,
		FindingCount:           summary.FindingCount,
		SeverityCritical:       summary.SeverityCounts["critical"],
		SeverityHigh:           summary.SeverityCounts["high"],
		SeverityMedium:         summary.SeverityCounts["medium"],
		SeverityLow:            summary.SeverityCounts["low"],
		CreatedAt:              now,
	}
}

// printSummary logs the review conclusion, finding counts and report paths.
func printSummary(rd *report.ReportData, jsonPath, mdPath string) {
	log.Printf("conclusion: %s", string(rd.Conclusion))
	log.Printf("findings: %d confirmed, %d warnings, %d need human review",
		rd.TotalFindings, rd.TotalWarnings, rd.NeedsHumanReview)
	log.Printf("blocked permissions: %d", rd.PermissionBlocked)
	log.Printf("report (json):     %s", jsonPath)
	log.Printf("report (markdown): %s", mdPath)
}

// scrubReviewSecrets redacts Evidence/Recommendation on every finding
// bucket in place so intermediate pipeline state never retains secrets.
func scrubReviewSecrets(rev *review.Report) {
	if rev == nil {
		return
	}
	for i := range rev.Findings {
		rev.Findings[i].Evidence = redact.MustText(rev.Findings[i].Evidence)
		rev.Findings[i].Recommendation = redact.MustText(rev.Findings[i].Recommendation)
	}
	for i := range rev.Warnings {
		rev.Warnings[i].Finding.Evidence = redact.MustText(rev.Warnings[i].Finding.Evidence)
		rev.Warnings[i].Finding.Recommendation = redact.MustText(rev.Warnings[i].Finding.Recommendation)
	}
	for i := range rev.NeedsHumanReview {
		rev.NeedsHumanReview[i].Evidence = redact.MustText(rev.NeedsHumanReview[i].Evidence)
		rev.NeedsHumanReview[i].Recommendation = redact.MustText(rev.NeedsHumanReview[i].Recommendation)
	}
}
