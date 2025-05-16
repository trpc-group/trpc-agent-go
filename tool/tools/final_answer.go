package tools

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// FinalAnswerTool represents a special tool to provide a final answer.
type FinalAnswerTool struct {
	name        string
	description string
}

// NewFinalAnswerTool creates a new final answer tool.
func NewFinalAnswerTool() *FinalAnswerTool {
	return &FinalAnswerTool{
		name:        "final_answer",
		description: "Use this tool to provide a final answer directly to the user, ending the current reasoning process.",
	}
}

// Name returns the tool name.
func (t *FinalAnswerTool) Name() string {
	return t.name
}

// Description returns the tool description.
func (t *FinalAnswerTool) Description() string {
	return t.description
}

// Parameters returns the parameter schema.
func (t *FinalAnswerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The final answer content to present to the user",
			},
		},
		"required": []string{"content"},
	}
}

// Execute executes the final answer tool.
func (t *FinalAnswerTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	content, ok := params["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	log.Infof("Final answer received: %s", content)

	// Create a result with the final answer
	result := tool.NewResult(content)
	result.ContentType = "text/plain"
	
	// Add metadata
	if result.Metadata == nil {
		result.Metadata = make(map[string]interface{})
	}
	result.Metadata["is_final_answer"] = true
	
	return result, nil
}

// GetDefinition returns the tool definition.
func (t *FinalAnswerTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.Name(), t.Description())
	def.AddParameter("content", tool.NewStringProperty("The final answer content to present to the user", nil, true), true)
	return def
} 