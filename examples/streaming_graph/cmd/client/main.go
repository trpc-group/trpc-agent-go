package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Default server URL
const defaultServerURL = "http://localhost:8080"

// StreamResponse represents a chunk of a streaming response
type StreamResponse struct {
	Type    string      `json:"type"`
	Content string      `json:"content,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// QueryRequest represents a request to the server
type QueryRequest struct {
	Message string `json:"message"`
}

func main() {
	// Get server URL from environment variable or use default
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		serverURL = defaultServerURL
	}

	fmt.Printf("Streaming Graph Client\n")
	fmt.Printf("Connected to: %s\n", serverURL)
	fmt.Printf("Type 'exit' or 'quit' to end the session\n")
	fmt.Printf("Type 'stream' to enable streaming mode or 'nostream' to disable it\n")
	fmt.Printf("--------------------------------------------------\n\n")

	// Create a scanner for reading user input
	scanner := bufio.NewScanner(os.Stdin)

	// Setup signal handling for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Set initial streaming mode
	streamingMode := true

	// Main interaction loop
	go func() {
		for {
			fmt.Print("> ")
			if !scanner.Scan() {
				break
			}

			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}

			// Check for exit commands
			if input == "exit" || input == "quit" {
				fmt.Println("Exiting...")
				os.Exit(0)
			}

			// Check for streaming mode commands
			if input == "stream" {
				streamingMode = true
				fmt.Println("Streaming mode enabled")
				continue
			}

			if input == "nostream" {
				streamingMode = false
				fmt.Println("Streaming mode disabled")
				continue
			}

			// Process the input
			if streamingMode {
				if err := sendStreamingRequest(serverURL, input); err != nil {
					fmt.Printf("Error: %v\n", err)
				}
			} else {
				if err := sendNonStreamingRequest(serverURL, input); err != nil {
					fmt.Printf("Error: %v\n", err)
				}
			}
		}
	}()

	// Wait for a termination signal
	<-signalChan
	fmt.Println("\nShutting down...")
}

// sendNonStreamingRequest sends a request to the non-streaming endpoint
func sendNonStreamingRequest(serverURL, message string) error {
	// Create the request payload
	req := QueryRequest{Message: message}
	payloadBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send the request
	url := fmt.Sprintf("%s/query", serverURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check for errors
	if resp.StatusCode != http.StatusOK {
		// Read at most 1024 bytes of the error message
		limitedReader := io.LimitReader(resp.Body, 1024)
		errorMsg, _ := io.ReadAll(limitedReader)
		return fmt.Errorf("server returned error: %s - %s", resp.Status, string(errorMsg))
	}

	// Parse the response
	var response struct {
		Content string `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Display the response
	fmt.Printf("\n%s\n\n", response.Content)
	return nil
}

// sendStreamingRequest sends a request to the streaming endpoint
func sendStreamingRequest(serverURL, message string) error {
	// Create the request payload
	req := QueryRequest{Message: message}
	payloadBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create a new request
	client := &http.Client{
		Timeout: 0, // No timeout for streaming requests
	}
	url := fmt.Sprintf("%s/stream", serverURL)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")

	fmt.Println("Connecting to server...")

	// Send the request
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check for errors
	if resp.StatusCode != http.StatusOK {
		// Read at most 1024 bytes of the error message
		limitedReader := io.LimitReader(resp.Body, 1024)
		errorMsg, _ := io.ReadAll(limitedReader)
		return fmt.Errorf("server returned error: %s - %s", resp.Status, string(errorMsg))
	}

	fmt.Println("Connected. Waiting for response...")

	// Process the streaming response
	reader := bufio.NewReader(resp.Body)
	var fullContent strings.Builder
	for {
		// Read a line from the SSE stream
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading response: %w", err)
		}

		// Check if this is a data line
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		// Parse the event data
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue // Skip empty data lines
		}

		var streamResp StreamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			fmt.Printf("Error parsing SSE data: %v - Raw data: %s\n", err, data)
			continue
		}
		// Handle different event types
		switch streamResp.Type {
		case "ping":
			// Just a heartbeat - ignore
			continue
		case "start":
			fmt.Print("Generating response... ")
			// Clear terminal line and move cursor to start
			fmt.Print("\r\033[K")
		case "chunk":
			// Print the chunk and add to full content
			if streamResp.Content != "" {
				fmt.Print(streamResp.Content)
				fullContent.WriteString(streamResp.Content)
				// Flush stdout to ensure immediate display
				os.Stdout.Sync()
			}
		case "message":
			// Full message received
			if streamResp.Content != "" && fullContent.Len() == 0 {
				fmt.Print(streamResp.Content)
				fullContent.WriteString(streamResp.Content)
				os.Stdout.Sync()
			}
		case "tool_call":
			fmt.Printf("\n[Using tool: %v]\n", streamResp.Data)
		case "error":
			return fmt.Errorf("server error: %s", streamResp.Content)
		default:
		}

		// Add a small delay to avoid consuming too much CPU
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println()
	return nil
}
