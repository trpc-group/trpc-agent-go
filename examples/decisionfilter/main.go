//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates decision-based tool filtering, skill visibility
// narrowing and model routing with plugin/toolfilter.
//
// The example wires the three toolfilter integrations into one agent:
//
//   - a toolfilter.ToolCallback as a BeforeModel callback that removes the
//     tools the decision model deems irrelevant for each user turn;
//   - a toolfilter.SkillFilter as a llmagent.WithSkillFilter that narrows
//     the skill overview to the relevant skills of each run;
//   - a toolfilter.ModelRouter as a llmagent.WithModelSelector that picks a
//     cheap or a strong model per run.
//
// All three fail open: when the decision endpoint is unreachable the agent
// behaves exactly as if no filtering were configured.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	openaigo "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolfilter"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	decisionURL := flag.String("decision-url", "https://api.typesafe.ai",
		"System One decision API base URL")
	decisionModel := flag.String("decision-model", "jev-2025-10-10",
		"decision model version")
	cheapModel := flag.String("cheap-model", "deepseek-v4-flash",
		"model name used for simple requests")
	strongModel := flag.String("strong-model", "deepseek-v4",
		"model name used for complex requests")
	flag.Parse()

	decisionKey := os.Getenv("TYPESAFE_API_KEY")

	ctx := context.Background()
	r, err := newRunner(ctx, runnerConfig{
		decisionURL:   *decisionURL,
		decisionModel: *decisionModel,
		decisionKey:   decisionKey,
		cheapModel:    *cheapModel,
		strongModel:   *strongModel,
	})
	if err != nil {
		log.Fatalf("setup failed: %v", err)
	}
	defer r.Close()

	if err := chat(ctx, r); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

type runnerConfig struct {
	decisionURL   string
	decisionModel string
	decisionKey   string
	cheapModel    string
	strongModel   string
}

// newRunner builds the agent with all three toolfilter integrations.
func newRunner(ctx context.Context, cfg runnerConfig) (runner.Runner, error) {
	observer := func(report *toolfilter.Report) {
		fmt.Printf("[toolfilter] %s %s: asked=%d dropped=%v saved~%d tokens",
			report.Catalog, report.Outcome, len(report.Asked), report.Dropped, report.SavedTokens)
		if report.Reason != "" {
			fmt.Printf(" (%s)", report.Reason)
		}
		fmt.Println()
	}

	baseOpts := []toolfilter.Option{
		toolfilter.WithAPIKey(cfg.decisionKey),
		toolfilter.WithDefaultModel(cfg.decisionModel),
		toolfilter.WithObserver(observer),
	}

	// 1. Tool pre-filtering: one decision request per user turn rewrites
	// the tool list of each model request.
	toolCallback, err := toolfilter.NewToolCallback(cfg.decisionURL, baseOpts...)
	if err != nil {
		return nil, fmt.Errorf("tool callback: %w", err)
	}

	// 2. Skill visibility: one batch decision per run narrows the skill
	// overview. The repository must be the one mounted on the agent.
	repo := newSkillRepo()
	skillFilter, err := toolfilter.NewSkillFilter(cfg.decisionURL, repo,
		append(baseOpts, toolfilter.WithKeepSkills("core-workflow"))...)
	if err != nil {
		return nil, fmt.Errorf("skill filter: %w", err)
	}

	// 3. Model routing: one Choice decision per run picks the model.
	cheap := openai.New(cfg.cheapModel, openai.WithChatRequestCallback(printTools))
	strong := openai.New(cfg.strongModel, openai.WithChatRequestCallback(printTools))
	modelRouter, err := toolfilter.NewModelRouter(cfg.decisionURL,
		append(baseOpts,
			toolfilter.WithModelCandidate("cheap", cheap,
				"fast and cheap: simple lookups, arithmetic, formatting"),
			toolfilter.WithModelCandidate("strong", strong,
				"complex reasoning: long-context analysis, code review"),
			toolfilter.WithFallbackModel(cheap),
		)...)
	if err != nil {
		return nil, fmt.Errorf("model router: %w", err)
	}

	agt := llmagent.New(
		"decision-filtered-agent",
		llmagent.WithModel(cheap),
		llmagent.WithDescription("A general assistant with decision-based filtering"),
		llmagent.WithInstruction(
			"You are a helpful assistant. Use your tools when they help.",
		),
		llmagent.WithTools(tools()),
		llmagent.WithSkills(repo),
		llmagent.WithSkillFilter(skillFilter.VisibilityFilter()),
		llmagent.WithModelSelector(modelRouter.Select),
		llmagent.WithModelCallbacks(&model.Callbacks{
			BeforeModel: []model.BeforeModelCallbackStructured{
				toolCallback.Callback(),
			},
		}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(2000),
			Temperature: floatPtr(0.7),
			Stream:      true,
		}),
	)
	return runner.NewRunner("decision-filter-demo", agt), nil
}

// printTools shows which tools survived filtering for the current request.
func printTools(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Function.Name)
	}
	fmt.Printf("📋 Tools sent to model: %v\n", names)
}

// chat runs the interactive conversation loop.
func chat(ctx context.Context, r runner.Runner) error {
	userID := "user"
	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("🚀 Decision filter demo (session %s)\n", sessionID)
	fmt.Println("Try: 'what time is it in words', 'calculate 12*7', 'review this algorithm idea'")
	fmt.Println("Enter 'exit' to end.")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 User: ")
		if !scanner.Scan() {
			return nil
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		eventChan, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(input))
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			continue
		}
		if err := printResponse(eventChan); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
}

// printResponse streams the assistant response.
func printResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Printf("\n❌ %s\n", evt.Error.Message)
			continue
		}
		if len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if len(choice.Message.ToolCalls) > 0 {
			for _, call := range choice.Message.ToolCalls {
				fmt.Printf("\n🔧 %s(%s)", call.Function.Name, call.Function.Arguments)
			}
			continue
		}
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
		}
		if evt.IsFinalResponse() {
			fmt.Println()
			return nil
		}
	}
	return nil
}

// tools returns a catalog large enough for filtering to pay off (the
// callback skips catalogs at or below the minimum tool count).
func tools() []tool.Tool {
	return []tool.Tool{
		calculatorTool(),
		textTool(),
		timeTool(),
		wordCountTool(),
		diceTool(),
		reverseTool(),
		bmiTool(),
		temperatureTool(),
		qrTool(),
	}
}

func calculatorTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Expression string `json:"expression" jsonschema:"description=Mathematical expression,required"`
		}) (struct {
			Result string `json:"result"`
		}, error) {
			expr := strings.ReplaceAll(req.Expression, " ", "")
			var result float64
			var left, right string
			var op byte
			for _, candidate := range []byte{'+', '-', '*', '/'} {
				if i := strings.IndexByte(expr, candidate); i > 0 {
					left, op, right = expr[:i], candidate, expr[i+1:]
					break
				}
			}
			if op == 0 {
				return struct {
					Result string `json:"result"`
				}{}, fmt.Errorf("unsupported expression")
			}
			l, err1 := parseFloat(left)
			rv, err2 := parseFloat(right)
			if err1 != nil || err2 != nil {
				return struct {
					Result string `json:"result"`
				}{}, fmt.Errorf("invalid numbers")
			}
			switch op {
			case '+':
				result = l + rv
			case '-':
				result = l - rv
			case '*':
				result = l * rv
			case '/':
				if rv == 0 {
					return struct {
						Result string `json:"result"`
					}{}, fmt.Errorf("division by zero")
				}
				result = l / rv
			}
			return struct {
				Result string `json:"result"`
			}{Result: fmt.Sprintf("%g", result)}, nil
		},
		function.WithName("calculator"),
		function.WithDescription("Perform basic arithmetic on two numbers"),
	)
}

func parseFloat(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%g", &v)
	return v, err
}

func textTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Text      string `json:"text" jsonschema:"description=Text to process,required"`
			Operation string `json:"operation" jsonschema:"description=uppercase or lowercase,required"`
		}) (struct {
			Result string `json:"result"`
		}, error) {
			out := req.Text
			switch req.Operation {
			case "uppercase":
				out = strings.ToUpper(req.Text)
			case "lowercase":
				out = strings.ToLower(req.Text)
			}
			return struct {
				Result string `json:"result"`
			}{Result: out}, nil
		},
		function.WithName("text_tool"),
		function.WithDescription("Convert text to uppercase or lowercase"),
	)
}

func timeTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (struct {
			Result string `json:"result"`
		}, error) {
			return struct {
				Result string `json:"result"`
			}{Result: time.Now().Format("2006-01-02 15:04:05")}, nil
		},
		function.WithName("time_tool"),
		function.WithDescription("Get the current date and time"),
	)
}

func wordCountTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Text string `json:"text" jsonschema:"description=Text to count,required"`
		}) (struct {
			Result int `json:"result"`
		}, error) {
			return struct {
				Result int `json:"result"`
			}{Result: len(strings.Fields(req.Text))}, nil
		},
		function.WithName("word_count"),
		function.WithDescription("Count the words in a text"),
	)
}

func diceTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Sides int `json:"sides" jsonschema:"description=Number of sides,required"`
		}) (struct {
			Result int `json:"result"`
		}, error) {
			if req.Sides < 2 {
				req.Sides = 6
			}
			return struct {
				Result int `json:"result"`
			}{Result: rand.Intn(req.Sides) + 1}, nil
		},
		function.WithName("dice_roll"),
		function.WithDescription("Roll a dice with the given number of sides"),
	)
}

func reverseTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Text string `json:"text" jsonschema:"description=Text to reverse,required"`
		}) (struct {
			Result string `json:"result"`
		}, error) {
			runes := []rune(req.Text)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return struct {
				Result string `json:"result"`
			}{Result: string(runes)}, nil
		},
		function.WithName("reverse_text"),
		function.WithDescription("Reverse the characters of a text"),
	)
}

func bmiTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			WeightKG float64 `json:"weight_kg" jsonschema:"description=Weight in kilograms,required"`
			HeightM  float64 `json:"height_m" jsonschema:"description=Height in meters,required"`
		}) (struct {
			Result string `json:"result"`
		}, error) {
			if req.HeightM <= 0 {
				return struct {
					Result string `json:"result"`
				}{}, fmt.Errorf("height must be positive")
			}
			bmi := req.WeightKG / (req.HeightM * req.HeightM)
			return struct {
				Result string `json:"result"`
			}{Result: fmt.Sprintf("%.1f", bmi)}, nil
		},
		function.WithName("bmi_calculator"),
		function.WithDescription("Compute the body mass index from weight and height"),
	)
}

func temperatureTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Celsius float64 `json:"celsius" jsonschema:"description=Temperature in Celsius,required"`
		}) (struct {
			Result string `json:"result"`
		}, error) {
			return struct {
				Result string `json:"result"`
			}{Result: fmt.Sprintf("%.1f F", req.Celsius*9/5+32)}, nil
		},
		function.WithName("temperature_convert"),
		function.WithDescription("Convert a Celsius temperature to Fahrenheit"),
	)
}

func qrTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, req struct {
			Text string `json:"text" jsonschema:"description=Text to encode,required"`
		}) (struct {
			Result string `json:"result"`
		}, error) {
			return struct {
				Result string `json:"result"`
			}{Result: "qr:" + req.Text}, nil
		},
		function.WithName("qr_encode"),
		function.WithDescription("Encode a short text as a QR code reference"),
	)
}

// newSkillRepo returns a static in-memory skill repository.
func newSkillRepo() skill.Repository {
	return &staticSkillRepo{summaries: []skill.Summary{
		{Name: "core-workflow", Description: "core workflow rules for this assistant"},
		{Name: "travel-booking", Description: "find and book flights and hotels"},
		{Name: "expense-report", Description: "file expense reports with receipts"},
		{Name: "math-helper", Description: "step-by-step arithmetic guidance"},
		{Name: "cooking", Description: "recipes and menu planning"},
		{Name: "email-drafting", Description: "draft polite business emails"},
	}}
}

// staticSkillRepo implements skill.Repository with fixed content.
type staticSkillRepo struct {
	summaries []skill.Summary
}

// Summaries implements skill.Repository.
func (r *staticSkillRepo) Summaries() []skill.Summary { return r.summaries }

// Get implements skill.Repository.
func (r *staticSkillRepo) Get(name string) (*skill.Skill, error) {
	for _, s := range r.summaries {
		if s.Name == name {
			return &skill.Skill{
				Summary: s,
				Body:    fmt.Sprintf("Guidance for %s: follow the %s process.", s.Name, s.Name),
			}, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

// Path implements skill.Repository.
func (r *staticSkillRepo) Path(name string) (string, error) {
	return "", fmt.Errorf("static skill repository has no paths")
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
