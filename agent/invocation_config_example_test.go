//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent_test

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CustomAgent is an example third-party agent that uses custom configuration.
type CustomAgent struct {
	name        string
	description string
}

func (a *CustomAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

func (a *CustomAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (a *CustomAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *CustomAgent) Tools() []tool.Tool {
	return nil
}

func (a *CustomAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Example 1: Get configuration for this agent type
		config := invocation.GetThirdPartyAgentConfig("custom-agent")
		if configMap, ok := config.(map[string]any); ok {
			// Access configuration values
			if apiKey, ok := configMap["api_key"].(string); ok {
				fmt.Printf("Using API key: %s\n", apiKey)
			}

			if timeout, ok := configMap["timeout"].(int); ok {
				fmt.Printf("Timeout: %d seconds\n", timeout)
			}
		}

		// Example 2: Use convenience method to get a single value
		modelName, ok := invocation.GetThirdPartyAgentConfigValue("custom-agent", "model").(string)
		if ok {
			fmt.Printf("Using model: %s\n", modelName)
		}
	}()

	return eventChan, nil
}

// Example 1: Using WithThirdPartyAgentConfigs for multiple agent types
func ExampleWithThirdPartyAgentConfigs() {
	// Configure multiple agent types at once
	configs := map[string]any{
		"custom-llm": map[string]any{
			"api_key":     "sk-xxx",
			"model":       "custom-model-v1",
			"temperature": 0.7,
			"max_tokens":  1000,
		},
		"custom-search": map[string]any{
			"headers": map[string]string{
				"X-Custom-Header": "value",
				"Authorization":   "Bearer token",
			},
			"timeout": 30,
		},
		"custom-translator": map[string]any{
			"endpoint": "https://api.custom-translator.com",
			"api_key":  "custom-key",
			"retry": map[string]any{
				"max_attempts": 3,
				"backoff":      "exponential",
			},
		},
	}

	// Pass to Run method
	// runner.Run(ctx, userID, sessionID, message,
	//     agent.WithThirdPartyAgentConfigs(configs),
	// )

	fmt.Printf("Configured %d agent types\n", len(configs))
}

// Example 2: Accessing configuration in a custom agent
func ExampleInvocation_GetThirdPartyAgentConfig() {
	customAgent := &CustomAgent{
		name:        "custom-agent",
		description: "A custom third-party agent",
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationAgent(customAgent),
		agent.WithInvocationRunOptions(agent.RunOptions{
			ThirdPartyAgentConfigs: map[string]any{
				"custom-agent": map[string]any{
					"api_key": "sk-xxx",
					"model":   "custom-model",
					"timeout": 30,
				},
			},
		}),
	)

	// In the agent's Run method:
	config := invocation.GetThirdPartyAgentConfig("custom-agent")
	if configMap, ok := config.(map[string]any); ok {
		apiKey := configMap["api_key"].(string)
		model := configMap["model"].(string)
		timeout := configMap["timeout"].(int)

		fmt.Printf("API Key: %s\n", apiKey)
		fmt.Printf("Model: %s\n", model)
		fmt.Printf("Timeout: %d\n", timeout)
	}
}

// Example 3: Accessing a single configuration value
func ExampleInvocation_GetThirdPartyAgentConfigValue() {
	customAgent := &CustomAgent{
		name:        "custom-agent",
		description: "A custom third-party agent",
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationAgent(customAgent),
		agent.WithInvocationRunOptions(agent.RunOptions{
			ThirdPartyAgentConfigs: map[string]any{
				"custom-agent": map[string]any{
					"api_key": "sk-xxx",
					"timeout": 30,
				},
			},
		}),
	)

	// Convenience method for getting a single value
	apiKey, ok := invocation.GetThirdPartyAgentConfigValue("custom-agent", "api_key").(string)
	if ok {
		fmt.Printf("API Key: %s\n", apiKey)
	}

	timeout, ok := invocation.GetThirdPartyAgentConfigValue("custom-agent", "timeout").(int)
	if ok {
		fmt.Printf("Timeout: %d seconds\n", timeout)
	}
}

// Example 4: Real-world usage with runner
func ExampleWithThirdPartyAgentConfigs_realWorld() {
	// This example shows how to use third-party agent configuration in a real scenario

	// When calling runner.Run(), pass agent configuration via RunOptions:
	//
	// eventChan, err := runner.Run(
	//     ctx,
	//     "user123",
	//     "session456",
	//     model.Message{
	//         Role:    model.RoleUser,
	//         Content: "Hello",
	//     },
	//     agent.WithThirdPartyAgentConfigs(map[string]any{
	//         "custom-llm": map[string]any{
	//             "api_key":     os.Getenv("CUSTOM_LLM_API_KEY"),
	//             "model":       "custom-model-v1",
	//             "temperature": 0.7,
	//         },
	//         "custom-search": map[string]any{
	//             "headers": map[string]string{
	//                 "X-Request-ID": requestID,
	//                 "X-User-ID":    userID,
	//             },
	//         },
	//     }),
	// )

	fmt.Println("See code comments for usage example")
}
