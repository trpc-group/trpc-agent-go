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

// Package main demonstrates an interactive chat using Runner + LLMAgent
// with Agent Skills enabled. It streams content and shows tool calls and
// tool responses during the conversation.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	flagModel  = flag.String("model", "deepseek-chat", "model name")
	flagStream = flag.Bool("stream", true, "stream responses")
	flagSkills = flag.String("skills-root", "", "skills root dir")
	flagExec   = flag.String(
		"executor", "local",
		"workspace executor: local|container",
	)
	flagArtifacts = flag.Bool("artifacts", true,
		"save output files via artifact service")
	flagOmitInline = flag.Bool("omit-inline", false,
		"omit inline file contents when saving artifacts")
	flagArtifactPref = flag.String("artifact-prefix", "",
		"artifact filename prefix (e.g., user:)")
	flagInputsHost = flag.String(
		"inputs-host", "",
		"host dir to bind as /opt/trpc-agent/inputs "+
			"(container exec)",
	)
)

const defaultSkillsDir = "skills"
const appName = "skill-run-chat"

// instructionText guides the assistant behavior in a general way so it
// works with different skills repositories without assuming specifics.
const instructionText = `
Be a concise, helpful assistant that can use Agent Skills.

When a task may need tools, first ask to list skills or suggest one.
Load a skill only when needed, then run commands from its docs exactly.
Prefer safe defaults; ask clarifying questions if anything is ambiguous.
When running, include output_files patterns if files are expected.
Summarize results, note saved files, and propose next steps briefly.

Inside a skill workspace, treat inputs/ and work/inputs/ as read-only
views of host files unless skill docs say they are writable. Do not
create, move, or modify files under inputs/ or work/inputs/.

When chaining multiple skills, read previous results directly from
out/ (or $OUTPUT_DIR) and write new files back to out/. Prefer using
skill_run inputs/outputs fields to map files instead of shell commands
like cp or mv where possible.`

func main() {
	flag.Parse()

	// Resolve skills root (flag, env, default ./skills).
	root := *flagSkills
	if root == "" {
		if s := os.Getenv(skill.EnvSkillsRoot); s != "" {
			root = s
		} else {
			cwd, _ := os.Getwd()
			root = filepath.Join(cwd, defaultSkillsDir)
		}
	}

	// Setup runner and agent.
	chat := &skillChat{
		modelName:  *flagModel,
		stream:     *flagStream,
		skillsRoot: root,
	}
	if err := chat.run(); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

type skillChat struct {
	modelName  string
	stream     bool
	skillsRoot string
	runner     runner.Runner
	userID     string
	sessionID  string
	executor   string
	artSvc     artifact.Service
}

func (c *skillChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return err
	}
	return c.startChat(ctx)
}

func (c *skillChat) setup(_ context.Context) error {
	// Model.
	mdl := openai.New(c.modelName)

	// Skills repository.
	repo, err := skill.NewFSRepository(c.skillsRoot)
	if err != nil {
		return fmt.Errorf("skills repo: %w", err)
	}

	// Choose workspace executor.
	var we codeexecutor.CodeExecutor
	execUsed := "local"
	switch strings.ToLower(strings.TrimSpace(*flagExec)) {
	case "container":
		// Bind the skills root read-only into the container to
		// enable fast in-container copy when staging directories.
		opts := []containerexec.Option{
			containerexec.WithBindMount(
				c.skillsRoot, "/opt/trpc-agent/skills", "ro",
			),
			// When an inputs-host directory is provided and bound
			// into the container, automatically expose it under
			// work/inputs (and thus inputs/) inside each workspace
			// so skills can read host files via inputs/ paths.
			containerexec.WithAutoInputs(true),
		}
		// Optional: bind a host directory for zero-copy inputs.
		if *flagInputsHost != "" {
			opts = append(opts, containerexec.WithBindMount(
				*flagInputsHost, "/opt/trpc-agent/inputs", "ro",
			))
		}
		if rt, e := containerexec.New(opts...); e == nil {
			we = rt
			execUsed = "container"
		} else {
			return fmt.Errorf("container executor: %w", e)
		}
	default:
		var lopts []localexec.CodeExecutorOption
		if *flagInputsHost != "" {
			lopts = append(
				lopts, localexec.WithWorkspaceInputsHostBase(
					*flagInputsHost,
				),
			)
		}
		we = localexec.New(lopts...)
	}
	c.executor = execUsed

	// Agent with skills enabled; skill_load + skill_run get registered.
	gen := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.4),
		Stream:      c.stream,
	}

	llm := llmagent.New(
		"skills-chat",
		llmagent.WithModel(mdl),
		llmagent.WithDescription(
			"General assistant that can use Agent Skills to run commands "+
				"and handle files.",
		),
		llmagent.WithInstruction(
			instructionText,
		),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(we),
		llmagent.WithToolCallbacks(buildToolCallbacks()),
	)

	// Runner + artifact service (in-memory for demo).
	svc := inmemory.NewService()
	c.artSvc = svc
	c.runner = runner.NewRunner(
		appName, llm, runner.WithArtifactService(svc),
	)
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-%d", time.Now().Unix())

	// Intro.
	fmt.Printf("🚀 Skill Run Chat\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Stream: %t\n", c.stream)
	fmt.Printf("Skills root: %s\n", c.skillsRoot)
	fmt.Printf("Executor: %s\n", c.executor)
	fmt.Printf("Session: %s\n", c.sessionID)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("Tips:")
	fmt.Println(" - You can ask to list skills (optional).")
	fmt.Println(
		" - No need to type 'load'; the assistant loads skills when " +
			"needed.",
	)
	fmt.Println(
		" - Ask to run a command exactly as in the docs.",
	)
	fmt.Println(
		" - Prefer writing files to $OUTPUT_DIR (collector reads out/).",
	)
	fmt.Println(
		" - Use $WORK_DIR/inputs for inputs and $OUTPUT_DIR for outputs.",
	)
	fmt.Println(
		" - Reference skill files via $SKILLS_DIR/<name>/...",
	)
	fmt.Println(
		" - Optional: add inputs/outputs fields to skill_run.",
	)
	fmt.Println(" - /artifacts lists saved artifact keys.")
	fmt.Println(" - /pull <name> [version] downloads an artifact.")
	fmt.Println(
		" - Try skill 'user-file-ops' to summarize a user text file.",
	)
	fmt.Println(
		"   Place it under work/inputs/ and write summaries to out/.",
	)
	fmt.Println(" - Type /exit to quit.")
	fmt.Println()
	return nil
}

// buildToolCallbacks injects default artifact parameters into
// skill_run calls based on CLI flags, without overriding explicit
// arguments the model already provided.
func buildToolCallbacks() *tool.Callbacks {
	cbs := tool.NewCallbacks()
	cbs.RegisterBeforeTool(func(
		_ context.Context, name string, _ *tool.Declaration, args *[]byte,
	) (any, error) {
		if name != "skill_run" || args == nil {
			return nil, nil
		}
		if !*flagArtifacts && !*flagOmitInline && *flagArtifactPref == "" {
			return nil, nil
		}
		// Parse JSON args to a small map and merge defaults.
		var m map[string]any
		if err := json.Unmarshal(*args, &m); err != nil {
			return nil, nil
		}
		if *flagArtifacts {
			m["save_as_artifacts"] = true
			if *flagOmitInline {
				m["omit_inline_content"] = true
			}
			if *flagArtifactPref != "" {
				if _, ok := m["artifact_prefix"]; !ok {
					m["artifact_prefix"] = *flagArtifactPref
				}
			}
		}
		b, err := json.Marshal(m)
		if err == nil {
			*args = b
		}
		return nil, nil
	})
	return cbs
}

func (c *skillChat) startChat(ctx context.Context) error {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !in.Scan() {
			break
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "/exit") {
			fmt.Println("👋 Bye!")
			return nil
		}
		if strings.HasPrefix(text, "/pull ") {
			if err := c.handlePull(text); err != nil {
				fmt.Printf("❌ Pull error: %v\n", err)
			}
			fmt.Println()
			continue
		}
		if strings.EqualFold(strings.TrimSpace(text), "/artifacts") {
			if err := c.handleListArtifacts(); err != nil {
				fmt.Printf("❌ List error: %v\n", err)
			}
			fmt.Println()
			continue
		}
		if err := c.processMessage(ctx, text); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return in.Err()
}

func (c *skillChat) processMessage(
	ctx context.Context, userMessage string,
) error {
	msg := model.NewUserMessage(userMessage)
	reqID := uuid.New().String()
	ch, err := c.runner.Run(
		ctx, c.userID, c.sessionID, msg, agent.WithRequestID(reqID),
	)
	if err != nil {
		return err
	}
	return c.processResponse(ch)
}

func (c *skillChat) processResponse(
	ch <-chan *event.Event,
) error {
	fmt.Print("🤖 Assistant: ")
	var (
		toolCalls bool
		started   bool
		full      string
	)
	for ev := range ch {
		if err := c.handleEvent(ev, &toolCalls, &started, &full); err != nil {
			return err
		}
		if ev.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

// handlePull downloads an artifact from the service and writes it to disk.
// Usage: /pull <name> [version]
func (c *skillChat) handlePull(text string) error {
	if c.artSvc == nil {
		return fmt.Errorf("artifact service not available")
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return fmt.Errorf("usage: /pull <name> [version]")
	}
	name := fields[1]
	var verPtr *int
	if len(fields) >= 3 {
		if v, err := parseInt(fields[2]); err == nil {
			verPtr = &v
		}
	}
	si := artifact.SessionInfo{
		AppName: appName, UserID: c.userID, SessionID: c.sessionID,
	}
	art, err := c.artSvc.LoadArtifact(context.Background(), si, name, verPtr)
	if err != nil {
		return err
	}
	if art == nil || len(art.Data) == 0 {
		return fmt.Errorf("artifact not found: %s", name)
	}
	dir := "downloads"
	_ = os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(out, art.Data, 0o644); err != nil {
		return err
	}
	fmt.Printf(
		"📥 Saved %s (%d bytes, %s)\n",
		out, len(art.Data), art.MimeType,
	)
	return nil
}

// handleListArtifacts lists artifact keys for the current session.
func (c *skillChat) handleListArtifacts() error {
	if c.artSvc == nil {
		return fmt.Errorf("artifact service not available")
	}
	si := artifact.SessionInfo{
		AppName: appName, UserID: c.userID, SessionID: c.sessionID,
	}
	keys, err := c.artSvc.ListArtifactKeys(context.Background(), si)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		fmt.Println("(no artifacts)")
		return nil
	}
	fmt.Println("Artifacts:")
	for _, k := range keys {
		fmt.Printf("- %s\n", k)
	}
	return nil
}

func parseInt(s string) (int, error) {
	var n int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid int: %s", s)
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}

func (c *skillChat) handleEvent(
	ev *event.Event, toolCalls *bool, started *bool, full *string,
) error {
	if ev.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
		return nil
	}
	if c.handleToolCalls(ev, toolCalls, started) {
		return nil
	}
	if c.handleToolResponses(ev) {
		return nil
	}
	c.handleContent(ev, toolCalls, started, full)
	return nil
}

func (c *skillChat) handleToolCalls(
	ev *event.Event, toolCalls *bool, started *bool,
) bool {
	if len(ev.Response.Choices) > 0 &&
		len(ev.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCalls = true
		if *started {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 CallableTool calls initiated:\n")
		for _, tc := range ev.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", tc.Function.Name, tc.ID)
			if len(tc.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n",
					string(tc.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

func (c *skillChat) handleToolResponses(ev *event.Event) bool {
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		has := false
		for _, ch := range ev.Response.Choices {
			if ch.Message.Role == model.RoleTool && ch.Message.ToolID != "" {
				fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
					ch.Message.ToolID,
					strings.TrimSpace(ch.Message.Content))
				// Try to surface artifact refs if present.
				var v struct {
					ArtifactFiles []struct {
						Name    string `json:"name"`
						Version int    `json:"version"`
					} `json:"artifact_files"`
				}
				if json.Unmarshal([]byte(ch.Message.Content), &v) == nil &&
					len(v.ArtifactFiles) > 0 {
					fmt.Printf("   Saved artifacts:\n")
					for _, a := range v.ArtifactFiles {
						fmt.Printf("   - %s (v%d)\n", a.Name, a.Version)
					}
				}
				has = true
			}
		}
		if has {
			return true
		}
	}
	return false
}

func (c *skillChat) handleContent(
	ev *event.Event, toolCalls *bool, started *bool, full *string,
) {
	if len(ev.Response.Choices) == 0 {
		return
	}
	choice := ev.Response.Choices[0]
	var content string
	if c.stream {
		content = choice.Delta.Content
	} else {
		content = choice.Message.Content
	}
	if content == "" {
		return
	}
	if !*started {
		if *toolCalls {
			fmt.Printf("\n🤖 Assistant: ")
		}
		*started = true
	}
	fmt.Print(content)
	*full += content
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
