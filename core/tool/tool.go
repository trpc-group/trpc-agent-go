// Package tool provides tool interfaces and implementations for the agent system.
package tool

import "context"

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
	// Name is the unique identifier of the tool
	Name string `json:"name"`

	// Description explains the tool's purpose and functionality
	Description string `json:"description"`

	// Arguments defines the expected input schema for the tool,  stored as JSON-marshaled data
	Arguments []byte `json:"arguments"`
}
