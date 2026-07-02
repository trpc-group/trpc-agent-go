//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type messageCollector struct {
	messages    []model.Message
	streams     map[string]*messageStream
	streamOrder []string
}

type messageStream struct {
	prefix       string
	message      model.Message
	toolCallKeys map[string]int
}

func newMessageCollector(input model.Message) *messageCollector {
	return &messageCollector{
		messages: []model.Message{input},
	}
}

func (c *messageCollector) addEvent(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	prefix := eventStreamPrefix(evt)
	for _, choice := range evt.Response.Choices {
		c.addChoice(streamChoiceKey(prefix, choice.Index), prefix, choice)
	}
	if evt.Response.Done {
		c.flushEvent(prefix)
	}
}

func (c *messageCollector) messagesList() []model.Message {
	return c.messages
}

func (c *messageCollector) flushAll() {
	keys := append([]string(nil), c.streamOrder...)
	for _, key := range keys {
		c.flush(key)
	}
}

func (c *messageCollector) flushEvent(prefix string) {
	keys := append([]string(nil), c.streamOrder...)
	for _, key := range keys {
		stream := c.streams[key]
		if stream != nil && stream.prefix == prefix {
			c.flush(key)
		}
	}
}

func (c *messageCollector) flush(key string) {
	if c.streams == nil {
		return
	}
	stream := c.streams[key]
	if stream == nil {
		return
	}
	delete(c.streams, key)
	c.removeStreamKey(key)
	if !messageHasPayload(stream.message) {
		return
	}
	c.appendMessage(stream.message)
}

func (c *messageCollector) removeStreamKey(key string) {
	for i, current := range c.streamOrder {
		if current == key {
			c.streamOrder = append(c.streamOrder[:i], c.streamOrder[i+1:]...)
			return
		}
	}
}

func (c *messageCollector) addChoice(key string, prefix string, choice model.Choice) {
	if messageHasPayload(choice.Delta) {
		if messageHasStreamingMetadata(choice.Message) {
			c.addDelta(key, prefix, choice.Message)
		}
		c.addDelta(key, prefix, choice.Delta)
	} else if messageHasPayload(choice.Message) {
		c.flush(key)
		c.appendMessage(choice.Message)
	}
	if choice.FinishReason != nil {
		c.flush(key)
	}
}

func (c *messageCollector) appendMessage(message model.Message) {
	if len(c.messages) > 0 && model.MessagesEqual(c.messages[len(c.messages)-1], message) {
		return
	}
	c.messages = append(c.messages, message)
}

func (c *messageCollector) addDelta(key string, prefix string, delta model.Message) {
	stream := c.stream(key, prefix)
	if delta.Role != "" {
		stream.message.Role = delta.Role
	}
	if delta.Content != "" {
		stream.message.Content += delta.Content
	}
	if delta.ReasoningContent != "" {
		stream.message.ReasoningContent += delta.ReasoningContent
	}
	if delta.ReasoningSignature != "" {
		stream.message.ReasoningSignature = delta.ReasoningSignature
	}
	if len(delta.ContentParts) > 0 {
		stream.message.ContentParts = append(stream.message.ContentParts, delta.ContentParts...)
	}
	if delta.ToolID != "" {
		stream.message.ToolID = delta.ToolID
	}
	if delta.ToolName != "" {
		stream.message.ToolName = delta.ToolName
	}
	if len(delta.ToolCalls) > 0 {
		c.mergeToolCallDeltas(stream, delta.ToolCalls)
	}
}

func (c *messageCollector) stream(key string, prefix string) *messageStream {
	if c.streams == nil {
		c.streams = make(map[string]*messageStream)
	}
	stream := c.streams[key]
	if stream != nil {
		return stream
	}
	stream = &messageStream{
		prefix:  prefix,
		message: model.Message{Role: model.RoleAssistant},
	}
	c.streams[key] = stream
	c.streamOrder = append(c.streamOrder, key)
	return stream
}

func (c *messageCollector) mergeToolCallDeltas(stream *messageStream, toolCalls []model.ToolCall) {
	if stream.toolCallKeys == nil {
		stream.toolCallKeys = make(map[string]int, len(toolCalls))
	}
	for i, delta := range toolCalls {
		key := stream.toolCallDeltaKey(delta, i)
		idx, ok := stream.toolCallKeys[key]
		if !ok {
			stream.message.ToolCalls = append(stream.message.ToolCalls, delta)
			idx = len(stream.message.ToolCalls) - 1
			stream.indexToolCall(i, idx, delta)
			continue
		}
		stream.message.ToolCalls[idx] = mergeToolCall(stream.message.ToolCalls[idx], delta)
		stream.indexToolCall(i, idx, stream.message.ToolCalls[idx])
	}
}

func eventStreamPrefix(evt *event.Event) string {
	parts := make([]string, 0, 6)
	if evt.ParentInvocationID != "" {
		parts = append(parts, "parent:"+evt.ParentInvocationID)
	}
	if evt.ParentMetadata != nil && evt.ParentMetadata.TriggerID != "" {
		parts = append(parts, "trigger:"+evt.ParentMetadata.TriggerID)
	}
	if evt.InvocationID != "" {
		parts = append(parts, "invocation:"+evt.InvocationID)
	}
	if evt.Branch != "" {
		parts = append(parts, "branch:"+evt.Branch)
	}
	if evt.Response != nil && evt.Response.ID != "" {
		parts = append(parts, "response:"+evt.Response.ID)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "|")
	}
	if evt.Author != "" {
		return "author:" + evt.Author
	}
	return "event:" + evt.ID
}

func streamChoiceKey(prefix string, choiceIndex int) string {
	return fmt.Sprintf("%s|choice:%d", prefix, choiceIndex)
}

func (s *messageStream) toolCallDeltaKey(delta model.ToolCall, position int) string {
	if delta.Index != nil {
		return fmt.Sprintf("index:%d", *delta.Index)
	}
	if position < len(s.message.ToolCalls) {
		existing := s.message.ToolCalls[position]
		if existing.Index != nil {
			return fmt.Sprintf("index:%d", *existing.Index)
		}
		if existing.ID != "" {
			return "id:" + existing.ID
		}
		return fmt.Sprintf("position:%d", position)
	}
	if delta.ID != "" {
		return "id:" + delta.ID
	}
	return fmt.Sprintf("position:%d", position)
}

func (s *messageStream) indexToolCall(position int, idx int, call model.ToolCall) {
	s.toolCallKeys[fmt.Sprintf("position:%d", position)] = idx
	if call.Index != nil {
		s.toolCallKeys[fmt.Sprintf("index:%d", *call.Index)] = idx
	}
	if call.ID != "" {
		s.toolCallKeys["id:"+call.ID] = idx
	}
}

func mergeToolCall(base model.ToolCall, delta model.ToolCall) model.ToolCall {
	if delta.Type != "" {
		base.Type = delta.Type
	}
	if delta.ID != "" {
		base.ID = delta.ID
	}
	if delta.Index != nil {
		base.Index = delta.Index
	}
	if delta.Function.Name != "" {
		base.Function.Name = delta.Function.Name
	}
	if delta.Function.Description != "" {
		base.Function.Description = delta.Function.Description
	}
	if delta.Function.Strict {
		base.Function.Strict = true
	}
	if len(delta.Function.Arguments) > 0 {
		base.Function.Arguments = append(base.Function.Arguments, delta.Function.Arguments...)
	}
	if len(delta.ExtraFields) > 0 {
		if base.ExtraFields == nil {
			base.ExtraFields = make(map[string]any, len(delta.ExtraFields))
		}
		for key, value := range delta.ExtraFields {
			base.ExtraFields[key] = value
		}
	}
	return base
}

func messageHasPayload(message model.Message) bool {
	return model.HasPayload(message) ||
		message.ReasoningSignature != "" ||
		len(message.ToolCalls) > 0 ||
		message.ToolID != "" ||
		message.ToolName != ""
}

func messageHasStreamingMetadata(message model.Message) bool {
	return message.Role != "" ||
		message.ReasoningSignature != "" ||
		len(message.ToolCalls) > 0 ||
		message.ToolID != "" ||
		message.ToolName != ""
}
