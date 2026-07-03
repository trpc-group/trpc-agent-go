//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an interactive chat backed by the OpenViking
// context database (https://github.com/volcengine/OpenViking) exposed as agent
// tools. The agent follows OpenViking's "search then read" pattern: it locates
// relevant viking:// URIs with viking_search/viking_find, then reads full
// content with viking_read only where needed.
//
// Prerequisites: an OpenViking server running locally (openviking-server),
// reachable at the URL passed via -openviking (default http://localhost:1933).
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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/openviking"
)

func main() {
	modelName := flag.String("model", "deepseek-v4-flash", "Name of the model to use")
	ovURL := flag.String("openviking", "http://localhost:1933", "OpenViking server URL")
	apiKey := flag.String("openviking-key", os.Getenv("OPENVIKING_API_KEY"), "OpenViking API key")
	account := flag.String("account", envOr("OPENVIKING_ACCOUNT", "default"), "OpenViking account identity (X-OpenViking-Account)")
	user := flag.String("user", envOr("OPENVIKING_USER", "default"), "OpenViking user identity (X-OpenViking-User)")
	profile := flag.String("profile", "agent", "Tool profile: retrieval | agent | admin")
	flag.Parse()

	selectedProfile, err := parseProfile(*profile)
	if err != nil {
		log.Fatalf("%v", err)
	}

	fmt.Printf("OpenViking Tools Chat Demo\n")
	fmt.Printf("Model: %s | OpenViking: %s | Profile: %s\n", *modelName, *ovURL, *profile)
	fmt.Println(strings.Repeat("=", 50))

	ts, err := openviking.NewToolSet(
		openviking.WithBaseURL(*ovURL),
		openviking.WithAPIKey(*apiKey),
		openviking.WithAccount(*account),
		openviking.WithUser(*user),
		openviking.WithProfile(selectedProfile),
	)
	if err != nil {
		log.Fatalf("failed to create OpenViking tool set: %v", err)
	}
	defer ts.Close()

	modelInstance := openai.New(*modelName)
	genConfig := model.GenerationConfig{
		Stream: true,
	}
	llmAgent := llmagent.New(
		"openviking-assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An assistant with access to the OpenViking context database."),
		llmagent.WithInstruction("Use viking_search or viking_find to locate relevant viking:// URIs; they return "+
			"short summaries only. Then call viking_read on just the few most relevant URIs to read full content "+
			"before answering. To stay within the context window, keep token usage low: read content_mode=overview "+
			"(or abstract) first and only escalate to content_mode=read for the specific URIs you truly need. "+
			"Avoid viking_browse with recursive=true and avoid reading many large files at once; prefer targeted "+
			"search over broad directory listings."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithToolSets([]tool.ToolSet{ts}),
	)

	appRunner := runner.NewRunner("openviking-chat", llmAgent)
	defer appRunner.Close()

	userID := "user"
	sessionID := fmt.Sprintf("openviking-session-%d", time.Now().Unix())
	fmt.Printf("Ready. Session: %s (type 'exit' to quit)\n\n", sessionID)

	if err := chatLoop(context.Background(), appRunner, userID, sessionID); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

// envOr returns the value of env key or def when the variable is unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseProfile validates the -profile flag and maps it to a known profile,
// failing fast on typos so a bad value cannot silently expose write tools.
func parseProfile(s string) (openviking.Profile, error) {
	switch openviking.Profile(s) {
	case openviking.ProfileRetrieval, openviking.ProfileAgent, openviking.ProfileAdmin:
		return openviking.Profile(s), nil
	default:
		return "", fmt.Errorf("invalid -profile %q: must be retrieval, agent, or admin", s)
	}
}

// truncateForDisplay caps long tool outputs (by rune) so the chat does not
// flood the terminal with full viking_read content.
func truncateForDisplay(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "...(truncated)"
}

func chatLoop(ctx context.Context, appRunner runner.Runner, userID, sessionID string) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "exit" {
			fmt.Println("Goodbye!")
			return nil
		}
		if err := processMessage(ctx, appRunner, userID, sessionID, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func processMessage(ctx context.Context, appRunner runner.Runner, userID, sessionID, text string) error {
	eventChan, err := appRunner.Run(ctx, userID, sessionID, model.NewUserMessage(text))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	fmt.Print("Assistant: ")
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\n[error] %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Response.Choices) > 0 {
			// Tool responses (the observations returned by the viking_* tools).
			// Printing these makes OpenViking's "search then read" flow visible:
			// the search hit list and the content viking_read pulled back.
			for _, choice := range ev.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("\n[result] %s -> %s\n",
						choice.Message.ToolID,
						truncateForDisplay(strings.TrimSpace(choice.Message.Content), 800))
				}
			}
			choice := ev.Response.Choices[0]
			for _, tc := range choice.Message.ToolCalls {
				fmt.Printf("\n[tool] %s %s\n", tc.Function.Name, string(tc.Function.Arguments))
			}
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
		}
		if ev.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return nil
}
