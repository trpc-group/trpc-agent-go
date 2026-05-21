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
	"flag"
	"fmt"
	"mime"
	"net/http"
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
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

var (
	flagModel          = flag.String("model", "deepseek-v4-flash", "model name")
	flagStream         = flag.Bool("stream", true, "stream responses")
	flagSkills         = flag.String("skills-root", "", "skills root dir")
	flagSkillsGuidance = flag.Bool(
		"skills-guidance",
		true,
		"include built-in skills tooling/workspace guidance",
	)
	flagSendFileInputs = flag.Bool(
		"send-file-inputs",
		false,
		"send file inputs to the model provider (may be unsupported)",
	)
	flagExec = flag.String(
		"executor", "local",
		"workspace executor: local|container|e2b",
	)
	flagTrustedLocal = flag.Bool(
		"trusted-local",
		false,
		"local executor only: reuse a fixed workspace root (unsafe)",
	)
	flagTrustedRoot = flag.String(
		"trusted-root",
		"",
		"trusted-local workspace root (default: ./skill_workspace)",
	)
	flagInputsHost = flag.String(
		"inputs-host", "",
		"host dir to bind as /opt/trpc-agent/inputs "+
			"(container exec)",
	)
)

const defaultSkillsDir = "skills"
const appName = "skill-run-chat"

const (
	artifactRefPrefix    = "artifact://"
	artifactUploadPrefix = "uploads/"
)

// instructionText guides the assistant behavior in a general way so it
// works with different skills repositories without assuming specifics.
const instructionText = `
Be a concise, helpful assistant that can use Agent Skills.

When a task may need tools, first ask to list skills or suggest one.
Load a skill with skill_load, then run the commands from its docs by
calling workspace_exec inside the loaded skill working copy
(cwd: skills/<name>). Prefer safe defaults; ask clarifying questions
if anything is ambiguous. Summarize results, note saved files, and
propose next steps briefly.

Inside the workspace, treat inputs/ and work/inputs/ as read-only
views of host files unless skill docs say they are writable. Do not
create, move, or modify files under inputs/ or work/inputs/.
User-uploaded file inputs from the conversation are staged under
work/inputs/ before scripts run.

When chaining multiple skills, read previous results directly from
out/ (or $OUTPUT_DIR) and write new files back to out/. If you need
a stable reference to an output file (for example to hand it to
another tool or to surface it back to the user), call
workspace_save_artifact on the workspace path.`

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
	model      *openai.Model
	uploaded   []string
}

func (c *skillChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return err
	}
	defer c.cleanupUploads(ctx)
	return c.startChat(ctx)
}

func (c *skillChat) setup(_ context.Context) error {
	// Model.
	var mdlOpts []openai.Option
	if !*flagSendFileInputs {
		mdlOpts = append(
			mdlOpts,
			openai.WithOmitFileContentParts(true),
		)
	}
	mdl := openai.New(c.modelName, mdlOpts...)
	c.model = mdl

	// Skills repository.
	repo, err := skill.NewFSRepository(c.skillsRoot)
	if err != nil {
		return fmt.Errorf("skills repo: %w", err)
	}

	// Choose workspace executor.
	var we codeexecutor.CodeExecutor
	execUsed := "local"
	switch strings.ToLower(strings.TrimSpace(*flagExec)) {
	case "e2b":
		we, err = e2bexec.New()
		if err != nil {
			return fmt.Errorf("e2b executor: %w", err)
		}
		execUsed = "e2b"
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
		if *flagTrustedLocal {
			root := strings.TrimSpace(*flagTrustedRoot)
			if root == "" {
				cwd, _ := os.Getwd()
				root = filepath.Join(cwd, "skill_workspace")
			}
			lopts = append(
				lopts,
				localexec.WithWorkDir(root),
				localexec.WithWorkspaceMode(
					localexec.WorkspaceModeTrustedLocal,
				),
			)
		}
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

	// Agent with skills enabled. The default knowledge_only skill tool
	// profile registers skill_load / skill_list_docs / skill_select_docs;
	// the explicit code executor wires workspace_exec (and
	// workspace_save_artifact when an artifact service is present) on
	// top of it, which is the recommended execution surface.
	gen := model.GenerationConfig{
		MaxTokens: intPtr(2000),
		Stream:    c.stream,
	}

	llm := llmagent.New(
		"skills-chat",
		buildAgentOptions(
			mdl,
			repo,
			we,
			gen,
		)...,
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
		" - /upload <path> attaches a local file (inline bytes).",
	)
	fmt.Println(
		" - /upload_id <path> uploads a file and attaches file_id.",
	)
	fmt.Println(
		" - /upload_artifact <path> uploads to the artifact " +
			"service and attaches artifact:// file_id.",
	)
	fmt.Println(
		" - Ask to run a command exactly as in the docs.",
	)
	fmt.Println(
		" - Prefer writing files to $OUTPUT_DIR (i.e. out/).",
	)
	fmt.Println(
		" - Use $WORK_DIR/inputs for inputs and $OUTPUT_DIR for outputs.",
	)
	fmt.Println(
		" - Reference skill files via $SKILLS_DIR/<name>/...",
	)
	fmt.Println(
		" - Ask the assistant to call workspace_save_artifact when " +
			"you want a stable artifact reference for an output file.",
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

func buildAgentOptions(
	mdl model.Model,
	repo skill.Repository,
	exec codeexecutor.CodeExecutor,
	gen model.GenerationConfig,
) []llmagent.Option {
	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithDescription(
			"General assistant that can use Agent Skills to run commands " +
				"and handle files.",
		),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(exec),
		// Disable fenced-code auto-execution so the model has to go
		// through workspace_exec instead of having ```sh blocks
		// scraped out of its prose.
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
	}
	if !*flagSkillsGuidance {
		opts = append(
			opts,
			llmagent.WithSkillsToolingGuidance(""),
		)
	}
	return opts
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
		if strings.HasPrefix(text, "/upload_artifact ") {
			if err := c.handleUploadArtifact(ctx, text); err != nil {
				fmt.Printf("❌ Upload error: %v\n", err)
			}
			fmt.Println()
			continue
		}
		if strings.HasPrefix(text, "/upload_id ") {
			if err := c.handleUpload(ctx, text, true); err != nil {
				fmt.Printf("❌ Upload error: %v\n", err)
			}
			fmt.Println()
			continue
		}
		if strings.HasPrefix(text, "/upload ") {
			if err := c.handleUpload(ctx, text, false); err != nil {
				fmt.Printf("❌ Upload error: %v\n", err)
			}
			fmt.Println()
			continue
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
	return c.processModelMessage(ctx, msg)
}

func (c *skillChat) processModelMessage(
	ctx context.Context, msg model.Message,
) error {
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

func (c *skillChat) handleUpload(
	ctx context.Context,
	text string,
	useFileIDs bool,
) error {
	cmd := "/upload "
	if useFileIDs {
		cmd = "/upload_id "
	}
	raw := strings.TrimSpace(strings.TrimPrefix(text, cmd))
	if raw == "" {
		return fmt.Errorf("usage: %s<path>", cmd)
	}
	base := filepath.Base(raw)
	msg := model.NewUserMessage("Uploaded file: " + base)
	if useFileIDs {
		if c.model == nil {
			return fmt.Errorf("model is not configured")
		}
		id, err := c.model.UploadFile(ctx, raw)
		if err != nil {
			return err
		}
		c.uploaded = append(c.uploaded, id)
		msg.AddFileIDWithName(id, base)
		fmt.Printf("📤 Uploaded as %s\n", id)
		return c.processModelMessage(ctx, msg)
	}
	data, err := os.ReadFile(raw)
	if err != nil {
		return err
	}
	msg.AddFileData(base, data, guessMimeType(base, data))
	fmt.Printf("📎 Attached %s (%d bytes)\n", base, len(data))
	return c.processModelMessage(ctx, msg)
}

func (c *skillChat) handleUploadArtifact(
	ctx context.Context,
	text string,
) error {
	if c.artSvc == nil {
		return fmt.Errorf("artifact service not available")
	}
	raw := strings.TrimSpace(strings.TrimPrefix(
		text,
		"/upload_artifact ",
	))
	if raw == "" {
		return fmt.Errorf("usage: /upload_artifact <path>")
	}
	base := filepath.Base(raw)
	data, err := os.ReadFile(raw)
	if err != nil {
		return err
	}
	name := artifactUploadPrefix + uuid.NewString() + "_" +
		base
	info := artifact.SessionInfo{
		AppName:   appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	}
	mt := guessMimeType(base, data)
	ver, err := c.artSvc.SaveArtifact(
		ctx,
		info,
		name,
		&artifact.Artifact{
			Data:     data,
			MimeType: mt,
			Name:     base,
		},
	)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf(
		"%s%s@%d",
		artifactRefPrefix,
		name,
		ver,
	)
	msg := model.NewUserMessage("Uploaded file: " + base)
	msg.AddFileIDWithName(ref, base)
	fmt.Printf("📤 Uploaded to %s@%d\n", name, ver)
	return c.processModelMessage(ctx, msg)
}

func guessMimeType(name string, data []byte) string {
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = mt[:i]
	}
	if strings.TrimSpace(mt) != "" {
		return mt
	}
	if len(data) == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(data)
}

func (c *skillChat) cleanupUploads(ctx context.Context) {
	if c.model == nil || len(c.uploaded) == 0 {
		return
	}
	for _, id := range c.uploaded {
		_ = c.model.DeleteFile(ctx, id)
	}
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

func intPtr(i int) *int { return &i }
