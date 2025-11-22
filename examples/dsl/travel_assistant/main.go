package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Import to register builtin components
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Step 1: Register model
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	// Create model registry
	modelRegistry := registry.NewModelRegistry()

	// Get model name from environment or use default
	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	modelClient := openai.New(
		modelName,
		openai.WithAPIKey(apiKey),
	)
	modelRegistry.MustRegister(modelName, modelClient)
	fmt.Printf("✅ Model registered: %s\n", modelName)

	// Step 1.5: Register custom tools to DefaultToolRegistry
	// Note: Built-in tools (duckduckgo_search) are already auto-registered via import
	registry.DefaultToolRegistry.MustRegister("search_flights", createSearchFlightsTool())
	registry.DefaultToolRegistry.MustRegister("check_flight_status", createCheckFlightStatusTool())
	registry.DefaultToolRegistry.MustRegister("get_destination_info", createGetDestinationInfoTool())
	registry.DefaultToolRegistry.MustRegister("suggest_activities", createSuggestActivitiesTool())
	fmt.Println("✅ Tools registered: search_flights, check_flight_status, get_destination_info, suggest_activities")
	fmt.Println("✅ Built-in tools available: duckduckgo_search (auto-registered)")
	fmt.Println()

	// Step 2: Load workflow from JSON file
	workflowData, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow file: %w", err)
	}

	var workflow dsl.Workflow
	if err := json.Unmarshal(workflowData, &workflow); err != nil {
		return fmt.Errorf("failed to parse workflow: %w", err)
	}
	fmt.Printf("✅ Workflow loaded: %s\n", workflow.Name)

	// Step 3: Compile workflow
	compiler := dsl.NewCompiler(registry.DefaultRegistry).
		WithModelRegistry(modelRegistry).
		WithToolRegistry(registry.DefaultToolRegistry)

	compiledGraph, err := compiler.Compile(&workflow)
	if err != nil {
		return fmt.Errorf("failed to compile workflow: %w", err)
	}
	fmt.Println("✅ Workflow compiled successfully")
	fmt.Println()

	// Step 4: Create GraphAgent and Runner
	graphAgent, err := graphagent.New("travel-assistant", compiledGraph,
		graphagent.WithDescription("Travel assistant with classifier routing"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service
	sessionService := inmemory.NewSessionService()

	// Create runner
	appRunner := runner.NewRunner(
		"travel-assistant-workflow",
		graphAgent,
		runner.WithSessionService(sessionService),
	)
	defer appRunner.Close()

	// Step 5: Test with different queries
	testQueries := []string{
		"I need to book a flight from Beijing to Shanghai on 2025-12-16",
		"Can you help me plan a 5-day trip to Japan?",
		"What's the status of flight CA1234?",
		"I want to create an itinerary for my vacation in Europe",
	}

	userID := "user"

	for i, query := range testQueries {
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Test %d: %s\n", i+1, query)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

		// Execute workflow
		sessionID := fmt.Sprintf("session-%d", i+1)
		if err := executeWorkflow(ctx, appRunner, userID, sessionID, query); err != nil {
			fmt.Printf("❌ Error: %v\n\n", err)
			continue
		}
	}

	return nil
}

// executeWorkflow executes the workflow with the given user input
func executeWorkflow(ctx context.Context, appRunner runner.Runner, userID, sessionID, userInput string) error {
	// Create user message
	message := model.NewUserMessage(userInput)

	// Run the workflow
	eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process events
	var lastResponse string
	var streaming bool

	for ev := range eventChan {
		// Handle errors
		if ev.Error != nil {
			return fmt.Errorf("workflow error: %s", ev.Error.Message)
		}

		// Stream LLM delta content for agents configured with stream=true.
		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]
			if choice.Delta.Content != "" {
				if !streaming {
					fmt.Print("🤖 ")
					streaming = true
				}
				fmt.Print(choice.Delta.Content)
			}
			// When an empty delta arrives after streaming, finish the line.
			if choice.Delta.Content == "" && streaming {
				fmt.Println()
				streaming = false
			}

			// Minimal tool call signal from the event stream (complementing tool logs).
			if len(choice.Delta.ToolCalls) > 0 {
				for _, tc := range choice.Delta.ToolCalls {
					fmt.Printf("\n[TA][tool_call] %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
				}
			}
		}

		// Capture state updates and final response.
		if ev.StateDelta != nil {
			// Log structured output (if classifier produced it).
			if raw, ok := ev.StateDelta["output_parsed"]; ok {
				var parsed map[string]any
				_ = json.Unmarshal(raw, &parsed)
				if cls, _ := parsed["classification"].(string); cls != "" {
					fmt.Printf("[TA][structured_output] classification=%q full=%s\n", cls, string(raw))
				} else {
					fmt.Printf("[TA][structured_output] %s\n", string(raw))
				}
			}

			if lastRespBytes, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var respStr string
				if err := json.Unmarshal(lastRespBytes, &respStr); err == nil {
					lastResponse = respStr
				}
			}
		}
	}

	// Print response
	if lastResponse != "" {
		fmt.Printf("\n🤖 Response:\n%s\n\n", lastResponse)
	} else {
		fmt.Printf("❌ No response generated\n\n")
	}

	return nil
}

// ============================================================================
// Tool Creation Functions
// ============================================================================

// Flight-related tool types and functions

type searchFlightsRequest struct {
	From string `json:"from" jsonschema:"description=Departure city or airport code"`
	To   string `json:"to" jsonschema:"description=Destination city or airport code"`
	Date string `json:"date" jsonschema:"description=Departure date (YYYY-MM-DD format)"`
}

type flightInfo struct {
	FlightNumber string  `json:"flight_number"`
	Airline      string  `json:"airline"`
	Departure    string  `json:"departure"`
	Arrival      string  `json:"arrival"`
	Duration     string  `json:"duration"`
	Price        float64 `json:"price"`
	Available    bool    `json:"available"`
}

type searchFlightsResponse struct {
	From    string       `json:"from"`
	To      string       `json:"to"`
	Date    string       `json:"date"`
	Flights []flightInfo `json:"flights"`
	Count   int          `json:"count"`
}

func searchFlights(_ context.Context, req searchFlightsRequest) (searchFlightsResponse, error) {
	// Mock flight data
	mockFlights := []flightInfo{
		{
			FlightNumber: "CA1234",
			Airline:      "Air China",
			Departure:    "08:00",
			Arrival:      "10:30",
			Duration:     "2h 30m",
			Price:        580.00,
			Available:    true,
		},
		{
			FlightNumber: "MU5678",
			Airline:      "China Eastern",
			Departure:    "10:15",
			Arrival:      "12:45",
			Duration:     "2h 30m",
			Price:        520.00,
			Available:    true,
		},
		{
			FlightNumber: "CZ9012",
			Airline:      "China Southern",
			Departure:    "14:30",
			Arrival:      "17:00",
			Duration:     "2h 30m",
			Price:        650.00,
			Available:    false,
		},
	}

	resp := searchFlightsResponse{
		From:    req.From,
		To:      req.To,
		Date:    req.Date,
		Flights: mockFlights,
		Count:   len(mockFlights),
	}

	fmt.Printf("[TA][tool] search_flights(from=%q,to=%q,date=%q) -> %d flights\n",
			req.From, req.To, req.Date, resp.Count)

	return resp, nil
}

func createSearchFlightsTool() tool.CallableTool {
	return function.NewFunctionTool(
		searchFlights,
		function.WithName("search_flights"),
		function.WithDescription("Search for available flights between two cities on a specific date. "+
			"Returns flight information including flight number, airline, departure/arrival times, duration, and price."),
	)
}

type checkFlightStatusRequest struct {
	FlightNumber string `json:"flight_number" jsonschema:"description=Flight number (e.g., CA1234)"`
}

type flightStatus struct {
	FlightNumber      string `json:"flight_number"`
	Airline           string `json:"airline"`
	Status            string `json:"status"`
	ScheduledDep      string `json:"scheduled_departure"`
	ActualDep         string `json:"actual_departure"`
	ScheduledArr      string `json:"scheduled_arrival"`
	EstimatedArr      string `json:"estimated_arrival"`
	DepartureGate     string `json:"departure_gate"`
	ArrivalGate       string `json:"arrival_gate"`
	DepartureTerminal string `json:"departure_terminal"`
	ArrivalTerminal   string `json:"arrival_terminal"`
}

func checkFlightStatus(_ context.Context, req checkFlightStatusRequest) (flightStatus, error) {
	// Mock flight status data
	flightNumber := strings.ToUpper(req.FlightNumber)

	// Simulate different statuses based on flight number
	var status flightStatus
	switch {
	case strings.Contains(flightNumber, "1234"):
		status = flightStatus{
			FlightNumber:      flightNumber,
			Airline:           "Air China",
			Status:            "On Time",
			ScheduledDep:      time.Now().Add(2 * time.Hour).Format("15:04"),
			ActualDep:         "",
			ScheduledArr:      time.Now().Add(4*time.Hour + 30*time.Minute).Format("15:04"),
			EstimatedArr:      time.Now().Add(4*time.Hour + 30*time.Minute).Format("15:04"),
			DepartureGate:     "A12",
			ArrivalGate:       "B8",
			DepartureTerminal: "T3",
			ArrivalTerminal:   "T2",
		}
	case strings.Contains(flightNumber, "5678"):
		status = flightStatus{
			FlightNumber:      flightNumber,
			Airline:           "China Eastern",
			Status:            "Delayed",
			ScheduledDep:      time.Now().Add(1 * time.Hour).Format("15:04"),
			ActualDep:         "",
			ScheduledArr:      time.Now().Add(3*time.Hour + 30*time.Minute).Format("15:04"),
			EstimatedArr:      time.Now().Add(4 * time.Hour).Format("15:04"),
			DepartureGate:     "C5",
			ArrivalGate:       "A15",
			DepartureTerminal: "T2",
			ArrivalTerminal:   "T3",
		}
	default:
		status = flightStatus{
			FlightNumber:      flightNumber,
			Airline:           "Unknown",
			Status:            "Scheduled",
			ScheduledDep:      time.Now().Add(3 * time.Hour).Format("15:04"),
			ActualDep:         "",
			ScheduledArr:      time.Now().Add(5*time.Hour + 30*time.Minute).Format("15:04"),
			EstimatedArr:      time.Now().Add(5*time.Hour + 30*time.Minute).Format("15:04"),
			DepartureGate:     "B10",
			ArrivalGate:       "C20",
			DepartureTerminal: "T1",
			ArrivalTerminal:   "T2",
		}
	}

	fmt.Printf("[TA][tool] check_flight_status(%q) -> status=%q airline=%q\n",
			req.FlightNumber, status.Status, status.Airline)

	return status, nil
}

func createCheckFlightStatusTool() tool.CallableTool {
	return function.NewFunctionTool(
		checkFlightStatus,
		function.WithName("check_flight_status"),
		function.WithDescription("Check the real-time status of a flight by flight number. "+
			"Returns detailed information including status, scheduled/actual times, gates, and terminals."),
	)
}

// Itinerary-related tool types and functions

type getDestinationInfoRequest struct {
	Destination string `json:"destination" jsonschema:"description=City or country name"`
}

type destinationInfo struct {
	Name        string   `json:"name"`
	Country     string   `json:"country"`
	Description string   `json:"description"`
	BestTime    string   `json:"best_time_to_visit"`
	Climate     string   `json:"climate"`
	Language    []string `json:"languages"`
	Currency    string   `json:"currency"`
	TimeZone    string   `json:"timezone"`
	Highlights  []string `json:"highlights"`
}

func getDestinationInfo(_ context.Context, req getDestinationInfoRequest) (destinationInfo, error) {
	// Mock destination data
	destination := strings.ToLower(req.Destination)

	var info destinationInfo
	switch {
	case strings.Contains(destination, "japan") || strings.Contains(destination, "tokyo"):
		info = destinationInfo{
			Name:        "Japan",
			Country:     "Japan",
			Description: "A fascinating blend of ancient traditions and cutting-edge technology",
			BestTime:    "March-May (Spring) and September-November (Autumn)",
			Climate:     "Temperate with four distinct seasons",
			Language:    []string{"Japanese"},
			Currency:    "Japanese Yen (JPY)",
			TimeZone:    "JST (UTC+9)",
			Highlights: []string{
				"Mount Fuji",
				"Tokyo Skytree",
				"Kyoto Temples",
				"Cherry Blossoms",
				"Traditional Onsen",
				"Sushi and Ramen",
			},
		}
	case strings.Contains(destination, "europe") || strings.Contains(destination, "paris"):
		info = destinationInfo{
			Name:        "Europe",
			Country:     "Multiple Countries",
			Description: "Rich history, diverse cultures, and stunning architecture",
			BestTime:    "April-June and September-October",
			Climate:     "Varies by region - Mediterranean, Continental, Oceanic",
			Language:    []string{"English", "French", "German", "Spanish", "Italian"},
			Currency:    "Euro (EUR) in most countries",
			TimeZone:    "CET/CEST (UTC+1/+2)",
			Highlights: []string{
				"Eiffel Tower (Paris)",
				"Colosseum (Rome)",
				"Big Ben (London)",
				"Sagrada Familia (Barcelona)",
				"Swiss Alps",
				"Greek Islands",
			},
		}
	case strings.Contains(destination, "shanghai") || strings.Contains(destination, "beijing"):
		info = destinationInfo{
			Name:        "China",
			Country:     "China",
			Description: "Ancient civilization with modern megacities",
			BestTime:    "April-May and September-October",
			Climate:     "Varies - subtropical in south, continental in north",
			Language:    []string{"Mandarin Chinese"},
			Currency:    "Chinese Yuan (CNY)",
			TimeZone:    "CST (UTC+8)",
			Highlights: []string{
				"Great Wall",
				"Forbidden City",
				"Terracotta Army",
				"Shanghai Bund",
				"West Lake",
				"Pandas in Chengdu",
			},
		}
	default:
		info = destinationInfo{
			Name:        req.Destination,
			Country:     "Unknown",
			Description: "A wonderful destination waiting to be explored",
			BestTime:    "Year-round",
			Climate:     "Varies",
			Language:    []string{"Local language"},
			Currency:    "Local currency",
			TimeZone:    "Local timezone",
			Highlights: []string{
				"Local attractions",
				"Cultural experiences",
				"Natural beauty",
			},
		}
	}

	fmt.Printf("[TA][tool] get_destination_info(%q) -> name=%q highlights=%d\n",
			req.Destination, info.Name, len(info.Highlights))

	return info, nil
}

func createGetDestinationInfoTool() tool.CallableTool {
	return function.NewFunctionTool(
		getDestinationInfo,
		function.WithName("get_destination_info"),
		function.WithDescription("Get detailed information about a travel destination including "+
			"best time to visit, climate, language, currency, and main highlights."),
	)
}

type suggestActivitiesRequest struct {
	Destination string `json:"destination" jsonschema:"description=City or country name"`
	Days        int    `json:"days" jsonschema:"description=Number of days for the trip"`
	Interests   string `json:"interests,omitempty" jsonschema:"description=User interests (e.g., culture, food, adventure, relaxation)"`
}

type activity struct {
	Day         int    `json:"day"`
	Time        string `json:"time"`
	Activity    string `json:"activity"`
	Location    string `json:"location"`
	Duration    string `json:"duration"`
	Description string `json:"description"`
	Cost        string `json:"estimated_cost"`
}

type suggestActivitiesResponse struct {
	Destination string     `json:"destination"`
	Days        int        `json:"days"`
	Activities  []activity `json:"activities"`
	TotalCost   string     `json:"total_estimated_cost"`
}

func suggestActivities(_ context.Context, req suggestActivitiesRequest) (suggestActivitiesResponse, error) {
	// Mock activity suggestions
	destination := strings.ToLower(req.Destination)
	days := req.Days
	if days > 5 {
		days = 5 // Limit to 5 days for mock data
	}

	var activities []activity

	switch {
	case strings.Contains(destination, "japan") || strings.Contains(destination, "tokyo"):
		activities = []activity{
			{Day: 1, Time: "09:00", Activity: "Visit Senso-ji Temple", Location: "Asakusa", Duration: "2 hours", Description: "Tokyo's oldest temple with traditional atmosphere", Cost: "Free"},
			{Day: 1, Time: "14:00", Activity: "Explore Shibuya Crossing", Location: "Shibuya", Duration: "2 hours", Description: "World's busiest pedestrian crossing and shopping", Cost: "¥2,000"},
			{Day: 2, Time: "08:00", Activity: "Day trip to Mount Fuji", Location: "Fuji Five Lakes", Duration: "Full day", Description: "Scenic views and nature experience", Cost: "¥8,000"},
			{Day: 3, Time: "10:00", Activity: "Tsukiji Outer Market", Location: "Tsukiji", Duration: "3 hours", Description: "Fresh sushi and seafood experience", Cost: "¥3,000"},
			{Day: 3, Time: "15:00", Activity: "Tokyo Skytree", Location: "Sumida", Duration: "2 hours", Description: "Panoramic city views from 450m", Cost: "¥3,000"},
		}
	case strings.Contains(destination, "europe") || strings.Contains(destination, "paris"):
		activities = []activity{
			{Day: 1, Time: "09:00", Activity: "Eiffel Tower Visit", Location: "Champ de Mars", Duration: "3 hours", Description: "Iconic landmark with city views", Cost: "€26"},
			{Day: 1, Time: "14:00", Activity: "Louvre Museum", Location: "1st Arrondissement", Duration: "4 hours", Description: "World's largest art museum", Cost: "€17"},
			{Day: 2, Time: "10:00", Activity: "Notre-Dame Cathedral", Location: "Île de la Cité", Duration: "2 hours", Description: "Gothic architecture masterpiece", Cost: "Free"},
			{Day: 2, Time: "15:00", Activity: "Seine River Cruise", Location: "Various docks", Duration: "1.5 hours", Description: "Romantic boat tour", Cost: "€15"},
			{Day: 3, Time: "09:00", Activity: "Versailles Palace", Location: "Versailles", Duration: "Full day", Description: "Royal palace and gardens", Cost: "€27"},
		}
	default:
		activities = []activity{
			{Day: 1, Time: "09:00", Activity: "City Walking Tour", Location: "City Center", Duration: "3 hours", Description: "Explore main attractions", Cost: "$30"},
			{Day: 1, Time: "14:00", Activity: "Local Museum Visit", Location: "Downtown", Duration: "2 hours", Description: "Learn about local history", Cost: "$15"},
			{Day: 2, Time: "10:00", Activity: "Cultural Experience", Location: "Cultural District", Duration: "4 hours", Description: "Immerse in local culture", Cost: "$40"},
		}
	}

	// Limit activities to requested days
	filteredActivities := []activity{}
	for _, act := range activities {
		if act.Day <= days {
			filteredActivities = append(filteredActivities, act)
		}
	}

	resp := suggestActivitiesResponse{
		Destination: req.Destination,
		Days:        days,
		Activities:  filteredActivities,
		TotalCost:   "Varies by choices",
	}

	fmt.Printf("[TA][tool] suggest_activities(dest=%q,days=%d,interests=%q) -> %d activities\n",
			req.Destination, req.Days, req.Interests, len(resp.Activities))

	return resp, nil
}

func createSuggestActivitiesTool() tool.CallableTool {
	return function.NewFunctionTool(
		suggestActivities,
		function.WithName("suggest_activities"),
		function.WithDescription("Suggest activities and create a day-by-day itinerary for a destination. "+
			"Provides activity details including time, location, duration, description, and estimated cost."),
	)
}
