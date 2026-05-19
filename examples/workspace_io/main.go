//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to mirror workspace files into a
// caller-managed store after an LLMAgent invocation, using nothing but
// codeexecutor/workspaceio.Workspace and a regular AgentCallbacks.
//
// LLMAgent does not ship a dedicated post-invocation flush option —
// the framework leaves the timing, error type, and budget choices to
// the caller. The pattern shown here is just:
//
//  1. Resolve Workspace from ctx.
//  2. Call ws.Collect to enumerate matched files.
//  3. Loop and pass each *workspaceio.File to your sink.
//
// LocalCodeExecutor keeps the example fully self-contained (no Docker,
// no remote sandbox); the "user-level skill store" is a host directory
// under ./skills_store. Real deployments would replace `directorySink`
// with a database / object store / HTTP service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	modelName = flag.String("model", "deepseek-v4-flash", "Name of the model to use")
	storeDir  = flag.String("store", "./skills_store",
		"Host directory used as the user-level skill store")
	prompt = flag.String(
		"prompt",
		"Say a short hello so I can verify the agent finished.",
		"User prompt sent to the agent",
	)
)

func main() {
	flag.Parse()

	absStore, err := filepath.Abs(*storeDir)
	if err != nil {
		log.Fatalf("resolve store dir: %v", err)
	}
	if err := os.MkdirAll(absStore, 0o755); err != nil {
		log.Fatalf("create store dir: %v", err)
	}

	fmt.Printf("Workspace I/O demo\n")
	fmt.Printf("- model:        %s\n", *modelName)
	fmt.Printf("- skill store:  %s\n", absStore)
	fmt.Println(strings.Repeat("=", 60))

	sink := newDirectorySink(absStore)

	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(seedWorkspaceProfile)
	cb.RegisterAfterAgent(mirrorSkillsAfterAgent(sink))

	a := llmagent.New(
		"workspace-flush-demo",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription(
			"Demonstrates programmatic workspaceio.Workspace usage.",
		),
		llmagent.WithInstruction(
			"You are a helpful assistant. Keep replies short.",
		),
		// LocalCodeExecutor gives every invocation its own work/ root.
		// Any backend (container, pcg123 NFS, cube) would work the
		// same way through codeexecutor.Engine.
		llmagent.WithCodeExecutor(localexec.New()),
		llmagent.WithAgentCallbacks(cb),
	)

	r := runner.NewRunner("workspace-flush-demo-app", a)
	defer r.Close()

	ctx := context.Background()
	events, err := r.Run(
		ctx, "demo-user", "demo-session",
		model.NewUserMessage(*prompt),
	)
	if err != nil {
		log.Fatalf("run agent: %v", err)
	}
	drainEvents(events)

	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("Skill store after invocation:")
	listStore(absStore)
}

// seedWorkspaceProfile pre-populates the workspace with two SKILL.md
// files, simulating an agent that loaded a user profile from somewhere
// and projected it into the workspace before the model runs.
func seedWorkspaceProfile(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	ws, ok := workspaceio.WorkspaceFromContext(ctx)
	if !ok {
		log.Printf(
			"Workspace not available; check that " +
				"WithCodeExecutor is configured",
		)
		return nil, nil
	}
	skills := []codeexecutor.PutFile{
		{
			Path:    "skills/echoer/SKILL.md",
			Content: []byte("# Echoer\n\nReplies with the same text.\n"),
		},
		{
			Path:    "skills/greeter/SKILL.md",
			Content: []byte("# Greeter\n\nGreets the user politely.\n"),
		},
	}
	if err := ws.PutFiles(ctx, skills...); err != nil {
		return nil, fmt.Errorf("seed workspace skills: %w", err)
	}
	for _, f := range skills {
		log.Printf("seeded workspace file: %s (%d bytes)", f.Path, len(f.Content))
	}
	return nil, nil
}

// mirrorSkillsAfterAgent returns an AfterAgent callback that copies
// every skills/*/SKILL.md from the workspace into sink. The whole
// pattern is a single Collect plus a sink loop — there is no framework
// helper involved on purpose.
func mirrorSkillsAfterAgent(sink *directorySink) agent.AfterAgentCallbackStructured {
	return func(
		ctx context.Context, args *agent.AfterAgentArgs,
	) (*agent.AfterAgentResult, error) {
		// Skip mirroring when the agent itself failed; the workspace
		// state is unreliable in that case.
		if args.Error != nil {
			return nil, nil
		}
		ws, ok := workspaceio.WorkspaceFromContext(ctx)
		if !ok {
			return nil, nil
		}
		files, err := ws.Collect(ctx, "skills/*/SKILL.md")
		if err != nil {
			return nil, fmt.Errorf("collect skills: %w", err)
		}
		for _, f := range files {
			if f.Truncated {
				return nil, fmt.Errorf(
					"%s was truncated by the executor (size=%d)",
					f.Path, f.SizeBytes,
				)
			}
			if err := validateSkillMarkdown(f); err != nil {
				return nil, err
			}
			if err := sink.Save(ctx, args.Invocation, f); err != nil {
				return nil, fmt.Errorf("sink %s: %w", f.Path, err)
			}
		}
		return nil, nil
	}
}

// validateSkillMarkdown rejects empty or heading-less SKILL.md files.
// A real validator would parse YAML frontmatter, check for required
// headings, etc.
func validateSkillMarkdown(file *workspaceio.File) error {
	if len(file.Data) == 0 {
		return fmt.Errorf("%s is empty", file.Path)
	}
	if !strings.Contains(string(file.Data), "#") {
		return fmt.Errorf("%s has no markdown heading", file.Path)
	}
	return nil
}

// directorySink persists each mirrored workspace file under root/<userID>/<path>,
// preserving the workspace-relative directory structure.
type directorySink struct {
	root string
}

func newDirectorySink(root string) *directorySink { return &directorySink{root: root} }

func (s *directorySink) Save(
	_ context.Context,
	inv *agent.Invocation,
	file *workspaceio.File,
) error {
	userID := "anonymous"
	if inv != nil && inv.Session != nil {
		userID = inv.Session.UserID
	}
	// Refuse to write outside s.root. file.Path comes from a workspace
	// collector and should already be workspace-relative, but this example
	// is copy-paste fodder — keep the containment check explicit so user
	// code stays safe by default. filepath.IsLocal (Go 1.20+) rejects
	// absolute paths, "..", and Windows UNC/volume escapes in one shot.
	rel := filepath.Join(userID, file.Path)
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("directorySink: refusing to write outside sink root: %q", rel)
	}
	dst := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, file.Data, 0o644); err != nil {
		return err
	}
	log.Printf("mirrored %s -> %s (%d bytes)", file.Path, dst, len(file.Data))
	return nil
}

func drainEvents(events <-chan *event.Event) {
	for ev := range events {
		if ev.Error != nil {
			log.Printf("agent error: %s", ev.Error.Message)
		}
		if len(ev.Response.Choices) > 0 {
			c := ev.Response.Choices[0]
			if c.Message.Content != "" {
				fmt.Printf("[assistant] %s\n", c.Message.Content)
			}
		}
		if ev.Done {
			return
		}
	}
}

func listStore(root string) {
	err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Printf("- %s (%d bytes)\n", rel, info.Size())
		return nil
	})
	if err != nil {
		log.Printf("walk store: %v", err)
	}
}
