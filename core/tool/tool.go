// Package tool provides tool interfaces and implementations for the agent system.
package tool

import (
	"context"
)

// Tool defines the core interface that all tools must implement.
type Tool interface {
	// Call calls the tool with the provided context and arguments.
	// Returns the result of execution or an error if the operation fails.
	Call(ctx context.Context, args []byte) (any, error)
	// Declaration returns the metadata describing the tool.
	Declaration() *Declaration
}

// Declaration describes the metadata of a tool, such as its name, description, and expected arguments.
type Declaration struct {
	Name        string `json:"name"`        // Name of the tool
	Description string `json:"description"` // Description of the tool's purpose
	Arguments   []byte `json:"arguments"`   // Expected arguments for the tool
	// ArgumentsType describes the serialization format of Arguments (e.g., "json", "proto"), defaults to "json".
	ArgumentsType string `json:"arguments_type,omitempty"`
}
