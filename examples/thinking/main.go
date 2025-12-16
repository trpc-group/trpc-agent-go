//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates reasoning/thinking mode using the Runner with streaming output.
package main

import (
	"bufio"
	"context"
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
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName       = flag.String("model", "deepseek-reasoner", "Name of the model to use")
	streaming       = flag.Bool("streaming", true, "Enable streaming mode for responses")
	thinkingEnabled = flag.Bool("thinking", true, "Enable reasoning/thinking mode if provider supports it")
	thinkingTokens  = flag.Int("thinking-tokens", 2048, "Max reasoning tokens if provider supports it")
	variant         = flag.String("variant", "openai", "Name of Variant to use when use openai provider, openai / hunyuan / deepseek / qwen")
	debug           = flag.Bool("debug", true, "Print messages sent to model API for debugging")
	reasoningMode   = flag.String("reasoning-mode", "discard_previous",
		"How to handle reasoning_content in history: keep_all, discard_previous, discard_all")
)

func main() {
	flag.Parse()

	fmt.Printf("🧠 Thinking Demo (Reasoning)")
	fmt.Printf("\nModel: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Thinking: %t (tokens=%d)\n", *thinkingEnabled, *thinkingTokens)
	fmt.Printf("Reasoning Mode: %s\n", *reasoningMode)
	fmt.Printf("Debug: %t\n", *debug)
	fmt.Println(strings.Repeat("=", 50))

	chat := &thinkingChat{modelName: *modelName, streaming: *streaming, variant: *variant}
	if err := chat.run(context.Background()); err != nil {
		log.Fatalf("Thinking demo failed: %v", err)
	}
}

type thinkingChat struct {
	modelName string
	streaming bool
	runner    runner.Runner
	userID    string
	sessionID string
	appName   string
	sessSvc   session.Service
	variant   string
}

func (c *thinkingChat) run(ctx context.Context) error {
	if err := c.setup(ctx); err != nil {
		return err
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	return c.startChat(ctx)
}

func (c *thinkingChat) setup(_ context.Context) error {
	modelOpts := []openai.Option{openai.WithVariant(openai.Variant(c.variant))}

	// Add debug callback if enabled.
	if *debug {
		modelOpts = append(modelOpts, openai.WithChatRequestCallback(printChatRequestMessages))
	}

	modelInstance := openai.New(c.modelName, modelOpts...)

	// always use in-memory session for this demo
	var sessionService session.Service = sessioninmemory.NewSessionService()

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}
	if thinkingEnabled != nil && *thinkingEnabled {
		genConfig.ThinkingEnabled = thinkingEnabled
		genConfig.ThinkingTokens = thinkingTokens
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A focused demo showing reasoning content with optional tools."),
		llmagent.WithInstruction("Be helpful and conversational. Use tools when appropriate."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(buildTools()),
	}
	// Add reasoning content mode based on flag.
	agentOpts = append(agentOpts, llmagent.WithReasoningContentMode(resolveReasoningMode()))

	agent := llmagent.New("thinking-assistant", agentOpts...)

	c.runner = runner.NewRunner(
		"thinking-demo",
		agent,
		runner.WithSessionService(sessionService),
	)
	c.userID = "user"
	c.sessionID = fmt.Sprintf("thinking-session-%d", time.Now().Unix())
	c.appName = "thinking-demo"
	c.sessSvc = sessionService
	fmt.Printf("✅ Ready! Session: %s\n", c.sessionID)
	fmt.Println()
	return nil
}

func (c *thinkingChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💡 Special commands:")
	fmt.Println("   /history  - Show conversation history")
	fmt.Println("   /new      - Start a new session")
	fmt.Println("   /exit     - End the conversation")
	fmt.Println()
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		msg := strings.TrimSpace(scanner.Text())
		if msg == "" {
			continue
		}
		switch strings.ToLower(msg) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/history":
			if err := c.showHistory(ctx); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}
			fmt.Println()
			continue
		case "/new":
			c.startNewSession()
			continue
		}
		if err := c.processMessage(ctx, msg); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func (c *thinkingChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return err
	}
	return c.processResponse(eventChan)
}

func (c *thinkingChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	assistantStarted := false
	printedReasoning := false
	reasoningClosed := false
	for e := range eventChan {
		if e.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", e.Error.Message)
			continue
		}
		// Show reasoning content.
		if len(e.Response.Choices) > 0 {
			ch := e.Response.Choices[0]
			if c.streaming {
				if rc := ch.Delta.ReasoningContent; rc != "" {
					// Dim style for reasoning content.
					fmt.Printf("\x1b[2m%s\x1b[0m", rc)
					printedReasoning = true
				}
			} else {
				if rc := ch.Message.ReasoningContent; rc != "" {
					// Dim style for reasoning content.
					fmt.Printf("\x1b[2m%s\x1b[0m\n", rc)
				}
			}
			// Show normal content.
			content := c.extractContent(ch)
			if content != "" {
				// Insert a newline once between reasoning and normal content in streaming mode.
				if c.streaming && printedReasoning && !reasoningClosed {
					fmt.Print("\n\n")
					reasoningClosed = true
				}
				if !assistantStarted {
					assistantStarted = true
				}
				fmt.Print(content)
			}
		}
		if e.IsFinalResponse() {
			// Print timing information at the end
			c.printTimingInfo(e)
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

// printTimingInfo displays timing information from the final event.
func (c *thinkingChat) printTimingInfo(event *event.Event) {
	colorReset := "\033[0m"
	colorYellow := "\033[33m" // For timing info
	if event.Response == nil || event.Response.Usage == nil || event.Response.Usage.TimingInfo == nil {
		return
	}

	timing := event.Response.Usage.TimingInfo
	fmt.Printf("\n\n%s⏱️  Timing Info:%s\n", colorYellow, colorReset)

	// Time to first token
	if timing.FirstTokenDuration > 0 {
		fmt.Printf("%s   • Time to first token: %v%s\n", colorYellow, timing.FirstTokenDuration, colorReset)
	}

	// Reasoning duration
	if timing.ReasoningDuration > 0 {
		fmt.Printf("%s   • Reasoning: %v%s\n", colorYellow, timing.ReasoningDuration, colorReset)
	}

	// Token usage
	if event.Response.Usage.TotalTokens > 0 {
		fmt.Printf("%s   • Tokens: %d (prompt: %d, completion: %d%s)\n",
			colorYellow,
			event.Response.Usage.TotalTokens,
			event.Response.Usage.PromptTokens,
			event.Response.Usage.CompletionTokens,
			colorReset)
	}
}

func (c *thinkingChat) extractContent(choice model.Choice) string {
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func (c *thinkingChat) showHistory(ctx context.Context) error {
	if c.sessSvc == nil {
		return fmt.Errorf("session service not initialized")
	}
	key := session.Key{AppName: c.appName, UserID: c.userID, SessionID: c.sessionID}
	sess, err := c.sessSvc.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if sess == nil {
		fmt.Println("(no session found)")
		return nil
	}
	evts := sess.GetEvents()
	if len(evts) == 0 {
		fmt.Println("(no events)")
		return nil
	}
	fmt.Println("\n===== Session History =====")
	for i, evt := range evts {
		author := evt.Author
		ts := evt.Timestamp.Format(time.RFC3339)
		fmt.Printf("[%02d] %s %s\n", i+1, ts, author)
		if evt.Response == nil || len(evt.Choices) == 0 {
			continue
		}
		ch := evt.Choices[0]
		// Print reasoning (dim) if present in final message.
		if rc := ch.Message.ReasoningContent; rc != "" {
			fmt.Printf("\x1b[2m%s\x1b[0m\n\n", rc)
		}
		// Then print visible content.
		if content := ch.Message.Content; content != "" {
			fmt.Println(content)
		}
		fmt.Println("--------------------------")
	}
	fmt.Println("===== End =====")
	return nil
}

func (c *thinkingChat) startNewSession() {
	old := c.sessionID
	c.sessionID = fmt.Sprintf("thinking-session-%d", time.Now().Unix())
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", old)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

// resolveReasoningMode converts the flag value to llmagent constant.
func resolveReasoningMode() string {
	switch *reasoningMode {
	case "discard_previous", "discard-previous":
		return llmagent.ReasoningContentModeDiscardPreviousTurns
	case "discard_all", "discard-all":
		return llmagent.ReasoningContentModeDiscardAll
	default:
		return llmagent.ReasoningContentModeKeepAll
	}
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
