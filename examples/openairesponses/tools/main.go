package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openairesponses "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	modelName := flag.String("model", envOr("OPENAI_MODEL", "gpt-5"), "Model name")
	apiKey := flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")
	baseURL := flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI base URL")
	streaming := flag.Bool("streaming", true, "Enable streaming")
	flag.Parse()

	fmt.Printf("🚀 OpenAI Responses tools\nModel: %s  Streaming: %t\n", *modelName, *streaming)
	opts := []openairesponses.Option{
		openairesponses.WithAPIKey(*apiKey),
		openairesponses.WithStore(false),
	}
	if *baseURL != "" {
		opts = append(opts, openairesponses.WithBaseURL(*baseURL))
	}
	r := runner.NewRunner(
		"openairesponses-tools",
		llmagent.New(
			"chat-assistant",
			llmagent.WithModel(openairesponses.New(*modelName, opts...)),
			llmagent.WithDescription("A helpful AI assistant with a calculator."),
			llmagent.WithInstruction("Use the calculator tool for arithmetic."),
			llmagent.WithGenerationConfig(model.GenerationConfig{
				MaxTokens: intPtr(2000),
				Stream:    *streaming,
			}),
			llmagent.WithTools([]tool.Tool{calculatorTool()}),
		),
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("tools-%d", time.Now().Unix())
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
		if evt.Response == nil {
			continue
		}
		if len(evt.Response.Choices) > 0 && len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
			fmt.Printf("\n🔧 tool calls:\n")
			for _, tc := range evt.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s) %s\n", tc.Function.Name, tc.ID, string(tc.Function.Arguments))
			}
		}
		if streaming && len(evt.Response.Choices) > 0 {
			fmt.Print(evt.Response.Choices[0].Delta.Content)
		} else if !streaming && len(evt.Response.Choices) > 0 && evt.Response.Choices[0].Message.Role == model.RoleAssistant {
			fmt.Print(evt.Response.Choices[0].Message.Content)
		}
		if evt.IsFinalResponse() {
			fmt.Println()
		}
	}
}

func calculatorTool() tool.Tool {
	type args struct {
		Operation string  `json:"operation" jsonschema:"description=The operation to perform,enum=add,enum=subtract,enum=multiply,enum=divide"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	type result struct {
		Result float64 `json:"result"`
	}
	return function.NewFunctionTool(func(_ context.Context, in args) (result, error) {
		var out float64
		switch strings.ToLower(in.Operation) {
		case "add", "+":
			out = in.A + in.B
		case "subtract", "-":
			out = in.A - in.B
		case "multiply", "*":
			out = in.A * in.B
		case "divide", "/":
			if in.B != 0 {
				out = in.A / in.B
			}
		default:
			out = math.NaN()
		}
		return result{Result: out}, nil
	}, function.WithName("calculator"), function.WithDescription("Perform basic mathematical calculations"))
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func intPtr(i int) *int { return &i }
