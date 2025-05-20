// Command client provides a multi-turn CLI client for the basic agent example.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// AgentRequest represents a request to the agent.
type ClientAgentRequest struct {
	Message string `json:"message"`
}

// AgentResponse represents a response from the agent.
type ClientAgentResponse struct {
	Message string                   `json:"message"`
	Steps   []map[string]interface{} `json:"steps,omitempty"`
}

func main() {
	serverURL := flag.String("server", "http://localhost:8080", "The agent server URL")
	initialMessage := flag.String("message", "", "Optional initial message to send to the agent")
	showReasoning := flag.Bool("reasoning", true, "Show agent reasoning steps")
	flag.Parse()

	// Check if the server is running
	client := &http.Client{}
	_, err := client.Get(*serverURL)
	if err != nil {
		fmt.Printf("Cannot connect to server at %s. Is it running?\n", *serverURL)
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("=== Multi-turn Chat with Agent ===")
	fmt.Printf("Connected to: %s\n", *serverURL)
	fmt.Println("Type 'exit' or 'quit' to end the conversation.")
	fmt.Println("Type 'clear' to clear the conversation history.")
	fmt.Println()

	// Keep track of conversation history
	var conversationHistory []string

	// Send initial message if provided
	if *initialMessage != "" {
		fmt.Printf("You: %s\n", *initialMessage)
		response, err := sendMessage(*serverURL, *initialMessage, *showReasoning)
		if err != nil {
			fmt.Println("Error sending message:", err)
			return
		}
		conversationHistory = append(conversationHistory, *initialMessage)
		conversationHistory = append(conversationHistory, response)
	}

	// Main conversation loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("You: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading input:", err)
			continue
		}

		// Trim whitespace
		input = strings.TrimSpace(input)

		// Check for exit commands
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			return
		}

		// Check for clear command
		if input == "clear" {
			conversationHistory = nil
			fmt.Println("Conversation history cleared.")
			continue
		}

		// Send message to agent
		response, err := sendMessage(*serverURL, input, *showReasoning)
		if err != nil {
			fmt.Println("Error sending message:", err)
			continue
		}

		// Update conversation history
		conversationHistory = append(conversationHistory, input)
		conversationHistory = append(conversationHistory, response)
	}
}

// sendMessage sends a message to the agent server and returns the response.
func sendMessage(serverURL, message string, showReasoning bool) (string, error) {
	// Create the request
	reqBody, err := json.Marshal(ClientAgentRequest{Message: message})
	if err != nil {
		return "", err
	}

	// Send the request
	url := fmt.Sprintf("%s/api/agent", serverURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse the response
	var agentResp ClientAgentResponse
	if err := json.Unmarshal(body, &agentResp); err != nil {
		return "", err
	}

	// Print the agent's response
	fmt.Printf("Agent: %s\n", agentResp.Message)

	// Print reasoning steps if requested
	if showReasoning && len(agentResp.Steps) > 0 {
		fmt.Println("\n=== Reasoning Steps ===")
		for i, step := range agentResp.Steps {
			fmt.Printf("Step %d:\n", i+1)

			// Print thought
			if thought, ok := step["thought"].(string); ok {
				fmt.Printf("  Thought: %s\n", thought)
			}

			// Print actions
			if actions, ok := step["actions"].([]interface{}); ok && len(actions) > 0 {
				fmt.Println("  Actions:")
				for _, actionI := range actions {
					if action, ok := actionI.(map[string]interface{}); ok {
						toolName := action["tool"].(string)
						toolInput, _ := json.MarshalIndent(action["input"], "    ", "  ")
						fmt.Printf("    Tool: %s\n    Input: %s\n", toolName, string(toolInput))
					}
				}
			}

			// Print observations
			if observations, ok := step["observations"].([]interface{}); ok && len(observations) > 0 {
				fmt.Println("  Observations:")
				for _, obsI := range observations {
					if obs, ok := obsI.(string); ok {
						fmt.Printf("    %s\n", obs)
					}
				}
			}
			fmt.Println()
		}
		fmt.Println("=====================")
	}

	fmt.Println()
	return agentResp.Message, nil
}
