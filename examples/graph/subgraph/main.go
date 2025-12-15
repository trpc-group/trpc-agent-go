//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates subgraph usage by composing a parent GraphAgent
// that delegates to a child GraphAgent via a Subgraph node (sugar over Agent node).
// It shows:
// - Building a child graph (LLM + Tools) and wrapping it as a sub-agent
// - Registering sub-agents on the parent GraphAgent
// - Delegating via AddSubgraphNode("assistant") and streaming forwarded events
// - Passing runtime state from parent to subgraph (tool reads from runtime state)
// - Interactive CLI with Runner using default streaming output
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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
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
	modelName = flag.String("model", os.Getenv("MODEL_NAME"), "OpenAI‑compatible model name (e.g., deepseek-chat)")
	baseURL   = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI‑compatible base URL")
	apiKey    = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key")
	verbose   = flag.Bool("v", false, "Verbose: print model/tool metadata")
	// Subgraph feature toggles
	parentInclude = flag.String("parent-include", "", "Parent include mode: none|filtered|all (empty uses default)")
	subIsolate    = flag.Bool("sub-isolate", false, "Isolate subgraph from parent session messages (include_contents=none)")
	subScope      = flag.String("sub-scope", scopeAssistant, "Subgraph event scope segment (empty disables)")
	subInput      = flag.String("sub-input", inputModeParsed, "Subgraph input mapping: parsed|all")
	subOutput     = flag.String("sub-output", outputModeCustom, "Subgraph output mapping: custom|default")
)

// Deduplicate raw string literals for ids, keys, names, and modes
const (
	defaultModelName = "deepseek-chat"

	appName    = "subgraph-app"
	parentName = "parent"
	// assistantID is the single source of truth for both the subgraph node ID and child agent name.
	assistantID    = "assistant"
	childAgentName = assistantID

	nodeParse     = "parse_input"
	nodeSubgraph  = assistantID
	nodeCollect   = "collect"
	nodeLLMDecide = "llm_decider"
	nodeTools     = "tools"

	toolScheduleName = "schedule_meeting"

	keyParsedTime  = "parsed_time"
	keyMeeting     = "meeting"
	keyFinal       = "final_payload"
	keyChildLast   = "child_last"
	keyChildFinal  = "child_final"
	keyChildFinalK = "child_final_keys"

	helpCmd    = "help"
	samplesCmd = "samples"
	exitCmd    = "exit"
	quitCmd    = "quit"

	includePrefix   = "include "
	includeNone     = "none"
	includeFiltered = "filtered"
	includeAll      = "all"

	scopeAssistant    = assistantID
	inputModeParsed   = "parsed"
	inputModeAll      = "all"
	outputModeCustom  = "custom"
	outputModeDefault = "default"
)

func main() {
	flag.Parse()
	if *modelName == "" {
		*modelName = defaultModelName
	}
	fmt.Printf("🧩 Subgraph Demo (Parent calls Child GraphAgent)\nModel: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 64))
	if *apiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("💡 Hint: provide -api-key/-base-url or set OPENAI_API_KEY/OPENAI_BASE_URL")
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Build child subgraph as a GraphAgent named "assistant".
	childGA, tools, err := buildChildSubgraph(childAgentName)
	if err != nil {
		return err
	}
	_ = tools // declared tools captured in child; kept to signal tool presence

	// Build parent graph that delegates to the child via AddSubgraphNode("assistant").
	parentGraph, err := buildParentGraph(parentConfig{
		SubIsolate: *subIsolate,
		SubScope:   *subScope,
		SubInput:   *subInput,
		SubOutput:  *subOutput,
	})
	if err != nil {
		return err
	}

	// Parent GraphAgent with sub-agent registration.
	parentGA, err := graphagent.New(
		parentName,
		parentGraph,
		graphagent.WithDescription("Parent graph that delegates to child subgraph"),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{childGA}),
	)
	if err != nil {
		return err
	}

	// Runner with in-memory session.
	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner(appName, parentGA, runner.WithSessionService(sessSvc))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	user := "user"
	session := fmt.Sprintf("sess-%d", time.Now().Unix())
	fmt.Printf("✅ Ready. Session: %s\n", session)
	printSamples()

	// Allow overriding parent include_contents at runtime to demonstrate CfgKeyIncludeContents.
	includeMode := strings.ToLower(strings.TrimSpace(*parentInclude)) // empty = default (all)

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
		if strings.EqualFold(msg, exitCmd) || strings.EqualFold(msg, quitCmd) {
			break
		}
		if strings.EqualFold(msg, helpCmd) {
			printHelp()
			continue
		}
		if strings.EqualFold(msg, samplesCmd) {
			printSamples()
			continue
		}
		// Commands to control parent include_contents: include none|filtered|all
		if strings.HasPrefix(strings.ToLower(msg), includePrefix) {
			mode := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(msg), includePrefix))
			switch mode {
			case includeNone, includeFiltered, includeAll:
				includeMode = mode
				fmt.Printf("include_contents set to %s for subsequent runs\n", includeMode)
			default:
				fmt.Println("unknown include mode; use: include none|filtered|all")
			}
			continue
		}

		runOpts := []agent.RunOption{}
		if includeMode != "" {
			runOpts = append(runOpts, agent.WithRuntimeState(map[string]any{graph.CfgKeyIncludeContents: includeMode}))
		}
		evs, err := r.Run(context.Background(), user, session, model.NewUserMessage(msg), runOpts...)
		if err != nil {
			fmt.Println("❌", err)
			continue
		}
		_ = stream(evs, *verbose)
	}
	return sc.Err()
}

// buildChildSubgraph constructs a child graph (LLM + Tools) and wraps it as a GraphAgent.
// The tool demonstrates reading runtime state injected by the parent (e.g., parsed_time).
func buildChildSubgraph(name string) (*graphagent.GraphAgent, map[string]tool.Tool, error) {
	mdl := openai.New(*modelName, openai.WithBaseURL(*baseURL), openai.WithAPIKey(*apiKey))

	// Define a callable tool that simulates scheduling and reads parsed time from runtime state if missing.
	type args struct{ Title, When string }
	type result struct{ MeetingID, Title, Time string }
	schedule := function.NewFunctionTool(func(ctx context.Context, in args) (result, error) {
		if in.When == "" {
			// Read from runtime state injected by the parent graph.
			inv, _ := agent.InvocationFromContext(ctx)
			if inv != nil && inv.RunOptions.RuntimeState != nil {
				if pt, ok := inv.RunOptions.RuntimeState[keyParsedTime].(string); ok && pt != "" {
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
		id := fmt.Sprintf("mtg-%s", time.Now().Format("20060102-150405"))
		return result{MeetingID: id, Title: in.Title, Time: in.When}, nil
	}, function.WithName(toolScheduleName), function.WithDescription("Schedule a meeting using Title and When"))

	tools := map[string]tool.Tool{toolScheduleName: schedule}

	// Child graph schema and structure (LLM -> Tools (conditional) -> LLM).
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode(
		nodeLLMDecide,
		mdl,
		"You are an assistant. If user asks to schedule a meeting, CALL the schedule_meeting tool with concise {title, when}. Otherwise, answer briefly.",
		tools,
	)
	sg.AddToolsNode(nodeTools, tools)
	// If llm_decider emits tool_calls, go to tools; otherwise finish.
	sg.AddToolsConditionalEdges(nodeLLMDecide, nodeTools, graph.End)
	// After tools, route back to LLM to get a natural assistant message summarizing tool results.
	sg.AddEdge(nodeTools, nodeLLMDecide)
	sg.SetEntryPoint(nodeLLMDecide).SetFinishPoint(nodeLLMDecide)
	g, err := sg.Compile()
	if err != nil {
		return nil, nil, err
	}

	ga, err := graphagent.New(name, g, graphagent.WithDescription("Child subgraph: assistant with tools"))
	if err != nil {
		return nil, nil, err
	}
	return ga, tools, nil
}

// buildParentGraph constructs the parent graph.
// Nodes: parse_input -> assistant(subgraph) -> collect
type parentConfig struct {
	SubIsolate bool
	SubScope   string
	SubInput   string // parsed|all
	SubOutput  string // custom|default
}

func buildParentGraph(cfg parentConfig) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField(keyParsedTime, graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer})
	schema.AddField(keyMeeting, graph.StateField{Type: reflect.TypeOf(map[string]any{}), Reducer: graph.MergeReducer, Default: func() any { return map[string]any{} }})
	schema.AddField(keyFinal, graph.StateField{Type: reflect.TypeOf(map[string]any{}), Reducer: graph.MergeReducer, Default: func() any { return map[string]any{} }})

	sg := graph.NewStateGraph(schema)
	sg.AddNode(nodeParse, parseInput)
	// Construct options per flags to demonstrate all subgraph features.
	var opts []graph.Option
	switch strings.ToLower(cfg.SubInput) {
	case inputModeParsed:
		opts = append(opts, graph.WithSubgraphInputMapper(func(parent graph.State) graph.State {
			out := graph.State{}
			if v, ok := parent[keyParsedTime]; ok {
				out[keyParsedTime] = v
			}
			return out
		}))
	case inputModeAll:
		// no input mapper (pass full state via default path)
	}
	if cfg.SubIsolate {
		opts = append(opts, graph.WithSubgraphIsolatedMessages(true))
	}
	if strings.TrimSpace(cfg.SubScope) != "" {
		opts = append(opts, graph.WithSubgraphEventScope(cfg.SubScope))
	}
	switch strings.ToLower(cfg.SubOutput) {
	case outputModeCustom:
		opts = append(opts, graph.WithSubgraphOutputMapper(func(parent graph.State, r graph.SubgraphResult) graph.State {
			return graph.State{
				keyChildLast:  r.LastResponse,
				keyChildFinal: r.FinalState,
			}
		}))
	case outputModeDefault:
		// no custom output mapper; fallback to default last_response + node_responses
	}
	sg.AddSubgraphNode(nodeSubgraph, opts...)
	sg.AddNode(nodeCollect, collect)

	sg.SetEntryPoint(nodeParse)
	sg.AddEdge(nodeParse, nodeSubgraph)
	sg.AddEdge(nodeSubgraph, nodeCollect)
	sg.SetFinishPoint(nodeCollect)

	return sg.Compile()
}

// parseInput writes parsed_time and a one-shot system message to shape the next LLM prompt.
func parseInput(ctx context.Context, state graph.State) (any, error) {
	in, _ := state[graph.StateKeyUserInput].(string)
	pt := parseTime(in)
	sys := model.NewSystemMessage(fmt.Sprintf("Context: if scheduling present, call %s. Parsed time: %s", toolScheduleName, pt))
	return graph.State{
		keyParsedTime:                 pt,
		graph.StateKeyOneShotMessages: []model.Message{sys},
	}, nil
}

// collect extracts tool JSON result into state["meeting"], then prepares a final payload.
func collect(ctx context.Context, state graph.State) (any, error) {
	// Extract latest tool.response JSON (search backward until user).
	var meeting map[string]any
	if msgs, ok := state[graph.StateKeyMessages].([]model.Message); ok {
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if m.Role == model.RoleTool && strings.TrimSpace(m.Content) != "" {
				var out map[string]any
				if err := json.Unmarshal([]byte(m.Content), &out); err == nil {
					meeting = out
				}
				break
			}
			if m.Role == model.RoleUser {
				break
			}
		}
	}

	// last_response may be the parent or child; we also recorded child_last via OutputMapper.
	last, _ := state[graph.StateKeyLastResponse].(string)
	childLast, _ := state[keyChildLast].(string)
	// child_final holds the entire subgraph final state snapshot (serializable keys only).
	var childFinal map[string]any
	if v, ok := state[keyChildFinal].(map[string]any); ok {
		childFinal = v
	}
	parsed, _ := state[keyParsedTime].(string)
	final := map[string]any{
		graph.StateKeyLastResponse: last,
		keyParsedTime:              parsed,
	}
	if meeting != nil {
		final[keyMeeting] = meeting
	}
	if childLast != "" {
		final[keyChildLast] = childLast
	}
	if childFinal != nil {
		final[keyChildFinalK] = len(childFinal)
	}
	return graph.State{
		keyMeeting: meeting,
		keyFinal:   final,
	}, nil
}

// stream consumes events and prints streaming model/tool details and final outputs.
func stream(ch <-chan *event.Event, verbose bool) error {
	var lastFinal string
	for e := range ch {
		if e == nil {
			continue
		}
		// Print streaming deltas.
		if e.Response != nil && len(e.Response.Choices) > 0 {
			choice := e.Response.Choices[0]
			if e.Response.IsPartial && choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				continue
			}
			if !e.Response.IsPartial && choice.Message.Content != "" {
				if choice.Message.Content != lastFinal {
					fmt.Println("\n---")
					fmt.Printf("[%s] %s\n", e.Author, choice.Message.Content)
					lastFinal = choice.Message.Content
				}
				continue
			}
		}
		// Verbose: print tool/model metadata and filter key.
		if verbose && e.StateDelta != nil {
			if e.FilterKey != "" {
				fmt.Printf(
					"\n[filter] %s\n",
					e.FilterKey,
				)
			}
			if b, ok := e.StateDelta[graph.MetadataKeyModel]; ok && len(b) > 0 {
				fmt.Printf("\n[model] %s\n", string(b))
			}
			if b, ok := e.StateDelta[graph.MetadataKeyTool]; ok && len(b) > 0 {
				fmt.Printf("\n[tool] %s\n", string(b))
			}
		}
		// On completion, show final payload if available.
		if e.Done && e.StateDelta != nil {
			if b, ok := e.StateDelta[keyFinal]; ok && len(b) > 0 {
				fmt.Printf("\n[final] %s\n", string(b))
			}
		}
		if e.Error != nil {
			fmt.Printf(
				"\n[error:%s] %s\n",
				e.Error.Type, e.Error.Message,
			)
			continue
		}
	}
	return nil
}

// Helpers

var (
	reTomorrow = regexp.MustCompile("(?i)tomorrow") // simplistic demo parser
	reToday    = regexp.MustCompile("(?i)today|now")
)

func parseTime(s string) string {
	now := time.Now()
	if reTomorrow.MatchString(s) {
		return now.Add(24 * time.Hour).Format(time.RFC3339)
	}
	if reToday.MatchString(s) || strings.TrimSpace(s) == "" {
		return now.Format(time.RFC3339)
	}
	// Fallback: return input verbatim; real parsers can be plugged here.
	return s
}

func printHelp() {
	fmt.Println("Commands: help, samples, exit")
}

func printSamples() {
	fmt.Println("Try:")
	fmt.Println("  schedule a meeting tomorrow at 3pm titled team sync")
	fmt.Println("  hello there")
}
