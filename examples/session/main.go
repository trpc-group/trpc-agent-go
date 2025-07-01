package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	goredis "github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session/redis"
)

var (
	appName        = "simple-chat"
	agentName      = "simple-chat-agent"
	sessionID      = "test-session"
	userID         = "test-user"
	sessionService = "inmemory"
	modelType      = "gpt-3.5-turbo"
)

func main() {
	flag.StringVar(&sessionService, "session", "inmemory", "The name of the session service, inmemory / redis / empty")
	flag.StringVar(&modelType, "model", "gpt-3.5-turbo", "The name of the model")
	flag.Parse()

	ctx := context.Background()
	// 1. Create OpenAI model
	modelInstance := openai.New(modelType, openai.Options{
		ChannelBufferSize: 50,
	})

	// 2. Create LLM agent
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(500),
		Temperature: floatPtr(0.7),
		Stream:      false,
	}

	agent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithSystemPrompt("You are a helpful assistant."),
		llmagent.WithGenerationConfig(genConfig),
	)

	var service session.Service
	var err error
	switch sessionService {
	case "inmemory":
		service = inmemory.NewSessionService()
	case "redis":
		redisClient := goredis.NewUniversalClient(&goredis.UniversalOptions{})
		service, err = redis.NewService(redis.WithRedisClient(redisClient))
		defer func() {
			service.DeleteSession(ctx, session.Key{
				AppName:   "simple-chat",
				UserID:    "user1",
				SessionID: "session1",
			}, &session.Options{})
			redisClient.Close()
		}()
	case "empty":
		service = &emptySessionService{
			SessionService: *inmemory.NewSessionService(),
		}
	default:
		log.Fatal("Invalid session service:", sessionService)
	}

	if err != nil {
		log.Fatal("New session service failed:", err)
	}

	runnerInstance := runner.New(appName, agent, runner.WithSessionService(service))

	runConversationWithSession(ctx, runnerInstance, service)
	runConversationWithHistorySession(ctx, runnerInstance, service)
}

func runConversationWithSession(ctx context.Context, runnerInstance *runner.Runner, _ session.Service) {
	fmt.Println("=== Simple Chat Example ===")
	// First message
	message1 := model.NewUserMessage("Hello, what's your name?")
	eventChan1, err := runnerInstance.Run(ctx, userID, sessionID, message1, struct{}{})
	if err != nil {
		log.Fatal("First chat failed:", err)
	}

	fmt.Printf("User: Hello, what's your name?\n")
	fmt.Printf("Assistant: ")
	processEvents(eventChan1)

	// Second message - test conversation history
	message2 := model.NewUserMessage("Tell the previous message I just asked you")
	eventChan2, err := runnerInstance.Run(ctx, userID, sessionID, message2, struct{}{})
	if err != nil {
		log.Fatal("Second chat failed:", err)
	}

	fmt.Printf("\nUser: Tell the previous message I just asked you\n")
	fmt.Printf("Assistant: ")
	processEvents(eventChan2)
}

func runConversationWithHistorySession(ctx context.Context, runnerInstance *runner.Runner, service session.Service) {
	fmt.Println("=== Simple Chat Example with History Session ===")

	sess, err := service.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}, session.StateMap{}, &session.Options{})

	if err != nil {
		log.Fatal("Create session failed:", err)
	}

	err = service.AppendEvent(ctx, sess, event.NewResponseEvent(sessionID, agentName, &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "I have a farm where chickens and rabbits are in the same place. There are a total of 7000 heads and 18800 feet.",
				},
			},
		},
	}), &session.Options{})

	if err != nil {
		log.Fatal("Append event failed:", err)
	}

	// First message
	message1 := model.NewUserMessage("How many chickens and rabbits are there, just reply two numbers in one line.")
	eventChan1, err := runnerInstance.Run(ctx, userID, sessionID, message1, struct{}{})
	if err != nil {
		log.Fatal("First chat failed:", err)
	}

	fmt.Println("History: I have a farm where chickens and rabbits are in the same cage. There are a total of 7000 heads and 18800 feet.")
	fmt.Printf("User: How many chickens and rabbits are there, just reply two numbers in one line.\n")
	fmt.Printf("Assistant: ")
	processEvents(eventChan1)

}

// processEvents handles the event stream and prints assistant responses
func processEvents(eventChan <-chan *event.Event) {
	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("Error: %s\n", event.Error.Message)
			continue
		}

		// Print assistant content
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Message.Content != "" {
				fmt.Print(choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
		}
	}
	fmt.Println() // Add newline after response
}

// Helper functions for creating pointers
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

// emptySessionService is a session service that does not store sessions.
type emptySessionService struct {
	inmemory.SessionService
}

// AppendEvent appends an event to a session.
func (e *emptySessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	event *event.Event,
	options *session.Options,
) error {
	sess.Events = append(sess.Events, *event)
	return nil
}
