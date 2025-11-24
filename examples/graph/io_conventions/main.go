//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the I/O conventions of a graph.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", os.Getenv("MODEL_NAME"), "OpenAI‑compatible model name (e.g., deepseek-chat)")
	baseURL   = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI‑compatible base URL")
	apiKey    = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key")
	verbose   = flag.Bool("v", false, "Verbose: print model/tool metadata")
)

func main() {
	flag.Parse()
	if *modelName == "" {
		*modelName = "deepseek-chat"
	}
	fmt.Printf("🔧 IO Conventions Example\nModel: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 56))
	if *apiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("💡 Hint: provide -api-key/-base-url or set OPENAI_API_KEY/OPENAI_BASE_URL")
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Build the graph
	g, err := buildGraph()
	if err != nil {
		return err
	}

	// Sub‑agent: simple English assistant that can read runtime state
	sub := buildSubAgent(*modelName, *baseURL, *apiKey)

	// GraphAgent
	ga, err := graphagent.New(
		"io-conventions",
		g,
		graphagent.WithDescription("Graph I/O conventions demo"),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{sub}),
	)
	if err != nil {
		return err
	}

	// Runner + memory session
	sessionSvc := inmemory.NewSessionService()
	r := runner.NewRunner("io-conventions-app", ga, runner.WithSessionService(sessionSvc))
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Interactive
	user := "user"
	session := fmt.Sprintf("sess-%d", time.Now().Unix())
	fmt.Printf("✅ Ready. Session: %s\n", session)
	printSamples()

	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !sc.Scan() {
			break
		}
		msg := strings.TrimSpace(sc.Text())
		if msg == "" {
			continue
		}
		if strings.EqualFold(msg, "exit") || strings.EqualFold(msg, "quit") {
			break
		}
		if strings.EqualFold(msg, "help") {
			printHelp()
			continue
		}
		if strings.EqualFold(msg, "samples") {
			printSamples()
			continue
		}
		if strings.EqualFold(msg, "demo") {
			runDemo(r, user, session)
			continue
		}

		// Run with runtime state (could carry caller metadata)
		evs, err := r.Run(context.Background(), user, session, model.NewUserMessage(msg),
			agent.WithRuntimeState(map[string]any{"request_ts": time.Now().Format(time.RFC3339)}),
		)
		if err != nil {
			fmt.Println("❌", err)
			continue
		}
		if err := stream(evs, *verbose); err != nil {
			fmt.Println("❌", err)
		}
	}
	return sc.Err()
}

// buildGraph defines: parse_input -> llm_summary -> subagent -> collect
func buildGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	// Custom keys to illustrate I/O flow
	schema.AddField("parsed_time", graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer})
	schema.AddField("llm_summary", graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer})
	schema.AddField("final_payload", graph.StateField{Type: reflect.TypeOf(map[string]any{}), Reducer: graph.MergeReducer, Default: func() any { return map[string]any{} }})

	sg := graph.NewStateGraph(schema)
	sg.AddNode("parse_input", parseInput)

	// LLM node: summarizes intent. It consumes one_shot_messages (from parse_input)
	mdl := openai.New(*modelName, openai.WithBaseURL(*baseURL), openai.WithAPIKey(*apiKey))
	sg.AddLLMNode(
		"llm_summary",
		mdl,
		"You write exactly one short sentence starting with 'Intent:'. Do not explain parsing. Do not discuss time in detail. If a scheduling time exists, include it succinctly in parentheses. English only.",
		nil,
	)

	// Collect LLM's textual output into a custom key for downstream use
	sg.AddNode("capture_llm", captureLLM)

	// Sub‑agent node
	sg.AddAgentNode("assistant")

	// Final collector shows how to read last_response/node_responses/custom keys
	sg.AddNode("collect", collect)

	// Wiring
	sg.AddEdge("parse_input", "llm_summary")
	sg.AddEdge("llm_summary", "capture_llm")
	sg.AddEdge("capture_llm", "assistant")
	sg.AddEdge("assistant", "collect")
	sg.SetEntryPoint("parse_input").SetFinishPoint("collect")
	return sg.Compile()
}

// parseInput extracts a simple time expression and writes both one_shot_messages and parsed_time.
func parseInput(ctx context.Context, state graph.State) (any, error) {
	in, _ := state[graph.StateKeyUserInput].(string)
	pt := parseTime(in)
	// Provide one_shot_messages to guide the LLM node. Make it tool-friendly and constrained.
	sysText := "You will produce exactly one sentence: 'Intent: <short intent>'. " +
		"If a concrete time has been parsed, include it in parentheses. " +
		"Never explain how parsing works. Do not ask questions. English only."
	if pt != "" {
		sysText += " Parsed time: " + pt
	}
	sys := model.NewSystemMessage(sysText)
	oneShot := []model.Message{sys}
	return graph.State{
		"parsed_time":                 pt,
		graph.StateKeyOneShotMessages: oneShot,
	}, nil
}

// captureLLM copies last_response into a custom key
func captureLLM(ctx context.Context, state graph.State) (any, error) {
	s, _ := state[graph.StateKeyLastResponse].(string)
	return graph.State{"llm_summary": s}, nil
}

// collect builds a final payload by reading outputs from previous nodes
func collect(ctx context.Context, state graph.State) (any, error) {
	last, _ := state[graph.StateKeyLastResponse].(string)
	parsed, _ := state["parsed_time"].(string)
	llmSum, _ := state["llm_summary"].(string)
	var fromNodes map[string]any
	if m, ok := state[graph.StateKeyNodeResponses].(map[string]any); ok {
		fromNodes = m
	}

	payload := map[string]any{
		"parsed_time":    parsed,
		"llm_summary":    llmSum,
		"agent_last":     last,
		"node_responses": fromNodes,
	}
	// Pretty‑print once for demo
	b, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Printf("\n📦 Final payload (collector):\n%s\n\n", string(b))
	return graph.State{"final_payload": payload}, nil
}

// buildSubAgent creates a simple assistant that reads parsed_time from runtime state
func buildSubAgent(name, baseURL, apiKey string) agent.Agent {
	mdl := openai.New(name, openai.WithBaseURL(baseURL), openai.WithAPIKey(apiKey))
	mcb := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		inv, _ := agent.InvocationFromContext(ctx)
		if inv == nil || inv.RunOptions.RuntimeState == nil {
			return nil, nil
		}
		pt, _ := inv.RunOptions.RuntimeState["parsed_time"].(string)
		if pt != "" {
			args.Request.Messages = append([]model.Message{model.NewSystemMessage("Use parsed_time=" + pt + " if relevant.")}, args.Request.Messages...)
		}
		return nil, nil
	})
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(mdl),
		llmagent.WithInstruction("You are a helpful assistant. Always answer in English."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		llmagent.WithModelCallbacks(mcb),
	)
	return ag
}

func stream(ch <-chan *event.Event, verbose bool) error {
	var streaming bool
	for e := range ch {
		if e == nil {
			continue
		}
		if e.Error != nil {
			fmt.Printf("❌ %s\n", e.Error.Message)
			continue
		}
		// Model/Tool metadata (from graph LLM node)
		if verbose {
			if e.StateDelta != nil {
				if b, ok := e.StateDelta[graph.MetadataKeyModel]; ok && len(b) > 0 {
					var md graph.ModelExecutionMetadata
					if json.Unmarshal(b, &md) == nil {
						if md.Phase == graph.ModelExecutionPhaseStart {
							fmt.Printf("🤖 [MODEL] start node=%s\n", md.NodeID)
						}
						if md.Phase == graph.ModelExecutionPhaseComplete {
							fmt.Printf("✅ [MODEL] done node=%s\n", md.NodeID)
						}
					}
				}
			}
		}
		// Streaming text
		if len(e.Choices) > 0 {
			c := e.Choices[0]
			if c.Delta.Content != "" {
				if !streaming {
					fmt.Print("💬 ")
					streaming = true
				}
				fmt.Print(c.Delta.Content)
			}
			if c.Delta.Content == "" && streaming {
				fmt.Println()
				streaming = false
			}
		}
		if e.Done {
			break
		}
	}
	return nil
}

// parseTime: tiny helper for demo
func parseTime(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`\b(today|tomorrow)\b.*?\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	if m := re.FindStringSubmatch(s); len(m) >= 3 {
		base := time.Now()
		if m[1] == "tomorrow" {
			base = base.Add(24 * time.Hour)
		}
		h, _ := strconv.Atoi(m[2])
		min := 0
		if m[3] != "" {
			min, _ = strconv.Atoi(m[3])
		}
		ap := m[4]
		if ap == "pm" && h < 12 {
			h += 12
		}
		if ap == "am" && h == 12 {
			h = 0
		}
		t := time.Date(base.Year(), base.Month(), base.Day(), h, min, 0, 0, base.Location())
		return t.Format(time.RFC3339)
	}
	return ""
}

func printSamples() {
	fmt.Println("💡 Type 'help' for commands. Try:")
	fmt.Println("   • schedule a sync tomorrow at 10am")
	fmt.Println("   • today at 3 pm standup")
	fmt.Println("   • summarize what you understood")
}

func printHelp() {
	fmt.Println("📚 Commands:")
	fmt.Println("   help     - show this help")
	fmt.Println("   samples  - print sample inputs")
	fmt.Println("   demo     - run a short scripted demo")
	fmt.Println("   exit     - quit")
}

func runDemo(r runner.Runner, user, session string) {
	inputs := []string{
		"schedule a sync tomorrow at 10am",
		"what did you understand?",
	}
	for _, in := range inputs {
		fmt.Printf("> %s\n", in)
		evs, err := r.Run(context.Background(), user, session, model.NewUserMessage(in))
		if err != nil {
			fmt.Println("❌", err)
			continue
		}
		_ = stream(evs, *verbose)
	}
}
