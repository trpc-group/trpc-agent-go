//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Code Review Agent CLI — entry point for the tRPC-Agent-Go code review system.
// Uses GraphAgent with 8 nodes in serial topology.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/config"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/graphagent"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/input"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/ruleengine"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/storage"
	storagewriter "github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/storagewriter"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func main() {
	configPath := flag.String("config", "code-review-agent.yaml", "path to YAML config file")
	diffFile := flag.String("diff-file", "", "path to unified diff file")
	diffText := flag.String("diff-text", "", "unified diff text (stdin)")
	repoPath := flag.String("repo-path", "", "git repository path for sandbox execution + git diff")
	baseRef := flag.String("base-ref", "origin/main", "base git ref for repo-path diff comparison")
	outputDir := flag.String("output-dir", "", "output directory for reports")
	mode := flag.String("mode", "", "override mode: live|dry_run")
	flag.Parse()

	ctx := context.Background()

	// 1. Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// CLI flags override config
	if *mode != "" {
		cfg.Mode = *mode
	}
	if *outputDir != "" {
		cfg.Output.Dir = *outputDir
	}

	// Determine input
	diffInput := *diffFile
	inputType := "diff_file"
	if *diffText != "" {
		inputType = "diff_text"
		diffInput = *diffText
	} else if *repoPath != "" {
		inputType = "repo_path"
		diffInput = *repoPath
	}
	if diffInput == "" && inputType == "diff_file" {
		// Fall back to config
		if cfg.Input.Source != "" {
			diffInput = cfg.Input.Source
			inputType = cfg.Input.Type
		}
	}
	if diffInput == "" {
		fmt.Fprintln(os.Stderr, "Error: no diff input. Use --diff-file, --diff-text, or --repo-path.")
		os.Exit(1)
	}

	// Read diff text (from file, stdin, or git repo)
	var diffContent string
	if inputType == "repo_path" && diffInput != "" {
		diffText, err := input.FromRepo(diffInput, *baseRef)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error extracting git diff from %s (base=%s): %v\n", diffInput, *baseRef, err)
			os.Exit(1)
		}
		diffContent = diffText
	} else if inputType == "diff_file" {
		data, err := os.ReadFile(diffInput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading diff file: %v\n", err)
			os.Exit(1)
		}
		diffContent = string(data)
	} else {
		diffContent = diffInput
	}
	diffHash := hashDiff(diffContent)

	// 2. Initialize storage
	var store storage.Storage
	if cfg.Database.Driver == "sqlite" {
		store, err = storage.NewSQLite(cfg.Database.DSN)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
	}
	// Note: PostgreSQL/MySQL supportable via storage.NewPostgres / storage.NewMySQL

	// 3. Create task
	taskID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	task := storage.TaskRow{
		ID:            taskID,
		Status:        "running",
		InputType:     inputType,
		InputSource:   diffInput,
		InputDiffHash: diffHash,
		BaseRef:       cfg.Input.BaseRef,
		ModelMode:     cfg.Mode,
		CreatedAt:     now,
		StartedAt:     now,
	}
	if err := store.CreateTask(ctx, task); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating task: %v\n", err)
		os.Exit(1)
	}

	// 4. Load skill rules
	rules, err := ruleengine.LoadRules(cfg.Skill.Dir, cfg.Skill.RulesGlob)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load rules: %v\n", err)
		rules = []types.Rule{}
	}

	// 5. Build GraphAgent
	sg, err := graphagent.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building graph: %v\n", err)
		os.Exit(1)
	}
	compiled, err := sg.Compile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error compiling graph: %v\n", err)
		os.Exit(1)
	}

	// 6. Prepare initial state
	initialState := graph.State{
		state.StateKeyInputDiffFile: *diffFile,
		state.StateKeyInputDiffText: *diffText,
		state.StateKeyInputRepoPath: *repoPath,
		state.StateKeyInputBaseRef:  cfg.Input.BaseRef,
		state.StateKeyOutputDir:     cfg.Output.Dir,
		state.StateKeyTaskID:        taskID,
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Type:         cfg.Executor.Type,
			TimeoutSec:   cfg.Executor.TimeoutSec,
			MaxOutputMB:  cfg.Executor.MaxOutputMB,
			EnvWhitelist: cfg.Executor.EnvWhitelist,
			Commands:     toSandboxCommands(cfg.Executor.Commands),
		},
		state.StateKeyLLMConfig: types.LLMConfig{
			ModelName:        cfg.LLM.ModelName,
			Temperature:      cfg.LLM.Temperature,
			MaxTokens:        cfg.LLM.MaxTokens,
			SystemPrompt:     cfg.LLM.SystemPromptPath,
			MockMode:         cfg.Mode == "dry_run",
			MockFindingsPath: "testdata/mock_llm_findings.json",
		},
		state.StateKeyDedupConfig: types.DedupConfig{
			ConfidenceThreshold: cfg.Dedup.ConfidenceThreshold,
		},
		state.StateKeySkillRules: rules,
	}

	// Pass storage via context (not state — *sql.DB can't survive deepCopy)
	ctx = storagewriter.WithStorage(ctx, store)

	// 7. Execute
	executor, err := graph.NewExecutor(compiled)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating executor: %v\n", err)
		os.Exit(1)
	}

	invocation := &agent.Invocation{
		InvocationID: taskID,
	}

	// Apply dry-run timeout
	if cfg.Mode == "dry_run" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.DryRunTimeout)
		defer cancel()
	}

	totalStart := time.Now()
	eventChan, err := executor.Execute(ctx, initialState, invocation)
	if err != nil {
		elapsed := time.Since(totalStart)
		fmt.Fprintf(os.Stderr, "Execution error: %v\n", err)
		store.UpdateTask(ctx, taskID, map[string]any{
			"status":            "failed",
			"completed_at":      time.Now().UTC().Format(time.RFC3339),
			"total_duration_ms": elapsed.Milliseconds(),
			"error_message":     err.Error(),
		})
		os.Exit(1)
	}

	// Drain events and collect final state from completion event
	finalState := initialState
	for evt := range eventChan {
		if evt != nil && evt.Response != nil && evt.Response.Done &&
			evt.Response.Object == graph.ObjectTypeGraphExecution {
			// Completion event: StateDelta carries accumulated state as JSON bytes
			if evt.StateDelta != nil {
				decoded := decodeStateDelta(evt.StateDelta, finalState)
				if decoded != nil {
					finalState = decoded
				}
			}
		}
	}

	totalDuration := time.Since(totalStart)

	// Update total duration
	store.UpdateTask(ctx, taskID, map[string]any{
		"total_duration_ms": totalDuration.Milliseconds(),
	})

	// 8. Report results
	jsonPath, _ := finalState[state.StateKeyJSONReportPath].(string)
	mdPath, _ := finalState[state.StateKeyMDReportPath].(string)

	done, _ := finalState[state.StateKeyStorageDone].(bool)

	fmt.Println()
	fmt.Println("══════════════════════════════════════════")
	fmt.Printf("  Code Review Complete\n")
	fmt.Printf("  Task ID:  %s\n", taskID)
	fmt.Printf("  Duration: %v\n", totalDuration.Round(time.Millisecond))
	fmt.Printf("  JSON:     %s\n", jsonPath)
	fmt.Printf("  MD:       %s\n", mdPath)
	fmt.Printf("  DB:       %v\n", done)
	fmt.Println("══════════════════════════════════════════")
}

func hashDiff(content string) string {
	h := sha256.New()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

// decodeStateDelta decodes StateDelta JSON bytes into graph.State, falling back
// to the provided fallback state for keys that fail to decode.
func decodeStateDelta(delta map[string][]byte, fallback graph.State) graph.State {
	out := make(graph.State)
	for k, v := range delta {
		var val any
		if err := json.Unmarshal(v, &val); err == nil {
			out[k] = val
		}
	}
	// Merge fallback keys not present in delta
	for k, v := range fallback {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

func toSandboxCommands(cfgs []config.CommandConfig) []types.SandboxCommand {
	cmds := make([]types.SandboxCommand, len(cfgs))
	for i, c := range cfgs {
		timeout := c.TimeoutSec * 1000
		if timeout == 0 {
			timeout = 30000
		}
		risk := c.RiskLevel
		if risk == "" {
			risk = "low"
		}
		cmds[i] = types.SandboxCommand{
			Name:      c.Name,
			Cmd:       c.Cmd,
			Args:      c.Args,
			Timeout:   timeout,
			RiskLevel: risk,
		}
	}
	return cmds
}
