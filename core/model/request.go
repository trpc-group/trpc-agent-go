package model

// Role represents the role of a message author.
type Role string

// Role constants for message authors.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// String returns the string representation of the role.
func (r Role) String() string {
	return string(r)
}

// IsValid checks if the role is one of the defined constants.
func (r Role) IsValid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant:
		return true
	default:
		return false
	}
}

// Message represents a single message in a conversation.
type Message struct {
	Role    Role   `json:"role"`    // The role of the message author
	Content string `json:"content"` // The message content
}

// NewSystemMessage creates a new system message.
func NewSystemMessage(content string) Message {
	return Message{
		Role:    RoleSystem,
		Content: content,
	}
}

// NewUserMessage creates a new user message.
func NewUserMessage(content string) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// NewAssistantMessage creates a new assistant message.
func NewAssistantMessage(content string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// Request is the request to the model.
type Request struct {
	// Model specifies which model to use (e.g., "gpt-4", "gpt-3.5-turbo").
	Model string `json:"model"`

	// Messages is the conversation history.
	Messages []Message `json:"messages"`

	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens *int `json:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0 to 2.0).
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling (0.0 to 1.0).
	TopP *float64 `json:"top_p,omitempty"`

	// Stream indicates whether to stream the response.
	Stream bool `json:"stream"`

	// Stop sequences where the API will stop generating further tokens.
	Stop []string `json:"stop,omitempty"`

	// PresencePenalty penalizes new tokens based on their existing frequency.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	// FrequencyPenalty penalizes new tokens based on their frequency in the text so far.
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
}
