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
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName  = flag.String("model", getEnvOrDefault("MODEL_NAME", "deepseek-chat"), "Model to use")
	host       = flag.String("host", "127.0.0.1:8888", "Host to use")
	streaming  = flag.Bool("streaming", true, "Streaming to use")
	serverMode = flag.String("server-mode", "agent", "A2A server build mode: agent or runner-card")
	remoteOnly = flag.Bool("remote-only", false, "Only output remote agent responses")
	debugMode  = flag.Bool("debug", true, "Enable debug mode to print session events after each turn")
)

// ANSI color codes for terminal output
const (
	colorReset = "\033[0m"
	colorCyan  = "\033[36m" // Cyan for reasoning/thinking content
)

const (
	optionalStateKey = "meta"
	appName          = "a2aagent-demo"
)

func main() {
	flag.Parse()

	// runRemoteAgent will start a a2a server that build with a remote agent
	if err := runA2AServerByAgent("agent_remote_joker", "I am a remote agent, I can tell a joke", *host); err != nil {
		log.Fatalf("Failed to start a2a server: %v", err)
	}

	httpURL := fmt.Sprintf("http://%s", *host)
	a2aAgent := buildA2AAgent(httpURL)

	// Build a different local agent
	localAgent := buildAgent("agent_local_joker", "I am a local agent, I can tell a joke",
		llmagent.WithTools([]tool.Tool{
			function.NewFunctionTool(
				getCurrentTime,
				function.WithName("getCurrentTime"),
				function.WithDescription("This is tool that can get current time")),
		}))
	fmt.Printf("Debug Mode: %t\n", *debugMode)
	startChat(localAgent, a2aAgent)
}

func startChat(localAgent agent.Agent, a2aAgent *a2aagent.A2AAgent) {

	card := a2aAgent.GetAgentCard()
	fmt.Printf("\n------- Agent Card -------\n")
	fmt.Printf("Name: %s\n", card.Name)
	fmt.Printf("Description: %s\n", card.Description)
	fmt.Printf("URL: %s\n", card.URL)
	fmt.Printf("------------------------\n")

	localSessionService := sessionmemory.NewSessionService()
	remoteSessionService := sessionmemory.NewSessionService()

	remoteRunner := runner.NewRunner(appName, a2aAgent, runner.WithSessionService(remoteSessionService))
	localRunner := runner.NewRunner(appName, localAgent, runner.WithSessionService(localSessionService))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer remoteRunner.Close()
	defer localRunner.Close()

	// Use different userIDs and sessionIDs for remote and local agents
	remoteUserID := "remote_user"
	remoteSessionID := "remote_session1"
	localUserID := "local_user"
	localSessionID := "local_session1"

	fmt.Println("Chat with the agent. Type 'new' for a new session, or 'exit' to quit.")
	fmt.Printf("Color legend: %sReasoning content%s | Normal content\n", colorCyan, colorReset)

	for {
		if err := processMessage(remoteRunner, localRunner, remoteUserID, &remoteSessionID, localUserID, &localSessionID); err != nil {
			if err.Error() == "exit" {
				fmt.Println("👋 Goodbye!")
				return
			}
			fmt.Printf("❌ Error: %v\n", err)
		}
		if *debugMode {
			printDebugSessions(
				context.Background(),
				remoteSessionService,
				remoteUserID,
				remoteSessionID,
				localSessionService,
				localUserID,
				localSessionID,
			)
		}

		fmt.Println() // Add spacing between turns
	}
}

func printDebugSessions(
	ctx context.Context,
	remoteSessionService session.Service,
	remoteUserID string,
	remoteSessionID string,
	localSessionService session.Service,
	localUserID string,
	localSessionID string,
) {
	fmt.Println()
	fmt.Println("------- Debug Sessions -------")
	fmt.Println("[remote a2a agent]")
	if err := printSessionEvents(
		ctx,
		remoteSessionService,
		appName,
		remoteUserID,
		remoteSessionID,
	); err != nil {
		fmt.Printf("Debug error (remote): %v\n", err)
	}
	if *remoteOnly {
		fmt.Println("------------------------------")
		return
	}
	fmt.Println("[local agent]")
	if err := printSessionEvents(
		ctx,
		localSessionService,
		appName,
		localUserID,
		localSessionID,
	); err != nil {
		fmt.Printf("Debug error (local): %v\n", err)
	}
	fmt.Println("------------------------------")
}

func printSessionEvents(
	ctx context.Context,
	svc session.Service,
	appName string,
	userID string,
	sessionID string,
) error {
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session failed: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found")
	}

	events := sess.GetEvents()
	fmt.Printf("│\n")
	fmt.Printf("│  [DEBUG] Session Events: %d\n", len(events))
	for i, evt := range events {
		role := ""
		detail := ""
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			msg := evt.Response.Choices[0].Message
			role = string(msg.Role)
			detail = buildDebugEventDetail(msg)
		}
		if detail == "" {
			detail = "<empty>"
		}
		fmt.Printf(
			"│    %d. %-9s: %s\n",
			i+1,
			role,
			strings.ReplaceAll(detail, "\n", "\n│              "),
		)
	}
	return nil
}

func buildDebugEventDetail(msg model.Message) string {
	var parts []string
	if msg.Content != "" {
		parts = append(parts, msg.Content)
	}
	if len(msg.ToolCalls) > 0 {
		var toolCallLines []string
		for _, toolCall := range msg.ToolCalls {
			line := fmt.Sprintf("tool_call: %s", toolCall.Function.Name)
			if len(toolCall.Function.Arguments) > 0 {
				line += fmt.Sprintf(" args=%s", string(toolCall.Function.Arguments))
			}
			if toolCall.ID != "" {
				line += fmt.Sprintf(" id=%s", toolCall.ID)
			}
			toolCallLines = append(toolCallLines, line)
		}
		parts = append(parts, strings.Join(toolCallLines, "\n"))
	}
	if msg.ToolID != "" {
		parts = append(parts, fmt.Sprintf("tool_id: %s", msg.ToolID))
	}
	if msg.ToolName != "" {
		parts = append(parts, fmt.Sprintf("tool_name: %s", msg.ToolName))
	}
	return strings.Join(parts, "\n")
}

func processMessage(
	remoteRunner runner.Runner,
	localRunner runner.Runner,
	remoteUserID string,
	remoteSessionID *string,
	localUserID string,
	localSessionID *string,
) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("User: ")
	if !scanner.Scan() {
		return fmt.Errorf("exit")
	}

	userInput := strings.TrimSpace(scanner.Text())
	if userInput == "" {
		return nil
	}

	switch strings.ToLower(userInput) {
	case "exit":
		return fmt.Errorf("exit")
	case "new":
		*remoteSessionID = startNewSession("remote")
		*localSessionID = startNewSession("local")
		return nil
	}

	fmt.Printf("%s remote agent %s\n", strings.Repeat("=", 8), strings.Repeat("=", 8))
	events, err := remoteRunner.Run(
		context.Background(),
		remoteUserID,
		*remoteSessionID,
		model.NewUserMessage(userInput),
		agent.WithRuntimeState(map[string]any{optionalStateKey: "test"}),
		// Example: Pass custom HTTP headers to A2A agent using WithA2ARequestOptions
		// This allows you to add authentication tokens, tracing IDs, or other custom headers
		agent.WithA2ARequestOptions(
			client.WithRequestHeader("X-Custom-Header", "custom-value"),
			client.WithRequestHeader("X-Request-ID", fmt.Sprintf("req-%d", time.Now().UnixNano())),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	if err := processResponse(events); err != nil {
		return fmt.Errorf("failed to process response: %w", err)
	}

	// Only run local agent if remote-only flag is not set
	if !*remoteOnly {
		fmt.Printf("\n%s local agent %s\n", strings.Repeat("=", 8), strings.Repeat("=", 8))
		events, err = localRunner.Run(
			context.Background(),
			localUserID,
			*localSessionID,
			model.NewUserMessage(userInput),
			agent.WithRuntimeState(map[string]any{optionalStateKey: "test"}),
		)
		if err != nil {
			return fmt.Errorf("failed to run agent: %w", err)
		}
		if err := processResponse(events); err != nil {
			return fmt.Errorf("failed to process response: %w", err)
		}
	}
	return nil
}

func startNewSession(prefix string) string {
	newSessionID := fmt.Sprintf("%s_session_%d", prefix, time.Now().UnixNano())
	fmt.Printf("🆕 Started new %s session: %s\n", prefix, newSessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
	return newSessionID
}

type hookProcessor struct {
	next taskmanager.MessageProcessor
}

func (h *hookProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	fmt.Printf("A2A Server: received message: %+v\n", message.MessageID)
	fmt.Printf("A2A Server: received metadata: %+v\n", message.Metadata)

	if message.Metadata != nil {
		if traceID, ok := message.Metadata["trace_id"]; ok {
			fmt.Printf("A2A Server: [BuildMessageHook] trace_id = %v\n", traceID)
		}
		if bizTag, ok := message.Metadata["business_tag"]; ok {
			fmt.Printf("A2A Server: [BuildMessageHook] business_tag = %v\n", bizTag)
		}
	}

	return h.next.ProcessMessage(ctx, message, options, handler)
}

func runA2AServerByAgent(agentName, desc, host string) error {
	fmt.Printf("A2A Server: starting on %s\n", host)
	remoteAgent := buildAgent(agentName, desc, llmagent.WithTools([]tool.Tool{
		function.NewFunctionTool(
			getCurrentTime,
			function.WithName("getCurrentTime"),
			function.WithDescription("This is tool that can get current time")),
	}))

	// Create in-memory memory service for demonstration.
	memoryService := inmemory.NewMemoryService()

	// Create in-memory session service for the runner.
	runnerSessionService := sessionmemory.NewSessionService()
	commonOpts := []a2a.Option{
		a2a.WithDebugLogging(false),
		a2a.WithErrorHandler(func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			errMsg := protocol.NewMessage(
				protocol.MessageRoleAgent,
				[]protocol.Part{
					protocol.NewTextPart("your own error msg"),
				},
			)
			return &errMsg, nil
		}),
		// Example: Use WithProcessMessageHook to inspect/modify incoming A2A messages.
		// This can read custom metadata injected by the client's BuildMessageHook.
		a2a.WithProcessMessageHook(
			func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
				return &hookProcessor{next: next}
			},
		),
	}

	var serverOpts []a2a.Option
	switch *serverMode {
	case "agent":
		serverOpts = append(serverOpts,
			a2a.WithHost(host),
			a2a.WithAgent(remoteAgent, *streaming),
		)
	case "runner-card":
		info := remoteAgent.Info()
		card, err := a2a.NewAgentCard(info.Name, info.Description, host, *streaming)
		if err != nil {
			log.Fatalf("Failed to build agent card: %v", err)
		}
		serverOpts = append(serverOpts,
			a2a.WithAgentCard(card),
			// In runner-only mode, the public agent identity must be supplied
			// explicitly via WithAgentCard.
			a2a.WithRunner(runner.NewRunner(
				remoteAgent.Info().Name,
				remoteAgent,
				runner.WithSessionService(runnerSessionService),
				runner.WithMemoryService(memoryService),
			)),
		)
	default:
		log.Fatalf("Unsupported server mode %q, expected agent or runner-card", *serverMode)
	}

	server, err := a2a.New(append(commonOpts, serverOpts...)...)
	if err != nil {
		return fmt.Errorf("create a2a server: %w", err)
	}
	if err := ensureHostAvailable(host); err != nil {
		return err
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.Start(host); err != nil {
			select {
			case serverErrCh <- err:
			default:
				slog.Error("A2A server exited", "error", err)
			}
		}
	}()

	if err := waitForAgentCardReady(host, serverErrCh, 5*time.Second); err != nil {
		return err
	}
	return nil
}

func ensureHostAvailable(host string) error {
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return fmt.Errorf("host %s is unavailable: %w", host, err)
	}
	return listener.Close()
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

func buildAgent(agentName, desc string, extraOptions ...llmagent.Option) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(*modelName)

	// Create LLM agent.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      *streaming,
	}
	options := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
	}
	options = append(options, extraOptions...)
	llmAgent := llmagent.New(agentName, options...)
	return llmAgent
}

func buildA2AAgent(httpURL string) *a2aagent.A2AAgent {
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(httpURL),

		// optional: specify the state key that transferred to the remote agent by metadata
		a2aagent.WithTransferStateKey(optionalStateKey),

		// Example: Use WithBuildMessageHook to inject custom metadata into A2A messages.
		// The hook wraps the default message converter as middleware, allowing you to
		// modify the message before/after conversion, or completely replace the conversion logic.
		// The custom metadata will be received by the server's ProcessMessageHook.
		a2aagent.WithBuildMessageHook(func(next a2aagent.ConvertToA2AMessageFunc) a2aagent.ConvertToA2AMessageFunc {
			return func(isStream bool, agentName string, inv *agent.Invocation) (*protocol.Message, error) {
				// Call the default converter. transferState keys are injected by the outer wrapper.
				msg, err := next(isStream, agentName, inv)
				if err != nil {
					return nil, err
				}
				// Inject custom metadata that will be visible in the server's ProcessMessageHook
				if msg.Metadata == nil {
					msg.Metadata = make(map[string]any)
				}
				msg.Metadata["trace_id"] = fmt.Sprintf("trace-%d", time.Now().UnixNano())
				msg.Metadata["business_tag"] = "example-demo"
				fmt.Printf("A2A Client: [BuildMessageHook] injected trace_id=%s, business_tag=%s\n",
					msg.Metadata["trace_id"], msg.Metadata["business_tag"])
				return msg, nil
			}
		}),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a agent: %v", err)
	}
	return a2aAgent
}

// processResponse handles both streaming and non-streaming responses with tool call visualization.
func processResponse(eventChan <-chan *event.Event) error {
	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if err := handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}

		// Check if this is the final event.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func handleEvent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle tool calls (return early to avoid processing tool call content as assistant response)
	if handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle tool responses (return early to avoid processing tool response content as assistant response)
	if handleToolResponses(event) {
		return nil
	}

	// Handle content.
	handleContent(event, toolCallsDetected, assistantStarted, fullContent)

	return nil
}

// handleToolCalls detects and displays tool calls.
func handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Response.Choices) == 0 {
		return false
	}

	choice := event.Response.Choices[0]

	// trpc-agent-go only puts tool calls in Message.ToolCalls, never in Delta.ToolCalls
	// even in streaming mode, tool calls are aggregated and sent in final response
	if len(choice.Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 CallableTool calls initiated:\n")
		for _, toolCall := range choice.Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

// handleToolResponses detects and displays tool responses.
func handleToolResponses(event *event.Event) bool {
	if event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}

	hasToolResponse := false
	for _, choice := range event.Response.Choices {
		// Tool responses are always in Message (never in Delta), even in streaming mode
		// This follows trpc-agent-go convention
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
			hasToolResponse = true
		}
	}
	return hasToolResponse
}

// handleContent processes and displays content.
func handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		content, reasoningContent := extractContent(choice)

		// Display reasoning content first (in cyan color)
		if reasoningContent != "" {
			displayReasoningContent(reasoningContent)
		}

		if content != "" {
			displayContent(content, toolCallsDetected, assistantStarted, fullContent)
		}
	}
}

// extractContent extracts content and reasoning content based on streaming mode.
func extractContent(choice model.Choice) (content string, reasoningContent string) {
	if *streaming {
		return choice.Delta.Content, choice.Delta.ReasoningContent
	}
	return choice.Message.Content, choice.Message.ReasoningContent
}

// displayReasoningContent prints reasoning content in cyan color.
func displayReasoningContent(reasoningContent string) {
	fmt.Printf("%s%s%s", colorCyan, reasoningContent, colorReset)
}

// displayContent prints content to console.
func displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		} else {
			fmt.Printf("🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

// getCurrentTime returns current time information.
func getCurrentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone

	// Handle timezone conversion.
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour) // Simplified EST.
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour) // Simplified PST.
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour) // Simplified CST.
	case "":
		t = now
		timezone = "Local"
	default:
		t = now.UTC()
		timezone = "UTC"
	}

	return timeResult{
		Timezone: timezone,
		Time:     t.Format("15:04:05"),
		Date:     t.Format("2006-01-02"),
		Weekday:  t.Weekday().String(),
	}, nil
}

// timeArgs represents arguments for the time tool.
type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=Timezone or leave empty for local"`
}

// timeResult represents the current time information.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

func getEnvOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}
