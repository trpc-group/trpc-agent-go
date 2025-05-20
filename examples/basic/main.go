package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// AgentRequest represents a request to the agent.
type AgentRequest struct {
	Message string `json:"message"`
}

// AgentResponse represents a response from the agent.
type AgentResponse struct {
	Message   string                   `json:"message"`
	Steps     []map[string]interface{} `json:"steps,omitempty"`
	SessionID string                   `json:"session_id,omitempty"`
}

// SessionData holds conversation data for a session
type SessionData struct {
	Messages []*message.Message
	mutex    sync.Mutex
}

func main() {
	modelName := flag.String("model-name", "gpt-3.5-turbo", "The model to use")
	openaiURL := flag.String("openai-url", "https://api.openai.com/v1", "The OpenAI API URL")
	flag.Parse()
	// Create tools
	calculatorTool := NewCalculatorTool()
	weatherTool := NewWeatherTool()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatalf("No OpenAI API key found")
	}
	llm := models.NewOpenAIModel(
		*modelName,
		models.WithOpenAIAPIKey(apiKey),
		models.WithOpenAIBaseURL(*openaiURL),
	)

	// Set up React agent
	registry := tool.NewRegistry()
	registry.Register(calculatorTool)
	registry.Register(weatherTool)

	// Create session manager
	sessionManager := session.NewMemoryManager(
		session.WithExpiration(24 * time.Hour),
	)

	agentConfig := react.AgentConfig{
		Name:        "Basic Example Agent",
		Description: "A simple agent that can perform calculations and check weather",
		Model:       llm,
		Tools:       []tool.Tool{calculatorTool, weatherTool},
		SystemPrompt: `You are a helpful assistant that can perform calculations and check weather.
You have access to the following tools:
- calculator: Performs basic arithmetic operations
- weather: Gets weather information for a location

Always think carefully about which tool to use based on the user's request.`,
		MaxIterations: 5,
	}

	agent, err := react.NewAgent(agentConfig)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Map to store session data
	sessions := make(map[string]*SessionData)
	var sessionsMutex sync.RWMutex

	// HTTP handler for agent requests
	http.HandleFunc("/api/agent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req AgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// Get or create session
		sessionID := r.Header.Get("X-Session-ID")
		sess, err := sessionManager.Get(r.Context(), sessionID)
		if err != nil {
			log.Errorf("Session error: %v", err)
			http.Error(w, "Session error", http.StatusInternalServerError)
			return
		}

		// Get session data
		sessionsMutex.RLock()
		sessionData, exists := sessions[sess.ID()]
		sessionsMutex.RUnlock()

		if !exists {
			sessionData = &SessionData{
				Messages: make([]*message.Message, 0),
			}
			sessionsMutex.Lock()
			sessions[sess.ID()] = sessionData
			sessionsMutex.Unlock()
		}

		// Create user message
		userMsg := message.NewUserMessage(req.Message)

		// Add message to history and create context for agent
		sessionData.mutex.Lock()
		sessionData.Messages = append(sessionData.Messages, userMsg)
		history := make([]*message.Message, len(sessionData.Messages))
		copy(history, sessionData.Messages)
		sessionData.mutex.Unlock()

		// Create context with history
		ctx := context.Background()

		// Run the agent
		log.Infof("Running agent with message: %s", req.Message)
		resp, err := agent.Run(ctx, userMsg)
		if err != nil {
			log.Errorf("Agent error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Add response to history
		sessionData.mutex.Lock()
		sessionData.Messages = append(sessionData.Messages, message.NewAssistantMessage(resp.Content))
		sessionData.mutex.Unlock()

		// Get reasoning steps if available
		var steps []map[string]interface{}
		cycles, _ := agent.GetHistory(ctx)
		for _, cycle := range cycles {
			step := map[string]interface{}{
				"thought": cycle.Thought.Content,
			}

			if len(cycle.Actions) > 0 {
				actions := make([]map[string]interface{}, 0, len(cycle.Actions))
				for _, action := range cycle.Actions {
					actions = append(actions, map[string]interface{}{
						"tool":  action.ToolName,
						"input": action.ToolInput,
					})
				}
				step["actions"] = actions
			}

			if len(cycle.Observations) > 0 {
				observations := make([]string, 0, len(cycle.Observations))
				for _, obs := range cycle.Observations {
					// Extract the output from the observation's ToolOutput
					var obsText string
					if obs.IsError {
						if errText, ok := obs.ToolOutput["error"].(string); ok {
							obsText = "Error: " + errText
						} else {
							obsText = "Unknown error"
						}
					} else {
						if output, ok := obs.ToolOutput["output"]; ok {
							// Convert output to string
							switch v := output.(type) {
							case string:
								obsText = v
							default:
								jsonData, _ := json.Marshal(output)
								obsText = string(jsonData)
							}
						} else {
							obsText = "No output"
						}
					}
					observations = append(observations, obsText)
				}
				step["observations"] = observations
			}

			steps = append(steps, step)
		}

		// Return response with session ID
		response := AgentResponse{
			Message:   resp.Content,
			Steps:     steps,
			SessionID: sess.ID(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Serve static files
	http.Handle("/", http.FileServer(http.Dir("./examples/basic/static")))

	// Start server
	port := 8080
	log.Infof("Starting server on port %d", port)
	log.Infof("Open http://localhost:%d in your browser", port)
	if err := http.ListenAndServe(":"+strconv.Itoa(port), nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// CalculatorTool is a simple tool for performing basic math operations.
type CalculatorTool struct {
	tool.BaseTool
}

// NewCalculatorTool creates a new calculator tool.
func NewCalculatorTool() *CalculatorTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "subtract", "multiply", "divide"},
				"description": "The arithmetic operation to perform",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand",
			},
		},
		"required": []string{"operation", "a", "b"},
	}

	return &CalculatorTool{
		BaseTool: *tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations like add, subtract, multiply, and divide",
			parameters,
		),
	}
}

// Execute performs the arithmetic operation.
func (t *CalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract operation and operands
	operation, _ := args["operation"].(string)
	a, _ := args["a"].(float64)
	b, _ := args["b"].(float64)

	var result float64

	switch operation {
	case "add":
		result = a + b
	case "subtract":
		result = a - b
	case "multiply":
		result = a * b
	case "divide":
		if b == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		result = a / b
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	return tool.NewResult(result), nil
}

// WeatherTool is a simple tool for getting weather information.
type WeatherTool struct {
	tool.BaseTool
}

// NewWeatherTool creates a new weather tool.
func NewWeatherTool() *WeatherTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The location to get weather for",
			},
		},
		"required": []string{"location"},
	}

	return &WeatherTool{
		BaseTool: *tool.NewBaseTool(
			"weather",
			"Gets weather information for a location",
			parameters,
		),
	}
}

// Execute gets simulated weather information.
func (t *WeatherTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	location, _ := args["location"].(string)

	// Simple hash-based simulation
	var temp int
	for _, c := range location {
		temp += int(c)
	}
	temp = (temp % 35) + 10 // Temperature between 10-45°C

	weatherInfo := map[string]interface{}{
		"location":    location,
		"temperature": temp,
		"unit":        "Celsius",
		"condition":   getWeatherCondition(temp),
	}

	return tool.NewJSONResult(weatherInfo), nil
}

// getWeatherCondition returns a weather condition based on temperature.
func getWeatherCondition(temp int) string {
	if temp < 15 {
		return "Cold"
	} else if temp < 25 {
		return "Mild"
	} else if temp < 35 {
		return "Warm"
	} else {
		return "Hot"
	}
}
