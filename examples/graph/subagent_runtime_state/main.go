//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using a sub-agent inside a GraphAgent while
// propagating graph state to the sub-agent via Invocation.RunOptions.RuntimeState.
// It shows:
//   - Pre node parses time and loads scene info → writes to state
//   - Sub-agent (LLMAgent) reads the graph state from ctx in model/tool callbacks
//   - Tools use parsed time from runtime state instead of LLM-guessed values
//   - Interactive streaming output from the single graph event channel
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

const (
	defaultModelName = "deepseek-chat"
)

var (
	modelName = flag.String("model", defaultModelName, "Model name (OpenAI compatible)")
	sceneID   = flag.String("scene", "scene-1", "Scene ID to fetch context for")
	baseURL   = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL (optional)")
	apiKey    = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key (optional; falls back to env)")
)

func main() {
	flag.Parse()
	fmt.Printf("🚀 Sub‑Agent Runtime State (GraphAgent)\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if *apiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("💡 Hint: no API key detected. Set -api-key or OPENAI_API_KEY; set -base-url or OPENAI_BASE_URL for non-OpenAI providers.")
	}
	// Build graph
	g, subAgent, err := buildGraphAndSubAgent(*modelName, *baseURL, *apiKey)
	if err != nil {
		return err
	}

	// GraphAgent with sub‑agent and initial empty state
	ga, err := graphagent.New(
		"coordinator",
		g,
		graphagent.WithDescription("Graph with pre‑processing and LLMAgent sub‑agent"),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{subAgent}),
	)
	if err != nil {
		return err
	}

	// Runner + session service
	sessionSvc := inmemory.NewSessionService()
	r := runner.NewRunner("subagent-runtime-state", ga, runner.WithSessionService(sessionSvc))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Interactive loop
	userID := "user"
	sessionID := fmt.Sprintf("subagent-%d", time.Now().Unix())
	fmt.Printf("✅ Ready. Session: %s\n", sessionID)
	fmt.Println("💡 Enter your request (type 'exit' to quit)")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			fmt.Println("👋 Bye!")
			return nil
		}

		// Build runtime state for this run; pre node will also add to/transform it
		rt := map[string]any{
			"scene_id": *sceneID,
		}

		evts, err := r.Run(
			context.Background(),
			userID,
			sessionID,
			model.NewUserMessage(input),
			agent.WithRuntimeState(rt),
		)
		if err != nil {
			fmt.Printf("❌ Run error: %v\n", err)
			continue
		}
		if err := streamEvents(evts); err != nil {
			fmt.Printf("❌ Stream error: %v\n", err)
		}
	}
	return scanner.Err()
}

// buildGraphAndSubAgent constructs:
//   - Graph with a pre node then an agent node "assistant"
//   - LLMAgent sub‑agent with callbacks reading graph state from ctx
func buildGraphAndSubAgent(modelName, baseURL, apiKey string) (*graph.Graph, agent.Agent, error) {
	// 1) Sub‑agent tools
	// schedule_meeting uses parsed_time from runtime state
	type scheduleArgs struct {
		Title string `json:"title"`
		When  string `json:"when,omitempty"` // ignored if runtime provides parsed_time
	}
	type scheduleResult struct {
		ScheduledAt string `json:"scheduled_at"`
		Title       string `json:"title"`
		Source      string `json:"source"`
	}
	scheduleTool := function.NewFunctionTool(func(ctx context.Context, in scheduleArgs) (scheduleResult, error) {
		inv, _ := agent.InvocationFromContext(ctx)
		used := in.When
		if inv != nil && inv.RunOptions.RuntimeState != nil {
			if v, ok := inv.RunOptions.RuntimeState["parsed_time"].(string); ok && v != "" {
				used = v // override with graph‑parsed time
			}
		}
		if used == "" {
			used = time.Now().Format(time.RFC3339)
		}
		return scheduleResult{ScheduledAt: used, Title: in.Title, Source: "tool"}, nil
	},
		function.WithName("schedule_meeting"),
		function.WithDescription("Schedule a meeting using pre‑parsed time from runtime state"),
	)

	// 2) Sub‑agent model callbacks – inject scene knowledge (English, tool-friendly)
	modelCbs := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		inv, _ := agent.InvocationFromContext(ctx)
		if inv == nil || inv.RunOptions.RuntimeState == nil {
			return nil, nil
		}
		sceneInfo, _ := inv.RunOptions.RuntimeState["scene_info"].(string)
		if sceneInfo != "" {
			// Prepend a system message carrying scene knowledge and guidance.
			// Always keep it English and tool-friendly to avoid suppressing tool calls.
			sys := sceneInfo + `\n\nGuidance:\n- Always respond in English.\n- If the user asks to schedule a meeting, CALL the schedule_meeting tool.\n- Use parsed_time from runtime state when present. If time is missing/ambiguous, ask a clarifying question.\n- Derive a concise meeting title from the user request.`
			args.Request.Messages = append([]model.Message{model.NewSystemMessage(sys)}, args.Request.Messages...)
		}
		return nil, nil
	})

	toolCbs := (&tool.Callbacks{}).RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		// Demonstrate we can read parsed_time from runtime state inside sub‑agent callback
		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil && inv.RunOptions.RuntimeState != nil {
			if v, ok2 := inv.RunOptions.RuntimeState["parsed_time"].(string); ok2 && v != "" {
				fmt.Printf("🟠 BeforeTool: using parsed_time from graph state = %s\n", v)
			}
			if s, ok2 := inv.RunOptions.RuntimeState["scene_info"].(string); ok2 && s != "" {
				fmt.Printf("🟠 BeforeTool: scene_info available (%d chars)\n", len(s))
			}
		}
		return nil, nil
	})

	// 3) Sub‑agent configuration
	// Build model with optional explicit API key/base URL to avoid silent misconfig.
	var modelOpts []openai.Option
	if baseURL != "" {
		modelOpts = append(modelOpts, openai.WithBaseURL(baseURL))
	}
	if apiKey != "" {
		modelOpts = append(modelOpts, openai.WithAPIKey(apiKey))
	}
	sub := mustNewLLMAgent(
		"assistant",
		openai.New(modelName, modelOpts...),
		`You are a helpful event assistant. Always reply in English.\n\nWhen the user asks to arrange/plan/schedule a meeting, you MUST call the schedule_meeting tool.\n- Use the runtime-provided parsed_time if present. Do not guess times.\n- If the time is missing or unclear, ask a clarifying question first.\n- Create a short, descriptive title from the user’s request (e.g., "Standup", "Sync with Alex", "Project Review").`,
		[]tool.Tool{scheduleTool},
		modelCbs,
		toolCbs,
	)

	// 4) Graph
	// Extend Messages schema with our custom keys so state merges print nicely
	schema := graph.MessagesStateSchema()
	schema.AddField("scene_id", graph.StateField{Type: reflectTypeOfString(), Reducer: graph.DefaultReducer})
	schema.AddField("scene_info", graph.StateField{Type: reflectTypeOfString(), Reducer: graph.DefaultReducer})
	schema.AddField("parsed_time", graph.StateField{Type: reflectTypeOfString(), Reducer: graph.DefaultReducer})

	sg := graph.NewStateGraph(schema)
	sg.AddNode("pre", preNode)
	sg.AddAgentNode("assistant")
	// Route from pre -> assistant; without this, the graph would stop after pre.
	sg.AddEdge("pre", "assistant")
	sg.SetEntryPoint("pre").SetFinishPoint("assistant")
	compiled, err := sg.Compile()
	return compiled, sub, err
}

func mustNewLLMAgent(name string, m model.Model, instruction string, tools []tool.Tool, mc *model.Callbacks, tc *tool.Callbacks) agent.Agent {
	agt := llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		llmagent.WithTools(tools),
		llmagent.WithModelCallbacks(mc),
		llmagent.WithToolCallbacks(tc),
	)
	return agt
}

// preNode acts as a pre‑processor:
// - loads scene info based on scene_id (dummy in this example)
// - parses a time from current user_input
// - writes them to state so the sub‑agent can read via RuntimeState in callbacks
func preNode(ctx context.Context, state graph.State) (any, error) {
	input, _ := state[graph.StateKeyUserInput].(string)
	sceneID, _ := state["scene_id"].(string)
	if sceneID == "" {
		// Also allow passing from RunOptions at call site
		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil && inv.RunOptions.RuntimeState != nil {
			if v, ok2 := inv.RunOptions.RuntimeState["scene_id"].(string); ok2 {
				sceneID = v
			}
		}
	}

	// Scene info in English to prevent non-English model behavior
	sceneInfo := fmt.Sprintf("[Scene %s] You are helping with event-related tasks (schedule, tickets, times).", sceneID)

	// Naive time parsing from phrases like "today/tomorrow HH:MM" or explicit 2006-01-02 15:04
	parsed := parseTimeInText(input)

	out := graph.State{
		"scene_id":    sceneID,
		"scene_info":  sceneInfo,
		"parsed_time": parsed,
	}
	return out, nil
}

// parseTimeInText is a tiny helper for demo purposes.
func parseTimeInText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Try explicit: 2006-01-02 15:04
	re := regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})[ T](\d{2}:\d{2})\b`)
	if m := re.FindStringSubmatch(s); len(m) == 3 {
		if t, err := time.ParseInLocation("2006-01-02 15:04", m[1]+" "+m[2], time.Local); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	// Simple today/tomorrow HH:MM (using keywords: today, tomorrow)
	re2 := regexp.MustCompile(`(today|tomorrow)\s*(\d{1,2}:\d{2})`)
	if m := re2.FindStringSubmatch(s); len(m) == 3 {
		base := time.Now()
		if m[1] == "tomorrow" {
			base = base.Add(24 * time.Hour)
		}
		tday := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location())
		hhmm := m[2]
		if tt, err := time.ParseInLocation("15:04", hhmm, base.Location()); err == nil {
			final := tday.Add(time.Duration(tt.Hour())*time.Hour + time.Duration(tt.Minute())*time.Minute)
			return final.Format(time.RFC3339)
		}
	}
	// English: today/tomorrow at HH:MM or HHam/HHpm patterns
	lower := strings.ToLower(s)
	engRe := regexp.MustCompile(`\b(today|tomorrow)\b.*?\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	if m := engRe.FindStringSubmatch(lower); len(m) >= 3 {
		base := time.Now()
		if m[1] == "tomorrow" {
			base = base.Add(24 * time.Hour)
		}
		hour := 0
		minute := 0
		fmtH := m[2]
		fmtM := m[3]
		ampm := m[4]
		if h, err := strconv.Atoi(fmtH); err == nil {
			hour = h
		}
		if fmtM != "" {
			if m2, err := strconv.Atoi(fmtM); err == nil {
				minute = m2
			}
		}
		if ampm == "pm" && hour < 12 {
			hour += 12
		}
		if ampm == "am" && hour == 12 {
			hour = 0
		}
		t := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, base.Location())
		return t.Format(time.RFC3339)
	}
	return ""
}

// streamEvents prints streaming content and basic execution metadata.
func streamEvents(ch <-chan *event.Event) error {
	var streaming bool
	for e := range ch {
		if e == nil {
			continue
		}
		// Print minimal execution metadata from StateDelta (model/tool phases)
		if e.StateDelta != nil {
			if md, ok := e.StateDelta[graph.MetadataKeyModel]; ok && len(md) > 0 {
				var info graph.ModelExecutionMetadata
				if json.Unmarshal(md, &info) == nil {
					switch info.Phase {
					case graph.ModelExecutionPhaseStart:
						fmt.Printf("🤖 [MODEL START] node=%s model=%s\n", info.NodeID, info.ModelName)
						if info.Input != "" {
							fmt.Printf("   📝 input: %s\n", truncate(info.Input, 100))
						}
					case graph.ModelExecutionPhaseComplete:
						fmt.Printf("✅ [MODEL DONE] node=%s dur=%v\n", info.NodeID, info.Duration)
					case graph.ModelExecutionPhaseError:
						fmt.Printf("❌ [MODEL ERROR] node=%s err=%s\n", info.NodeID, info.Error)
					}
				}
			}
			if td, ok := e.StateDelta[graph.MetadataKeyTool]; ok && len(td) > 0 {
				var info graph.ToolExecutionMetadata
				if json.Unmarshal(td, &info) == nil {
					switch info.Phase {
					case graph.ToolExecutionPhaseStart:
						fmt.Printf("🔧 [TOOL START] %s (id=%s)\n", info.ToolName, info.ToolID)
						if info.Input != "" {
							fmt.Printf("   📥 %s\n", truncate(info.Input, 100))
						}
					case graph.ToolExecutionPhaseComplete:
						fmt.Printf("✅ [TOOL DONE] %s -> %s\n", info.ToolName, truncate(info.Output, 120))
					case graph.ToolExecutionPhaseError:
						fmt.Printf("❌ [TOOL ERROR] %s err=%s\n", info.ToolName, info.Error)
					}
				}
			}
		}

		// Errors
		if e.Error != nil {
			fmt.Printf("❌ %s\n", e.Error.Message)
			continue
		}

		// Fallback: sub-agent tool.response without graph metadata
		if e.Response != nil && e.Response.Object == model.ObjectTypeToolResponse && len(e.Choices) > 0 {
			msg := e.Choices[0].Message
			if msg.ToolName != "" || msg.Content != "" {
				fmt.Printf("✅ [TOOL DONE] %s -> %s\n", msg.ToolName, truncate(msg.Content, 120))
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

		if e.Done && e.Object == model.ObjectTypeRunnerCompletion {
			break
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// tiny helper to avoid importing reflect in many places here
func reflectTypeOfString() reflect.Type { var x string; return reflect.TypeOf(x) }
