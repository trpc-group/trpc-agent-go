// Package event provides the event system for agent communication.
package event

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Content represents rich content that can contain text and function calls.
// This is the core content structure that replaces the old message system.
type Content struct {
	Parts []*Part `json:"parts,omitempty"`
}

// Part represents a single part of content - either text or a function call/response.
type Part struct {
	// Text content
	Text string `json:"text,omitempty"`
	
	// Tool integration (embedded in content, not separate events)
	FunctionCall     *FunctionCall     `json:"function_call,omitempty"`
	FunctionResponse *FunctionResponse `json:"function_response,omitempty"`
}

// FunctionCall represents a call to a tool/function.
type FunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
	ID        string                 `json:"id,omitempty"`
}

// FunctionResponse represents the response from a tool/function.
type FunctionResponse struct {
	Name   string      `json:"name"`
	Result interface{} `json:"result"`
	ID     string      `json:"id,omitempty"`
}

// Content creation helpers

// NewTextContent creates new content with a single text part.
func NewTextContent(text string) *Content {
	return &Content{
		Parts: []*Part{
			NewTextPart(text),
		},
	}
}

// NewContentWithParts creates content with the provided parts.
func NewContentWithParts(parts []*Part) *Content {
	return &Content{
		Parts: parts,
	}
}

// Part creation helpers

// NewTextPart creates a new text part.
func NewTextPart(text string) *Part {
	return &Part{
		Text: text,
	}
}

// NewFunctionCallPart creates a new function call part.
func NewFunctionCallPart(name string, args map[string]interface{}) *Part {
	return &Part{
		FunctionCall: &FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}
}

// NewFunctionCallPartWithID creates a new function call part with an ID.
func NewFunctionCallPartWithID(name string, args map[string]interface{}, id string) *Part {
	return &Part{
		FunctionCall: &FunctionCall{
			Name:      name,
			Arguments: args,
			ID:        id,
		},
	}
}

// NewFunctionResponsePart creates a new function response part.
func NewFunctionResponsePart(name string, result interface{}) *Part {
	return &Part{
		FunctionResponse: &FunctionResponse{
			Name:   name,
			Result: result,
		},
	}
}

// NewFunctionResponsePartWithID creates a new function response part with an ID.
func NewFunctionResponsePartWithID(name string, result interface{}, id string) *Part {
	return &Part{
		FunctionResponse: &FunctionResponse{
			Name:   name,
			Result: result,
			ID:     id,
		},
	}
}

// Content analysis helpers

// GetText extracts all text from the content parts.
func (c *Content) GetText() string {
	if c == nil || len(c.Parts) == 0 {
		return ""
	}
	
	var texts []string
	for _, part := range c.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	
	return strings.Join(texts, " ")
}

// GetFunctionCalls extracts all function calls from the content.
func (c *Content) GetFunctionCalls() []*FunctionCall {
	if c == nil || len(c.Parts) == 0 {
		return nil
	}
	
	var calls []*FunctionCall
	for _, part := range c.Parts {
		if part.FunctionCall != nil {
			calls = append(calls, part.FunctionCall)
		}
	}
	
	return calls
}

// GetFunctionResponses extracts all function responses from the content.
func (c *Content) GetFunctionResponses() []*FunctionResponse {
	if c == nil || len(c.Parts) == 0 {
		return nil
	}
	
	var responses []*FunctionResponse
	for _, part := range c.Parts {
		if part.FunctionResponse != nil {
			responses = append(responses, part.FunctionResponse)
		}
	}
	
	return responses
}

// HasFunctionCalls returns true if the content contains any function calls.
func (c *Content) HasFunctionCalls() bool {
	return len(c.GetFunctionCalls()) > 0
}

// HasFunctionResponses returns true if the content contains any function responses.
func (c *Content) HasFunctionResponses() bool {
	return len(c.GetFunctionResponses()) > 0
}

// HasText returns true if the content contains any text.
func (c *Content) HasText() bool {
	if c == nil || len(c.Parts) == 0 {
		return false
	}
	
	for _, part := range c.Parts {
		if part.Text != "" {
			return true
		}
	}
	
	return false
}

// AddPart adds a part to the content.
func (c *Content) AddPart(part *Part) {
	if c.Parts == nil {
		c.Parts = make([]*Part, 0)
	}
	c.Parts = append(c.Parts, part)
}

// AddText adds a text part to the content.
func (c *Content) AddText(text string) {
	c.AddPart(NewTextPart(text))
}

// AddFunctionCall adds a function call part to the content.
func (c *Content) AddFunctionCall(name string, args map[string]interface{}) {
	c.AddPart(NewFunctionCallPart(name, args))
}

// AddFunctionResponse adds a function response part to the content.
func (c *Content) AddFunctionResponse(name string, result interface{}) {
	c.AddPart(NewFunctionResponsePart(name, result))
}

// String returns a human-readable representation of the content.
func (c *Content) String() string {
	if c == nil || len(c.Parts) == 0 {
		return ""
	}
	
	var parts []string
	for _, part := range c.Parts {
		if part.Text != "" {
			parts = append(parts, fmt.Sprintf("Text: %s", part.Text))
		}
		if part.FunctionCall != nil {
			parts = append(parts, fmt.Sprintf("FunctionCall: %s", part.FunctionCall.Name))
		}
		if part.FunctionResponse != nil {
			parts = append(parts, fmt.Sprintf("FunctionResponse: %s", part.FunctionResponse.Name))
		}
	}
	
	return fmt.Sprintf("Content[%s]", strings.Join(parts, ", "))
}

// MarshalJSON provides custom JSON marshaling for Content.
func (c *Content) MarshalJSON() ([]byte, error) {
	if c == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(struct {
		Parts []*Part `json:"parts,omitempty"`
	}{
		Parts: c.Parts,
	})
}

// UnmarshalJSON provides custom JSON unmarshaling for Content.
func (c *Content) UnmarshalJSON(data []byte) error {
	var temp struct {
		Parts []*Part `json:"parts,omitempty"`
	}
	
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	
	c.Parts = temp.Parts
	return nil
} 