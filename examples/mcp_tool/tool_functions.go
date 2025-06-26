package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// File system simulation types and function.
type fileSystemInput struct {
	Operation string `json:"operation"` // "list", "read", "write"
	Path      string `json:"path,omitempty"`
	Content   string `json:"content,omitempty"`
}

type fileSystemOutput struct {
	Success bool     `json:"success"`
	Result  string   `json:"result,omitempty"`
	Files   []string `json:"files,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func simulateFileSystem(input fileSystemInput) fileSystemOutput {
	switch input.Operation {
	case "list":
		if input.Path == "" {
			input.Path = "/"
		}
		return fileSystemOutput{
			Success: true,
			Result:  fmt.Sprintf("Listing contents of %s", input.Path),
			Files:   []string{"document.txt", "data.json", "config.yaml", "logs/", "temp/"},
		}
	case "read":
		if input.Path == "" {
			return fileSystemOutput{
				Success: false,
				Error:   "Path is required for read operation",
			}
		}
		return fileSystemOutput{
			Success: true,
			Result:  fmt.Sprintf("Content of %s: This is simulated file content from MCP tool demo", input.Path),
		}
	case "write":
		if input.Path == "" || input.Content == "" {
			return fileSystemOutput{
				Success: false,
				Error:   "Path and content are required for write operation",
			}
		}
		return fileSystemOutput{
			Success: true,
			Result:  fmt.Sprintf("Successfully wrote %d bytes to %s", len(input.Content), input.Path),
		}
	default:
		return fileSystemOutput{
			Success: false,
			Error:   "Invalid operation. Supported: list, read, write",
		}
	}
}

// Weather function types
type getWeatherInput struct {
	Location string `json:"location"`
}

type getWeatherOutput struct {
	Location    string `json:"location"`
	Temperature string `json:"temperature"`
	Condition   string `json:"condition"`
	Humidity    string `json:"humidity"`
	WindSpeed   string `json:"wind_speed"`
}

func getWeather(input getWeatherInput) getWeatherOutput {
	// Simulate different weather for different cities
	weather := map[string]getWeatherOutput{
		"tokyo": {
			Location:    "Tokyo, Japan",
			Temperature: "22°C",
			Condition:   "Partly Cloudy",
			Humidity:    "65%",
			WindSpeed:   "8 km/h",
		},
		"newyork": {
			Location:    "New York, USA",
			Temperature: "18°C",
			Condition:   "Sunny",
			Humidity:    "45%",
			WindSpeed:   "12 km/h",
		},
		"london": {
			Location:    "London, UK",
			Temperature: "15°C",
			Condition:   "Rainy",
			Humidity:    "80%",
			WindSpeed:   "15 km/h",
		},
	}

	location := strings.ToLower(strings.TrimSpace(input.Location))
	if result, exists := weather[location]; exists {
		return result
	}

	// Default weather for unknown locations
	return getWeatherOutput{
		Location:    input.Location,
		Temperature: "20°C",
		Condition:   "Unknown",
		Humidity:    "50%",
		WindSpeed:   "10 km/h",
	}
}

// Calculator function types
type calculateInput struct {
	Expression string `json:"expression"`
}

type calculateOutput struct {
	Expression string  `json:"expression"`
	Result     float64 `json:"result"`
	Error      string  `json:"error,omitempty"`
}

func calculate(input calculateInput) calculateOutput {
	expr := strings.TrimSpace(input.Expression)
	if expr == "" {
		return calculateOutput{
			Expression: expr,
			Error:      "Expression cannot be empty",
		}
	}

	// Simple expression evaluator for basic operations
	result, err := evaluateExpression(expr)
	if err != nil {
		return calculateOutput{
			Expression: expr,
			Error:      err.Error(),
		}
	}

	return calculateOutput{
		Expression: expr,
		Result:     result,
	}
}

// Simple expression evaluator (supports +, -, *, /)
func evaluateExpression(expr string) (float64, error) {
	// Remove spaces
	expr = strings.ReplaceAll(expr, " ", "")

	// Handle basic operations: 15 * 23 + 7
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid addition expression")
		}
		left, err := evaluateExpression(parts[0])
		if err != nil {
			return 0, err
		}
		right, err := evaluateExpression(parts[1])
		if err != nil {
			return 0, err
		}
		return left + right, nil
	}

	if strings.Contains(expr, "-") && !strings.HasPrefix(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid subtraction expression")
		}
		left, err := evaluateExpression(parts[0])
		if err != nil {
			return 0, err
		}
		right, err := evaluateExpression(parts[1])
		if err != nil {
			return 0, err
		}
		return left - right, nil
	}

	if strings.Contains(expr, "*") {
		parts := strings.Split(expr, "*")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid multiplication expression")
		}
		left, err := evaluateExpression(parts[0])
		if err != nil {
			return 0, err
		}
		right, err := evaluateExpression(parts[1])
		if err != nil {
			return 0, err
		}
		return left * right, nil
	}

	if strings.Contains(expr, "/") {
		parts := strings.Split(expr, "/")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid division expression")
		}
		left, err := evaluateExpression(parts[0])
		if err != nil {
			return 0, err
		}
		right, err := evaluateExpression(parts[1])
		if err != nil {
			return 0, err
		}
		if right == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return left / right, nil
	}

	// Parse as number
	return strconv.ParseFloat(expr, 64)
}

// Time function types
type getCurrentTimeInput struct {
	Format   string `json:"format,omitempty"`   // Optional time format
	Timezone string `json:"timezone,omitempty"` // Optional timezone
}

type getCurrentTimeOutput struct {
	CurrentTime string `json:"current_time"`
	Timezone    string `json:"timezone"`
	Unix        int64  `json:"unix"`
	Format      string `json:"format"`
}

func getCurrentTime(input getCurrentTimeInput) getCurrentTimeOutput {
	now := time.Now()

	// Default format
	format := "2006-01-02 15:04:05"
	if input.Format != "" {
		// Handle common format conversions
		switch input.Format {
		case "YYYY-MM-DD HH:mm:ss":
			format = "2006-01-02 15:04:05"
		case "YYYY-MM-DD":
			format = "2006-01-02"
		case "HH:mm:ss":
			format = "15:04:05"
		case "RFC3339":
			format = time.RFC3339
		default:
			// Try to use the format as-is (assuming it's already in Go format)
			format = input.Format
		}
	}

	// Handle timezone if specified
	timezone := "Local"
	if input.Timezone != "" {
		if loc, err := time.LoadLocation(input.Timezone); err == nil {
			now = now.In(loc)
			timezone = input.Timezone
		}
	}

	return getCurrentTimeOutput{
		CurrentTime: now.Format(format),
		Timezone:    timezone,
		Unix:        now.Unix(),
		Format:      format,
	}
}
