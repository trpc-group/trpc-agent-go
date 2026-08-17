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
	openairesponses "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	modelName := flag.String("model", envOr("OPENAI_MODEL", "gpt-5"), "Model name")
	apiKey := flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")
	baseURL := flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI base URL")
	streaming := flag.Bool("streaming", true, "Enable streaming")
	flag.Parse()

	fmt.Printf("🚀 OpenAI Responses chat\nModel: %s  Streaming: %t\n", *modelName, *streaming)
	opts := []openairesponses.Option{
		openairesponses.WithAPIKey(*apiKey),
		openairesponses.WithStore(false),
	}
	if *baseURL != "" {
		opts = append(opts, openairesponses.WithBaseURL(*baseURL))
	}
	r := runner.NewRunner(
		"openairesponses-chat",
		llmagent.New(
			"chat-assistant",
			llmagent.WithModel(openairesponses.New(*modelName, opts...)),
			llmagent.WithDescription("A helpful assistant."),
			llmagent.WithInstruction("Stay concise."),
			llmagent.WithGenerationConfig(model.GenerationConfig{
				MaxTokens: intPtr(2000),
				Stream:    *streaming,
			}),
		),
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("chat-%d", time.Now().Unix())
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Type '/exit' to end the conversation")
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" {
			return
		}
		ch, err := r.Run(context.Background(), "demo-user", sessionID, model.NewUserMessage(text))
		if err != nil {
			log.Printf("run: %v", err)
			continue
		}
		printEvents(ch, *streaming)
	}
}

func printEvents(ch <-chan *event.Event, streaming bool) {
	fmt.Print("🤖 Assistant: ")
	for evt := range ch {
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			if evt.IsFinalResponse() {
				fmt.Println()
			}
			continue
		}
		if streaming {
			fmt.Print(evt.Response.Choices[0].Delta.Content)
		} else if evt.Response.Choices[0].Message.Content != "" {
			fmt.Print(evt.Response.Choices[0].Message.Content)
		}
		if evt.IsFinalResponse() {
			fmt.Println()
		}
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func intPtr(i int) *int { return &i }
