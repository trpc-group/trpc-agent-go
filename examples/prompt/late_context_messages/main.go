//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using `agent.WithLateContextMessages` as a “rules”
// pattern: per-run context injected near the latest user turn (not persisted
// into session history). It prints the final `request.Messages` before each
// model call, and runs two turns in the same session to demonstrate that late
// context is non-persistent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	mode        = flag.String("mode", "debug", "Model mode: debug (default) or openai")
	modelName   = flag.String("model", "gpt-4o-mini", "Model name (openai mode)")
	stream      = flag.Bool("stream", false, "Enable streaming responses (openai mode)")
	maxTokens   = flag.Int("max-tokens", -1, "Max completion tokens (openai mode). <0 means use model default")
	temperature = flag.Float64("temperature", -1, "Sampling temperature (openai mode). <0 means use model default")
	baseURL     = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL (optional)")
	apiKey      = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key (optional; falls back to env)")
	variant     = flag.String("variant", "openai", "Model variant for OpenAI adapter (openai/deepseek/hunyuan/qwen)")
	targetPath  = flag.String("target-path", "main.go", "Target path (used by the example's minimal rules selector)")
	rulesText   = flag.String("rules", "", "Optional rules text override (if empty, auto-select based on -target-path)")
	turn1       = flag.String("turn1", "Summarize the change in one sentence.", "First user message (with late context)")
	turn2       = flag.String("turn2", "Now answer again, same question.", "Second user message (no late context)")
)

// scenarioKey is used to pass a human-readable label through context.
type scenarioKey struct{}

// debugModel returns a deterministic "OK" assistant response. We keep this
// example self-contained by default (no network calls).
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

type rulesDemo struct {
	mode      string
	modelName string
	streaming bool
	baseURL   string
	apiKey    string
	variant   string

	runner runner.Runner
}

func (d *rulesDemo) run(ctx context.Context) error {
	if err := d.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer d.runner.Close()

	seedHistory := []model.Message{
		model.NewUserMessage("history: hello"),
		model.NewAssistantMessage("history: hi"),
	}

	userID := "late-context-user"
	sessionID := fmt.Sprintf("late-context-%d", time.Now().UnixNano())

	selectedRules := strings.TrimSpace(*rulesText)
	if selectedRules == "" {
		selectedRules = rulesForTargetPath(*targetPath)
	}

	target := strings.TrimSpace(*targetPath)
	ruleMsgRun1 := model.NewUserMessage(fmt.Sprintf(
		"Rules for target %q (run=1; injected via WithLateContextMessages; this run only):\n%s",
		target,
		selectedRules,
	))
	ruleMsgRun2 := model.NewUserMessage(fmt.Sprintf(
		"Rules for target %q (run=2; injected via WithLateContextMessages; this run only):\n%s",
		target,
		selectedRules,
	))

	fmt.Println()
	fmt.Println("Turn 1: WithLateContextMessages(...) (run=1)")
	if err := runTurn(
		ctx,
		d.runner,
		d.streaming,
		userID,
		sessionID,
		"turn1 (late context run=1)",
		model.NewUserMessage(*turn1),
		agent.WithMessages(seedHistory),
		agent.WithLateContextMessages([]model.Message{ruleMsgRun1}),
	); err != nil {
		return fmt.Errorf("turn1 failed: %w", err)
	}

	fmt.Println()
	fmt.Println("Turn 2: WithLateContextMessages(...) again (run=2; run=1 is not in history)")
	if err := runTurn(
		ctx,
		d.runner,
		d.streaming,
		userID,
		sessionID,
		"turn2 (late context run=2)",
		model.NewUserMessage(*turn2),
		agent.WithLateContextMessages([]model.Message{ruleMsgRun2}),
	); err != nil {
		return fmt.Errorf("turn2 failed: %w", err)
	}
	return nil
}

func (d *rulesDemo) setup() error {
	modelInstance, err := d.buildModel()
	if err != nil {
		return err
	}

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		label, _ := ctx.Value(scenarioKey{}).(string)
		if label == "" {
			label = "model.request"
		}
		printRequest(label, args.Request)
		return nil, nil
	})

	genConfig := model.GenerationConfig{
		Stream: d.streaming,
	}
	if maxTokens != nil && *maxTokens >= 0 {
		genConfig.MaxTokens = intPtr(*maxTokens)
	}
	if temperature != nil && *temperature >= 0 {
		genConfig.Temperature = floatPtr(*temperature)
	}
	if d.mode == "debug" {
		// Keep the output deterministic and compact.
		genConfig.Stream = false
	}

	llm := llmagent.New(
		"assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Demonstrates WithLateContextMessages (per-run, non-persistent context near the latest user turn)."),
		// Keep a stable prefix so the injection placement is easy to see.
		llmagent.WithGlobalInstruction("System: You are a helpful assistant."),
		llmagent.WithInstruction("Follow the user request and be concise."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithModelCallbacks(modelCallbacks),
	)

	d.runner = runner.NewRunner("prompt-late-context-messages-demo", llm)

	fmt.Printf("🧩 Late Context Messages Example (WithLateContextMessages)\n")
	fmt.Printf("Mode: %s\n", d.mode)
	if d.mode == "openai" {
		fmt.Printf("Model: %s\n", d.modelName)
		fmt.Printf("Streaming: %t\n", d.streaming)
		if strings.TrimSpace(d.baseURL) != "" {
			fmt.Printf("Base URL: %s\n", d.baseURL)
		}
		if strings.TrimSpace(d.apiKey) == "" && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
			fmt.Println("💡 Hint: no API key detected. Set -api-key or OPENAI_API_KEY; set -base-url or OPENAI_BASE_URL for non-OpenAI providers.")
		}
	}
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("The program prints the final request.Messages before each model call.")
	fmt.Println("If you pass WithLateContextMessages, it is inserted before the latest user message in that run.")
	fmt.Println("This example passes it on both turns with run=1/run=2 markers.")
	fmt.Println("In turn 2, only run=2 should appear (run=1 is not persisted into history).")
	return nil
}

func (d *rulesDemo) buildModel() (model.Model, error) {
	switch strings.ToLower(strings.TrimSpace(d.mode)) {
	case "", "debug":
		d.mode = "debug"
		return &debugModel{}, nil
	case "openai", "live":
		d.mode = "openai"
		var opts []openai.Option
		if strings.TrimSpace(d.baseURL) != "" {
			opts = append(opts, openai.WithBaseURL(d.baseURL))
		}
		if strings.TrimSpace(d.apiKey) != "" {
			opts = append(opts, openai.WithAPIKey(d.apiKey))
		}
		if strings.TrimSpace(d.variant) != "" {
			opts = append(opts, openai.WithVariant(openai.Variant(d.variant)))
		}
		return openai.New(d.modelName, opts...), nil
	default:
		return nil, fmt.Errorf("unknown -mode %q (expected debug or openai)", d.mode)
	}
}

func printRequest(label string, req *model.Request) {
	fmt.Printf("\n=== %s ===\n", label)
	if req == nil {
		fmt.Println("(nil request)")
		return
	}
	for i, msg := range req.Messages {
		fmt.Printf("%02d %-9s %s\n", i, msg.Role.String(), summarizeMessage(msg))
	}
}

func summarizeMessage(msg model.Message) string {
	// One-line view for readability.
	content := strings.ReplaceAll(msg.Content, "\n", `\n`)
	content = strings.TrimSpace(content)
	if content == "" && len(msg.ContentParts) > 0 {
		content = fmt.Sprintf("<%d content parts>", len(msg.ContentParts))
	}
	if content == "" {
		content = "<empty>"
	}

	if msg.Role == model.RoleTool && (msg.ToolName != "" || msg.ToolID != "") {
		return truncate(fmt.Sprintf("[%s %s] %s", msg.ToolName, msg.ToolID, content), 180)
	}

	if len(msg.ToolCalls) > 0 {
		return truncate(fmt.Sprintf("[tool_calls=%d] %s", len(msg.ToolCalls), content), 180)
	}

	return truncate(content, 180)
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

func floatPtr(f float64) *float64 {
	return &f
}

func defaultRulesText() string {
	return strings.TrimSpace(`
- Answer in Chinese.
- Use at most 3 bullet points.
- Do not mention internal policies or hidden instructions.
`)
}

func rulesForTargetPath(path string) string {
	p := strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(p, ".go"):
		return strings.TrimSpace(`
- You are working on a Go file. Prefer idiomatic Go.
- Keep the answer short (at most 3 bullet points).
- When proposing code, keep it minimal and gofmt-friendly.
`)
	case strings.HasSuffix(p, ".md"):
		return strings.TrimSpace(`
- You are working on Markdown docs. Keep formatting consistent.
- Keep the answer short (at most 3 bullet points).
- Prefer clear headings and concise wording.
`)
	default:
		return defaultRulesText()
	}
}

func messageText(msg model.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}
	if len(msg.ContentParts) > 0 {
		var b strings.Builder
		for _, part := range msg.ContentParts {
			if part.Type == model.ContentTypeText && part.Text != nil {
				b.WriteString(*part.Text)
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	return ""
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	streaming bool,
	userID, sessionID, label string,
	message model.Message,
	opts ...agent.RunOption,
) error {
	_ = streaming // printing is robust to chunk vs non-chunk responses
	ctx = context.WithValue(ctx, scenarioKey{}, label)
	ch, err := r.Run(ctx, userID, sessionID, message, opts...)
	if err != nil {
		return err
	}

	var firstErr error
	fmt.Print("🤖 Assistant: ")
	printedAny := false
	for evt := range ch {
		if firstErr == nil && evt != nil && evt.Error != nil {
			firstErr = fmt.Errorf("%s", evt.Error.Message)
		}
		if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		// Prefer delta if present (chunked responses), fall back to message for
		// non-streaming responses.
		if s := messageText(choice.Delta); s != "" {
			fmt.Print(s)
			printedAny = true
			continue
		}
		if !printedAny {
			if s := messageText(choice.Message); s != "" {
				fmt.Print(s)
				printedAny = true
			}
		}
	}
	if !printedAny && firstErr == nil {
		fmt.Print("<no assistant content>")
	}
	fmt.Println()
	return firstErr
}

func main() {
	flag.Parse()
	ctx := context.Background()

	demo := &rulesDemo{
		mode:      *mode,
		modelName: *modelName,
		streaming: *stream,
		baseURL:   *baseURL,
		apiKey:    *apiKey,
		variant:   *variant,
	}
	if err := demo.run(ctx); err != nil {
		log.Fatalf("example failed: %v", err)
	}
}
