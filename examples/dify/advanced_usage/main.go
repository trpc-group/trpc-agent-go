//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates advanced Dify agent usage with custom converters,
// state management, and metadata handling.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	difySDK "github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/dify"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// CustomEventConverter demonstrates how to create a custom event converter
// that adds metadata and modifies responses.
type CustomEventConverter struct{}

func (c *CustomEventConverter) ConvertToEvent(
	resp *difySDK.ChatMessageResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	var content string
	if resp != nil {
		// Add custom formatting and metadata
		content = fmt.Sprintf("[Dify:%s] %s", resp.ConversationID, resp.Answer)
	}

	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content,
	}

	evt := event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Choices:   []model.Choice{{Message: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			Done:      true,
		}),
	)

	return evt
}

func (c *CustomEventConverter) ConvertStreamingToEvent(
	resp difySDK.ChatMessageStreamChannelResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	if resp.Answer == "" {
		return nil
	}

	message := model.Message{
		Role:    model.RoleAssistant,
		Content: resp.Answer,
	}

	evt := event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Object:    model.ObjectTypeChatCompletionChunk,
			Choices:   []model.Choice{{Delta: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: true,
		}),
	)

	return evt
}

// CustomRequestConverter demonstrates how to customize requests sent to Dify
type CustomRequestConverter struct{}

func (c *CustomRequestConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*difySDK.ChatMessageRequest, error) {
	// Extract user preferences from runtime state
	userPrefs := extractUserPreferences(invocation.RunOptions.RuntimeState)

	req := &difySDK.ChatMessageRequest{
		Query:  invocation.Message.Content,
		Inputs: make(map[string]any),
	}

	// Set user ID
	if invocation.Session != nil {
		req.User = invocation.Session.UserID
	}
	if req.User == "" {
		req.User = "anonymous"
	}

	// Add user preferences to inputs
	for key, value := range userPrefs {
		req.Inputs[key] = value
	}

	// Add context information
	req.Inputs["timestamp"] = time.Now().Format(time.RFC3339)
	req.Inputs["invocation_id"] = invocation.InvocationID

	// Handle streaming
	if isStream {
		req.ResponseMode = "streaming"
	}

	// Add conversation context if available
	if conversationID, ok := invocation.RunOptions.RuntimeState["conversation_id"]; ok {
		req.ConversationID = conversationID.(string)
	}

	return req, nil
}

// extractUserPreferences extracts user preferences from runtime state
func extractUserPreferences(state map[string]any) map[string]any {
	prefs := make(map[string]any)

	// Extract common preferences
	if lang, ok := state["user_language"]; ok {
		prefs["language"] = lang
	}
	if tone, ok := state["response_tone"]; ok {
		prefs["tone"] = tone
	}
	if format, ok := state["response_format"]; ok {
		prefs["format"] = format
	}
	if expertise, ok := state["expertise_level"]; ok {
		prefs["expertise_level"] = expertise
	}

	return prefs
}

func main() {
	// Get Dify configuration
	difyBaseURL := os.Getenv("DIFY_BASE_URL")
	if difyBaseURL == "" {
		difyBaseURL = "https://api.dify.ai/v1"
	}

	difyAPISecret := os.Getenv("DIFY_API_SECRET")
	if difyAPISecret == "" {
		log.Fatal("DIFY_API_SECRET environment variable is required")
	}

	// Create custom converters
	eventConverter := &CustomEventConverter{}
	requestConverter := &CustomRequestConverter{}

	// Create Dify agent with custom converters
	difyAgent, err := dify.New(
		dify.WithBaseUrl(difyBaseURL),
		dify.WithName("dify-advanced-assistant"),
		dify.WithDescription("Advanced Dify assistant with custom converters"),
		dify.WithCustomEventConverter(eventConverter),
		dify.WithCustomRequestConverter(requestConverter),
		dify.WithTransferStateKey("user_language", "response_tone", "expertise_level"),
		dify.WithEnableStreaming(false),
		// WithGetDifyClientFunc allows customizing the Dify client creation for each invocation
		// This is optional - if not provided, the agent will use a default client
		// Use this when you need:
		// - Different API keys per user/session
		// - Custom timeout settings per request
		// - Dynamic host selection based on invocation context
		// - Custom authentication or headers
		dify.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*difySDK.Client, error) {
			// Create a custom Dify client with specific configuration
			// The invocation parameter provides access to session, user info, and runtime state
			return difySDK.NewClientWithConfig(&difySDK.ClientConfig{
				Host:             difyBaseURL,      // Base URL for Dify API
				DefaultAPISecret: difyAPISecret,    // API secret for authentication
				Timeout:          30 * time.Second, // Request timeout duration
			}), nil
		}),
	)
	if err != nil {
		log.Fatalf("Failed to create Dify agent: %v", err)
	}

	// Create session service
	sessionService := inmemory.NewSessionService()

	// Create runner
	chatRunner := runner.NewRunner(
		"dify-advanced-runner",
		difyAgent,
		runner.WithSessionService(sessionService),
	)

	ctx := context.Background()
	userID := "advanced-user"

	// Demonstrate different user scenarios with different preferences
	scenarios := []struct {
		sessionID   string
		preferences map[string]any
		message     string
		description string
	}{
		{
			sessionID: "expert-session",
			preferences: map[string]any{
				"user_language":   "en",
				"response_tone":   "professional",
				"expertise_level": "expert",
				"response_format": "detailed",
			},
			message:     "Explain quantum computing",
			description: "Expert user asking about quantum computing",
		},
		{
			sessionID: "beginner-session",
			preferences: map[string]any{
				"user_language":   "en",
				"response_tone":   "friendly",
				"expertise_level": "beginner",
				"response_format": "simple",
			},
			message:     "What is artificial intelligence?",
			description: "Beginner user asking about AI",
		},
		{
			sessionID: "casual-session",
			preferences: map[string]any{
				"user_language":   "en",
				"response_tone":   "casual",
				"expertise_level": "intermediate",
				"response_format": "concise",
			},
			message:     "Tell me about the latest tech trends",
			description: "Casual conversation about tech trends",
		},
	}

	fmt.Println("🔧 Starting Dify Advanced Usage Example")
	fmt.Println(strings.Repeat("=", 60))

	for i, scenario := range scenarios {
		fmt.Printf("\n📋 Scenario %d: %s\n", i+1, scenario.description)
		fmt.Printf("👤 User: %s\n", scenario.message)

		// Show user preferences
		fmt.Println("⚙️  User Preferences:")
		for key, value := range scenario.preferences {
			fmt.Printf("   • %s: %v\n", key, value)
		}

		fmt.Print("🤖 Assistant: ")

		// Run with specific preferences
		events, err := chatRunner.Run(
			ctx,
			userID,
			scenario.sessionID,
			model.NewUserMessage(scenario.message),
			agent.WithRuntimeState(scenario.preferences),
		)
		if err != nil {
			log.Printf("Error running agent: %v", err)
			continue
		}

		// Process events and show metadata
		for event := range events {
			if event.Error != nil {
				log.Printf("Event error: %s", event.Error.Message)
				continue
			}

			if event.Response != nil && len(event.Response.Choices) > 0 {
				choice := event.Response.Choices[0]
				if event.Response.Done {
					fmt.Println(choice.Message.Content)
				}
			}
		}

		// Add separator
		if i < len(scenarios)-1 {
			fmt.Println("\n" + strings.Repeat("-", 40))
			time.Sleep(1 * time.Second)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🎯 Advanced usage example completed!")
	fmt.Println("\nKey features demonstrated:")
	fmt.Println("• Custom event converter with metadata")
	fmt.Println("• Custom request converter with user preferences")
	fmt.Println("• State transfer between sessions")
	fmt.Println("• Dynamic conversation context")
}
