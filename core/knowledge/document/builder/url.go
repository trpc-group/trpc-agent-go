// Package builder provides URL document builder logic.
package builder

import "net/http"

// URLOption represents a functional option for URL document creation.
type URLOption func(*urlConfig)

// urlConfig holds configuration for URL document creation.
type urlConfig struct {
	id         string
	name       string
	metadata   map[string]interface{}
	httpClient *http.Client
} 