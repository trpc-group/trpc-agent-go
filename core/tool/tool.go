// Package tool provides tool interfaces and implementations for the agent system.
package tool

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// Tool defines the core interface that all tools must implement.
type Tool interface {
	// Run executes the tool with the provided context and arguments.
	// Returns the result of execution or an error if the operation fails.
	Run(ctx context.Context, args Arguments) (any, error)
	// Declaration returns the metadata describing the tool.
	Declaration() *Declaration
	// Combine merges the tool with a given request, possibly returning a new request and tool.
	Combine(*model.Request) (*model.Request, Tool, error)
}

// Arguments represents a generic map of argument names to their values.
type Arguments map[string]any

// Declaration describes the metadata of a tool, such as its name, description, and expected arguments.
type Declaration struct {
	Name        string         `json:"name"`        // Name of the tool
	Description string         `json:"description"` // Description of the tool's purpose
	Arguments   map[string]any `json:"arguments"`   // Expected arguments for the tool
}
