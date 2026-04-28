//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", os.Getenv("MODEL_NAME"), "OpenAI‑compatible model name (e.g., deepseek-v4-flash)")
	baseURL   = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI‑compatible base URL")
	apiKey    = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key")
	verbose   = flag.Bool("v", false, "Verbose: print model/tool metadata")
)

func main() {
	flag.Parse()
	if *modelName == "" {
		*modelName = "deepseek-v4-flash"
	}
	fmt.Printf("🧩 IO Conventions — Tools Node\nModel: %s\n", *modelName)
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
	g, tools, err := buildGraph()
	if err != nil {
		return err
	}

	sub := buildSubAgent(*modelName, *baseURL, *apiKey)
	ga, err := graphagent.New(
		"io-tools",
		g,
		graphagent.WithDescription("Graph I/O with Tools node"),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{sub}),
		// Optionally pass ToolCallbacks/ModelCallbacks via GraphAgent if needed.
	)
	if err != nil {
		return err
	}

	_ = tools // tools var kept to signal declared tools; graph nodes hold actual refs.

	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner("io-tools-app", ga, runner.WithSessionService(sessSvc))
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

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

		evs, err := r.Run(context.Background(), user, session, model.NewUserMessage(msg),
			agent.WithRuntimeState(map[string]any{"request_ts": time.Now().Format(time.RFC3339)}),
		)
		if err != nil {
			fmt.Println("❌", err)
			continue
		}
		_ = stream(evs, *verbose)
	}
	return sc.Err()
}

// buildGraph: parse_input -> llm_decider -> (tools or assistant) -> capture_tool (if tools) -> collect
func buildGraph() (*graph.Graph, map[string]tool.Tool, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField("parsed_time", graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer})
	schema.AddField("meeting", graph.StateField{Type: reflect.TypeOf(map[string]any{}), Reducer: graph.MergeReducer, Default: func() any { return map[string]any{} }})
	schema.AddField("final_payload", graph.StateField{Type: reflect.TypeOf(map[string]any{}), Reducer: graph.MergeReducer, Default: func() any { return map[string]any{} }})

	sg := graph.NewStateGraph(schema)
	sg.AddNode("parse_input", parseInput)

	mdl := openai.New(*modelName, openai.WithBaseURL(*baseURL), openai.WithAPIKey(*apiKey))

	// Define a callable tool that simulates meeting scheduling.
	type args struct{ Title, When string }
	type result struct{ MeetingID, Title, Time string }
	schedule := function.NewFunctionTool(func(ctx context.Context, in args) (result, error) {
		if in.When == "" {
			// Allow runtime override from parsed_time if the LLM omitted it
			inv, _ := agent.InvocationFromContext(ctx)
			if inv != nil && inv.RunOptions.RuntimeState != nil {
				if pt, ok := inv.RunOptions.RuntimeState["parsed_time"].(string); ok && pt != "" {
					in.When = pt
				}
			}
		}
		if in.Title == "" {
			in.Title = "meeting"
		}
		if in.When == "" {
			in.When = time.Now().Format(time.RFC3339)
		}
		id := fmt.Sprintf("mtg-%s", time.Now().Format("20060102-1504"))
		return result{MeetingID: id, Title: in.Title, Time: in.When}, nil
	}, function.WithName("schedule_meeting"), function.WithDescription("Schedule a meeting using Title and When"))

	tools := map[string]tool.Tool{"schedule_meeting": schedule}

	// LLM decider has tool declaration so it may emit tool_calls
	sg.AddLLMNode(
		"llm_decider",
		mdl,
		"You plan user intents. If the user asks to schedule a meeting, CALL the schedule_meeting tool with concise {title, when}. Otherwise, respond briefly.",
		tools,
	)

	// Tools node executes tool calls when present
	sg.AddToolsNode("tools", tools)

	// After tools, capture the JSON response into state["meeting"]
	sg.AddNode("capture_tool", captureTool)

	// Sub‑agent fallback when no tool call
	sg.AddAgentNode("assistant")

	// Collector prints final payload
	sg.AddNode("collect", collect)

	// Wiring with conditional tools edge: llm_decider -> tools OR assistant
	sg.AddToolsConditionalEdges("llm_decider", "tools", "assistant")
	sg.AddEdge("parse_input", "llm_decider")
	sg.AddEdge("tools", "capture_tool")
	sg.AddEdge("capture_tool", "collect")
	sg.AddEdge("assistant", "collect")

	sg.SetEntryPoint("parse_input").SetFinishPoint("collect")
	g, err := sg.Compile()
	return g, tools, err
}

// parseInput writes parsed_time and an intent‑shaping one_shot system message.
func parseInput(ctx context.Context, state graph.State) (any, error) {
	in, _ := state[graph.StateKeyUserInput].(string)
	pt := parseTime(in)
	sys := model.NewSystemMessage("Context: if a scheduling request is present, call schedule_meeting. Parsed time: " + pt)
	return graph.State{
		"parsed_time":                 pt,
		graph.StateKeyOneShotMessages: []model.Message{sys},
	}, nil
}

// captureTool finds the latest tool.response in messages and stores structured JSON in state["meeting"].
func captureTool(ctx context.Context, state graph.State) (any, error) {
	msgs, _ := state[graph.StateKeyMessages].([]model.Message)
	if len(msgs) == 0 {
		return nil, nil
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == model.RoleTool && strings.TrimSpace(m.Content) != "" {
			var out map[string]any
			if err := json.Unmarshal([]byte(m.Content), &out); err == nil {
				return graph.State{"meeting": out}, nil
			}
			break
		}
		if m.Role == model.RoleUser {
			break
		}
	}
	return nil, nil
}

// collect prints final payload (meeting if present + last_response + node_responses + parsed_time)
func collect(ctx context.Context, state graph.State) (any, error) {
	last, _ := state[graph.StateKeyLastResponse].(string)
	parsed, _ := state["parsed_time"].(string)
	var meeting map[string]any
	if v, ok := state["meeting"].(map[string]any); ok {
		meeting = v
	}
	var fromNodes map[string]any
	if m, ok := state[graph.StateKeyNodeResponses].(map[string]any); ok {
		fromNodes = m
	}

	payload := map[string]any{
		"parsed_time":    parsed,
		"meeting":        meeting,
		"agent_last":     last,
		"node_responses": fromNodes,
	}
	b, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Printf("\n📦 Final payload (collector):\n%s\n\n", string(b))
	return graph.State{"final_payload": payload}, nil
}

// buildSubAgent: simple assistant fallback
func buildSubAgent(name, baseURL, apiKey string) agent.Agent {
	mdl := openai.New(name, openai.WithBaseURL(baseURL), openai.WithAPIKey(apiKey))
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(mdl),
		llmagent.WithInstruction("You are a helpful assistant. Be brief. English only."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	)
	return ag
}

func stream(ch <-chan *event.Event, verbose bool) error {
	var streaming bool
	for e := range ch {
		if e == nil {
			continue
		}
		if verbose && e.StateDelta != nil {
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
			if b, ok := e.StateDelta[graph.MetadataKeyTool]; ok && len(b) > 0 {
				var td graph.ToolExecutionMetadata
				if json.Unmarshal(b, &td) == nil {
					if td.Phase == graph.ToolExecutionPhaseStart {
						fmt.Printf("🔧 [TOOL] start %s\n", td.ToolName)
					}
					if td.Phase == graph.ToolExecutionPhaseComplete {
						fmt.Printf("✅ [TOOL] done %s\n", td.ToolName)
					}
				}
			}
		}
		if e.Error != nil {
			fmt.Printf("❌ %s\n", e.Error.Message)
			continue
		}
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
	fmt.Println("   • schedule a meeting tomorrow at 10am titled sync with Alex")
	fmt.Println("   • schedule team standup today at 3 pm")
	fmt.Println("   • tell me a fun fact")
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
		"schedule a meeting tomorrow at 10am titled sync with Alex",
		"tell me a fun fact",
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
