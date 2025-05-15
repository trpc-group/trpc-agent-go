package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ContextAwareToolSelector is an advanced action selector that analyzes reasoning context
// to intelligently select tools and their parameters. This selector goes beyond simple
// pattern matching by considering:
// 1. The specific reasoning in the thought
// 2. The context from previous thoughts and observations
// 3. The agent's working memory (if available)
// 4. The capabilities of each available tool
type ContextAwareToolSelector struct {
	model           model.Model
	contextWindow   int
	defaultStrategy ToolSelectionStrategy
	memory          ReactWorkingMemory
}

// ToolSelectionStrategy defines the approach for tool selection.
type ToolSelectionStrategy string

const (
	// SelectMostRelevant chooses the single most relevant tool.
	SelectMostRelevant ToolSelectionStrategy = "most_relevant"

	// RankByRelevance ranks tools by relevance and selects from top candidates.
	RankByRelevance ToolSelectionStrategy = "rank_by_relevance"

	// PlanBasedSelection selects tools based on the current step in a multi-step plan.
	PlanBasedSelection ToolSelectionStrategy = "plan_based"
)

// ContextAwareToolSelectorConfig holds configuration options for the selector.
type ContextAwareToolSelectorConfig struct {
	// Model is the LLM used for reasoning.
	Model model.Model

	// ContextWindow is the number of previous cycles to include in context.
	ContextWindow int

	// SelectionStrategy determines how tools are selected.
	SelectionStrategy ToolSelectionStrategy

	// Memory provides context from working memory (optional).
	Memory ReactWorkingMemory
}

// NewContextAwareToolSelector creates a new context-aware tool selector.
func NewContextAwareToolSelector(config ContextAwareToolSelectorConfig) *ContextAwareToolSelector {
	if config.ContextWindow <= 0 {
		config.ContextWindow = 3 // Default to last 3 cycles
	}

	if config.SelectionStrategy == "" {
		config.SelectionStrategy = SelectMostRelevant
	}

	return &ContextAwareToolSelector{
		model:           config.Model,
		contextWindow:   config.ContextWindow,
		defaultStrategy: config.SelectionStrategy,
		memory:          config.Memory,
	}
}

// Select selects an action based on the thought with contextual awareness.
func (s *ContextAwareToolSelector) Select(
	ctx context.Context,
	thought *Thought,
	tools []tool.Tool,
) (*Action, error) {
	if s.model == nil {
		return nil, fmt.Errorf("model is required for ContextAwareToolSelector")
	}

	if thought == nil {
		return nil, fmt.Errorf("thought cannot be nil")
	}

	if len(tools) == 0 {
		return nil, fmt.Errorf("at least one tool is required")
	}

	// Get previous cycles from context
	var previousCycles []*Cycle
	cycleManager, ok := GetCycleManager(ctx)
	if ok {
		cycles, _ := cycleManager.GetHistory(ctx)
		previousCycles = cycles
	}

	// Build a context-aware prompt for tool selection
	prompt := s.buildContextAwarePrompt(thought, tools, previousCycles)
	promptMsg := message.NewSystemMessage(prompt)

	// Create a user message with the thought content
	thoughtMsg := message.NewUserMessage(thought.Content)

	// Generate the tool selection using the model
	opts := model.DefaultOptions()
	response, err := s.model.GenerateWithMessages(ctx, []*message.Message{promptMsg, thoughtMsg}, opts)
	if err != nil {
		return nil, fmt.Errorf("context-aware tool selection failed: %w", err)
	}

	actionText := ""
	if len(response.Messages) > 0 {
		actionText = response.Messages[0].Content
	} else if response.Text != "" {
		actionText = response.Text
	} else {
		return nil, fmt.Errorf("model returned empty response")
	}

	// Extract JSON from the response
	actionJSON := extractJSON(actionText)
	if actionJSON == "" {
		return nil, fmt.Errorf("no valid JSON found in model response")
	}

	// Parse the JSON
	var actionData struct {
		ToolName    string                 `json:"tool_name"`
		ToolInput   map[string]interface{} `json:"tool_input"`
		Explanation string                 `json:"explanation,omitempty"`
	}
	if err := json.Unmarshal([]byte(actionJSON), &actionData); err != nil {
		return nil, fmt.Errorf("failed to parse action JSON: %w", err)
	}

	// Validate the tool name
	var validTool bool
	var selectedTool tool.Tool
	for _, t := range tools {
		if t.Name() == actionData.ToolName {
			validTool = true
			selectedTool = t
			break
		}
	}
	if !validTool {
		return nil, fmt.Errorf("selected tool '%s' is not available", actionData.ToolName)
	}

	// Store the context in working memory if available
	if s.memory != nil {
		s.storeToolSelectionContext(ctx, thought, selectedTool, actionData.ToolInput, actionData.Explanation)
	}

	// Create the action
	action := &Action{
		ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
		ThoughtID: thought.ID,
		ToolName:  actionData.ToolName,
		ToolInput: actionData.ToolInput,
		Timestamp: time.Now().Unix(),
	}

	return action, nil
}

// buildContextAwarePrompt creates a context-rich prompt for tool selection.
func (s *ContextAwareToolSelector) buildContextAwarePrompt(
	thought *Thought,
	tools []tool.Tool,
	previousCycles []*Cycle,
) string {
	var prompt strings.Builder

	prompt.WriteString("You are an intelligent tool selector for a ReAct agent. Your task is to analyze the agent's reasoning and select the most appropriate tool and parameters based on context.\n\n")

	// Include available tools with detailed information
	prompt.WriteString("## Available Tools\n\n")
	for _, t := range tools {
		prompt.WriteString(fmt.Sprintf("### %s\n", t.Name()))
		prompt.WriteString(fmt.Sprintf("Description: %s\n", t.Description()))

		// Add parameter information
		params := t.Parameters()
		if len(params) > 0 {
			prompt.WriteString("Parameters:\n")
			paramsJSON, err := json.MarshalIndent(params, "", "  ")
			if err == nil {
				prompt.WriteString("```json\n")
				prompt.WriteString(string(paramsJSON))
				prompt.WriteString("\n```\n")
			}
		}
		prompt.WriteString("\n")
	}

	// Add context from previous cycles, limited by context window
	if len(previousCycles) > 0 {
		prompt.WriteString("## Recent Context\n\n")

		// Limit to the configured context window
		startIdx := 0
		if len(previousCycles) > s.contextWindow {
			startIdx = len(previousCycles) - s.contextWindow
		}

		for i := startIdx; i < len(previousCycles); i++ {
			cycle := previousCycles[i]

			if cycle.Thought != nil {
				prompt.WriteString(fmt.Sprintf("Thought: %s\n\n", cycle.Thought.Content))
			}

			if cycle.Action != nil {
				prompt.WriteString(fmt.Sprintf("Tool Selected: %s\n", cycle.Action.ToolName))
				inputJSON, err := json.MarshalIndent(cycle.Action.ToolInput, "", "  ")
				if err == nil {
					prompt.WriteString(fmt.Sprintf("Parameters: %s\n", string(inputJSON)))
				}
			}

			if cycle.Observation != nil {
				if cycle.Observation.IsError {
					prompt.WriteString(fmt.Sprintf("Error: %s\n", getErrorMessage(cycle.Observation)))
				} else {
					prompt.WriteString(fmt.Sprintf("Result: %s\n", getSuccessMessage(cycle.Observation)))
				}
			}

			prompt.WriteString("\n---\n\n")
		}
	}

	// Add working memory context if available
	if s.memory != nil {
		memoryContext := s.memory.GetContext(context.Background())
		if memoryContext != "" && memoryContext != "No context available." {
			prompt.WriteString("## Working Memory Context\n\n")
			prompt.WriteString(memoryContext)
			prompt.WriteString("\n\n")
		}
	}

	// Instructions for the response format
	prompt.WriteString("## Tool Selection Instructions\n\n")
	prompt.WriteString("1. Analyze the agent's current reasoning provided in the user message.\n")
	prompt.WriteString("2. Consider the recent context and working memory.\n")
	prompt.WriteString("3. Select the most appropriate tool and parameters.\n")
	prompt.WriteString("4. Provide a brief explanation of your selection.\n\n")

	prompt.WriteString("Respond with a JSON object in the following format:\n\n")
	prompt.WriteString("```json\n")
	prompt.WriteString("{\n")
	prompt.WriteString("  \"tool_name\": \"name_of_selected_tool\",\n")
	prompt.WriteString("  \"tool_input\": {\n")
	prompt.WriteString("    \"parameter1\": \"value1\",\n")
	prompt.WriteString("    \"parameter2\": 42\n")
	prompt.WriteString("  },\n")
	prompt.WriteString("  \"explanation\": \"Brief explanation of why this tool was selected\"\n")
	prompt.WriteString("}\n")
	prompt.WriteString("```\n")

	return prompt.String()
}

// storeToolSelectionContext stores the tool selection context in working memory.
func (s *ContextAwareToolSelector) storeToolSelectionContext(
	ctx context.Context,
	thought *Thought,
	selectedTool tool.Tool,
	toolInput map[string]interface{},
	explanation string,
) {
	// Skip if memory is not available
	if s.memory == nil {
		return
	}

	// Create the memory item
	toolSelectionItem := &WorkingMemoryItem{
		ID:        fmt.Sprintf("tool-selection-%d", time.Now().UnixNano()),
		Type:      "tool_selection",
		Name:      fmt.Sprintf("Selection of %s", selectedTool.Name()),
		Content:   toolInput,
		CreatedAt: time.Now().Unix(),
		Metadata: map[string]interface{}{
			"thought_id":   thought.ID,
			"thought_text": thought.Content,
			"tool_name":    selectedTool.Name(),
			"explanation":  explanation,
			"tool_purpose": selectedTool.Description(),
		},
	}

	// Store the item
	_ = s.memory.StoreItem(ctx, toolSelectionItem)
}

// GetCycleManager extracts the cycle manager from context if available.
func GetCycleManager(ctx context.Context) (CycleManager, bool) {
	if ctx == nil {
		return nil, false
	}

	cycleManagerVal := ctx.Value("cycleManager")
	if cycleManagerVal == nil {
		return nil, false
	}

	cycleManager, ok := cycleManagerVal.(CycleManager)
	return cycleManager, ok
}

// Add helper functions for working with observations
// getErrorMessage extracts error message from an observation
func getErrorMessage(observation *CycleObservation) string {
	if observation == nil || observation.ToolOutput == nil {
		return "Unknown error"
	}

	// Check for error field
	if errVal, ok := observation.ToolOutput["error"]; ok {
		if errStr, ok := errVal.(string); ok {
			return errStr
		}
		// Try to use the whole error value as a string
		return fmt.Sprintf("%v", errVal)
	}

	return "Error occurred during tool execution"
}

// getSuccessMessage extracts success message from an observation
func getSuccessMessage(observation *CycleObservation) string {
	if observation == nil || observation.ToolOutput == nil {
		return "Tool execution was successful"
	}

	// Check for output or result field
	for _, key := range []string{"output", "result"} {
		if val, ok := observation.ToolOutput[key]; ok {
			if str, ok := val.(string); ok {
				return str
			}
			// Try to use the whole value as a string
			return fmt.Sprintf("%v", val)
		}
	}

	return "Tool execution was successful"
}
