//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to use the MCP broker tools with LLMAgent.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker"
)

var (
	modelName = flag.String("model", "gpt-4o-mini", "Name of the model to use")
	variant   = flag.String("variant", "", "Optional provider variant. If empty, infer from OPENAI_BASE_URL")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
	prompt    = flag.String("prompt", "", "Optional single-turn prompt. If empty, start interactive chat")
)

const (
	appName   = "mcp-broker-demo"
	agentName = "mcp-broker-assistant"
)

func main() {
	flag.Parse()

	chat := &mcpBrokerChat{
		modelName: *modelName,
		variant:   *variant,
		streaming: *streaming,
		prompt:    strings.TrimSpace(*prompt),
	}

	if err := chat.run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

type mcpBrokerChat struct {
	modelName          string
	variant            string
	streaming          bool
	prompt             string
	runner             runner.Runner
	userID             string
	sessionID          string
	visibleTools       []string
	generatedSkillsDir string
	preferredServer    string
	availableServerIDs []string
	availableSkillIDs  []string
	remoteMCPURL       string
	remoteHTTPDemo     *remoteHTTPDemo
}

func (c *mcpBrokerChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	defer c.cleanupGeneratedSkillsDir()
	defer c.closeRemoteHTTPDemo()

	c.printBanner()

	if c.prompt != "" {
		return c.processMessage(ctx, c.prompt)
	}
	return c.startChat(ctx)
}

func (c *mcpBrokerChat) setup(_ context.Context) error {
	exampleDir, err := exampleDir()
	if err != nil {
		return err
	}
	remoteDemo := startRemoteHTTPDemoServer()
	remoteMCPURL := remoteDemo.url

	skillsDir, err := prepareRenderedSkillsRoot(exampleDir, remoteMCPURL)
	if err != nil {
		remoteDemo.Close()
		return err
	}

	repo, err := skill.NewFSRepository(skillsDir)
	if err != nil {
		remoteDemo.Close()
		_ = os.RemoveAll(skillsDir)
		return fmt.Errorf("load generated skills repo: %w", err)
	}

	serverPath := filepath.Join(exampleDir, "stdioserver", "main.go")

	brokerOpts, serverIDs, preferredServer, err := c.buildBrokerOptions(serverPath)
	if err != nil {
		remoteDemo.Close()
		_ = os.RemoveAll(skillsDir)
		return err
	}
	c.generatedSkillsDir = skillsDir
	c.availableServerIDs = serverIDs
	c.availableSkillIDs = skillNames(repo)
	c.preferredServer = preferredServer
	c.remoteHTTPDemo = remoteDemo
	c.remoteMCPURL = remoteMCPURL

	broker := mcpbroker.New(brokerOpts...)

	modelOpts := []openai.Option{}
	if strings.TrimSpace(c.variant) != "" {
		modelOpts = append(modelOpts, openai.WithVariant(openai.Variant(c.variant)))
	}
	modelInstance := openai.New(c.modelName, modelOpts...)
	sessionService := sessioninmemory.NewSessionService()

	genConfig := model.GenerationConfig{
		MaxTokens: intPtr(2000),
		Stream:    c.streaming,
	}

	instruction := strings.Join([]string{
		"You are a helpful assistant demonstrating MCP broker style tool usage.",
		"Your MCP entry points are mcp_list_servers, mcp_list_tools, mcp_inspect_tools, and mcp_call.",
		"The remote MCP service used in this example is already running before the conversation starts.",
		"Do not try to start the remote MCP service yourself. The skill only documents its endpoint and capabilities.",
		"When the user asks about MCP capabilities, inspect named servers first.",
		"Use mcp_list_tools with a selector such as local_stdio_code or https://example.com/mcp.",
		"When you need exact parameter structure for one or more tools, call mcp_inspect_tools with their names before calling them.",
		"When using mcp_call, pass a selector such as local_stdio_code.add or https://example.com/mcp.add.",
		"With mcp_call, always put remote MCP tool parameters inside the arguments object, and use {} when the tool takes no parameters.",
		"Never put MCP tool parameters like a, b, text, or issueId at the top level of mcp_call.",
		"Do not infer a tool result from the user request. If a tool call fails or returns an incomplete result, inspect the target tools and retry with corrected arguments.",
		"Prefer named servers over ad-hoc URLs when possible.",
		fmt.Sprintf("There is also a skill named %q that reveals a remote streamable HTTP MCP endpoint. Load that skill before using the remote ad-hoc path.", remoteSkillName),
		fmt.Sprintf("Prefer server %q for the local demo unless the user asks for another named server.", c.preferredServer),
	}, " ")

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A demo assistant that discovers and calls MCP tools through broker tools."),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(broker.Tools()),
		llmagent.WithSkills(repo),
		llmagent.WithSkillToolProfile(llmagent.SkillToolProfileKnowledgeOnly),
	)
	c.visibleTools = toolNames(llmAgent.Tools())

	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)
	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("mcp-broker-session-%d", time.Now().Unix())

	return nil
}

func (c *mcpBrokerChat) buildBrokerOptions(serverPath string) (
	[]mcpbroker.Option,
	[]string,
	string,
	error,
) {
	opts := []mcpbroker.Option{
		mcpbroker.WithAllowAdHocHTTP(true),
		mcpbroker.WithServers(map[string]mcpcfg.ConnectionConfig{
			"local_stdio_code": {
				Transport: "stdio",
				Command:   "go",
				Args:      []string{"run", serverPath},
				Timeout:   10 * time.Second,
			},
		}),
	}
	return opts, []string{"local_stdio_code"}, "local_stdio_code", nil
}

func (c *mcpBrokerChat) cleanupGeneratedSkillsDir() {
	if strings.TrimSpace(c.generatedSkillsDir) == "" {
		return
	}
	_ = os.RemoveAll(c.generatedSkillsDir)
}

func (c *mcpBrokerChat) closeRemoteHTTPDemo() {
	if c.remoteHTTPDemo == nil {
		return
	}
	c.remoteHTTPDemo.Close()
}

func (c *mcpBrokerChat) printBanner() {
	fmt.Printf("🚀 MCP Broker Example\n")
	fmt.Printf("Model: %s\n", c.modelName)
	if strings.TrimSpace(c.variant) == "" {
		fmt.Printf("Variant: auto\n")
	} else {
		fmt.Printf("Variant: %s\n", c.variant)
	}
	fmt.Printf("Streaming: %t\n", c.streaming)
	fmt.Printf("Agent-visible tools: %s\n", strings.Join(c.visibleTools, ", "))
	fmt.Printf("Demo named servers: %s\n", strings.Join(c.availableServerIDs, ", "))
	if len(c.availableSkillIDs) > 0 {
		fmt.Printf("Demo skills: %s\n", strings.Join(c.availableSkillIDs, ", "))
	}
	if c.prompt == "" {
		fmt.Println("Type '/tips' for sample prompts, '/visible-tools' to print the visible tools, or '/exit' to quit.")
	}
	fmt.Println(strings.Repeat("=", 72))
}

func (c *mcpBrokerChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/tips":
			c.printTips()
			continue
		case "/visible-tools":
			fmt.Printf("Visible tools: %s\n\n", strings.Join(c.visibleTools, ", "))
			continue
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n\n", err)
			continue
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *mcpBrokerChat) printTips() {
	fmt.Println("💡 Sample prompts:")
	fmt.Println("   1. What MCP servers are available to you?")
	fmt.Printf("   2. Inspect server %q and summarize its tools.\n", c.preferredServer)
	fmt.Printf("   3. Find the issue creation tool on %q, inspect its schema, and create an issue titled 'Broker demo'.\n", c.preferredServer)
	fmt.Printf("   4. Find the documentation search tool on %q and search for 'MCP broker'.\n", c.preferredServer)
	fmt.Printf("   5. Use %q to add 12 and 30.\n", c.preferredServer)
	fmt.Printf("   6. Use %q to echo the text 'hello broker'.\n", c.preferredServer)
	fmt.Printf("   7. Load the skill %q, find its already-running remote MCP endpoint, inspect the announcement tools there, and publish an announcement titled 'Broker via skill'.\n", remoteSkillName)
	fmt.Println()
}

func (c *mcpBrokerChat) processMessage(ctx context.Context, userMessage string) error {
	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
		agent.WithRequestID(uuid.NewString()),
	)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processResponse(eventChan)
}

func (c *mcpBrokerChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		toolCallsDetected bool
		assistantStarted  bool
	)

	for evt := range eventChan {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("runner event error: %s", evt.Error.Message)
		}
		if c.handleToolCalls(evt, &toolCallsDetected, &assistantStarted) {
			continue
		}
		if c.handleToolResponses(evt) {
			continue
		}
		c.handleContent(evt, &toolCallsDetected, &assistantStarted)
		if evt.IsFinalResponse() {
			fmt.Println()
			return nil
		}
	}
	return nil
}

func (c *mcpBrokerChat) handleToolCalls(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	toolCalls := evt.Response.Choices[0].Message.ToolCalls
	if len(toolCalls) == 0 {
		return false
	}

	*toolCallsDetected = true
	if *assistantStarted {
		fmt.Println()
	}
	fmt.Println()
	fmt.Println("🔧 Tool calls:")
	for _, toolCall := range toolCalls {
		fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", compactJSON(toolCall.Function.Arguments))
		}
	}
	fmt.Println("🔄 Executing tools...")
	return true
}

func (c *mcpBrokerChat) handleToolResponses(evt *event.Event) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}

	printed := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role != model.RoleTool || choice.Message.ToolID == "" {
			continue
		}
		if !printed {
			fmt.Println()
			fmt.Println("✅ Tool results:")
			printed = true
		}
		fmt.Printf("   • %s\n", preview(choice.Message.Content, 220))
	}
	return printed
}

func (c *mcpBrokerChat) handleContent(
	evt *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}

	content := extractContent(evt.Response.Choices[0], c.streaming)
	if content == "" {
		return
	}
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Println()
			fmt.Print("🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
}

func exampleDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve example directory: runtime.Caller failed")
	}
	return filepath.Dir(file), nil
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		if tl == nil || tl.Declaration() == nil {
			continue
		}
		names = append(names, tl.Declaration().Name)
	}
	sort.Strings(names)
	return names
}

func skillNames(repo skill.Repository) []string {
	if repo == nil {
		return nil
	}
	summaries := repo.Summaries()
	names := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if strings.TrimSpace(summary.Name) == "" {
			continue
		}
		names = append(names, summary.Name)
	}
	sort.Strings(names)
	return names
}

func extractContent(choice model.Choice, streaming bool) string {
	if streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func compactJSON(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "{}"
	}
	return text
}

func preview(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func intPtr(v int) *int {
	return &v
}
