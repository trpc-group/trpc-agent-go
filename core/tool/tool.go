// Package tool provides tool interfaces and implementations for the agent system.
package tool

import "context"

// BaseTool defines the core interface that all tools must implement.
// This interface provides a standardized way to define and execute tools.
type BaseTool interface {
	Name() string
	Description() string
	InputSchema() map[string]interface{}
	Run(ctx context.Context, toolInput map[string]interface{}) (string, error)
}
