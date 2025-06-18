package tool

import "context"

// Tool defines the core interface that all tools must implement.
// ToolSet defines an interface for managing a set of tools.
// It provides methods to retrieve the current tools and to perform cleanup.
type ToolSet interface {
	// Tools returns a slice of Tool instances available in the set based on the provided context.
	Tools(context.Context) []Tool

	// Close releases any resources held by the ToolSet.
	Close() error
}
