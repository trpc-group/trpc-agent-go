//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how a business-owned context manager can use
// agent.WithSessionContextMessagesFunc to create append-only context diffs.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName   = "append-only-context-diff-demo"
	userID    = "debug-user"
	agentName = "runtime-context-agent"
)

var (
	mode      = flag.String("mode", "openai", "Model mode: openai (default) or debug")
	modelName = flag.String("model", "gpt-4o-mini", "OpenAI-compatible model name")
	baseURL   = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL (optional)")
	apiKey    = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key (optional; falls back to OPENAI_API_KEY)")
)

type runtimeState struct {
	CWD               string
	WorkspaceRoots    []string
	ActiveFiles       []string
	PermissionProfile string
	ApprovalPolicy    string
	Sandbox           string
	NetworkMode       string
	AllowedDomains    []string
	Model             string
	TaskMode          string
	GitBranch         string
	DirtyFiles        []string
	LastToolAction    string
}

type stateChange struct {
	Field  string
	Before string
	After  string
}

type runtimeContextManager struct {
	previous *runtimeState
	sequence int
}

type turnScenario struct {
	Label string
	Cause string
	User  string
	State runtimeState
}

type debugModel struct{}

func (m *debugModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		ch <- &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Model:  m.Info().Name,
			Choices: []model.Choice{
				{Index: 0, Message: model.NewAssistantMessage("OK")},
			},
		}
	}()
	return ch, nil
}

func (m *debugModel) Info() model.Info {
	return model.Info{Name: "debug-model"}
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	modelInstance, err := buildModel()
	if err != nil {
		return err
	}
	runtimeModelName := strings.TrimSpace(modelInstance.Info().Name)
	if runtimeModelName == "" {
		runtimeModelName = strings.TrimSpace(*modelName)
	}

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		printModelRequest(args.Request)
		return nil, nil
	})

	agt := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithGlobalInstruction("You are a concise coding-agent debug assistant."),
		llmagent.WithInstruction("Answer briefly and follow the latest runtime context."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false, MaxTokens: intPtr(128)}),
		llmagent.WithModelCallbacks(modelCallbacks),
	)

	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("append-only-context-%d", time.Now().UnixNano())
	manager := &runtimeContextManager{}

	fmt.Println("Append-only context diff with WithSessionContextMessagesFunc")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("mode=%s model=%s\n", strings.ToLower(strings.TrimSpace(*mode)), runtimeModelName)
	fmt.Println("A business-owned manager observes runtime state, emits full/diff/skip,")
	fmt.Println("and lets Runner persist the returned Messages before the current user.")

	for i, scenario := range buildScenarios(runtimeModelName) {
		fmt.Printf("\n--- Turn %d: %s ---\n", i+1, scenario.Label)
		fmt.Printf("state cause: %s\n", scenario.Cause)
		if err := runTurn(
			ctx,
			r,
			userID,
			sessionID,
			model.NewUserMessage(scenario.User),
			manager.SessionContextOption(scenario.State, scenario.Cause),
		); err != nil {
			return err
		}
	}

	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	printSessionTranscript(sess)
	return nil
}

func buildModel() (model.Model, error) {
	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "", "openai":
		*mode = "openai"
		if strings.TrimSpace(*apiKey) == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required for default openai mode; use -mode=debug for a local deterministic run")
		}
		var opts []openai.Option
		if strings.TrimSpace(*baseURL) != "" {
			opts = append(opts, openai.WithBaseURL(strings.TrimSpace(*baseURL)))
		}
		opts = append(opts, openai.WithAPIKey(strings.TrimSpace(*apiKey)))
		return openai.New(strings.TrimSpace(*modelName), opts...), nil
	case "debug":
		return &debugModel{}, nil
	default:
		return nil, fmt.Errorf("unknown -mode %q (expected openai or debug)", *mode)
	}
}

func buildScenarios(runtimeModelName string) []turnScenario {
	return []turnScenario{
		{
			Label: "new session starts",
			Cause: "session bootstrap collected initial runtime state",
			User:  "Inspect the project and tell me what you can access.",
			State: runtimeState{
				CWD:               "/repo/trpc-agent-go",
				WorkspaceRoots:    []string{"/repo/trpc-agent-go"},
				ActiveFiles:       nil,
				PermissionProfile: "read-only",
				ApprovalPolicy:    "on-request",
				Sandbox:           "workspace-read",
				NetworkMode:       "disabled",
				AllowedDomains:    nil,
				Model:             runtimeModelName,
				TaskMode:          "planning",
				GitBranch:         "feature/context-messages",
				DirtyFiles:        nil,
				LastToolAction:    "none",
			},
		},
		{
			Label: "file discovery tool updates active context",
			Cause: "tool:list_files found relevant files for this task",
			User:  "Focus on the runner and prompt examples.",
			State: runtimeState{
				CWD:               "/repo/trpc-agent-go",
				WorkspaceRoots:    []string{"/repo/trpc-agent-go"},
				ActiveFiles:       []string{"runner/runner.go", "examples/prompt/session_context_messages/main.go"},
				PermissionProfile: "read-only",
				ApprovalPolicy:    "on-request",
				Sandbox:           "workspace-read",
				NetworkMode:       "disabled",
				AllowedDomains:    nil,
				Model:             runtimeModelName,
				TaskMode:          "planning",
				GitBranch:         "feature/context-messages",
				DirtyFiles:        nil,
				LastToolAction:    "list_files",
			},
		},
		{
			Label: "user grants write access and edit tool changes files",
			Cause: "user approved editing; tool:apply_patch modified the example",
			User:  "Now update the example to show append-only context diffs.",
			State: runtimeState{
				CWD:               "/repo/trpc-agent-go",
				WorkspaceRoots:    []string{"/repo/trpc-agent-go"},
				ActiveFiles:       []string{"examples/prompt/session_context_messages/main.go", "examples/prompt/session_context_messages/README.md"},
				PermissionProfile: "workspace-write",
				ApprovalPolicy:    "on-request",
				Sandbox:           "workspace-write",
				NetworkMode:       "disabled",
				AllowedDomains:    nil,
				Model:             runtimeModelName,
				TaskMode:          "editing",
				GitBranch:         "feature/context-messages",
				DirtyFiles:        []string{"examples/prompt/session_context_messages/main.go"},
				LastToolAction:    "apply_patch",
			},
		},
		{
			Label: "follow-up question with no runtime change",
			Cause: "no user/tool/runtime field changed since the previous turn",
			User:  "What is the latest runtime state?",
			State: runtimeState{
				CWD:               "/repo/trpc-agent-go",
				WorkspaceRoots:    []string{"/repo/trpc-agent-go"},
				ActiveFiles:       []string{"examples/prompt/session_context_messages/main.go", "examples/prompt/session_context_messages/README.md"},
				PermissionProfile: "workspace-write",
				ApprovalPolicy:    "on-request",
				Sandbox:           "workspace-write",
				NetworkMode:       "disabled",
				AllowedDomains:    nil,
				Model:             runtimeModelName,
				TaskMode:          "editing",
				GitBranch:         "feature/context-messages",
				DirtyFiles:        []string{"examples/prompt/session_context_messages/main.go"},
				LastToolAction:    "apply_patch",
			},
		},
		{
			Label: "docs lookup enables limited network and README becomes dirty",
			Cause: "tool:web_fetch was allowed for pkg.go.dev; tool:apply_patch updated README",
			User:  "Include the docs nuance in the README too.",
			State: runtimeState{
				CWD:               "/repo/trpc-agent-go",
				WorkspaceRoots:    []string{"/repo/trpc-agent-go"},
				ActiveFiles:       []string{"examples/prompt/session_context_messages/main.go", "examples/prompt/session_context_messages/README.md"},
				PermissionProfile: "workspace-write",
				ApprovalPolicy:    "on-request",
				Sandbox:           "workspace-write",
				NetworkMode:       "limited",
				AllowedDomains:    []string{"pkg.go.dev"},
				Model:             runtimeModelName,
				TaskMode:          "editing",
				GitBranch:         "feature/context-messages",
				DirtyFiles:        []string{"examples/prompt/session_context_messages/main.go", "examples/prompt/session_context_messages/README.md"},
				LastToolAction:    "web_fetch, apply_patch",
			},
		},
	}
}

func (m *runtimeContextManager) SessionContextOption(
	current runtimeState,
	cause string,
) agent.RunOption {
	return agent.WithSessionContextMessagesFunc(func(
		ctx context.Context,
		args *agent.SessionContextMessagesArgs,
	) ([]model.Message, error) {
		return m.BuildMessages(current, cause, args), nil
	})
}

func (m *runtimeContextManager) BuildMessages(
	current runtimeState,
	cause string,
	args *agent.SessionContextMessagesArgs,
) []model.Message {
	if m.previous == nil {
		m.sequence++
		m.previous = cloneRuntimeState(current)
		return []model.Message{
			model.NewUserMessage(renderFullSnapshot(m.sequence, current, cause, args)),
		}
	}

	changes := diffRuntimeState(*m.previous, current)
	if len(changes) == 0 {
		return nil
	}

	m.sequence++
	m.previous = cloneRuntimeState(current)
	return []model.Message{
		model.NewUserMessage(renderDiff(m.sequence, changes, current, cause)),
	}
}

func diffRuntimeState(previous, current runtimeState) []stateChange {
	var changes []stateChange
	add := func(field string, before, after any) {
		if reflect.DeepEqual(before, after) {
			return
		}
		changes = append(changes, stateChange{
			Field:  field,
			Before: formatValue(before),
			After:  formatValue(after),
		})
	}

	add("cwd", previous.CWD, current.CWD)
	add("workspace_roots", previous.WorkspaceRoots, current.WorkspaceRoots)
	add("active_files", previous.ActiveFiles, current.ActiveFiles)
	add("permission_profile", previous.PermissionProfile, current.PermissionProfile)
	add("approval_policy", previous.ApprovalPolicy, current.ApprovalPolicy)
	add("sandbox", previous.Sandbox, current.Sandbox)
	add("network_mode", previous.NetworkMode, current.NetworkMode)
	add("allowed_domains", previous.AllowedDomains, current.AllowedDomains)
	add("model", previous.Model, current.Model)
	add("task_mode", previous.TaskMode, current.TaskMode)
	add("git_branch", previous.GitBranch, current.GitBranch)
	add("dirty_files", previous.DirtyFiles, current.DirtyFiles)
	add("last_tool_action", previous.LastToolAction, current.LastToolAction)
	return changes
}

func renderFullSnapshot(
	sequence int,
	state runtimeState,
	cause string,
	args *agent.SessionContextMessagesArgs,
) string {
	lines := []string{
		fmt.Sprintf("[context:runtime_state seq=%d] full snapshot", sequence),
		"op=replace",
		"scope=runtime_state",
		"cause=" + cause,
		"latest message for this scope is authoritative.",
		"original_user_message=" + args.OriginalMessage.Content,
		"",
		"current state:",
		"cwd=" + state.CWD,
		"workspace_roots=" + formatStrings(state.WorkspaceRoots),
		"active_files=" + formatStrings(state.ActiveFiles),
		"permission_profile=" + state.PermissionProfile,
		"approval_policy=" + state.ApprovalPolicy,
		"sandbox=" + state.Sandbox,
		"network_mode=" + state.NetworkMode,
		"allowed_domains=" + formatStrings(state.AllowedDomains),
		"model=" + state.Model,
		"task_mode=" + state.TaskMode,
		"git_branch=" + state.GitBranch,
		"dirty_files=" + formatStrings(state.DirtyFiles),
		"last_tool_action=" + state.LastToolAction,
	}
	return strings.Join(lines, "\n")
}

func renderDiff(
	sequence int,
	changes []stateChange,
	current runtimeState,
	cause string,
) string {
	lines := []string{
		fmt.Sprintf("[context:runtime_state seq=%d] diff", sequence),
		"op=patch",
		"scope=runtime_state",
		"cause=" + cause,
		"apply these field replacements to the latest runtime_state.",
		"latest message for this scope is authoritative.",
		"",
		"changed fields:",
	}
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("- %s: %s -> %s", change.Field, change.Before, change.After))
	}
	lines = append(
		lines,
		"",
		"current critical state:",
		"permission_profile="+current.PermissionProfile,
		"sandbox="+current.Sandbox,
		"network_mode="+current.NetworkMode,
		"active_files="+formatStrings(current.ActiveFiles),
		"dirty_files="+formatStrings(current.DirtyFiles),
	)
	return strings.Join(lines, "\n")
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	msg model.Message,
	opts ...agent.RunOption,
) error {
	ch, err := r.Run(ctx, userID, sessionID, msg, opts...)
	if err != nil {
		return err
	}
	for evt := range ch {
		printEvent(evt)
	}
	return nil
}

func printModelRequest(req *model.Request) {
	fmt.Println()
	fmt.Println("model request:")
	if req == nil {
		fmt.Println("(nil request)")
		return
	}
	for i, msg := range req.Messages {
		fmt.Printf("%02d %-9s %s\n", i, msg.Role.String(), summarizeMessage(msg))
	}
}

func printSessionTranscript(sess *session.Session) {
	fmt.Println()
	fmt.Println("persisted session transcript:")
	if sess == nil {
		fmt.Println("(nil session)")
		return
	}
	for i, evt := range sess.GetEvents() {
		msg, ok := eventMessage(evt)
		if !ok {
			continue
		}
		fmt.Printf("%02d %-9s %s\n", i, msg.Role.String(), summarizeMessage(msg))
	}
}

func eventMessage(evt event.Event) (model.Message, bool) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return model.Message{}, false
	}
	return evt.Response.Choices[0].Message, true
}

func printEvent(evt *event.Event) {
	if evt == nil {
		return
	}
	if evt.Error != nil {
		fmt.Printf("event error: %s\n", evt.Error.Message)
		return
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	msg := evt.Response.Choices[0].Message
	if strings.TrimSpace(msg.Content) != "" {
		fmt.Printf("assistant: %s\n", strings.TrimSpace(msg.Content))
	}
}

func cloneRuntimeState(state runtimeState) *runtimeState {
	copied := state
	copied.WorkspaceRoots = append([]string(nil), state.WorkspaceRoots...)
	copied.ActiveFiles = append([]string(nil), state.ActiveFiles...)
	copied.AllowedDomains = append([]string(nil), state.AllowedDomains...)
	copied.DirtyFiles = append([]string(nil), state.DirtyFiles...)
	return &copied
}

func summarizeMessage(msg model.Message) string {
	content := strings.ReplaceAll(msg.Content, "\n", `\n`)
	content = strings.TrimSpace(content)
	if content == "" && len(msg.ContentParts) > 0 {
		content = fmt.Sprintf("<%d content parts>", len(msg.ContentParts))
	}
	if content == "" {
		content = "<empty>"
	}
	return truncate(content, 220)
}

func formatValue(value any) string {
	switch v := value.(type) {
	case []string:
		return formatStrings(v)
	case string:
		if v == "" {
			return "<empty>"
		}
		return v
	default:
		return fmt.Sprint(v)
	}
}

func formatStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	return "[" + strings.Join(values, ", ") + "]"
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func intPtr(i int) *int {
	return &i
}
