//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to fetch a prompt from Langfuse, render it
// with runtime variables, and dynamically apply it to an llmagent via the
// existing SetInstruction API before each run.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
	promptlangfuse "trpc.group/trpc-go/trpc-agent-go/prompt/provider/langfuse"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	lfconfig "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse/config"
)

var (
	modelName   = flag.String("model", "deepseek-v4-flash", "Model name to use")
	streaming   = flag.Bool("stream", true, "Enable streaming responses")
	promptName  = flag.String("prompt-name", "movie-critic", "Langfuse prompt name")
	promptLabel = flag.String("prompt-label", "production", "Langfuse prompt label")
	movieTitle  = flag.String("movie", "", "Run once with the given movie title and exit")
	criticLevel = flag.String("critic-level", "expert", "Value for the {{criticlevel}} prompt variable")
	cacheTTL    = flag.Duration("cache-ttl", 30*time.Second, "TTL for the Langfuse prompt source cache")
	followUp    = flag.String("follow-up", "Answer in 2-3 sentences and mention one strength and one weakness.", "User message sent after the instruction is refreshed from Langfuse")
)

func main() {
	flag.Parse()

	ctx := context.Background()
	app := &langfusePromptChat{
		modelName:   *modelName,
		streaming:   *streaming,
		promptName:  *promptName,
		promptLabel: *promptLabel,
		movieTitle:  *movieTitle,
		criticLevel: *criticLevel,
		cacheTTL:    *cacheTTL,
		followUp:    *followUp,
		userID:      "langfuse-demo-user",
	}
	if err := app.run(ctx); err != nil {
		log.Fatalf("example failed: %v", err)
	}
}

type langfusePromptChat struct {
	modelName   string
	streaming   bool
	promptName  string
	promptLabel string
	movieTitle  string
	criticLevel string
	cacheTTL    time.Duration
	followUp    string
	userID      string

	source prompt.Source
	agent  *llmagent.LLMAgent
	runner runner.Runner
}

func (c *langfusePromptChat) run(ctx context.Context) error {
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	if strings.TrimSpace(c.movieTitle) != "" {
		return c.reviewMovie(ctx, c.movieTitle)
	}
	return c.startChat(ctx)
}

func (c *langfusePromptChat) setup(_ context.Context) error {
	cfg := lfconfig.FromEnv()
	if cfg.PublicKey == "" || cfg.SecretKey == "" || cfg.BaseURL == "" {
		return fmt.Errorf("Langfuse config is incomplete; set LANGFUSE_PUBLIC_KEY, LANGFUSE_SECRET_KEY, and LANGFUSE_BASE_URL or LANGFUSE_HOST")
	}

	client := promptlangfuse.NewClient(cfg)
	c.source = client.TextPromptSourceWithOptions(
		c.promptName,
		[]promptlangfuse.FetchOption{
			promptlangfuse.WithLabel(c.promptLabel),
		},
		promptlangfuse.WithCacheTTL(c.cacheTTL),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(800),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	c.agent = llmagent.New(
		"langfuse-movie-critic",
		llmagent.WithModel(openai.New(c.modelName)),
		llmagent.WithDescription("Movie critic demo whose instruction is refreshed from a Langfuse prompt source before each run."),
		llmagent.WithInstruction("You are a helpful movie critic. Give a concise opinion."),
		llmagent.WithGenerationConfig(genConfig),
	)
	c.runner = runner.NewRunner("langfuse-prompt-demo", c.agent)

	fmt.Printf("🎬 Langfuse Prompt + LLMAgent Demo\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Streaming: %t\n", c.streaming)
	fmt.Printf("Prompt: %s (label=%s)\n", c.promptName, c.promptLabel)
	fmt.Printf("Critic level: %s\n", c.criticLevel)
	fmt.Printf("Cache TTL: %s\n", c.cacheTTL)
	fmt.Printf("Type a movie title to refresh the instruction from Langfuse.\n")
	fmt.Printf("Type 'exit' to quit.\n")
	fmt.Println(strings.Repeat("=", 60))
	return nil
}

func (c *langfusePromptChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Default prompt variables come from `examples/prompt/langfuse/prompt.txt`:")
	fmt.Println("   - name: movie-critic")
	fmt.Println("   - label: production")
	fmt.Println("   - variables: {{criticlevel}}, {{movie}}")
	fmt.Println()

	for {
		fmt.Print("🎞️  Movie: ")
		if !scanner.Scan() {
			break
		}

		movie := strings.TrimSpace(scanner.Text())
		if movie == "" {
			continue
		}
		if strings.EqualFold(movie, "exit") {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := c.reviewMovie(ctx, movie); err != nil {
			fmt.Printf("❌ Error: %v\n\n", err)
			continue
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *langfusePromptChat) reviewMovie(ctx context.Context, movie string) error {
	text, rendered, err := c.renderInstruction(ctx, movie)
	if err != nil {
		return err
	}

	// Use the existing llmagent API to update the instruction dynamically.
	c.agent.SetInstruction(rendered)

	if text.Meta.Name != "" {
		fmt.Printf("🧩 Langfuse prompt: %s", text.Meta.Name)
		if text.Meta.Version != "" {
			fmt.Printf(" (v%s)", text.Meta.Version)
		}
		fmt.Println()
	}
	fmt.Printf("📝 Raw prompt:\n%s\n", text.Template)
	fmt.Printf("🛠️ Rendered instruction:\n%s\n", rendered)
	fmt.Printf("🤖 Assistant: ")

	sessionID := fmt.Sprintf("langfuse-%d", time.Now().UnixNano())
	eventChan, err := c.runner.Run(ctx, c.userID, sessionID, model.NewUserMessage(c.followUp))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func (c *langfusePromptChat) renderInstruction(ctx context.Context, movie string) (prompt.Text, string, error) {
	text, err := c.source.FetchPrompt(ctx)
	if err != nil {
		return prompt.Text{}, "", fmt.Errorf("failed to fetch prompt from Langfuse: %w", err)
	}

	rendered, err := text.Render(prompt.RenderEnv{
		Vars: prompt.Vars{
			"criticlevel": c.criticLevel,
			"movie":       movie,
		},
	})
	if err != nil {
		return prompt.Text{}, "", fmt.Errorf("failed to render prompt %q: %w", text.Meta.Name, err)
	}
	return text, rendered, nil
}

func (c *langfusePromptChat) processResponse(eventChan <-chan *event.Event) error {
	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("\n")
			return errors.New(event.Error.Message)
		}

		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			content := c.extractContent(choice)
			if content != "" {
				fmt.Print(content)
			}
		}

		if event.Done {
			fmt.Println()
			break
		}
	}
	return nil
}

func (c *langfusePromptChat) extractContent(choice model.Choice) string {
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
