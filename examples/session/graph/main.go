//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a multi-turn graph agent with session persistence.
// It shows that graph completion snapshot keys are visible on runner completion
// events, while only compact business state and response identity are retained
// in session.State.
//
// Usage:
//
//	go run ./graph
//	go run ./graph -debug=false
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"sort"
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
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
)

const (
	appName         = "session-graph-demo"
	userID          = "user"
	keyNormalized   = "normalized_input"
	keyDraft        = "draft_answer"
	keyAgentReply   = "agent_reply"
	keyAgentReplyID = "agent_reply_id"
	keyBusiness     = "business_result"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the model to use (default: MODEL_NAME env var)",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode for the LLM agent node",
	)
	debugMode = flag.Bool(
		"debug",
		true,
		"Print session events and state after each turn",
	)
)

type graphChat struct {
	runner         runner.Runner
	sessionService session.Service
	sessionID      string
	debug          bool
}

func main() {
	flag.Parse()
	if *modelName == "" {
		*modelName = "deepseek-chat"
	}
	chat := &graphChat{
		sessionID: fmt.Sprintf("graph-session-%d", time.Now().Unix()),
		debug:     *debugMode,
	}
	if err := chat.run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func (c *graphChat) run(ctx context.Context) error {
	if err := c.setup(); err != nil {
		return err
	}
	defer c.runner.Close()

	fmt.Printf("Graph session demo ready. Session: %s\n\n", c.sessionID)
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println()
	c.printCommands()
	return c.startChat(ctx)
}

func (c *graphChat) setup() error {
	graphAgent, err := buildGraphAgent()
	if err != nil {
		return err
	}
	c.sessionService = sessioninmemory.NewSessionService()
	c.runner = runner.NewRunner(
		appName,
		graphAgent,
		runner.WithSessionService(c.sessionService),
	)
	return nil
}

func (c *graphChat) startChat(ctx context.Context) error {
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

		lower := strings.ToLower(input)
		switch {
		case lower == "/exit" || lower == "/quit":
			fmt.Println("Goodbye.")
			return nil
		case lower == "/help":
			c.printCommands()
			continue
		case lower == "/debug":
			c.debug = !c.debug
			fmt.Printf("Debug mode: %t\n\n", c.debug)
			continue
		case lower == "/state":
			if err := c.printCurrentState(ctx); err != nil {
				fmt.Printf("State error: %v\n\n", err)
			}
			continue
		case lower == "/sessions":
			c.listSessions(ctx)
			continue
		case strings.HasPrefix(lower, "/new"):
			c.startNewSession(strings.TrimSpace(input[len("/new"):]))
			continue
		}

		if err := c.processMessage(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		if c.debug {
			if err := c.printDebug(ctx); err != nil {
				fmt.Printf("Debug error: %v\n", err)
			}
		}
		fmt.Println()
	}
	return scanner.Err()
}

func (c *graphChat) printCommands() {
	fmt.Println("Commands:")
	fmt.Println("   /help        - Show commands")
	fmt.Println("   /debug       - Toggle debug output")
	fmt.Println("   /state       - Print persisted session.State")
	fmt.Println("   /new [id]    - Start a new session")
	fmt.Println("   /sessions    - List sessions")
	fmt.Println("   /exit        - End the conversation")
	fmt.Println()
}

func (c *graphChat) processMessage(ctx context.Context, userInput string) error {
	events, err := c.runner.Run(
		ctx,
		userID,
		c.sessionID,
		model.NewUserMessage(userInput),
		agent.WithGraphEmitFinalModelResponses(true),
		agent.WithDisableGraphCompletionEvent(true),
	)
	if err != nil {
		return err
	}
	completion := lastRunnerCompletion(events)
	if completion == nil {
		return fmt.Errorf("runner completion event not found")
	}
	fmt.Printf("Assistant: %s\n", completionText(completion))
	return nil
}

func buildGraphAgent() (*graphagent.GraphAgent, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField(keyNormalized, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(keyDraft, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(keyAgentReply, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(keyAgentReplyID, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(keyBusiness, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})

	assistant, err := buildAssistantAgent()
	if err != nil {
		return nil, err
	}

	sg := graph.NewStateGraph(schema)
	sg.AddNode("normalize", normalizeInput)
	sg.AddNode("answer", draftAnswer)
	sg.AddAgentNode(
		"assistant",
		graph.WithSubgraphInputMapper(func(parent graph.State) graph.State {
			return graph.State{
				graph.StateKeyUserInput: parent[keyDraft],
			}
		}),
		graph.WithSubgraphOutputMapper(func(parent graph.State, result graph.SubgraphResult) graph.State {
			finalState := result.EffectiveState()
			responseID, _ := finalState[graph.StateKeyLastResponseID].(string)
			return graph.State{
				keyAgentReply:   result.LastResponse,
				keyAgentReplyID: responseID,
			}
		}),
	)
	sg.AddNode("collect", collectAnswer)
	sg.AddEdge("normalize", "answer")
	sg.AddEdge("answer", "assistant")
	sg.AddEdge("assistant", "collect")
	compiled := sg.SetEntryPoint("normalize").SetFinishPoint("collect").MustCompile()
	return graphagent.New(
		"session-graph-agent",
		compiled,
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{assistant}),
	)
}

func buildAssistantAgent() (agent.Agent, error) {
	var opts []openai.Option
	modelInstance := openai.New(*modelName, opts...)
	return llmagent.New(
		"assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("LLM sub-agent called from a graph AgentNode."),
		llmagent.WithInstruction(
			"Rewrite the graph draft into one concise assistant reply. "+
				"Do not mention implementation details unless the draft asks for them.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming}),
	), nil
}

func normalizeInput(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	input, _ := state[graph.StateKeyUserInput].(string)
	normalized := strings.Join(strings.Fields(strings.ToLower(input)), " ")
	if normalized == "" {
		normalized = "(empty input)"
	}
	return graph.State{keyNormalized: normalized}, nil
}

func draftAnswer(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	normalized, _ := state[keyNormalized].(string)
	draft := fmt.Sprintf("I routed your message through a graph with an agent node. Normalized input: %q.", normalized)
	return graph.State{keyDraft: draft}, nil
}

func collectAnswer(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	agentReply, _ := state[keyAgentReply].(string)
	agentReplyID, _ := state[keyAgentReplyID].(string)
	return graph.State{
		keyBusiness:                  fmt.Sprintf("last turn at %s", time.Now().Format(time.RFC3339)),
		graph.StateKeyLastResponse:   agentReply,
		graph.StateKeyLastResponseID: agentReplyID,
	}, nil
}

func lastRunnerCompletion(events <-chan *event.Event) *event.Event {
	var completion *event.Event
	for e := range events {
		if e != nil && e.IsRunnerCompletion() {
			completion = e
		}
	}
	return completion
}

func completionText(completion *event.Event) string {
	if completion != nil && completion.Response != nil && len(completion.Response.Choices) > 0 {
		if content := completion.Response.Choices[0].Message.Content; content != "" {
			return content
		}
	}
	if raw, ok := completion.StateDelta[graph.StateKeyLastResponse]; ok {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
		return string(raw)
	}
	return "(no assistant text)"
}

func (c *graphChat) printDebug(ctx context.Context) error {
	sess, err := c.currentSession(ctx)
	if err != nil {
		return err
	}
	printSessionDebug(sess)
	return nil
}

func (c *graphChat) printCurrentState(ctx context.Context) error {
	sess, err := c.currentSession(ctx)
	if err != nil {
		return err
	}
	printSessionState(sess)
	return nil
}

func printSessionDebug(sess *session.Session) {
	printSessionEvents(sess)
	printSessionState(sess)
}

func printSessionEvents(sess *session.Session) {
	events := sess.GetEvents()
	fmt.Println("│")
	fmt.Printf("│  [DEBUG] Session Events: %d\n", len(events))
	for i, evt := range events {
		role := ""
		content := ""
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			role = string(evt.Response.Choices[0].Message.Role)
			content = evt.Response.Choices[0].Message.Content
		}
		content = strings.ReplaceAll(content, "\n", " ")
		fmt.Printf("│    %d. %-9s: %s\n", i+1, role, util.Truncate(content, 60))
	}
}

func printSessionState(sess *session.Session) {
	fmt.Println("│")
	fmt.Println("│  [DEBUG] Persisted session.State:")
	for _, key := range sortedSessionStateKeys(sess.State) {
		fmt.Printf("│    - %s = %s\n", key, util.Truncate(formatStateValue(sess.State[key]), 80))
	}
	fmt.Println("│")
	fmt.Println("│  [DEBUG] Graph snapshot keys in session.State:")
	for _, key := range graphSnapshotKeys() {
		_, ok := sess.GetState(key)
		fmt.Printf("│    - %-20s %t\n", key, ok)
	}
}

func (c *graphChat) currentSession(ctx context.Context) (*session.Session, error) {
	return c.sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: c.sessionID,
	})
}

func (c *graphChat) startNewSession(customID string) {
	old := c.sessionID
	if customID != "" {
		c.sessionID = customID
	} else {
		c.sessionID = fmt.Sprintf("graph-session-%d", time.Now().Unix())
	}
	fmt.Printf("Started new session.\n")
	fmt.Printf("   Previous: %s\n", old)
	fmt.Printf("   Current:  %s\n\n", c.sessionID)
}

func (c *graphChat) listSessions(ctx context.Context) {
	sessions, err := c.sessionService.ListSessions(ctx, session.UserKey{
		AppName: appName,
		UserID:  userID,
	})
	if err != nil {
		fmt.Printf("Failed to list sessions: %v\n\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Println("(no sessions recorded yet)")
		fmt.Println()
		return
	}
	fmt.Println("Sessions:")
	for _, sess := range sessions {
		marker := " "
		if sess.ID == c.sessionID {
			marker = "*"
		}
		fmt.Printf("   %s %s (updated: %s)\n", marker, sess.ID, sess.UpdatedAt.Format(time.RFC3339))
	}
	fmt.Println()
}

func graphSnapshotKeys() []string {
	return []string{
		graph.StateKeyMessages,
		graph.StateKeyUserInput,
		graph.StateKeyLastResponse,
		graph.StateKeyLastToolResponse,
		graph.StateKeyNodeResponses,
		graph.MetadataKeyCompletion,
	}
}

func sortedSessionStateKeys(state session.StateMap) []string {
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatStateValue(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return fmt.Sprintf("%v", value)
	}
	return strings.TrimSpace(string(raw))
}
