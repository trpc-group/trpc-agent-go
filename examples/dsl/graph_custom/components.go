package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// State keys
const (
	stateKeyDocumentLength  = "document_length"
	stateKeyWordCount       = "word_count"
	stateKeyComplexityLevel = "complexity_level"
	stateKeyProcessingStage = "processing_stage"
	stateKeyOriginalText    = "original_text"
)

// Complexity levels
const (
	complexitySimple   = "simple"
	complexityModerate = "moderate"
	complexityComplex  = "complex"
)

// PreprocessDocumentComponent preprocesses the input document
type PreprocessDocumentComponent struct{}

func (c *PreprocessDocumentComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "custom.preprocess_document",
		DisplayName: "Preprocess Document",
		Description: "Preprocesses the input document and extracts metadata",
		Category:    "Document Processing",
		Meta: map[string]any{
			"icon":  "📄",
			"color": "#3B82F6",
		},
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyUserInput,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Input document text",
				Required:    true,
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        stateKeyDocumentLength,
				Type:        "int",
				GoType:      reflect.TypeOf(0),
				Description: "Document length in characters",
			},
			{
				Name:        stateKeyWordCount,
				Type:        "int",
				GoType:      reflect.TypeOf(0),
				Description: "Word count",
			},
			{
				Name:        stateKeyOriginalText,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Original text",
			},
			{
				Name:        stateKeyProcessingStage,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Current processing stage",
			},
		},
	}
}

func (c *PreprocessDocumentComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Get input from state
	var input string
	if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
		input = userInput
	}
	if input == "" {
		return nil, errors.New("no input document found")
	}

	// Basic preprocessing
	input = strings.TrimSpace(input)
	if len(input) < 10 {
		return nil, errors.New("document too short for processing (minimum 10 characters)")
	}

	// Create initial message with the document
	userMessage := model.NewUserMessage(input)

	wordCount := len(strings.Fields(input))

	// Return state with preprocessing results
	return graph.State{
		graph.StateKeyMessages:  []model.Message{userMessage},
		stateKeyDocumentLength:  len(input),
		stateKeyWordCount:       wordCount,
		stateKeyOriginalText:    input,
		stateKeyProcessingStage: "preprocessing",
	}, nil
}

// RouteComplexityComponent prepares state for complexity routing
type RouteComplexityComponent struct{}

func (c *RouteComplexityComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "custom.route_complexity",
		DisplayName: "Route Complexity",
		Description: "Prepares state for complexity-based routing",
		Category:    "Document Processing",
		Meta: map[string]any{
			"icon":  "🔀",
			"color": "#8B5CF6",
		},
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        stateKeyOriginalText,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Original document text",
			},
			{
				Name:        graph.StateKeyUserInput,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "User input",
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyUserInput,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Updated user input",
			},
			{
				Name:        stateKeyProcessingStage,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Current processing stage",
			},
			{
				Name:        stateKeyComplexityLevel,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Complexity level (simple/moderate/complex)",
			},
		},
	}
}

func (c *RouteComplexityComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	var level string

	// 1) Prefer tool-derived result when present (most reliable)
	if msgs, ok := state[graph.StateKeyMessages].([]model.Message); ok {
		for i := len(msgs) - 1; i >= 0; i-- { // scan backwards for latest tool result
			msg := msgs[i]
			if msg.Role != model.RoleTool {
				continue
			}
			var result complexityResult
			if err := json.Unmarshal([]byte(msg.Content), &result); err == nil {
				switch strings.ToLower(strings.TrimSpace(result.Level)) {
				case complexityComplex:
					level = complexityComplex
				case complexityModerate:
					level = complexityModerate
				case complexitySimple:
					level = complexitySimple
				}
				if level != "" {
					break
				}
			}
		}
	}

	// 2) Try to parse the LLM textual response robustly
	if level == "" {
		if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok {
			normalized := strings.ToLower(strings.TrimSpace(lastResponse))
			// Try exact token match first
			switch normalized {
			case complexitySimple:
				level = complexitySimple
			case complexityModerate:
				level = complexityModerate
			case complexityComplex:
				level = complexityComplex
			}
			if level == "" {
				// Fallback: contains any
				if strings.Contains(normalized, "complex") {
					level = complexityComplex
				} else if strings.Contains(normalized, "moderate") {
					level = complexityModerate
				} else if strings.Contains(normalized, "simple") {
					level = complexitySimple
				}
			}
		}
	}

	// 3) Final fallback: heuristic on word count
	if level == "" {
		const complexityThreshold = 200
		if wordCount, ok := state[stateKeyWordCount].(int); ok {
			if wordCount > complexityThreshold {
				level = complexityComplex
			} else if wordCount > 50 {
				level = complexityModerate
			} else {
				level = complexitySimple
			}
		} else {
			level = complexitySimple
		}
	}

	// Prefer to pass original text directly to downstream nodes
	var newInput string
	if orig, ok := state[stateKeyOriginalText].(string); ok && strings.TrimSpace(orig) != "" {
		newInput = orig
	} else if in, ok := state[graph.StateKeyUserInput].(string); ok {
		newInput = in
	}

	out := graph.State{
		stateKeyProcessingStage: "complexity_routing",
		stateKeyComplexityLevel: level, // Set complexity level here!
	}
	if newInput != "" {
		out[graph.StateKeyUserInput] = newInput
	}
	return out, nil
}

// ComplexityConditionComponent determines routing based on complexity
type ComplexityConditionComponent struct{}

func (c *ComplexityConditionComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "custom.complexity_condition",
		DisplayName: "Complexity Condition",
		Description: "Determines routing based on document complexity",
		Category:    "Document Processing",
		Meta: map[string]any{
			"icon":  "🎯",
			"color": "#F59E0B",
		},
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyMessages,
				Type:        "[]model.Message",
				GoType:      reflect.TypeOf([]model.Message{}),
				Description: "Message history",
			},
			{
				Name:        graph.StateKeyLastResponse,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Last LLM response",
			},
			{
				Name:        stateKeyWordCount,
				Type:        "int",
				GoType:      reflect.TypeOf(0),
				Description: "Word count",
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "route",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Routing decision (simple/moderate/complex)",
			},
			{
				Name:        stateKeyComplexityLevel,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Complexity level",
			},
		},
	}
}

func (c *ComplexityConditionComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Read complexity_level from state (should be set by route_complexity node)
	level, ok := state[stateKeyComplexityLevel].(string)
	if !ok || level == "" {
		// Fallback: default to simple
		level = complexitySimple
	}

	// Return routing decision
	return graph.State{"route": level}, nil
}

// complexityResult is the result from the analyze_complexity tool
type complexityResult struct {
	Level         string  `json:"level"`
	Score         float64 `json:"score"`
	WordCount     int     `json:"word_count"`
	SentenceCount int     `json:"sentence_count"`
}

// FormatOutputComponent formats the final output
type FormatOutputComponent struct{}

func (c *FormatOutputComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "custom.format_output",
		DisplayName: "Format Output",
		Description: "Formats the final processing output",
		Category:    "Document Processing",
		Meta: map[string]any{
			"icon":  "📋",
			"color": "#10B981",
		},
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyLastResponse,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Last response from LLM",
			},
			{
				Name:        stateKeyComplexityLevel,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Complexity level",
			},
			{
				Name:        stateKeyWordCount,
				Type:        "int",
				GoType:      reflect.TypeOf(0),
				Description: "Word count",
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        graph.StateKeyLastResponse,
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Formatted output",
			},
		},
	}
}

func (c *FormatOutputComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Try last_response first
	content, ok := state[graph.StateKeyLastResponse].(string)
	if !ok || strings.TrimSpace(content) == "" {
		content = "(No content produced by the workflow)"
	}

	// Create final formatted output
	complexityLevel, _ := state[stateKeyComplexityLevel].(string)
	wordCount, _ := state[stateKeyWordCount].(int)

	finalOutput := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                    DOCUMENT PROCESSING RESULTS                   ║
╚══════════════════════════════════════════════════════════════════╝

%s

╔══════════════════════════════════════════════════════════════════╗
║                         PROCESSING DETAILS                       ║
╚══════════════════════════════════════════════════════════════════╝

📊 Processing Statistics:
   • Complexity Level: %s
   • Word Count: %d

✅ Processing completed successfully!
`,
		content,
		complexityLevel,
		wordCount)

	return graph.State{
		graph.StateKeyLastResponse: finalOutput,
	}, nil
}

// AnalyzeComplexityToolComponent wraps the analyze_complexity tool
type AnalyzeComplexityToolComponent struct{}

func (c *AnalyzeComplexityToolComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "custom.analyze_complexity_tool",
		DisplayName: "Analyze Complexity Tool",
		Description: "Analyzes document complexity (used as a tool)",
		Category:    "Document Processing",
		Meta: map[string]any{
			"icon":  "🔍",
			"color": "#EF4444",
		},
		Version:     "1.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        "text",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Document text to analyze",
				Required:    true,
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "result",
				Type:        "complexityResult",
				GoType:      reflect.TypeOf(complexityResult{}),
				Description: "Complexity analysis result",
			},
		},
	}
}

func (c *AnalyzeComplexityToolComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// This component is not meant to be executed directly in the graph
	// It's used as a tool function
	return nil, errors.New("AnalyzeComplexityToolComponent should not be executed directly")
}

// AnalyzeComplexity is the actual tool function
func AnalyzeComplexity(ctx context.Context, args complexityArgs) (complexityResult, error) {
	text := args.Text

	// Simple complexity analysis
	wordCount := len(strings.Fields(text))
	sentenceCount := strings.Count(text, ".") + strings.Count(text, "!") + strings.Count(text, "?")

	var level string
	var score float64

	if wordCount < 50 {
		level = complexitySimple
		score = 0.3
	} else if wordCount < 200 {
		level = complexityModerate
		score = 0.6
	} else {
		level = complexityComplex
		score = 0.9
	}

	return complexityResult{
		Level:         level,
		Score:         score,
		WordCount:     wordCount,
		SentenceCount: sentenceCount,
	}, nil
}

// complexityArgs is the input for the analyze_complexity tool
type complexityArgs struct {
	Text string `json:"text" jsonschema:"description=Document text to analyze,required"`
}

// CreateAnalyzeComplexityTool creates the analyze_complexity tool instance.
// This function should be called during application initialization to register the tool.
func CreateAnalyzeComplexityTool() tool.Tool {
	return function.NewFunctionTool(
		AnalyzeComplexity,
		function.WithName("analyze_complexity"),
		function.WithDescription("Analyzes document complexity level"),
	)
}

// CreateModel creates and configures the LLM model instance.
// This function should be called during application initialization to create the model.
func CreateModel(modelName, baseURL, apiKey string) model.Model {
	return openai.New(modelName,
		openai.WithBaseURL(baseURL),
		openai.WithAPIKey(apiKey),
	)
}
