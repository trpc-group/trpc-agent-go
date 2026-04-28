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
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	sessionmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName = flag.String("model", getEnvOrDefault("MODEL_NAME", "deepseek-v4-flash"), "Model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming output")
)

const (
	appName            = "a2aagent-customdatapart-demo"
	customEventTag     = "demo.custom_data"
	customDataPartType = "custom_data"
	customDataPartKind = "custom_part_kind"
	customEventExtKey  = "trpc.a2a.custom_payload"
	customEventHint    = "Custom data part data"
	colorReset         = "\033[0m"
	colorCyan          = "\033[36m"
)

type customPayload struct {
	TraceID string `json:"trace_id"`
	Source  string `json:"source"`
	Hint    string `json:"hint"`
}

func main() {
	flag.Parse()

	httpURL, err := runA2AServer()
	if err != nil {
		log.Fatalf("failed to start a2a server: %v", err)
	}
	a2aAgent := buildA2AAgent(httpURL)
	startChat(a2aAgent)
}

func startChat(a2aAgent *a2aagent.A2AAgent) {
	card := a2aAgent.GetAgentCard()
	fmt.Printf("\nA2A Agent Card\n")
	fmt.Printf("- Name: %s\n", card.Name)
	fmt.Printf("- Description: %s\n", card.Description)
	fmt.Printf("- URL: %s\n", card.URL)
	fmt.Printf("\nExample flow\n")
	fmt.Printf("1. Remote agent emits normal text\n")
	fmt.Printf("2. Wrapper emits one extra graph.node.custom event with payload hint in event.Extensions\n")
	fmt.Printf("3. a2a.WithEventToA2APartMapper converts that extension into a custom DataPart(kind=%q)\n", customDataPartType)
	fmt.Printf("4. a2aagent.WithA2ADataPartMapper restores that DataPart into event.Extensions\n")
	fmt.Printf("5. Demo UI reads the extension payload and prints a custom line\n\n")
	fmt.Printf("Reasoning content is shown in %scyan%s. Type 'new' for a new session, or 'exit' to quit.\n\n", colorCyan, colorReset)

	run := runner.NewRunner(appName, a2aAgent, runner.WithSessionService(sessionmemory.NewSessionService()))
	defer run.Close()

	userID := "demo_user"
	sessionID := "demo_session_1"
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("User: ")
		if !scanner.Scan() {
			fmt.Println("\nGoodbye.")
			return
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit":
			fmt.Println("Goodbye.")
			return
		case "new":
			sessionID = fmt.Sprintf("demo_session_%d", time.Now().UnixNano())
			fmt.Printf("Started new session: %s\n\n", sessionID)
			continue
		}

		events, err := run.Run(context.Background(), userID, sessionID, model.NewUserMessage(input))
		if err != nil {
			fmt.Printf("Run error: %v\n\n", err)
			continue
		}
		if err := processResponse(events); err != nil {
			fmt.Printf("Process error: %v\n\n", err)
			continue
		}
		fmt.Println()
	}
}

func runA2AServer() (string, error) {
	host, err := allocateDemoHost()
	if err != nil {
		return "", err
	}
	remoteAgent := wrapAgentWithCustomDataPart(
		buildAgent(
			"agent_remote_customdatapart",
			"You are a helpful remote agent. Answer directly and briefly.",
		),
		"agent_remote_customdatapart",
	)

	server, err := a2a.New(
		a2a.WithHost(host),
		a2a.WithAgent(remoteAgent, *streaming),
		a2a.WithGraphEventObjectAllowlist("graph.node.*"),
		a2a.WithEventToA2APartMapper(customDataPartMapper),
	)
	if err != nil {
		return "", fmt.Errorf("create a2a server: %w", err)
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.Start(host); err != nil {
			select {
			case serverErrCh <- err:
			default:
			}
		}
	}()

	if err := waitForAgentCardReady(host, serverErrCh, 5*time.Second); err != nil {
		return "", err
	}
	httpURL := fmt.Sprintf("http://%s", host)
	fmt.Printf("Auto-selected demo A2A server: %s\n", httpURL)
	return httpURL, nil
}

type customDataPartWrapper struct {
	base agent.Agent
	name string
}

func wrapAgentWithCustomDataPart(base agent.Agent, name string) agent.Agent {
	return &customDataPartWrapper{base: base, name: name}
}

func (w *customDataPartWrapper) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	baseCh, err := w.base.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	out := make(chan *event.Event)
	go func() {
		defer close(out)
		hasVisibleContent := false
		for {
			if ctx.Err() != nil {
				return
			}
			var evt *event.Event
			var ok bool
			select {
			case <-ctx.Done():
				return
			case evt, ok = <-baseCh:
			}
			if !ok {
				break
			}
			// Always forward the original event stream unchanged so the wrapped
			// agent keeps its normal behavior.
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case out <- evt:
			}
			if eventContent(evt) != "" {
				hasVisibleContent = true
			}
		}

		if !hasVisibleContent {
			return
		}
		// Emit the custom event only after the original response stream finishes,
		// so the UI prints the structured payload after the normal assistant text.
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case out <- newCustomDataPartEvent(invocation.InvocationID, w.name, customEventHint):
		}
	}()
	return out, nil
}

func (w *customDataPartWrapper) Tools() []tool.Tool { return w.base.Tools() }

func (w *customDataPartWrapper) Info() agent.Info { return w.base.Info() }

func (w *customDataPartWrapper) SubAgents() []agent.Agent { return w.base.SubAgents() }

func (w *customDataPartWrapper) FindSubAgent(name string) agent.Agent {
	return w.base.FindSubAgent(name)
}

func newCustomDataPartEvent(invocationID, author, hint string) *event.Event {
	resp := &model.Response{
		ID:        fmt.Sprintf("custom-%d", time.Now().UnixNano()),
		Object:    graph.ObjectTypeGraphNodeCustom,
		Choices:   []model.Choice{},
		Timestamp: time.Now(),
		Created:   time.Now().Unix(),
	}
	return event.NewResponseEvent(
		invocationID,
		author,
		resp,
		event.WithTag(customEventTag),
		event.WithExtension(customEventExtKey, customPayload{
			TraceID: fmt.Sprintf("trace-%d", time.Now().UnixNano()),
			Source:  author,
			Hint:    hint,
		}),
	)
}

func customDataPartMapper(ctx context.Context, evt *event.Event) ([]protocol.Part, error) {
	_ = ctx
	if evt == nil || evt.Response == nil || evt.Response.Object != graph.ObjectTypeGraphNodeCustom {
		return nil, nil
	}
	payload, ok, err := event.GetExtension[customPayload](evt, customEventExtKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	dp := protocol.NewDataPart(map[string]any{"payload": payload})
	markAsCustomDataPart(&dp)
	return []protocol.Part{&dp}, nil
}

func buildAgent(agentName, desc string, extraOptions ...llmagent.Option) agent.Agent {
	modelInstance := openai.New(*modelName)
	genConfig := model.GenerationConfig{MaxTokens: intPtr(1200), Temperature: floatPtr(0.7), Stream: *streaming}
	options := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
	}
	options = append(options, extraOptions...)
	return llmagent.New(agentName, options...)
}

func buildA2AAgent(httpURL string) *a2aagent.A2AAgent {
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(httpURL),
		// Restore the custom DataPart back into event.Extensions so downstream
		// graph logic and UI code can consume the structured payload directly.
		a2aagent.WithA2ADataPartMapper(func(
			part *protocol.DataPart,
			result *a2aagent.A2ADataPartMappingResult,
		) (bool, error) {
			if part == nil || result == nil {
				return false, nil
			}
			payload, ok := customDataPartPayload(part)
			if !ok {
				return false, nil
			}
			// Rehydrate the wire-format DataPart payload back into event.Extensions
			// so the rest of the local pipeline can consume typed structured data.
			if err := result.SetEventExtension(customEventExtKey, payload); err != nil {
				return false, err
			}
			return true, nil
		}),
	)
	if err != nil {
		log.Fatalf("failed to create a2a agent: %v", err)
	}
	return a2aAgent
}

func processResponse(eventChan <-chan *event.Event) error {
	var assistantStarted bool
	for evt := range eventChan {
		if err := handleEvent(evt, &assistantStarted); err != nil {
			return err
		}
	}
	return nil
}

func handleEvent(evt *event.Event, assistantStarted *bool) error {
	if evt == nil {
		return nil
	}
	if evt.Error != nil {
		fmt.Printf("\nError: %s\n", evt.Error.Message)
		return nil
	}
	if evt.ContainsTag(customEventTag) {
		return printMappedCustomHint(evt)
	}
	return printAssistantContent(evt, assistantStarted)
}

func printMappedCustomHint(evt *event.Event) error {
	payload, ok, err := event.GetExtension[customPayload](evt, customEventExtKey)
	if err != nil {
		return err
	}
	if !ok || payload.Hint == "" {
		return nil
	}
	fmt.Printf("\n🧩 Agent mapper(custom_data): %s\n", payload.Hint)
	return nil
}

func printAssistantContent(evt *event.Event, assistantStarted *bool) error {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return nil
	}
	content, reasoning := extractContent(evt.Response.Choices[0])
	if reasoning != "" {
		fmt.Printf("%s%s%s", colorCyan, reasoning, colorReset)
	}
	if content == "" {
		return nil
	}
	if !*assistantStarted {
		fmt.Print("🤖 Assistant: ")
		*assistantStarted = true
	}
	fmt.Print(content)
	return nil
}

func extractContent(choice model.Choice) (string, string) {
	if *streaming {
		return choice.Delta.Content, choice.Delta.ReasoningContent
	}
	return choice.Message.Content, choice.Message.ReasoningContent
}

func eventContent(evt *event.Event) string {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}
	choice := evt.Response.Choices[0]
	if choice.Message.Content != "" {
		return choice.Message.Content
	}
	return choice.Delta.Content
}

func allocateDemoHost() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate demo host: %w", err)
	}
	host := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("close demo host listener %s: %w", host, err)
	}
	return host, nil
}

func waitForAgentCardReady(host string, serverErrCh <-chan error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	httpClient := &http.Client{Timeout: time.Second}
	agentCardURL := fmt.Sprintf("http://%s%s", host, protocol.AgentCardPath)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-serverErrCh:
				return fmt.Errorf("a2a server failed before ready: %w", err)
			default:
			}
			return fmt.Errorf("timed out waiting for a2a server readiness at %s", agentCardURL)
		case err := <-serverErrCh:
			return fmt.Errorf("a2a server exited before ready: %w", err)
		case <-ticker.C:
			resp, err := httpClient.Get(agentCardURL)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }

func markAsCustomDataPart(part *protocol.DataPart) {
	if part == nil {
		return
	}
	if part.Metadata == nil {
		part.Metadata = make(map[string]any)
	}
	part.Metadata[customDataPartKind] = customDataPartType
}

func customDataPartPayload(part *protocol.DataPart) (any, bool) {
	if part == nil {
		return nil, false
	}
	typeValue, ok := part.Metadata[customDataPartKind].(string)
	if !ok || typeValue != customDataPartType {
		return nil, false
	}
	dataMap, ok := part.Data.(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := dataMap["payload"]
	if !ok {
		return nil, false
	}
	return value, true
}

func getEnvOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}
