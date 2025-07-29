package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	a2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/examples/a2a/registry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2a "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	entranceAgentName = "EntranceAgent"
	codeCheckAgent    = "CodeCheckAgent"
)

func main() {
	// Parse command-line flags.
	host := flag.String("host", "localhost:8087", "Host to listen on")
	modelName := flag.String("model", "deepseek-chat", "Model to use")
	flag.Parse()

	// simulate a registry center
	registry.RegisterAgentService(codeCheckAgent, "http://localhost:8088/a2a/codecheck/")

	// generate tool list
	agentList, err := registry.GenerateToolList()
	if err != nil {
		log.Fatalf("failed to generate tool list: %v", err)
	}

	// Build the entrance agent
	entranceAgent := buildEntranceAgent(*modelName, agentList)

	// Create agent card
	agentCard := buildAgentCard(*host, agentList)

	// Create a2a server with the agent
	server, err := a2a.New(
		a2a.WithHost(*host),
		a2a.WithAgent(entranceAgent),
		a2a.WithAgentCard(agentCard),
		a2a.WithExtraA2AOptions(a2aserver.WithBasePath("/a2a/entrance/")),
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

func buildEntranceAgent(modelName string, agentList []tool.Tool) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(modelName, openai.WithChannelBufferSize(512))

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	desc := "A entrance agent, it will delegate the task to the sub-agent by a2a protocol, or try to solve the task by itself, agent list:"
	for _, tool := range agentList {
		desc += fmt.Sprintf("\n- %s: %s", tool.Declaration().Name, tool.Declaration().Description)
	}

	llmAgent := llmagent.New(
		entranceAgentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools(agentList),
	)

	return llmAgent
}

func buildAgentCard(host string, agentList []tool.Tool) a2aserver.AgentCard {
	desc := "A entrance agent, it will delegate the task to the sub-agent by a2a protocol, or try to solve the task by itself"
	skills := make([]a2aserver.AgentSkill, 0, len(agentList))
	for _, tool := range agentList {
		skills = append(skills, a2aserver.AgentSkill{
			ID:          tool.Declaration().Name,
			Name:        tool.Declaration().Name,
			Description: stringPtr(tool.Declaration().Description),
		})
	}

	return a2aserver.AgentCard{
		Name:        entranceAgentName,
		Description: desc,
		URL:         fmt.Sprintf("http://%s/a2a/entrance/", host),
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
		Skills:             skills,
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
