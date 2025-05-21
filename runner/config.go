package runner

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Config defines configuration options for runners.
type Config struct {
	// MaxConcurrent is the maximum number of concurrent executions.
	MaxConcurrent int `json:"max_concurrent"`

	// Timeout is the maximum duration for an execution.
	Timeout time.Duration `json:"timeout"`

	// RetryCount is the number of times to retry on failure.
	RetryCount int `json:"retry_count"`

	// RetryDelay is the delay between retries.
	RetryDelay time.Duration `json:"retry_delay"`

	// BufferSize is the size of event channels.
	BufferSize int `json:"buffer_size"`

	// SessionOptions contains configuration for session management.
	SessionOptions session.Options `json:"session_options,omitempty"`

	// Custom contains additional custom configuration.
	Custom map[string]interface{} `json:"custom,omitempty"`
}

// DefaultConfig returns a default runner configuration.
func DefaultConfig() Config {
	return Config{
		MaxConcurrent: 10,
		Timeout:       time.Minute,
		RetryCount:    3,
		RetryDelay:    time.Second,
		BufferSize:    100,
		SessionOptions: session.Options{
			Expiration: 24 * time.Hour, // Default 24-hour session expiration
		},
		Custom: make(map[string]interface{}),
	}
}

// WithTimeout sets the timeout for the config.
func (c Config) WithTimeout(timeout time.Duration) Config {
	c.Timeout = timeout
	return c
}

// WithMaxConcurrent sets the maximum concurrent executions.
func (c Config) WithMaxConcurrent(max int) Config {
	c.MaxConcurrent = max
	return c
}

// WithRetry sets the retry parameters.
func (c Config) WithRetry(count int, delay time.Duration) Config {
	c.RetryCount = count
	c.RetryDelay = delay
	return c
}

// WithBufferSize sets the buffer size for event channels.
func (c Config) WithBufferSize(size int) Config {
	c.BufferSize = size
	return c
}

// WithSessionOptions sets the session options.
func (c Config) WithSessionOptions(options session.Options) Config {
	c.SessionOptions = options
	return c
}

// WithSessionExpiration sets the session expiration duration.
func (c Config) WithSessionExpiration(duration time.Duration) Config {
	c.SessionOptions.Expiration = duration
	return c
}

// WithCustomParam adds a custom parameter to the config.
func (c Config) WithCustomParam(key string, value interface{}) Config {
	if c.Custom == nil {
		c.Custom = make(map[string]interface{})
	}
	c.Custom[key] = value
	return c
}
