// Package main provides a CLI client for the session server.
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

	"trpc.group/trpc-go/trpc-agent-go/message"
)

var (
	// Command line flags for the CLI client
	serverURL = flag.String("server", "http://localhost:8080", "Session server URL")
	sessionID = flag.String("session", "", "Session ID (will create new if empty)")
	useStream = flag.Bool("stream", true, "Use streaming API")
)

// Response represents the server response structure
type Response struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
	ID      string `json:"id,omitempty"` // Added for session creation
}

// Request represents the request structure
type Request struct {
	Message string `json:"message"`
}

// ClientSession represents a client session
type ClientSession struct {
	ID        string
	ServerURL string
	UseStream bool
}

// NewSession creates a new session on the server
func NewSession(serverURL string) (*ClientSession, error) {
	resp, err := http.Post(fmt.Sprintf("%s/sessions", serverURL), "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer resp.Body.Close()

	// Accept both 200 OK and 201 Created as successful responses
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var response Response
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Determine the session ID based on the response
	sessionID := response.Message
	if response.ID != "" {
		// If ID field is present, use it
		sessionID = response.ID
	}

	return &ClientSession{
		ID:        sessionID,
		ServerURL: serverURL,
	}, nil
}

// SendMessage sends a message to the server and returns the response
func (s *ClientSession) SendMessage(msg string, useStream bool) (string, error) {
	requestBody, err := json.Marshal(Request{Message: msg})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := "/run"
	if useStream {
		endpoint = "/run_stream"
	}

	url := fmt.Sprintf("%s/sessions/%s%s", s.ServerURL, s.ID, endpoint)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	if useStream {
		// Process SSE stream
		reader := bufio.NewReader(resp.Body)
		var fullResponse strings.Builder

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				return "", fmt.Errorf("error reading stream: %w", err)
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// SSE format: "data: {json data}"
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// Skip [DONE] marker
			if data == "[DONE]" {
				break
			}

			var event struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}

			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue // Skip malformed events
			}

			// Handle different event types
			switch event.Type {
			case "stream_chunk":
				if content, ok := event.Data["content"].(string); ok {
					fmt.Print(content)
					fullResponse.WriteString(content)
				}
			case "stream_tool_call":
				fmt.Printf("\n[Tool Call] %s: ", event.Data["name"])
				if args, ok := event.Data["arguments"].(string); ok {
					fmt.Printf("%s\n", args)
				}
			case "stream_tool_result":
				fmt.Print("\n[Tool Result] ")
				if result, ok := event.Data["result"]; ok {
					resultBytes, _ := json.MarshalIndent(result, "", "  ")
					fmt.Printf("%s\n", string(resultBytes))
				}
				if errStr, ok := event.Data["error"].(string); ok && errStr != "" {
					fmt.Printf("Error: %s\n", errStr)
				}
			}
		}
		fmt.Println() // Print final newline
		return fullResponse.String(), nil
	}

	// For non-streaming response
	type nonStreamingResponse struct {
		Message message.Message `json:"message"`
	}

	var response nonStreamingResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	// Print the response for non-streaming mode
	fmt.Print(response.Message.Content)
	fmt.Println()
	return response.Message.Content, nil
}

func main() {
	flag.Parse()

	var session *ClientSession
	var err error

	// Get or create session
	if *sessionID == "" {
		fmt.Println("Creating new session...")
		session, err = NewSession(*serverURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Session created: %s\n", session.ID)
	} else {
		session = &ClientSession{
			ID:        *sessionID,
			ServerURL: *serverURL,
		}
		fmt.Printf("Using existing session: %s\n", session.ID)
	}

	// Main chat loop
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Enter your messages (type 'quit' to exit):")

	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}

		input := scanner.Text()
		if input == "quit" || input == "exit" {
			break
		}

		fmt.Print("\nAI: ")
		_, err := session.SendMessage(input, *useStream)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			continue
		}

		// For streaming, the output is already printed
		// For non-streaming, it gets printed when received
	}

	fmt.Println("\nSession ended.")
}
