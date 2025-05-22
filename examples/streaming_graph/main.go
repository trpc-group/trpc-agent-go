package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/graph"
)

func main() {
	log.SetLevel(log.LevelDebug)
	// Check for required environment variables
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable not set")
	}

	modelName := os.Getenv("OPENAI_MODEL_NAME")
	if modelName == "" {
		modelName = "gpt-4o" // Default to GPT-4o if not specified
		log.Infof("OPENAI_MODEL_NAME not set, using default: %s", modelName)
	}

	openaiURL := os.Getenv("OPENAI_BASE_URL")
	if openaiURL == "" {
		openaiURL = "https://api.openai.com/v1" // Default OpenAI API URL
		log.Infof("OPENAI_BASE_URL not set, using default: %s", openaiURL)
	} else {
		log.Infof("Using custom OpenAI API URL: %s", openaiURL)
	}

	// Create a custom timeout
	timeout := 60 * time.Second
	timeoutStr := os.Getenv("API_TIMEOUT_SECONDS")
	if timeoutStr != "" {
		if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
			timeout = time.Duration(t) * time.Second
		}
	}
	log.Infof("Using API timeout: %v", timeout)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default port
	}

	// Create the graph with OpenAI model
	g := buildGraph(apiKey, modelName, openaiURL)

	// Set up HTTP server
	http.HandleFunc("/query", createQueryHandler(g))
	http.HandleFunc("/stream", createStreamHandler(g))
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	log.Infof("Starting server on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// buildGraph creates a graph with an OpenAI model node and supporting nodes
func buildGraph(apiKey, modelName, apiURL string) *graph.Graph {
	// Create OpenAI model
	openAIModel := model.NewOpenAIStreamingModel(
		modelName,
		model.WithOpenAIAPIKey(apiKey),
		model.WithOpenAIBaseURL(apiURL),
		model.WithOpenAIDefaultOptions(model.GenerationOptions{
			Temperature:      0.7,
			MaxTokens:        2000,
			TopP:             1.0,
			FrequencyPenalty: 0.0,
			PresencePenalty:  0.0,
		}),
	)

	// Test the OpenAI connection
	log.Infof("Testing connection to OpenAI API...")
	testCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testResp, err := openAIModel.GenerateWithMessages(
		testCtx,
		[]*message.Message{message.NewUserMessage("Hello")},
		model.GenerationOptions{MaxTokens: 5},
	)

	if err != nil {
		log.Infof("Warning: OpenAI API test failed: %v", err)
		log.Infof("The server will start anyway, but API calls may fail")
	} else {
		log.Infof("Successfully connected to OpenAI API")
		if len(testResp.Messages) > 0 {
			log.Infof("Model response: %s", testResp.Messages[0].Content)
		}
	}
	modelNode := graph.NewModelNode(openAIModel, modelName, "OpenAI API compatible language model for generating responses")

	// Create a preprocessing node
	preprocessNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Add metadata to the message
		input.SetMetadata("processed_at", time.Now().Format(time.RFC3339))
		input.SetMetadata("client_ip", ctx.Value("client_ip"))

		// Log the input
		log.Infof("Processing request: %s", input.Content)
		return input, nil
	}).WithInfo("preprocess", "Preprocesses the input message")

	// Create a system prompt node
	systemPromptNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Create a system message with instructions
		return message.NewSystemMessage(`You are a helpful AI assistant that specializes in providing information about 
		software architecture and design patterns. You excel at explaining complex concepts in simple terms.
		When responding to code-related questions, include brief examples to illustrate your points.
		Always be concise, accurate, and helpful.`), nil
	}).WithInfo("system_prompt", "Provides system instructions")

	// Create a weather tool
	weatherTool := NewWeatherTool()

	// Create a tool calling node that can use tools
	toolCallingNode := NewToolCallingNode([]tool.Tool{weatherTool})

	// Create a postprocessing node
	postprocessNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Add a signature to the response
		enhanced := fmt.Sprintf("%s\n\n---\nPowered by %s", input.Content, modelName)
		return message.NewAssistantMessage(enhanced), nil
	}).WithInfo("postprocess", "Adds signature to the response")

	// Create the graph
	g := graph.NewGraph("openai_streaming_graph", "Graph with OpenAI model and streaming")

	// Add nodes to the graph
	g.AddNode("preprocess", preprocessNode)
	g.AddNode("system_prompt", systemPromptNode)
	g.AddNode("tool_calling", toolCallingNode)
	g.AddNode("model", modelNode)
	g.AddNode("postprocess", postprocessNode)

	// Connect nodes
	g.AddEdge("preprocess", "tool_calling")
	g.AddEdge("tool_calling", "model")
	g.AddEdge("system_prompt", "model")
	g.AddEdge("model", "postprocess")

	// Set start and end nodes
	g.SetStartNode("preprocess")
	g.AddEndNode("postprocess")

	return g
}

// WeatherTool is a simple tool for getting weather information
type WeatherTool struct{}

// NewWeatherTool creates a new weather tool
func NewWeatherTool() *WeatherTool {
	return &WeatherTool{}
}

// Name returns the name of the tool
func (t *WeatherTool) Name() string {
	return "get_weather"
}

// Description returns a description of the tool
func (t *WeatherTool) Description() string {
	return "Get the current weather for a location"
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *WeatherTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The city and state/country, e.g. 'San Francisco, CA'",
			},
		},
		"required": []string{"location"},
	}
}

// GetDefinition returns the tool definition
func (t *WeatherTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())
	def.AddParameter("location", &tool.Property{
		Type:        "string",
		Description: "The city and state/country, e.g. 'San Francisco, CA'",
	}, true)
	return def
}

// Execute runs the tool with the given arguments
func (t *WeatherTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	location, ok := args["location"].(string)
	if !ok {
		return nil, fmt.Errorf("location parameter is required")
	}

	// In a real implementation, you would call a weather API here
	// For this example, we'll just return mock data
	weatherData := map[string]interface{}{
		"location":    location,
		"temperature": 22,
		"unit":        "celsius",
		"condition":   "Partly Cloudy",
		"humidity":    65,
		"wind":        "10 km/h",
		"updated_at":  time.Now().Format(time.RFC3339),
	}

	// Log the request
	log.Infof("Weather requested for: %s", location)

	return tool.NewJSONResult(weatherData), nil
}

// ToolCallingNode is a node that handles tool calling
type ToolCallingNode struct {
	name        string
	description string
	tools       []tool.Tool
	toolSet     *tool.ToolSet
}

// NewToolCallingNode creates a new tool calling node
func NewToolCallingNode(tools []tool.Tool) *ToolCallingNode {
	toolSet := tool.NewToolSet()
	for _, t := range tools {
		toolSet.Add(t)
	}

	return &ToolCallingNode{
		name:        "tool_calling_node",
		description: "Handles tool calling in the graph",
		tools:       tools,
		toolSet:     toolSet,
	}
}

// Process implements the Node interface
func (n *ToolCallingNode) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	// Check if the message contains tool calls
	toolCalls, hasCalls := input.GetMetadata("tool_calls")
	if !hasCalls {
		// No tool calls, just pass through the message
		return input, nil
	}

	// Process tool calls if present
	toolCallsData, ok := toolCalls.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid tool_calls format")
	}

	// Create a response message
	responseMsg := message.NewAssistantMessage("")
	responseMsg.SetMetadata("processed_tool_calls", true)

	// Process each tool call
	for _, tc := range toolCallsData {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Extract tool call information
		toolName, _ := tcMap["name"].(string)
		toolID, _ := tcMap["id"].(string)
		toolArgs, _ := tcMap["arguments"].(string)

		// Find the tool
		t, found := n.toolSet.Get(toolName)
		if !found || t == nil {
			responseMsg.SetMetadata("tool_error_"+toolID, fmt.Sprintf("Tool not found: %s", toolName))
			continue
		}

		// Parse arguments
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(toolArgs), &args); err != nil {
			responseMsg.SetMetadata("tool_error_"+toolID, fmt.Sprintf("Invalid arguments: %v", err))
			continue
		}

		// Execute the tool
		result, err := t.Execute(ctx, args)
		if err != nil {
			responseMsg.SetMetadata("tool_error_"+toolID, err.Error())
			continue
		}

		// Store the result
		responseMsg.SetMetadata("tool_result_"+toolID, result)
	}

	return responseMsg, nil
}

// ProcessStream implements the Node interface
func (n *ToolCallingNode) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	go func() {
		defer close(eventCh)

		// Signal stream start with session ID
		sessionID := uuid.New().String()
		startEvent := event.NewStreamStartEvent(sessionID)
		eventCh <- startEvent

		// Forward the input message as an event
		messageEvent := event.NewMessageEvent(input)
		eventCh <- messageEvent

		// Check if the message contains tool calls
		toolCalls, hasCalls := input.GetMetadata("tool_calls")
		if hasCalls {
			// Process tool calls if present
			toolCallsData, ok := toolCalls.([]interface{})
			if ok {
				// Process each tool call
				for _, tc := range toolCallsData {
					tcMap, ok := tc.(map[string]interface{})
					if !ok {
						continue
					}

					// Extract tool call information
					toolName, _ := tcMap["name"].(string)
					toolID, _ := tcMap["id"].(string)
					toolArgs, _ := tcMap["arguments"].(string)

					// Send tool call event
					toolCallEvent := event.NewStreamToolCallEvent(toolName, toolArgs, toolID)
					eventCh <- toolCallEvent

					// Find the tool
					t, found := n.toolSet.Get(toolName)
					if !found || t == nil {
						// Tool not found, send error
						errEvent := event.NewErrorEvent(
							fmt.Errorf("Tool not found: %s", toolName),
							404,
						)
						eventCh <- errEvent
						continue
					}

					// Parse arguments
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(toolArgs), &args); err != nil {
						// Invalid arguments, send error
						errEvent := event.NewErrorEvent(
							fmt.Errorf("Invalid arguments: %v", err),
							400,
						)
						eventCh <- errEvent
						continue
					}

					// Execute the tool
					result, err := t.Execute(ctx, args)
					if err != nil {
						// Tool execution error, send error
						errEvent := event.NewErrorEvent(err, 500)
						eventCh <- errEvent
						continue
					}

					// Send tool result event
					toolResultEvent := event.NewStreamToolResultEvent(
						toolName,
						result.Output,
						nil,
					)
					eventCh <- toolResultEvent
				}
			}
		}

		// Signal stream end with complete text
		endEvent := event.NewStreamEndEvent(input.Content)
		eventCh <- endEvent
	}()

	return eventCh, nil
}

// SupportsStreaming implements the Node interface
func (n *ToolCallingNode) SupportsStreaming() bool {
	return true
}

// Info implements the Node interface
func (n *ToolCallingNode) Info() graph.NodeInfo {
	return graph.NodeInfo{
		Name:        n.name,
		Description: n.description,
		Type:        "tool_calling",
	}
}

// QueryRequest represents a request to the query endpoint
type QueryRequest struct {
	Message string `json:"message"`
}

// QueryResponse represents a response from the query endpoint
type QueryResponse struct {
	Content string `json:"content"`
}

// createQueryHandler creates an HTTP handler for the query endpoint
func createQueryHandler(g *graph.Graph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		// Create context with client IP and timeout
		timeoutDuration := 60 * time.Second
		ctx, cancel := context.WithTimeout(r.Context(), timeoutDuration)
		ctx = context.WithValue(ctx, "client_ip", r.RemoteAddr)
		defer cancel()

		// Create a runner for the graph
		runner, err := graph.NewGraphRunner(g)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create graph runner: %v", err), http.StatusInternalServerError)
			return
		}

		// Create a message from the request
		input := message.NewUserMessage(req.Message)

		// Execute the graph
		output, err := runner.Execute(ctx, input)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to execute graph: %v", err), http.StatusInternalServerError)
			return
		}

		// Create a response
		resp := QueryResponse{
			Content: output.Content,
		}

		// Set content type and write response
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
	}
}

// StreamResponse represents a chunk of a streaming response
type StreamResponse struct {
	Type    string      `json:"type"`
	Content string      `json:"content,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// createStreamHandler creates an HTTP handler for the streaming endpoint
func createStreamHandler(g *graph.Graph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		// Create a background context with timeout instead of using request context
		// This prevents context cancellation when the client disconnects
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ctx = context.WithValue(ctx, "client_ip", r.RemoteAddr)

		// Create a runner for the graph
		runner, err := graph.NewGraphRunner(g)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create graph runner: %v", err), http.StatusInternalServerError)
			return
		}

		// Create a message from the request
		input := message.NewUserMessage(req.Message)

		// Set up streaming response
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Accel-Buffering", "no") // Disable buffering for Nginx

		// Create a flusher for streaming
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		// Send a heartbeat to establish the connection
		if _, err := fmt.Fprintf(w, "data: {\"type\":\"ping\"}\n\n"); err != nil {
			log.Infof("Error sending heartbeat: %v", err)
			return
		}
		flusher.Flush()

		// Execute the graph with streaming
		eventCh, err := runner.ExecuteStream(ctx, input)
		if err != nil {
			// Send error as SSE message
			errData, _ := json.Marshal(StreamResponse{Type: "error", Content: err.Error()})
			fmt.Fprintf(w, "data: %s\n\n", errData)
			flusher.Flush()
			return
		}

		// Create a done channel
		done := make(chan struct{})
		defer close(done)

		// Watch for client disconnection
		go func() {
			select {
			case <-r.Context().Done():
				// Client disconnected
				log.Infof("Client disconnected from streaming response")
				cancel() // Cancel our background context
			case <-done:
				// Streaming complete
				return
			}
		}()

		// Process events
		for evt := range eventCh {
			var resp StreamResponse

			// Convert event to response based on event type
			switch evt.Type {
			case event.TypeStreamStart:
				// Use the direct event type constant
				resp = StreamResponse{Type: "start"}

				// Check for session ID in metadata
				if sessionID, ok := evt.GetMetadata("session_id"); ok {
					resp.Data = map[string]interface{}{"session_id": sessionID}
				}

			case event.TypeStreamEnd:
				// For stream end events, get the complete text from metadata
				if completeText, ok := evt.GetMetadata("complete_text"); ok {
					resp = StreamResponse{Type: "end", Content: fmt.Sprintf("%v", completeText)}
				} else {
					// Fall back to data if metadata is not available
					switch data := evt.Data.(type) {
					case string:
						resp = StreamResponse{Type: "end", Content: data}
					case map[string]interface{}:
						if completeText, ok := data["complete_text"]; ok {
							resp = StreamResponse{Type: "end", Content: fmt.Sprintf("%v", completeText)}
						} else {
							contentBytes, _ := json.Marshal(data)
							resp = StreamResponse{Type: "end", Content: string(contentBytes)}
						}
					default:
						resp = StreamResponse{Type: "end", Content: fmt.Sprintf("%v", data)}
					}
				}

			case event.TypeStreamChunk:
				// For stream chunk events, get content from metadata
				if content, ok := evt.GetMetadata("content"); ok {
					resp = StreamResponse{Type: "chunk", Content: fmt.Sprintf("%v", content)}
				} else if content, ok := evt.Data.(string); ok {
					resp = StreamResponse{Type: "chunk", Content: content}
				} else {
					resp = StreamResponse{Type: "chunk", Content: fmt.Sprintf("%v", evt.Data)}
				}

				// Include sequence number if available
				if sequence, ok := evt.GetMetadata("sequence"); ok {
					if resp.Data == nil {
						resp.Data = make(map[string]interface{})
					}
					if dataMap, ok := resp.Data.(map[string]interface{}); ok {
						dataMap["sequence"] = sequence
					}
				}

			case event.TypeMessage:
				if msg, ok := evt.Data.(*message.Message); ok {
					resp = StreamResponse{
						Type:    "message",
						Content: msg.Content,
						Data: map[string]interface{}{
							"role": msg.Role,
							"id":   msg.ID,
						},
					}
				}

			case event.TypeStreamToolCall:
				// Extract tool call information from event data
				toolData := make(map[string]interface{})

				// Check if data contains tool call info
				if data, ok := evt.Data.(map[string]interface{}); ok {
					if name, ok := data["name"]; ok {
						toolData["name"] = name
					}
					if id, ok := data["id"]; ok {
						toolData["id"] = id
					}
					if args, ok := data["arguments"]; ok {
						toolData["arguments"] = args
					}
				}

				// Also check for name in the Name field
				if evt.Name != "" {
					toolData["name"] = evt.Name
				}

				// Check for ID in the event ID
				if evt.ID != "" {
					toolData["id"] = evt.ID
				}

				resp = StreamResponse{Type: "tool_call", Data: toolData}

			case event.TypeStreamToolResult:
				// Extract tool result information
				if data, ok := evt.Data.(map[string]interface{}); ok {
					resp = StreamResponse{
						Type: "tool_result",
						Data: data,
					}
				}

			case event.TypeError:
				// Handle error events
				var errMsg string
				if errObj, ok := evt.Data.(error); ok {
					errMsg = errObj.Error()
				} else if errStr, ok := evt.GetMetadata("error"); ok {
					errMsg = fmt.Sprintf("%v", errStr)
				} else {
					errMsg = fmt.Sprintf("%v", evt.Data)
				}

				resp = StreamResponse{
					Type:    "error",
					Content: errMsg,
				}

				// Include error code if available
				if errCode, ok := evt.GetMetadata("error_code"); ok {
					if resp.Data == nil {
						resp.Data = make(map[string]interface{})
					}
					if dataMap, ok := resp.Data.(map[string]interface{}); ok {
						dataMap["error_code"] = errCode
					}
				}

			case event.TypeCustom:
				// Handle custom event types
				resp = StreamResponse{
					Type: string(evt.Name),
					Data: evt.Data,
				}

			default:
				// Skip other event types
				continue
			}

			// Encode and send the response
			data, err := json.Marshal(resp)
			if err != nil {
				log.Infof("Failed to encode event: %v", err)
				continue
			}

			// Write the event data
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				log.Infof("Error writing to stream: %v", err)
				return
			}

			// Flush after each message
			flusher.Flush()
		}
	}
}
