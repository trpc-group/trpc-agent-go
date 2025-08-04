// Package main provides a code check agent for A2A (Agent-to-Agent) communication.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	a2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2a "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	agentName = "CodeCheckAgent"
)

func main() {
	// Parse command-line flags.
	host := flag.String("host", "localhost:8088", "Host to listen on")
	modelName := flag.String("model", "deepseek-chat", "Model to use")
	flag.Parse()

	// Build the code check agent
	codeCheckAgent := buildCodeCheckAgent(*modelName)

	// Create agent card
	agentCard := buildAgentCard()

	// Create a2a server with the agent
	server, err := a2a.New(
		a2a.WithHost(*host),
		a2a.WithAgent(codeCheckAgent),
		a2a.WithAgentCard(agentCard),
		a2a.WithExtraA2AOptions(a2aserver.WithBasePath("/a2a/codecheck/")),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a server: %v", err)
	}

	// Set up a channel to listen for termination signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine.
	go func() {
		log.Infof("Starting server on %s...", *host)
		if err := server.Start(*host); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for termination signal.
	sig := <-sigChan
	log.Infof("Received signal %v, shutting down...", sig)

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		log.Errorf("Failed to stop server gracefully: %v", err)
	}
}

func buildCodeCheckAgent(modelName string) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(modelName)

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	readSpecTool := function.NewFunctionTool(
		readSpecFile,
		function.WithName("ReadGolangStandardSpec"),
		function.WithDescription("Read the golang standard spec file from go language standard"),
	)

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A agent that can analyze code and check code quality by Go Language Standard"),
		llmagent.WithInstruction("Analyze the code and check code quality by Go Language Standard"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{readSpecTool}),
	)

	return llmAgent
}

func buildAgentCard() a2aserver.AgentCard {
	return a2aserver.AgentCard{
		Name:        agentName,
		Description: "Check code quality by Go Language Standard; Query the golang standard/spec that user needed",
		Version:     "1.0.0",
		Provider: &a2aserver.AgentProvider{
			Organization: "tRPC-Go",
			URL:          stringPtr("https://trpc.group/trpc-go/"),
		},
		Capabilities: a2aserver.AgentCapabilities{
			Streaming:              boolPtr(true),
			PushNotifications:      boolPtr(false),
			StateTransitionHistory: boolPtr(false),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []a2aserver.AgentSkill{
			{
				ID:          "code_check",
				Name:        "code_check",
				Description: stringPtr("Check code quality by Go Language Standard; Query the golang standard/spec that user needed"),
				Tags:        []string{"code", "check", "golang"},
				Examples: []string{
					`
					Analyze the code and check code quality by Go Language Standard.
					Query the golang standard spec/standard file.
					`,
				},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
		},
	}
}

func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func floatPtr(f float64) *float64 {
	return &f
}
