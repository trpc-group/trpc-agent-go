// Package main provides a standalone CLI demo showcasing how to wire the
// trpc-agent-go orchestration layer with an LLM agent that exposes two
// simple tools: a calculator and a time query. It starts an HTTP server
// compatible with ADK Web UI for manual testing.
package main

import (
	"flag"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/server/adk"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const defaultListenAddr = ":8080"

func main() {
	var addr string
	flag.StringVar(&addr, "addr", defaultListenAddr, "Listen address")
	flag.Parse()

	// --- Model and tools setup ---
	modelName := "deepseek-chat"
	modelInstance := openai.New(modelName, openai.Options{})

	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	timeTool := function.NewFunctionTool(
		getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	agentName := "assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools"),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
	)

	agents := map[string]agent.Agent{
		agentName: llmAgent,
	}

	server := adk.New(agents)

	log.Infof("CLI server listening on %s (apps: %v)", addr, agents)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Constants & helpers ----------------------------------------------------------
// -----------------------------------------------------------------------------

const (
	opAdd      = "add"
	opSubtract = "subtract"
	opMultiply = "multiply"
	opDivide   = "divide"
)

// Calculator tool input.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

// Calculator tool output.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

// Time tool input.
type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

// Time tool output.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// Calculator tool implementation.
func calculate(args calculatorArgs) calculatorResult {
	var result float64
	switch strings.ToLower(args.Operation) {
	case opAdd:
		result = args.A + args.B
	case opSubtract:
		result = args.A - args.B
	case opMultiply:
		result = args.A * args.B
	case opDivide:
		if args.B != 0 {
			result = args.A / args.B
		}
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}
}

// Time tool implementation.
func getCurrentTime(args timeArgs) timeResult {
	loc := time.Local
	zone := args.Timezone
	if zone != "" {
		var err error
		loc, err = time.LoadLocation(zone)
		if err != nil {
			loc = time.Local
		}
	}
	now := time.Now().In(loc)
	return timeResult{
		Timezone: loc.String(),
		Time:     now.Format("15:04:05"),
		Date:     now.Format("2006-01-02"),
		Weekday:  now.Weekday().String(),
	}
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
