package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/session"
)

// Content inclusion options.
const (
	IncludeContentsNone     = "none"
	IncludeContentsAll      = "all"
	IncludeContentsFiltered = "filtered"
)

// ContentRequestProcessor implements content processing logic.
type ContentRequestProcessor struct {
	// IncludeContents determines how to include content from session events.
	// Options: "none", "all", "filtered" (default: "all").
	IncludeContents string
}

// NewContentRequestProcessor creates a new content request processor.
func NewContentRequestProcessor() *ContentRequestProcessor {
	return &ContentRequestProcessor{
		IncludeContents: IncludeContentsAll, // Default to include all contents.
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It handles adding messages from the session events and current invocation to the request.
func (p *ContentRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.Errorf("Content request processor: request is nil")
		return
	}

	// Process session events if available and includeContents is not "none".
	if p.IncludeContents != IncludeContentsNone && invocation.Session != nil {
		sessionMessages := p.getMessagesFromSession(
			invocation.Session,
		)
		req.Messages = append(req.Messages, sessionMessages...)
	}

	// Send a preprocessing event.
	if invocation != nil {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = model.ObjectTypePreprocessingContent

		select {
		case ch <- evt:
			log.Debugf("Content request processor: sent preprocessing event")
		case <-ctx.Done():
			log.Debugf("Content request processor: context cancelled")
		}
	}
}

// getMessagesFromSession extracts and filters messages from session events.
func (p *ContentRequestProcessor) getMessagesFromSession(
	sess *session.Session,
) []model.Message {
	if sess == nil || len(sess.Events) == 0 {
		log.Infof("getMessagesFromSession: session is nil or has no events")
		return []model.Message{}
	}
	// Filter events based on criteria.
	filteredEvents := p.filterEvents(sess.Events)
	// Convert events to messages.
	messages := p.convertEventsToMessages(filteredEvents)
	return messages
}

// filterEvents filters session events based on various criteria.
func (p *ContentRequestProcessor) filterEvents(
	events []event.Event,
) []event.Event {
	var filteredEvents []event.Event

	for _, evt := range events {
		// Skip events without content or meaningful data.
		if !p.hasValidContent(&evt) {
			continue
		}
		filteredEvents = append(filteredEvents, evt)
	}

	return filteredEvents
}

// hasValidContent checks if an event has valid content for message generation.
func (p *ContentRequestProcessor) hasValidContent(evt *event.Event) bool {
	// Check if event has choices with content.
	if len(evt.Choices) > 0 {
		for _, choice := range evt.Choices {
			if choice.Message.Content != "" {
				return true
			}
			if choice.Delta.Content != "" {
				return true
			}
		}
	}
	if len(evt.Choices) > 0 && evt.Choices[0].Message.ToolID != "" {
		return true
	}
	if len(evt.Choices) > 0 && len(evt.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	return false
}

// convertEventsToMessages converts filtered events to model messages.
func (p *ContentRequestProcessor) convertEventsToMessages(
	events []event.Event,
) []model.Message {
	var messages []model.Message

	for _, evt := range events {
		// Convert choices to messages.
		for _, choice := range evt.Choices {
			if choice.Message.Content != "" || choice.Message.ToolID != "" || len(choice.Message.ToolCalls) > 0 {
				messages = append(messages, choice.Message)
			}
		}
	}

	return messages
}
