package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	utils "trpc.group/trpc-go/trpc-agent-go/examples/a2a/agents"
	"trpc.group/trpc-go/trpc-agent-go/examples/a2a/registry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	port           = 8081
	agentName      = "EntranceAgent"
	codeCheckAgent = "CodeCheckAgent"
	globalRunner   runner.Runner
	defaultUserID  = "default"
)

// entranceProcessor implements the taskmanager.MessageProcessor interface.
type entranceProcessor struct{}

// ProcessMessage implements the taskmanager.MessageProcessor interface.
func (p *entranceProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	ctxID := handler.GetContextID()
	if ctxID == "" {
		log.Infof("context ID is empty")
		return nil, fmt.Errorf("context ID is empty")
	}

	text := utils.ExtractTextFromMessage(message)
	if text == "" {
		log.Infof("input is empty!")
		message := protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{
			protocol.NewTextPart(""),
		})
		return &taskmanager.MessageProcessingResult{
			Result: &message,
		}, nil
	}

	agentMsg := model.NewUserMessage(text)
	agentMsgChan, err := globalRunner.Run(ctx, defaultUserID, ctxID, agentMsg, agent.RunOptions{})
	if err != nil {
		log.Errorf("failed to run agent: %v", err)
		return nil, err
	}

	content, err := utils.ProcessStreamingResponse(agentMsgChan)
	if err != nil {
		log.Errorf("failed to process streaming response: %v", err)
		return nil, err
	}

	message = protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
		protocol.NewTextPart(content),
	})
	return &taskmanager.MessageProcessingResult{
		Result: &message,
	}, nil
}

func main() {
	// Parse command-line flags.
	host := flag.String("host", "localhost", "Host to listen on")
	modelName := flag.String("model", "deepseek-chat", "Model to use")
	flag.Parse()

	// Create the message processor.
	processor := &entranceProcessor{}

	// Create task manager and inject processor.
	taskManager, err := taskmanager.NewMemoryTaskManager(
		processor,
	)
	if err != nil {
		log.Fatalf("Failed to create task manager: %v", err)
	}

	// register code check agent service
	registry.RegisterAgentService(codeCheckAgent, fmt.Sprintf("http://%s:%d", *host, 8082))
	// generate tool list
	agentList, err := registry.GenerateToolList()
	if err != nil {
		log.Fatalf("failed to generate tool list: %v", err)
	}

	globalRunner = buildGlobalRunner(*modelName, agentList)
	agentCard := buildAgentCard(*host, agentList)
	srv, err := server.NewA2AServer(agentCard, taskManager)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Set up a channel to listen for termination signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine.
	go func() {
		serverAddr := fmt.Sprintf("%s:%d", *host, port)
		log.Infof("Starting server on %s...", serverAddr)
		if err := srv.Start(serverAddr); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for termination signal.
	sig := <-sigChan
	log.Infof("Received signal %v, shutting down...", sig)
}

func buildAgentCard(host string, agentList []tool.Tool) server.AgentCard {
	desc := "A entrance agent, it will delegate the task to the sub-agent by a2a protocol, or try to solve the task by itself"
	skills := make([]server.AgentSkill, 0, len(agentList))
	for _, tool := range agentList {
		skills = append(skills, server.AgentSkill{
			ID:          tool.Declaration().Name,
			Name:        tool.Declaration().Name,
			Description: utils.StringPtr(tool.Declaration().Description),
		})
	}

	return server.AgentCard{
		Name:        agentName,
		Description: desc,
		URL:         fmt.Sprintf("http://%s:%d/", host, port),
		Version:     "1.0.0",
		Provider: &server.AgentProvider{
			Organization: "tRPC-Go",
			URL:          utils.StringPtr("https://trpc.group/trpc-go/"),
		},
		Capabilities: server.AgentCapabilities{
			Streaming:              utils.BoolPtr(false),
			PushNotifications:      utils.BoolPtr(false),
			StateTransitionHistory: utils.BoolPtr(false),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             skills,
	}
}

func buildGlobalRunner(modelName string, agentList []tool.Tool) runner.Runner {
	// Create OpenAI model.
	modelInstance := openai.New(modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   utils.IntPtr(2000),
		Temperature: utils.FloatPtr(0.7),
		Stream:      true, // Enable streaming
	}

	desc := "A entrance agent, it will delegate the task to the sub-agent by a2a protocol, or try to solve the task by itself, agent list:"
	for _, tool := range agentList {
		desc += fmt.Sprintf("\n- %s: %s", tool.Declaration().Name, tool.Declaration().Description)
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools(agentList),
	)

	sessionService := inmemory.NewSessionService()
	return runner.NewRunner(agentName, llmAgent, runner.WithSessionService(sessionService))
}
