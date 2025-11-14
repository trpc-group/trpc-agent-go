//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dify

import (
	"context"
	"strings"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// DifyEventConverter defines an interface for converting Dify response to Event.
type DifyEventConverter interface {
	// ConvertToEvent converts an A2A protocol type to an Event.
	ConvertToEvent(
		resp *dify.ChatMessageResponse,
		agentName string,
		invocation *agent.Invocation,
	) *event.Event

	// ConvertStreamingToEvent converts a streaming A2A protocol type to an Event.
	ConvertStreamingToEvent(
		resp dify.ChatMessageStreamChannelResponse,
		agentName string,
		invocation *agent.Invocation,
	) *event.Event
}

// DifyRequestConverter defines an interface for converting invocations to Dify request messages.
type DifyRequestConverter interface {
	// ConvertToDifyRequest converts agent invocation to Dify request
	ConvertToDifyRequest(
		ctx context.Context,
		invocation *agent.Invocation,
		isStream bool,
	) (*dify.ChatMessageRequest, error)
}

// DifyWorkflowRequestConverter defines an interface for converting invocations to Dify workflow requests.
type DifyWorkflowRequestConverter interface {
	// ConvertToWorkflowRequest converts agent invocation to Dify workflow request
	ConvertToWorkflowRequest(
		ctx context.Context,
		invocation *agent.Invocation,
	) (dify.WorkflowRequest, error)
}

type defaultDifyEventConverter struct {
}

func (d *defaultDifyEventConverter) ConvertToEvent(
	resp *dify.ChatMessageResponse,
	agentName string,
	invocation *agent.Invocation,
) (evt *event.Event) {
	if resp == nil {
		defaultMessage := model.Message{Role: model.RoleAssistant, Content: ""}
		evt = event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: defaultMessage, Delta: defaultMessage}}},
		)
		return
	}

	message := model.Message{
		Role:    model.RoleAssistant,
		Content: resp.Answer,
	}

	evt = event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Choices:   []model.Choice{{Message: message, Delta: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: false,
			Done:      true,
		}),
	)
	return
}

func (d *defaultDifyEventConverter) ConvertStreamingToEvent(
	resp dify.ChatMessageStreamChannelResponse,
	agentName string,
	invocation *agent.Invocation,
) (evt *event.Event) {
	if resp.Answer == "" {
		return
	}
	message := model.Message{
		Role:    model.RoleAssistant,
		Content: resp.Answer,
	}

	evt = event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Object:    model.ObjectTypeChatCompletionChunk,
			Choices:   []model.Choice{{Delta: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: true,
			Done:      false,
		}),
		event.WithObject(model.ObjectTypeChatCompletionChunk),
	)
	return
}

type defaultEventDifyConverter struct {
}

func (d *defaultEventDifyConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest, error) {
	req := &dify.ChatMessageRequest{
		Query:  invocation.Message.Content,
		Inputs: make(map[string]any),
	}
	if invocation.Session != nil {
		req.User = invocation.Session.UserID
	}
	if req.User == "" {
		req.User = "anonymous"
	}

	// 流式返回
	if isStream {
		req.ResponseMode = "streaming"
	}

	// Handle content parts if available
	for _, contentPart := range invocation.Message.ContentParts {
		switch contentPart.Type {
		case model.ContentTypeText:
			if contentPart.Text != nil {
				// Append text content to query
				if req.Query != "" {
					req.Query += "\n" + *contentPart.Text
				} else {
					req.Query = *contentPart.Text
				}
			}
		case model.ContentTypeImage:
			// For now, we can't directly handle images in Dify requests
			// This would need to be handled based on specific Dify capabilities
			if contentPart.Image != nil && contentPart.Image.URL != "" {
				req.Inputs["image_url"] = contentPart.Image.URL
			}
		case model.ContentTypeFile:
			// Similar to images, file handling depends on Dify capabilities
			if contentPart.File != nil && contentPart.File.Name != "" {
				req.Inputs["file_name"] = contentPart.File.Name
			}
		default:
			// Handle other content types as needed
			req.Inputs["other_content_type"] = contentPart.Type
		}
	}

	return req, nil
}

// defaultWorkflowRequestConverter is the default converter for workflow requests
type defaultWorkflowRequestConverter struct{}

func (d *defaultWorkflowRequestConverter) ConvertToWorkflowRequest(
	ctx context.Context,
	invocation *agent.Invocation,
) (dify.WorkflowRequest, error) {
	inputs := make(map[string]any)
	inputs["query"] = invocation.Message.Content

	// Handle content parts if available
	for _, contentPart := range invocation.Message.ContentParts {
		switch contentPart.Type {
		case model.ContentTypeText:
			if contentPart.Text != nil {
				inputs["query"] = *contentPart.Text
			}
		case model.ContentTypeImage:
			if contentPart.Image != nil && contentPart.Image.URL != "" {
				inputs["image_url"] = contentPart.Image.URL
			}
		case model.ContentTypeFile:
			if contentPart.File != nil && contentPart.File.Name != "" {
				inputs["file_name"] = contentPart.File.Name
			}
		default:
			inputs["other_content_type"] = contentPart.Type
		}
	}

	user := "anonymous"
	if invocation.Session != nil && invocation.Session.UserID != "" {
		user = invocation.Session.UserID
	}

	return dify.WorkflowRequest{
		Inputs:       inputs,
		User:         user,
		ResponseMode: "blocking", // workflow default mode
	}, nil
}

// extractTextFromParts extracts text content from protocol message parts
func extractTextFromParts(parts []protocol.Part) string {
	var content strings.Builder
	for _, part := range parts {
		if part.GetKind() == protocol.KindText {
			p, ok := part.(*protocol.TextPart)
			if !ok {
				log.Warnf("unexpected part type: %T", part)
				continue
			}
			content.WriteString(p.Text)
		}
	}
	return content.String()
}

// buildResponseForEvent creates a Response object based on streaming mode
func buildResponseForEvent(isStreaming bool, content string) *model.Response {
	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content,
	}

	if isStreaming {
		return &model.Response{
			Choices:   []model.Choice{{Delta: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: true,
			Done:      false,
		}
	}

	return &model.Response{
		Choices:   []model.Choice{{Message: message}},
		Timestamp: time.Now(),
		Created:   time.Now().Unix(),
		IsPartial: false,
		Done:      true,
	}
}

// buildRespEvent converts A2A response to tRPC event
func (d *defaultDifyEventConverter) buildRespEvent(
	isStreaming bool,
	msg *protocol.Message,
	agentName string,
	invocation *agent.Invocation) *event.Event {

	// Extract text content from parts
	content := extractTextFromParts(msg.Parts)

	// Create event with appropriate response
	evt := event.New(invocation.InvocationID, agentName)
	evt.Response = buildResponseForEvent(isStreaming, content)

	return evt
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
