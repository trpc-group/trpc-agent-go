package agents

import (
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ProcessStreamingResponse handles the streaming response with tool call visualization.
func ProcessStreamingResponse(eventChan <-chan *event.Event) (string, error) {
	var (
		fullContent string
	)

	for event := range eventChan {
		if event.Error != nil {
			log.Errorf("streaming process error: %v", event.Error)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				log.Infof("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					log.Infof("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Delta.Content != "" {
				fullContent += choice.Delta.Content
			}
		}

		if event.Done {
			break
		}
	}

	return fullContent, nil
}

// BoolPtr returns a pointer to the boolean value.
func BoolPtr(b bool) *bool {
	return &b
}

// StringPtr returns a pointer to the string value.
func StringPtr(s string) *string {
	return &s
}

// IntPtr returns a pointer to the int value.
func IntPtr(i int) *int {
	return &i
}

// FloatPtr returns a pointer to the float64 value.
func FloatPtr(f float64) *float64 {
	return &f
}

// ExtractTextFromMessage extracts the text content from a message.
func ExtractTextFromMessage(message protocol.Message) string {
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			return textPart.Text
		}
	}
	return ""
}
