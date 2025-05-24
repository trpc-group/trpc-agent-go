package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/graph"
)

// clientIPKey is a typed context key for the client IP address.
type clientIPKey struct{}

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
	// Create tools
	weatherTool := NewWeatherTool()
	searchTool := NewSearchTool()
	calculatorTool := NewCalculatorTool()
	wordCounterTool := NewWordCounterTool()
	urlShortenerTool := NewURLShortenerTool()
	unitConverterTool := NewUnitConverterTool()
	currencyConverterTool := NewCurrencyConverterTool()
	tools := []tool.Tool{
		weatherTool,
		searchTool,
		calculatorTool,
		wordCounterTool,
		urlShortenerTool,
		unitConverterTool,
		currencyConverterTool,
	}

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
		model.WithOpenAITools(convertToolsToToolDefinitions(tools)),
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
		input.SetMetadata("client_ip", ctx.Value(clientIPKey{}))
		log.Infof("Processing request: %s", input.Content)
		return input, nil
	}).WithInfo("preprocess", "Preprocesses the input message")

	// Create a system prompt node
	systemPromptNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Create a system message with enhanced instructions
		return message.NewSystemMessage(`You are a helpful AI assistant that specializes in providing information about 
		software architecture, design patterns, and general knowledge. You excel at explaining complex concepts in simple terms.
		
		When responding to code-related questions, include brief examples to illustrate your points.
		
		You have access to the following tools:
		1. get_weather - To check weather and forecasts for locations around the world
		2. web_search - To search for up-to-date information on various topics
		3. calculator - To perform arithmetic operations (add, subtract, multiply, divide, power, sqrt)
		4. word_counter - To count words, characters, and sentences in text
		5. url_shortener - To create shortened URLs for long web addresses
		6. unit_converter - To convert between different units of measurement (length, weight, temperature, etc.)
		7. currency_converter - To convert amounts between different currencies
		
		Use these tools when appropriate to provide the most accurate and helpful responses.
		
		Always be concise, accurate, and helpful.`), nil
	}).WithInfo("system_prompt", "Provides system instructions")
	// Create a tool calling node that can use tools
	toolCallingNode := NewToolCallingNode(tools)

	// Create a conversation manager node to maintain history and prepare messages for the model
	conversationManagerNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		log.Infof("Conversation manager node called with input: %v", input)
		// Get conversation from metadata or initialize it
		var conversation []*message.Message
		conversationData, exists := input.GetMetadata("conversation")
		log.Infof("Conversation data: %v", conversationData)
		if exists {
			if conv, ok := conversationData.([]*message.Message); ok {
				conversation = conv
			}
		}

		// If this is a new conversation (no metadata yet), add the system message
		if !exists || len(conversation) == 0 {
			// Get the system message
			systemMsg, err := systemPromptNode.Process(ctx, input)
			if err != nil {
				return nil, fmt.Errorf("failed to get system message: %w", err)
			}
			conversation = append(conversation, systemMsg)
		}

		// Add the current user message if it doesn't already exist in the conversation
		if input.Role == message.RoleUser {
			// Check if the message is already in the conversation to avoid duplicates
			isDuplicate := false
			for _, msg := range conversation {
				if msg.ID == input.ID {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				conversation = append(conversation, input)
			}
		}

		// If the message contains tool results, add those to the conversation
		_, hasResults := input.GetMetadata("processed_tool_calls")
		log.Infof("Has results: %v", hasResults)
		if hasResults {
			// Find all tool results in metadata
			for key, value := range input.Metadata {
				if strings.HasPrefix(key, "tool_result_") {
					toolID := strings.TrimPrefix(key, "tool_result_")
					if result, ok := value.(*tool.Result); ok {
						toolMsg := message.NewAssistantMessage(
							"Tool result: " + result.String() + "\n\n" +
								"Use this tool result to answer the user's question.",
						)
						toolMsg.SetMetadata("tool_id", toolID)
						conversation = append(conversation, toolMsg)
						log.Infof("Tool result for %s: %v", toolID, result)
					}
				}
			}
		}

		// Store conversation in metadata
		input.SetMetadata("conversation", conversation)

		// Pass the full conversation to the model
		inputWithConversation := message.NewUserMessage(input.Content)
		inputWithConversation.SetMetadata("full_messages", conversation)
		inputWithConversation.SetMetadata("conversation", conversation)

		return inputWithConversation, nil
	}).WithInfo("conversation_manager", "Manages conversation history and prepares messages for the model")

	// Create a response selector node to decide if we need to get a final response or continue using tools
	responseSelectorNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		log.Infof("Response selector node called with input: %v", input)
		// Check if the message contains tool calls
		_, hasCalls := input.GetMetadata("tool_calls")

		// Get conversation from metadata
		conversationData, _ := input.GetMetadata("conversation")
		log.Infof("Conversation data: %v", conversationData)
		var conversation []*message.Message
		if conv, ok := conversationData.([]*message.Message); ok {
			conversation = conv
		}

		// Add this assistant message to the conversation if it's not a tool call
		if !hasCalls {
			conversation = append(conversation, input)
			input.SetMetadata("conversation", conversation)
			input.SetMetadata("is_final_response", true)
		} else if hasCalls {
			// Indicate that we need to process tool calls
			input.SetMetadata("needs_tool_calling", true)
		}

		return input, nil
	}).WithInfo("response_selector", "Determines whether to continue with tool calls or produce final response")

	// Create a postprocessing node
	postprocessNode := graph.NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Add a signature to the response
		enhanced := fmt.Sprintf("%s\n\n---\nPowered by %s", input.Content, modelName)
		return message.NewAssistantMessage(enhanced), nil
	}).WithInfo("postprocess", "Adds signature to the response")

	// Create the graph with a more effective structure
	g := graph.NewGraph("enhanced_streaming_graph", "Enhanced graph with proper tool calling loop")

	// Add nodes to the graph
	g.AddNode("preprocess", preprocessNode)
	g.AddNode("conversation_manager", conversationManagerNode)
	g.AddNode("tool_calling", toolCallingNode)
	g.AddNode("model", modelNode)
	g.AddNode("response_selector", responseSelectorNode)
	g.AddNode("postprocess", postprocessNode)

	// Connect nodes
	g.AddEdge("preprocess", "conversation_manager")
	g.AddEdge("conversation_manager", "model")
	g.AddEdge("model", "response_selector")

	// Add conditional edges for tool calling loop
	g.AddConditionalEdge("response_selector", "tool_calling", func(ctx context.Context, msg *message.Message) bool {
		needsTools, _ := msg.GetMetadata("needs_tool_calling")
		return needsTools != nil && needsTools.(bool)
	})

	g.AddEdge("tool_calling", "conversation_manager") // Loop back to add tool results to conversation

	// Only go to postprocess if this is the final response
	g.AddConditionalEdge("response_selector", "postprocess", func(ctx context.Context, msg *message.Message) bool {
		isFinal, _ := msg.GetMetadata("is_final_response")
		return isFinal != nil && isFinal.(bool)
	})

	// Set start and end nodes
	g.SetStartNode("preprocess")
	g.AddEndNode("postprocess")

	return g
}

func convertToolsToToolDefinitions(tools []tool.Tool) []*tool.ToolDefinition {
	toolDefs := make([]*tool.ToolDefinition, len(tools))
	for i, t := range tools {
		toolDefs[i] = t.GetDefinition()
	}
	return toolDefs
}

// WeatherTool is a tool for getting weather information
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
	return `Get the current weather and forecast for a location
	Example json input:
	{
		"location": "San Francisco, CA"
	}
	`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *WeatherTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The city and state/country, e.g. 'San Francisco, CA' or 'Tokyo, Japan'",
			},
			"units": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"metric", "imperial"},
				"description": "Temperature units ('metric' for Celsius, 'imperial' for Fahrenheit)",
				"default":     "metric",
			},
		},
		"required": []string{"location"},
	}
}

// GetDefinition returns the tool definition
func (t *WeatherTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add location parameter
	def.AddParameter("location", &tool.Property{
		Type:        "string",
		Description: "The city and state/country, e.g. 'San Francisco, CA' or 'Tokyo, Japan'",
	}, true)

	// Add units parameter
	def.AddParameter("units", &tool.Property{
		Type:        "string",
		Description: "Temperature units ('metric' for Celsius, 'imperial' for Fahrenheit)",
		Enum:        []interface{}{"metric", "imperial"},
		Default:     "metric",
	}, false)

	return def
}

// Execute simulates getting weather data for a location
func (t *WeatherTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract location
	location, ok := args["location"].(string)
	if !ok || location == "" {
		return nil, fmt.Errorf("location is required and must be a string")
	}

	// Extract units (default to metric)
	units := "metric"
	if unitsArg, ok := args["units"].(string); ok && unitsArg != "" {
		if unitsArg == "imperial" || unitsArg == "metric" {
			units = unitsArg
		}
	}

	// Simulate API call delay
	time.Sleep(300 * time.Millisecond)

	// Generate a deterministic hash from the location to create consistent "random" weather
	h := fnv.New32a()
	h.Write([]byte(location))
	seed := h.Sum32()
	r := rand.New(rand.NewSource(int64(seed)))

	// Generate simulated weather data
	var tempRange, humidity, windSpeed float64
	var condition, tempUnit, windUnit string

	// Set base temperature range and units based on selected unit system
	if units == "metric" {
		tempRange = 35.0 // -5 to 30 Celsius
		tempUnit = "°C"
		windUnit = "km/h"
	} else {
		tempRange = 60.0 // 20 to 80 Fahrenheit
		tempUnit = "°F"
		windUnit = "mph"
	}

	// Calculate temperature using the hash
	baseTemp := float64(seed%100) / 100.0 * tempRange
	if units == "metric" {
		baseTemp -= 5 // -5 to 30 Celsius
	} else {
		baseTemp += 20 // 20 to 80 Fahrenheit
	}

	// Add some daily variation (±3 degrees)
	dailyVariation := (float64(time.Now().Day()%6) - 3.0)
	currentTemp := baseTemp + dailyVariation

	// Round to 1 decimal place
	currentTemp = math.Round(currentTemp*10) / 10

	// Determine condition based on temperature
	// This uses a combination of temperature and hash to determine weather
	conditionSeed := seed % 10
	switch {
	case currentTemp < 0:
		if conditionSeed < 3 {
			condition = "Snowing"
		} else if conditionSeed < 7 {
			condition = "Freezing"
		} else {
			condition = "Overcast"
		}
	case currentTemp < 10 && units == "metric", currentTemp < 50 && units == "imperial":
		if conditionSeed < 3 {
			condition = "Rainy"
		} else if conditionSeed < 6 {
			condition = "Cloudy"
		} else if conditionSeed < 8 {
			condition = "Partly Cloudy"
		} else {
			condition = "Clear"
		}
	default:
		if conditionSeed < 2 {
			condition = "Rainy"
		} else if conditionSeed < 4 {
			condition = "Partly Cloudy"
		} else {
			condition = "Sunny"
		}
	}

	// Generate humidity (30-80%)
	humidity = 30.0 + (float64(seed % 50))
	humidity = math.Round(humidity)

	// Generate wind speed (0-30 km/h or 0-20 mph)
	if units == "metric" {
		windSpeed = float64(r.Intn(300)) / 10.0
	} else {
		windSpeed = float64(r.Intn(200)) / 10.0
	}
	windSpeed = math.Round(windSpeed*10) / 10

	// Generate a 3-day forecast
	forecast := make([]map[string]interface{}, 3)
	for i := 0; i < 3; i++ {
		// Temperature varies by ±5 degrees from current
		forecastTemp := currentTemp + float64(r.Intn(100)-50)/10.0
		forecastTemp = math.Round(forecastTemp*10) / 10

		// Generate conditions with some consistency
		var forecastCondition string
		forecastSeed := (seed + uint32(i)) % 10

		if forecastSeed < 3 {
			forecastCondition = condition // Same as today
		} else {
			// Pick a different condition
			conditions := []string{"Sunny", "Partly Cloudy", "Cloudy", "Rainy"}
			forecastCondition = conditions[forecastSeed%4]
		}

		forecast[i] = map[string]interface{}{
			"day":        time.Now().AddDate(0, 0, i+1).Format("Mon"),
			"temp":       forecastTemp,
			"condition":  forecastCondition,
			"humidity":   math.Round(humidity + float64(r.Intn(20)-10)),
			"wind_speed": math.Round((windSpeed+float64(r.Intn(50)-25)/10.0)*10) / 10,
		}
	}

	// Create the result
	result := map[string]interface{}{
		"location":    location,
		"temperature": currentTemp,
		"temp_unit":   tempUnit,
		"condition":   condition,
		"humidity":    humidity,
		"wind_speed":  windSpeed,
		"wind_unit":   windUnit,
		"updated_at":  time.Now().Format(time.RFC3339),
		"forecast":    forecast,
	}

	return tool.NewJSONResult(result), nil
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
	toolCallsData, ok := toolCalls.([]model.ToolCall)
	if !ok {
		return nil, fmt.Errorf("invalid tool_calls format")
	}

	// Create a response message
	responseMsg := message.NewAssistantMessage("")
	responseMsg.SetMetadata("processed_tool_calls", true)

	// Process each tool call
	for _, tc := range toolCallsData {
		// Extract tool call information
		toolName := tc.Function.Name
		toolID := tc.ID
		toolArgs := tc.Function.Arguments

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
		responseMsg := message.NewAssistantMessage("")
		responseMsg.SetMetadata("processed_tool_calls", true)
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
			toolCallsData, ok := toolCalls.([]model.ToolCall)
			if ok {
				// Process each tool call
				for _, tc := range toolCallsData {
					// Extract tool call information
					toolName := tc.Function.Name
					toolID := tc.ID
					toolArgs := tc.Function.Arguments

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
						responseMsg.SetMetadata("tool_error_"+toolID, fmt.Sprintf("Tool not found: %s", toolName))
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
						responseMsg.SetMetadata("tool_error_"+toolID, fmt.Sprintf("Invalid arguments: %v", err))
						continue
					}

					// Execute the tool
					result, err := t.Execute(ctx, args)
					log.Infof("Tool args: %v, result: %v, error: %v", args, result, err)
					if err != nil {
						// Tool execution error, send error
						errEvent := event.NewErrorEvent(err, 500)
						eventCh <- errEvent
						responseMsg.SetMetadata("tool_error_"+toolID, err.Error())
						continue
					}

					// Send tool result event
					toolResultEvent := event.NewStreamToolResultEvent(
						toolName,
						result.Output,
						nil,
					)
					eventCh <- toolResultEvent
					responseMsg.SetMetadata("tool_result_"+toolID, result)
				}
				conversation, _ := input.GetMetadata("conversation")
				responseMsg.SetMetadata("conversation", conversation)
				eventCh <- event.NewMessageEvent(responseMsg)
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
		ctx = context.WithValue(ctx, clientIPKey{}, r.RemoteAddr)
		defer cancel()

		// Create a runner for the graph
		runner, err := graph.NewGraphRunner(g)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create graph runner: %v", err), http.StatusInternalServerError)
			return
		}

		// Create a message from the request
		input := message.NewUserMessage(req.Message)

		// Execute the graph with loop support
		runOpts := graph.RunOptions{
			MaxIterations:       15, // Allow more iterations for tool calling loops
			EnableLoopDetection: true,
		}
		output, err := runner.Execute(ctx, input, runOpts)
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
		ctx = context.WithValue(ctx, clientIPKey{}, r.RemoteAddr)

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

		// Execute the graph with streaming and explicit loop support
		runOpts := graph.RunOptions{
			MaxIterations:       15, // Allow more iterations for tool calling loops
			EnableLoopDetection: true,
		}
		eventCh, err := runner.ExecuteStream(ctx, input, runOpts)
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

			// 	// Include sequence number if available
			// 	if sequence, ok := evt.GetMetadata("sequence"); ok {
			// 		if resp.Data == nil {
			// 			resp.Data = make(map[string]interface{})
			// 		}
			// 		if dataMap, ok := resp.Data.(map[string]interface{}); ok {
			// 			dataMap["sequence"] = sequence
			// 		}
			// 	}

			// case event.TypeMessage:
			// 	if msg, ok := evt.Data.(*message.Message); ok {
			// 		resp = StreamResponse{
			// 			Type:    "message",
			// 			Content: msg.Content,
			// 			Data: map[string]interface{}{
			// 				"role": msg.Role,
			// 				"id":   msg.ID,
			// 			},
			// 		}
			// 	}

			// case event.TypeStreamToolCall:
			// 	// Extract tool call information from event data
			// 	toolData := make(map[string]interface{})

			// 	// Check if data contains tool call info
			// 	if data, ok := evt.Data.(map[string]interface{}); ok {
			// 		if name, ok := data["name"]; ok {
			// 			toolData["name"] = name
			// 		}
			// 		if id, ok := data["id"]; ok {
			// 			toolData["id"] = id
			// 		}
			// 		if args, ok := data["arguments"]; ok {
			// 			toolData["arguments"] = args
			// 		}
			// 	}

			// 	// Also check for name in the Name field
			// 	if evt.Name != "" {
			// 		toolData["name"] = evt.Name
			// 	}

			// 	// Check for ID in the event ID
			// 	if evt.ID != "" {
			// 		toolData["id"] = evt.ID
			// 	}

			// 	resp = StreamResponse{Type: "tool_call", Data: toolData}

			// case event.TypeStreamToolResult:
			// 	// Extract tool result information
			// 	if data, ok := evt.Data.(map[string]interface{}); ok {
			// 		resp = StreamResponse{
			// 			Type: "tool_result",
			// 			Data: data,
			// 		}
			// 	}

			// case event.TypeError:
			// 	// Handle error events
			// 	var errMsg string
			// 	if errObj, ok := evt.Data.(error); ok {
			// 		errMsg = errObj.Error()
			// 	} else if errStr, ok := evt.GetMetadata("error"); ok {
			// 		errMsg = fmt.Sprintf("%v", errStr)
			// 	} else {
			// 		errMsg = fmt.Sprintf("%v", evt.Data)
			// 	}

			// 	resp = StreamResponse{
			// 		Type:    "error",
			// 		Content: errMsg,
			// 	}

			// 	// Include error code if available
			// 	if errCode, ok := evt.GetMetadata("error_code"); ok {
			// 		if resp.Data == nil {
			// 			resp.Data = make(map[string]interface{})
			// 		}
			// 		if dataMap, ok := resp.Data.(map[string]interface{}); ok {
			// 			dataMap["error_code"] = errCode
			// 		}
			// 	}

			// case event.TypeCustom:
			// 	// Handle custom event types
			// 	resp = StreamResponse{
			// 		Type: string(evt.Name),
			// 		Data: evt.Data,
			// 	}

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

// SearchTool simulates a web search tool
type SearchTool struct{}

// NewSearchTool creates a new search tool
func NewSearchTool() *SearchTool {
	return &SearchTool{}
}

// Name returns the name of the tool
func (t *SearchTool) Name() string {
	return "web_search"
}

// Description returns a description of the tool
func (t *SearchTool) Description() string {
	return `Search the web for information on a topic
	Example json input:
	{
		"query": "What is the capital of France?"
	}
	`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *SearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "The search query",
			},
			"num_results": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results to return (max 5)",
				"default":     3,
			},
		},
		"required": []string{"query"},
	}
}

// GetDefinition returns the tool definition
func (t *SearchTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add query parameter
	def.AddParameter("query", &tool.Property{
		Type:        "string",
		Description: "The search query",
	}, true)

	// Add num_results parameter
	def.AddParameter("num_results", &tool.Property{
		Type:        "integer",
		Description: "Number of results to return (max 5)",
		Default:     3,
	}, false)

	return def
}

// Execute simulates a web search
func (t *SearchTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract query
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required and must be a string")
	}

	// Extract num_results (default to 3)
	numResults := 3
	if numResultsArg, ok := args["num_results"].(float64); ok {
		numResults = int(numResultsArg)
		if numResults < 1 {
			numResults = 1
		} else if numResults > 5 {
			numResults = 5
		}
	}

	// Simulate API call delay
	time.Sleep(500 * time.Millisecond)

	// Generate simulated search results based on the query
	results := generateSearchResults(query, numResults)

	// Create the result
	result := map[string]interface{}{
		"query":    query,
		"results":  results,
		"time":     fmt.Sprintf("%.2f seconds", 0.1+rand.Float64()*0.3),
		"total":    len(results) + rand.Intn(1000),
		"provider": "Simulated Search Engine",
	}

	return tool.NewJSONResult(result), nil
}

// generateSearchResults creates simulated search results for a query
func generateSearchResults(query string, count int) []map[string]interface{} {
	// Common websites for different types of information
	websites := []string{
		"wikipedia.org",
		"github.com",
		"stackoverflow.com",
		"medium.com",
		"dev.to",
		"nytimes.com",
		"bbc.com",
		"cnn.com",
		"techcrunch.com",
		"wired.com",
	}

	// Normalize query by lowercasing
	queryLower := strings.ToLower(query)

	// List of topics to categorize the query
	topics := map[string][]string{
		"programming": {"code", "programming", "language", "software", "developer", "algorithm", "api"},
		"technology":  {"tech", "technology", "computer", "hardware", "smartphone", "gadget", "device"},
		"science":     {"science", "physics", "chemistry", "biology", "research", "study"},
		"health":      {"health", "medical", "doctor", "disease", "symptom", "cure", "treatment"},
		"news":        {"news", "current", "event", "politics", "election", "war", "economy"},
		"history":     {"history", "ancient", "medieval", "century", "war", "civilization"},
		"culture":     {"culture", "art", "music", "movie", "film", "book", "literature"},
	}

	// Determine likely topic
	topic := "general"
	maxMatches := 0
	for t, keywords := range topics {
		matches := 0
		for _, keyword := range keywords {
			if strings.Contains(queryLower, keyword) {
				matches++
			}
		}
		if matches > maxMatches {
			maxMatches = matches
			topic = t
		}
	}

	// Generate consistent search results based on query and topic
	h := fnv.New32a()
	h.Write([]byte(query))
	seed := h.Sum32()
	r := rand.New(rand.NewSource(int64(seed)))

	// Generate results
	results := make([]map[string]interface{}, 0, count)
	for i := 0; i < count; i++ {
		// Choose a website that's somewhat relevant to the topic
		var website string
		switch topic {
		case "programming":
			website = websites[r.Intn(3)] // github, stackoverflow, etc.
		case "news":
			website = websites[5+r.Intn(3)] // news sites
		default:
			website = websites[r.Intn(len(websites))]
		}

		// Generate a title
		words := strings.Fields(query)
		if len(words) > 3 {
			words = words[:3]
		}

		// Add some filler words to make it sound like a title
		fillerStart := []string{
			"Complete Guide to",
			"Understanding",
			"Introduction to",
			"The Ultimate",
			"Everything About",
			"How to",
			"Why",
			"What is",
		}

		fillerEnd := []string{
			"Explained",
			"- A Comprehensive Guide",
			"in 2023",
			"for Beginners",
			"You Need to Know",
			"and How It Works",
		}

		title := fillerStart[r.Intn(len(fillerStart))] + " " + strings.Join(words, " ")
		if r.Intn(2) == 0 {
			title += " " + fillerEnd[r.Intn(len(fillerEnd))]
		}

		// Generate a snippet
		snippet := fmt.Sprintf("This article covers %s in detail. Learn about the most important aspects of %s and how to apply this knowledge in practical situations.",
			query, strings.Join(words, " "))

		// Create URL
		url := fmt.Sprintf("https://www.%s/article/%s",
			website,
			strings.ReplaceAll(strings.ToLower(query), " ", "-"))

		// Create result
		result := map[string]interface{}{
			"title":   title,
			"url":     url,
			"snippet": snippet,
			"date":    time.Now().AddDate(0, -r.Intn(6), -r.Intn(30)).Format("2006-01-02"),
		}

		results = append(results, result)
	}

	return results
}

// CalculatorTool performs basic arithmetic operations
type CalculatorTool struct{}

// NewCalculatorTool creates a new calculator tool
func NewCalculatorTool() *CalculatorTool {
	return &CalculatorTool{}
}

// Name returns the name of the tool
func (t *CalculatorTool) Name() string {
	return "calculator"
}

// Description returns a description of the tool
func (t *CalculatorTool) Description() string {
	return `Perform basic arithmetic operations (add, subtract, multiply, divide)
	Example input :
	{
		"operation": "add",
		"a": 10,
		"b": 5
	}`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *CalculatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "subtract", "multiply", "divide", "power", "sqrt"},
				"description": "The arithmetic operation to perform",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand (or the only operand for sqrt)",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand (not required for sqrt)",
			},
		},
		"required": []string{"operation", "a"},
	}
}

// GetDefinition returns the tool definition
func (t *CalculatorTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add operation parameter
	def.AddParameter("operation", &tool.Property{
		Type:        "string",
		Description: "The arithmetic operation to perform",
		Enum:        []interface{}{"add", "subtract", "multiply", "divide", "power", "sqrt"},
	}, true)

	// Add a parameter
	def.AddParameter("a", &tool.Property{
		Type:        "number",
		Description: "The first operand (or the only operand for sqrt)",
	}, true)

	// Add b parameter
	def.AddParameter("b", &tool.Property{
		Type:        "number",
		Description: "The second operand (not required for sqrt)",
	}, false)

	return def
}

// Execute performs the calculation
func (t *CalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract operation
	operation, ok := args["operation"].(string)
	if !ok || operation == "" {
		return nil, fmt.Errorf("operation is required and must be a string")
	}

	// Extract first operand
	a, ok := args["a"].(float64)
	if !ok {
		return nil, fmt.Errorf("a is required and must be a number")
	}

	// Calculate result based on operation
	var result float64
	var explanation string

	switch operation {
	case "sqrt":
		if a < 0 {
			return nil, fmt.Errorf("cannot calculate square root of negative number")
		}
		result = math.Sqrt(a)
		explanation = fmt.Sprintf("The square root of %.2f is %.4f", a, result)
	case "add", "subtract", "multiply", "divide", "power":
		// Extract second operand for binary operations
		b, ok := args["b"].(float64)
		if !ok {
			return nil, fmt.Errorf("b is required for %s operation", operation)
		}

		switch operation {
		case "add":
			result = a + b
			explanation = fmt.Sprintf("%.2f + %.2f = %.2f", a, b, result)
		case "subtract":
			result = a - b
			explanation = fmt.Sprintf("%.2f - %.2f = %.2f", a, b, result)
		case "multiply":
			result = a * b
			explanation = fmt.Sprintf("%.2f × %.2f = %.2f", a, b, result)
		case "divide":
			if b == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			result = a / b
			explanation = fmt.Sprintf("%.2f ÷ %.2f = %.4f", a, b, result)
		case "power":
			result = math.Pow(a, b)
			explanation = fmt.Sprintf("%.2f raised to the power of %.2f = %.4f", a, b, result)
		}
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	// Create the result with both numerical value and explanation
	output := map[string]interface{}{
		"operation":   operation,
		"result":      result,
		"explanation": explanation,
	}

	return tool.NewJSONResult(output), nil
}

// WordCounterTool counts words, characters and sentences in text
type WordCounterTool struct{}

// NewWordCounterTool creates a new word counter tool
func NewWordCounterTool() *WordCounterTool {
	return &WordCounterTool{}
}

// Name returns the name of the tool
func (t *WordCounterTool) Name() string {
	return "word_counter"
}

// Description returns a description of the tool
func (t *WordCounterTool) Description() string {
	return `Count words, characters, and sentences in text
	Example json input:
	{
		"text": "Hello, world!"
	}
	`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *WordCounterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "The text to analyze",
			},
		},
		"required": []string{"text"},
	}
}

// GetDefinition returns the tool definition
func (t *WordCounterTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add text parameter
	def.AddParameter("text", &tool.Property{
		Type:        "string",
		Description: "The text to analyze",
	}, true)

	return def
}

// Execute counts words, characters, and sentences in the text
func (t *WordCounterTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract text
	text, ok := args["text"].(string)
	if !ok {
		return nil, fmt.Errorf("text is required and must be a string")
	}

	if text == "" {
		return tool.NewJSONResult(map[string]interface{}{
			"words":                0,
			"characters":           0,
			"characters_no_spaces": 0,
			"sentences":            0,
			"paragraphs":           0,
			"lines":                0,
		}), nil
	}

	// Count words
	words := len(strings.Fields(text))

	// Count characters (with and without spaces)
	characters := len(text)
	charactersNoSpaces := len(strings.ReplaceAll(text, " ", ""))

	// Count sentences (approximately by counting periods, exclamation points, and question marks)
	sentenceEndRegex := regexp.MustCompile(`[.!?]+`)
	sentenceMatches := sentenceEndRegex.FindAllString(text, -1)
	sentences := len(sentenceMatches)

	// Count paragraphs (separated by double newlines)
	paragraphRegex := regexp.MustCompile(`\n\s*\n`)
	paragraphs := len(paragraphRegex.FindAllString("\n"+text+"\n", -1)) + 1

	// Count lines
	lines := len(strings.Split(text, "\n"))

	// Create the result
	result := map[string]interface{}{
		"words":                words,
		"characters":           characters,
		"characters_no_spaces": charactersNoSpaces,
		"sentences":            sentences,
		"paragraphs":           paragraphs,
		"lines":                lines,
	}

	return tool.NewJSONResult(result), nil
}

// URLShortenerTool creates shortened URLs (simulated)
type URLShortenerTool struct{}

// NewURLShortenerTool creates a new URL shortener tool
func NewURLShortenerTool() *URLShortenerTool {
	return &URLShortenerTool{}
}

// Name returns the name of the tool
func (t *URLShortenerTool) Name() string {
	return "url_shortener"
}

// Description returns a description of the tool
func (t *URLShortenerTool) Description() string {
	return `Create a shortened URL for a long URL
	Example json input:
	{
		"url": "https://www.google.com"
	}
	`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *URLShortenerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "The long URL to shorten",
			},
		},
		"required": []string{"url"},
	}
}

// GetDefinition returns the tool definition
func (t *URLShortenerTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add url parameter
	def.AddParameter("url", &tool.Property{
		Type:        "string",
		Description: "The long URL to shorten",
	}, true)

	return def
}

// Execute creates a shortened URL
func (t *URLShortenerTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract URL
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("url is required and must be a string")
	}

	// Validate URL (simple check)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("invalid URL. URL must start with http:// or https://")
	}

	// Generate a deterministic short URL based on the hash of the long URL
	h := fnv.New32a()
	h.Write([]byte(url))
	hash := h.Sum32()

	// Convert hash to base62 (alphanumeric) - simplified version
	const chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	shortCode := ""
	hashValue := hash
	for hashValue > 0 || shortCode == "" {
		shortCode = string(chars[hashValue%62]) + shortCode
		hashValue /= 62
	}

	// Use only 6 characters for the short code
	if len(shortCode) > 6 {
		shortCode = shortCode[:6]
	}

	// Create the shortened URL (using a fictional domain)
	shortURL := fmt.Sprintf("https://shrt.ex/%s", shortCode)

	// Create the result
	result := map[string]interface{}{
		"original_url": url,
		"short_url":    shortURL,
		"note":         "This is a simulated URL shortener for demonstration purposes.",
	}

	return tool.NewJSONResult(result), nil
}

// UnitConverterTool converts values between different units
type UnitConverterTool struct{}

// NewUnitConverterTool creates a new unit converter tool
func NewUnitConverterTool() *UnitConverterTool {
	return &UnitConverterTool{}
}

// Name returns the name of the tool
func (t *UnitConverterTool) Name() string {
	return "unit_converter"
}

// Description returns a description of the tool
func (t *UnitConverterTool) Description() string {
	return `Convert values between different units (length, weight, temperature, etc.)
	Example json input:
	{
		"value": 1,
		"from_unit": "km",
		"to_unit": "miles"
	}
	`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *UnitConverterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{
				"type":        "number",
				"description": "The value to convert",
			},
			"from_unit": map[string]interface{}{
				"type":        "string",
				"description": "The source unit (e.g., 'km', 'miles', 'celsius', 'kg', 'lb')",
			},
			"to_unit": map[string]interface{}{
				"type":        "string",
				"description": "The target unit (e.g., 'miles', 'km', 'fahrenheit', 'lb', 'kg')",
			},
		},
		"required": []string{"value", "from_unit", "to_unit"},
	}
}

// GetDefinition returns the tool definition
func (t *UnitConverterTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add value parameter
	def.AddParameter("value", &tool.Property{
		Type:        "number",
		Description: "The value to convert",
	}, true)

	// Add from_unit parameter
	def.AddParameter("from_unit", &tool.Property{
		Type:        "string",
		Description: "The source unit (e.g., 'km', 'miles', 'celsius', 'kg', 'lb')",
	}, true)

	// Add to_unit parameter
	def.AddParameter("to_unit", &tool.Property{
		Type:        "string",
		Description: "The target unit (e.g., 'miles', 'km', 'fahrenheit', 'lb', 'kg')",
	}, true)

	return def
}

// Execute performs the unit conversion
func (t *UnitConverterTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract value
	value, ok := args["value"].(float64)
	if !ok {
		return nil, fmt.Errorf("value is required and must be a number")
	}

	// Extract units
	fromUnit, ok := args["from_unit"].(string)
	if !ok || fromUnit == "" {
		return nil, fmt.Errorf("from_unit is required and must be a string")
	}

	toUnit, ok := args["to_unit"].(string)
	if !ok || toUnit == "" {
		return nil, fmt.Errorf("to_unit is required and must be a string")
	}

	// Normalize unit names
	fromUnit = strings.ToLower(strings.TrimSpace(fromUnit))
	toUnit = strings.ToLower(strings.TrimSpace(toUnit))

	// Define conversion functions for different unit types
	var result float64
	var unitType string
	var explanation string
	var formula string

	// Length conversions
	lengthConversions := map[string]float64{
		"mm":     0.001,
		"cm":     0.01,
		"m":      1.0,
		"km":     1000.0,
		"inch":   0.0254,
		"inches": 0.0254,
		"ft":     0.3048,
		"feet":   0.3048,
		"foot":   0.3048,
		"yd":     0.9144,
		"yard":   0.9144,
		"yards":  0.9144,
		"mi":     1609.344,
		"mile":   1609.344,
		"miles":  1609.344,
	}

	// Weight conversions
	weightConversions := map[string]float64{
		"mg":         0.000001,
		"g":          0.001,
		"gram":       0.001,
		"grams":      0.001,
		"kg":         1.0,
		"kilogram":   1.0,
		"kilograms":  1.0,
		"oz":         0.0283495,
		"ounce":      0.0283495,
		"ounces":     0.0283495,
		"lb":         0.453592,
		"pound":      0.453592,
		"pounds":     0.453592,
		"st":         6.35029,
		"stone":      6.35029,
		"ton":        907.185,
		"tons":       907.185,
		"tonne":      1000.0,
		"tonnes":     1000.0,
		"metric ton": 1000.0,
	}

	// Volume conversions
	volumeConversions := map[string]float64{
		"ml":           0.000001,
		"milliliter":   0.000001,
		"milliliters":  0.000001,
		"l":            0.001,
		"liter":        0.001,
		"liters":       0.001,
		"m3":           1.0,
		"cubic meter":  1.0,
		"cubic meters": 1.0,
		"floz":         0.0000295735,
		"fluid ounce":  0.0000295735,
		"fluid ounces": 0.0000295735,
		"cup":          0.000236588,
		"cups":         0.000236588,
		"pint":         0.000473176,
		"pints":        0.000473176,
		"quart":        0.000946353,
		"quarts":       0.000946353,
		"gallon":       0.00378541,
		"gallons":      0.00378541,
	}

	// Try to convert between temperature units first (special case)
	if (fromUnit == "celsius" || fromUnit == "c" || fromUnit == "°c") &&
		(toUnit == "fahrenheit" || toUnit == "f" || toUnit == "°f") {
		result = (value * 9 / 5) + 32
		unitType = "temperature"
		formula = "°F = (°C × 9/5) + 32"
		explanation = fmt.Sprintf("%.2f°C = %.2f°F", value, result)
	} else if (fromUnit == "fahrenheit" || fromUnit == "f" || fromUnit == "°f") &&
		(toUnit == "celsius" || toUnit == "c" || toUnit == "°c") {
		result = (value - 32) * 5 / 9
		unitType = "temperature"
		formula = "°C = (°F - 32) × 5/9"
		explanation = fmt.Sprintf("%.2f°F = %.2f°C", value, result)
	} else if (fromUnit == "celsius" || fromUnit == "c" || fromUnit == "°c") &&
		(toUnit == "kelvin" || toUnit == "k") {
		result = value + 273.15
		unitType = "temperature"
		formula = "K = °C + 273.15"
		explanation = fmt.Sprintf("%.2f°C = %.2fK", value, result)
	} else if (fromUnit == "kelvin" || fromUnit == "k") &&
		(toUnit == "celsius" || toUnit == "c" || toUnit == "°c") {
		result = value - 273.15
		unitType = "temperature"
		formula = "°C = K - 273.15"
		explanation = fmt.Sprintf("%.2fK = %.2f°C", value, result)
	} else if (fromUnit == "fahrenheit" || fromUnit == "f" || fromUnit == "°f") &&
		(toUnit == "kelvin" || toUnit == "k") {
		result = (value-32)*5/9 + 273.15
		unitType = "temperature"
		formula = "K = (°F - 32) × 5/9 + 273.15"
		explanation = fmt.Sprintf("%.2f°F = %.2fK", value, result)
	} else if (fromUnit == "kelvin" || fromUnit == "k") &&
		(toUnit == "fahrenheit" || toUnit == "f" || toUnit == "°f") {
		result = (value-273.15)*9/5 + 32
		unitType = "temperature"
		formula = "°F = (K - 273.15) × 9/5 + 32"
		explanation = fmt.Sprintf("%.2fK = %.2f°F", value, result)
	} else if fromFactor, fromOk := lengthConversions[fromUnit]; fromOk {
		// Length conversion
		if toFactor, toOk := lengthConversions[toUnit]; toOk {
			result = value * (fromFactor / toFactor)
			unitType = "length"
			formula = fmt.Sprintf("result = value × (%.6f / %.6f)", fromFactor, toFactor)
			explanation = fmt.Sprintf("%.2f %s = %.4f %s", value, fromUnit, result, toUnit)
		} else {
			return nil, fmt.Errorf("cannot convert from length unit '%s' to '%s'", fromUnit, toUnit)
		}
	} else if fromFactor, fromOk := weightConversions[fromUnit]; fromOk {
		// Weight conversion
		if toFactor, toOk := weightConversions[toUnit]; toOk {
			result = value * (fromFactor / toFactor)
			unitType = "weight"
			formula = fmt.Sprintf("result = value × (%.6f / %.6f)", fromFactor, toFactor)
			explanation = fmt.Sprintf("%.2f %s = %.4f %s", value, fromUnit, result, toUnit)
		} else {
			return nil, fmt.Errorf("cannot convert from weight unit '%s' to '%s'", fromUnit, toUnit)
		}
	} else if fromFactor, fromOk := volumeConversions[fromUnit]; fromOk {
		// Volume conversion
		if toFactor, toOk := volumeConversions[toUnit]; toOk {
			result = value * (fromFactor / toFactor)
			unitType = "volume"
			formula = fmt.Sprintf("result = value × (%.6f / %.6f)", fromFactor, toFactor)
			explanation = fmt.Sprintf("%.2f %s = %.4f %s", value, fromUnit, result, toUnit)
		} else {
			return nil, fmt.Errorf("cannot convert from volume unit '%s' to '%s'", fromUnit, toUnit)
		}
	} else {
		return nil, fmt.Errorf("unsupported unit conversion from '%s' to '%s'", fromUnit, toUnit)
	}

	// Create the result
	conversionResult := map[string]interface{}{
		"from_value":  value,
		"from_unit":   fromUnit,
		"to_value":    result,
		"to_unit":     toUnit,
		"unit_type":   unitType,
		"formula":     formula,
		"explanation": explanation,
	}

	return tool.NewJSONResult(conversionResult), nil
}

// CurrencyConverterTool converts between different currencies
type CurrencyConverterTool struct{}

// NewCurrencyConverterTool creates a new currency converter tool
func NewCurrencyConverterTool() *CurrencyConverterTool {
	return &CurrencyConverterTool{}
}

// Name returns the name of the tool
func (t *CurrencyConverterTool) Name() string {
	return "currency_converter"
}

// Description returns a description of the tool
func (t *CurrencyConverterTool) Description() string {
	return `Convert amounts between different currencies
	Example json input:
	{
		"amount": 100,
		"from_currency": "USD",
		"to_currency": "EUR"
	}
	`
}

// Parameters returns the JSON Schema for the tool's parameters
func (t *CurrencyConverterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"amount": map[string]interface{}{
				"type":        "number",
				"description": "The amount to convert",
			},
			"from_currency": map[string]interface{}{
				"type":        "string",
				"description": "The source currency code (e.g., 'USD', 'EUR', 'JPY')",
			},
			"to_currency": map[string]interface{}{
				"type":        "string",
				"description": "The target currency code (e.g., 'EUR', 'USD', 'GBP')",
			},
		},
		"required": []string{"amount", "from_currency", "to_currency"},
	}
}

// GetDefinition returns the tool definition
func (t *CurrencyConverterTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())

	// Add amount parameter
	def.AddParameter("amount", &tool.Property{
		Type:        "number",
		Description: "The amount to convert",
	}, true)

	// Add from_currency parameter
	def.AddParameter("from_currency", &tool.Property{
		Type:        "string",
		Description: "The source currency code (e.g., 'USD', 'EUR', 'JPY')",
	}, true)

	// Add to_currency parameter
	def.AddParameter("to_currency", &tool.Property{
		Type:        "string",
		Description: "The target currency code (e.g., 'EUR', 'USD', 'GBP')",
	}, true)

	return def
}

// Execute performs the currency conversion
func (t *CurrencyConverterTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract amount
	amount, ok := args["amount"].(float64)
	if !ok {
		return nil, fmt.Errorf("amount is required and must be a number")
	}

	// Extract currencies
	fromCurrency, ok := args["from_currency"].(string)
	if !ok || fromCurrency == "" {
		return nil, fmt.Errorf("from_currency is required and must be a string")
	}

	toCurrency, ok := args["to_currency"].(string)
	if !ok || toCurrency == "" {
		return nil, fmt.Errorf("to_currency is required and must be a string")
	}

	// Normalize currency codes
	fromCurrency = strings.ToUpper(strings.TrimSpace(fromCurrency))
	toCurrency = strings.ToUpper(strings.TrimSpace(toCurrency))

	// Simulate API call delay
	time.Sleep(300 * time.Millisecond)

	// Generate a deterministic exchange rate based on the currency pair
	h := fnv.New32a()
	h.Write([]byte(fromCurrency + toCurrency))
	seed := h.Sum32()
	r := rand.New(rand.NewSource(int64(seed)))

	// Define base exchange rates against USD (roughly based on real rates)
	// These are the number of units of currency per 1 USD
	currencyRates := map[string]float64{
		"USD": 1.0,
		"EUR": 0.85,
		"JPY": 110.0,
		"GBP": 0.75,
		"AUD": 1.35,
		"CAD": 1.25,
		"CHF": 0.92,
		"CNY": 6.5,
		"HKD": 7.8,
		"NZD": 1.45,
		"SEK": 8.5,
		"KRW": 1150.0,
		"SGD": 1.35,
		"NOK": 8.7,
		"MXN": 20.0,
		"INR": 75.0,
		"BRL": 5.2,
		"RUB": 75.0,
		"ZAR": 15.0,
		"TRY": 8.5,
	}

	// Add some variability to the rates (±5%)
	for curr, rate := range currencyRates {
		variation := 0.95 + (r.Float64() * 0.1) // 0.95 to 1.05
		currencyRates[curr] = rate * variation
	}

	// Check if currencies are supported
	fromRate, fromOk := currencyRates[fromCurrency]
	toRate, toOk := currencyRates[toCurrency]

	if !fromOk {
		return nil, fmt.Errorf("unsupported source currency: %s", fromCurrency)
	}

	if !toOk {
		return nil, fmt.Errorf("unsupported target currency: %s", toCurrency)
	}

	// Convert to USD first, then to target currency
	amountInUSD := amount / fromRate
	result := amountInUSD * toRate

	// Format with appropriate precision
	var formattedResult string
	if toCurrency == "JPY" || toCurrency == "KRW" || toCurrency == "INR" {
		// Currencies typically shown without decimal places
		formattedResult = fmt.Sprintf("%.0f", result)
	} else {
		// Most currencies shown with 2 decimal places
		formattedResult = fmt.Sprintf("%.2f", result)
	}

	// Calculate the exchange rate
	exchangeRate := toRate / fromRate

	// Create the result
	conversionResult := map[string]interface{}{
		"from_amount":   amount,
		"from_currency": fromCurrency,
		"to_amount":     result,
		"to_currency":   toCurrency,
		"exchange_rate": fmt.Sprintf("1 %s = %.4f %s", fromCurrency, exchangeRate, toCurrency),
		"formatted":     fmt.Sprintf("%.2f %s = %s %s", amount, fromCurrency, formattedResult, toCurrency),
		"updated_at":    time.Now().Format(time.RFC3339),
		"note":          "This is a simulated currency converter using approximate exchange rates for demonstration purposes.",
	}

	return tool.NewJSONResult(conversionResult), nil
}
