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
)

func main() {
	flag.Parse()

	fmt.Printf("🧠 Thinking Demo (Reasoning)")
	fmt.Printf("\nModel: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Thinking: %t (tokens=%d)\n", *thinkingEnabled, *thinkingTokens)
	fmt.Println(strings.Repeat("=", 50))

	chat := &thinkingChat{modelName: *modelName, streaming: *streaming}
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
}

func (c *thinkingChat) run(ctx context.Context) error {
	if err := c.setup(ctx); err != nil {
		return err
	}
	return c.startChat(ctx)
}

func (c *thinkingChat) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName)

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

	agent := llmagent.New(
		"thinking-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A focused demo showing reasoning content."),
		llmagent.WithInstruction("Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
	)

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
	fmt.Printf("(Note: dim text indicates internal reasoning; normal text is the final answer)\n\n")
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
			fmt.Printf("\n")
			break
		}
	}
	return nil
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

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
