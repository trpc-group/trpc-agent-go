//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"encoding/base64"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// A2AEventConverter defines an interface for converting A2A protocol types to Event.
type A2AEventConverter interface {
	// ConvertToEvent converts an A2A protocol type to an Event.

	ConvertToEvent(result protocol.MessageResult, agentName string, invocation *agent.Invocation) (*event.Event, error)

	// ConvertStreamingToEvent converts a streaming A2A protocol type to an Event.
	ConvertStreamingToEvent(result protocol.StreamingMessageEvent, agentName string, invocation *agent.Invocation) (*event.Event, error)
}

// InvocationA2AConverter defines an interface for converting invocations to A2A protocol messages.
type InvocationA2AConverter interface {
	// ConvertToA2AMessage converts an invocation to an A2A protocol Message.
	ConvertToA2AMessage(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error)
}

type defaultA2AEventConverter struct {
}

func (d *defaultA2AEventConverter) ConvertToEvent(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) (*event.Event, error) {
	if result.Result == nil {
		return event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		), nil
	}

	var responseMsg *protocol.Message
	var event *event.Event
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
		event = d.buildRespEvent(false, responseMsg, agentName, invocation)
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
		event = d.buildRespEvent(false, responseMsg, agentName, invocation)
	default:
		// Handle unknown response types
		responseMsg = &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{protocol.NewTextPart("Received unknown response type")},
		}
		event = d.buildRespEvent(false, responseMsg, agentName, invocation)
	}
	event.Done = true
	event.IsPartial = false
	return event, nil
}

func (d *defaultA2AEventConverter) ConvertStreamingToEvent(
	result protocol.StreamingMessageEvent,
	agentName string,
	invocation *agent.Invocation,
) (*event.Event, error) {
	if result.Result == nil {
		return event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		), nil
	}

	var event *event.Event
	var responseMsg *protocol.Message
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.TaskStatusUpdateEvent:
		responseMsg = convertTaskStatusToMessage(v)
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.TaskArtifactUpdateEvent:
		responseMsg = convertTaskArtifactToMessage(v)
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	default:
		log.Infof("unexpected event type: %T", result.Result)
	}
	return event, nil
}

type defaultEventA2AConverter struct {
}

// ConvertToA2AMessage converts an event to an A2A protocol Message.
func (d *defaultEventA2AConverter) ConvertToA2AMessage(
	isStream bool,
	agentName string,
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	var parts []protocol.Part

	// Convert invocation.Message.Content (text) to TextPart
	if invocation.Message.Content != "" {
		parts = append(parts, protocol.NewTextPart(invocation.Message.Content))
	}

	// Convert invocation.Message.ContentParts to A2A Parts
	for _, contentPart := range invocation.Message.ContentParts {
		switch contentPart.Type {
		case model.ContentTypeText:
			if contentPart.Text != nil {
				parts = append(parts, protocol.NewTextPart(*contentPart.Text))
			}
		case model.ContentTypeImage:
			if contentPart.Image != nil {
				if len(contentPart.Image.Data) > 0 {
					// Handle inline image data
					parts = append(parts, protocol.NewFilePartWithBytes(
						"image",
						contentPart.Image.Format,
						base64.StdEncoding.EncodeToString(contentPart.Image.Data),
					))
				} else if contentPart.Image.URL != "" {
					// Handle image URL
					parts = append(parts, protocol.NewFilePartWithURI(
						"image",
						contentPart.Image.Format,
						contentPart.Image.URL,
					))
				}
			}
		case model.ContentTypeAudio:
			if contentPart.Audio != nil && contentPart.Audio.Data != nil {
				// Handle audio data as file with bytes
				parts = append(parts, protocol.NewFilePartWithBytes(
					"audio",
					contentPart.Audio.Format,
					base64.StdEncoding.EncodeToString(contentPart.Audio.Data),
				))
			}
		case model.ContentTypeFile:
			if contentPart.File != nil {
				if len(contentPart.File.Data) > 0 {
					fileName := contentPart.File.Name
					if fileName == "" {
						fileName = "file"
					}
					parts = append(parts, protocol.NewFilePartWithBytes(
						fileName,
						contentPart.File.MimeType,
						base64.StdEncoding.EncodeToString(contentPart.File.Data),
					))
				}
			}
		}
	}

	// If no content, create an empty text part to ensure message is not empty
	if len(parts) == 0 {
		parts = append(parts, protocol.NewTextPart(""))
	}
	message := protocol.NewMessage(protocol.MessageRoleUser, parts)
	return &message, nil
}

// buildRespEvent converts A2A response to tRPC event
func (d *defaultA2AEventConverter) buildRespEvent(
	isStreaming bool,
	msg *protocol.Message,
	agentName string,
	invocation *agent.Invocation) *event.Event {

	// Convert A2A parts to model content parts
	var content strings.Builder

	// Don't handle content parts of output temporally
	for _, part := range msg.Parts {
		if part.GetKind() == protocol.KindText {
			p, ok := part.(*protocol.TextPart)
			if !ok {
				log.Warnf("unexpected part type: %T", part)
				continue
			}
			content.WriteString(p.Text)
		}
	}
	// Create message with both content and content parts
	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content.String(),
	}
	event := event.New(invocation.InvocationID, agentName)
	if isStreaming {
		event.Response = &model.Response{
			Choices:   []model.Choice{{Delta: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: true,
			Done:      false,
		}
		return event
	}

	event.Response = &model.Response{
		Choices:   []model.Choice{{Message: message}},
		Timestamp: time.Now(),
		Created:   time.Now().Unix(),
		IsPartial: false,
		Done:      true,
	}
	return event
}

// convertTaskToMessage converts a Task to a Message
func convertTaskToMessage(task *protocol.Task) *protocol.Message {
	var parts []protocol.Part

	// Add artifacts if any
	for _, artifact := range task.Artifacts {
		parts = append(parts, artifact.Parts...)
	}

	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: parts,
	}
}

// convertTaskStatusToMessage converts a TaskStatusUpdateEvent to a Message
func convertTaskStatusToMessage(event *protocol.TaskStatusUpdateEvent) *protocol.Message {
	msg := &protocol.Message{
		Role: protocol.MessageRoleAgent,
	}
	if event.Status.Message != nil {
		msg.Parts = event.Status.Message.Parts
	}
	return msg
}

// convertTaskArtifactToMessage converts a TaskArtifactUpdateEvent to a Message
func convertTaskArtifactToMessage(event *protocol.TaskArtifactUpdateEvent) *protocol.Message {
	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: event.Artifact.Parts,
	}
}
