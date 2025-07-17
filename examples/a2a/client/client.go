package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
)

func main() {
	// Parse command line flags
	agentURL := flag.String("url", "http://localhost:8081", "A2A agent URL")
	flag.Parse()

	fmt.Printf("🚀 A2A Interactive Client\n")
	fmt.Printf("Agent URL: %s\n", *agentURL)
	fmt.Printf("Type 'exit' to quit\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create A2A client
	a2aClient, err := client.NewA2AClient(*agentURL, client.WithTimeout(120*time.Second))
	if err != nil {
		log.Fatalf("Failed to create A2A client: %v", err)
	}

	// Test agent connection by getting agent card via HTTP
	fmt.Println("🔗 Connecting to agent...")
	agentCard, err := fetchAgentCard(*agentURL)
	if err != nil {
		log.Fatalf("Failed to get agent card: %v", err)
	}

	fmt.Printf("✅ Connected to agent: %s\n", agentCard.Name)
	fmt.Printf("📝 Description: %s\n", agentCard.Description)
	fmt.Printf("🏷️  Version: %s\n", agentCard.Version)
	if len(agentCard.Skills) > 0 {
		fmt.Printf("🛠️  Skills:\n")
		for _, skill := range agentCard.Skills {
			fmt.Printf("   • %s: %s\n", skill.Name, *skill.Description)
		}
	}
	fmt.Println()

	// Start interactive chat
	if err := startInteractiveChat(a2aClient); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

func fetchAgentCard(agentURL string) (server.AgentCard, error) {
	agentCardURL := fmt.Sprintf("%s/.well-known/agent.json", agentURL)

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Get(agentCardURL)
	if err != nil {
		return server.AgentCard{}, fmt.Errorf("failed to fetch agent card: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return server.AgentCard{}, fmt.Errorf("failed to fetch agent card: status %d", resp.StatusCode)
	}

	var card server.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return server.AgentCard{}, fmt.Errorf("failed to decode agent card: %w", err)
	}

	return card, nil
}

func startInteractiveChat(a2aClient *client.A2AClient) error {
	scanner := bufio.NewScanner(os.Stdin)
	ctx := context.Background()
	ctxID := ""

	fmt.Println("💬 Start chatting (type 'exit' to quit):")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle exit command
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Send message to agent
		newCtxID, err := sendMessage(ctx, ctxID, a2aClient, userInput)
		ctxID = newCtxID
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			continue
		}

		fmt.Printf("\nconversation finished ctx id: %s\n", ctxID) // Add spacing between conversations
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

func sendMessage(ctx context.Context, ctxID string, a2aClient *client.A2AClient, userInput string) (string, error) {
	var latestCtxID *string

	message := protocol.Message{
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{protocol.NewTextPart(userInput)},
		ContextID: &ctxID,
	}

	// Create send message params
	params := protocol.SendMessageParams{
		Message: message,
	}

	fmt.Printf("📤 Sending message to agent...\n")

	// Send message to agent
	result, err := a2aClient.SendMessage(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	// Display agent response
	fmt.Printf("🤖 Agent: ")

	// Handle the UnaryMessageResult which can be Message or Task
	switch v := result.Result.(type) {
	case *protocol.Message:
		// Extract text from message parts
		for _, part := range v.Parts {
			if textPart, ok := part.(*protocol.TextPart); ok {
				fmt.Print(textPart.Text)
			}
		}
		latestCtxID = v.ContextID
		fmt.Println()
	case *protocol.Task:
		latestCtxID = &v.ContextID
		fmt.Printf("(Received task response: %s)", v.ID)
		fmt.Println()
	default:
		fmt.Println("(Received unknown response type)")
	}

	if latestCtxID != nil {
		return *latestCtxID, nil
	}
	return "", nil
}
