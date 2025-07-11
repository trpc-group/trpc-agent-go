package tool

import "context"

// ToolSet defines an interface for managing a set of tools.
// It provides methods to retrieve the current tools and to perform cleanup.
type ToolSet interface {
	// Tools returns a slice of CallableTool instances available in the set based on the provided context.
	Tools(context.Context) []CallableTool

	// Close releases any resources held by the ToolSet.
	Close() error
}
