//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type detectorState struct {
	mu          sync.Mutex
	previous    string
	repeatCount int
	warned      bool
	pending     *pendingRound
}

type pendingRound struct {
	identity  string
	toolCalls []model.ToolCall
	results   map[string]model.Message
}

type roundFingerprint struct {
	ToolCalls []callFingerprint `json:"tool_calls"`
}

type callFingerprint struct {
	ToolName  string        `json:"tool_name"`
	Arguments string        `json:"arguments"`
	Result    model.Message `json:"result"`
}

func (s *detectorState) observeToolMessages(
	toolCalls []model.ToolCall,
	toolResultMessages []model.Message,
) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observeToolMessagesLocked(toolCalls, toolResultMessages)
}

func (s *detectorState) observeToolMessagesLocked(
	toolCalls []model.ToolCall,
	toolResultMessages []model.Message,
) bool {

	identity, ok := toolRoundIdentity(toolCalls)
	if !ok {
		s.resetLocked()
		return false
	}
	if s.pending == nil || s.pending.identity != identity {
		if s.pending != nil {
			// A different round before all prior results arrived breaks
			// adjacency, but the new round may still seed a comparison.
			s.resetLocked()
		}
		s.pending = &pendingRound{
			identity:  identity,
			toolCalls: cloneToolCalls(toolCalls),
			results:   make(map[string]model.Message, len(toolCalls)),
		}
	}
	if !s.pending.addResults(toolResultMessages) {
		s.resetLocked()
		return false
	}
	if len(s.pending.results) < len(s.pending.toolCalls) {
		return false
	}

	orderedResults := make([]model.Message, 0, len(s.pending.toolCalls))
	for _, toolCall := range s.pending.toolCalls {
		result, exists := s.pending.results[toolCall.ID]
		if !exists {
			s.resetLocked()
			return false
		}
		orderedResults = append(orderedResults, result)
	}
	fingerprint, ok := fingerprintRound(
		s.pending.toolCalls,
		orderedResults,
	)
	s.pending = nil
	if !ok {
		s.resetLocked()
		return false
	}
	if fingerprint != s.previous {
		s.previous = fingerprint
		s.repeatCount = 1
		s.warned = false
		return false
	}
	s.repeatCount++
	if s.warned || s.repeatCount < 2 {
		return false
	}
	s.warned = true
	return true
}

func (s *detectorState) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

func (s *detectorState) resetLocked() {
	s.previous = ""
	s.repeatCount = 0
	s.warned = false
	s.pending = nil
}

func (p *pendingRound) addResults(messages []model.Message) bool {
	if p == nil || len(messages) == 0 {
		return false
	}
	expected := make(map[string]struct{}, len(p.toolCalls))
	for _, toolCall := range p.toolCalls {
		expected[toolCall.ID] = struct{}{}
	}
	for _, message := range messages {
		if message.Role != model.RoleTool || message.ToolID == "" {
			return false
		}
		if _, exists := expected[message.ToolID]; !exists {
			return false
		}
		if _, exists := p.results[message.ToolID]; exists {
			return false
		}
		p.results[message.ToolID] = message
	}
	return true
}

func toolRoundIdentity(toolCalls []model.ToolCall) (string, bool) {
	if len(toolCalls) == 0 {
		return "", false
	}
	type identityCall struct {
		ID        string `json:"id"`
		ToolName  string `json:"tool_name"`
		Arguments string `json:"arguments"`
	}
	identity := make([]identityCall, 0, len(toolCalls))
	seen := make(map[string]struct{}, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall.ID == "" || toolCall.Function.Name == "" {
			return "", false
		}
		if _, exists := seen[toolCall.ID]; exists {
			return "", false
		}
		seen[toolCall.ID] = struct{}{}
		identity = append(identity, identityCall{
			ID:        toolCall.ID,
			ToolName:  toolCall.Function.Name,
			Arguments: string(toolCall.Function.Arguments),
		})
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func fingerprintRound(
	toolCalls []model.ToolCall,
	toolResultMessages []model.Message,
) (string, bool) {
	if len(toolCalls) == 0 || len(toolCalls) != len(toolResultMessages) {
		return "", false
	}
	fingerprint := roundFingerprint{
		ToolCalls: make([]callFingerprint, 0, len(toolCalls)),
	}
	for i, toolCall := range toolCalls {
		fingerprint.ToolCalls = append(fingerprint.ToolCalls, callFingerprint{
			ToolName: toolCall.Function.Name,
			Arguments: digestText(
				canonicalArguments(toolCall.Function.Arguments),
			),
			Result: boundedResultMessage(toolResultMessages[i]),
		})
	}
	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func cloneToolCalls(toolCalls []model.ToolCall) []model.ToolCall {
	cloned := make([]model.ToolCall, len(toolCalls))
	copy(cloned, toolCalls)
	for i := range cloned {
		cloned[i].Function.Arguments = append(
			[]byte(nil),
			toolCalls[i].Function.Arguments...,
		)
	}
	return cloned
}

func canonicalArguments(arguments []byte) string {
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		var extra any
		if decoder.Decode(&extra) == io.EOF {
			if canonical, err := json.Marshal(value); err == nil {
				return string(canonical)
			}
		}
	}
	return string(trimmed)
}

func boundedResultMessage(message model.Message) model.Message {
	message.ToolID = ""
	// The tool-call fingerprint already carries the authoritative tool name.
	// Tool-result replacement hooks are required to preserve role and tool ID,
	// but are allowed to omit ToolName.
	message.ToolName = ""
	// Tool calls belong to assistant messages and do not participate in a
	// tool-result fingerprint. Clearing them also keeps unexpected payloads
	// bounded.
	message.ToolCalls = nil
	message.Content = digestText(message.Content)
	message.ReasoningContent = digestText(message.ReasoningContent)
	message.ReasoningSignature = digestText(message.ReasoningSignature)
	if len(message.ContentParts) == 0 {
		return message
	}
	parts := make([]model.ContentPart, len(message.ContentParts))
	for i, part := range message.ContentParts {
		parts[i] = boundedContentPart(part)
	}
	message.ContentParts = parts
	return message
}

func boundedContentPart(part model.ContentPart) model.ContentPart {
	if part.Text != nil {
		text := digestText(*part.Text)
		part.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		image.Data = digestBytes(image.Data)
		part.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = digestBytes(audio.Data)
		part.Audio = &audio
	}
	if part.Video != nil {
		video := *part.Video
		video.Data = digestBytes(video.Data)
		part.Video = &video
	}
	if part.File != nil {
		file := *part.File
		file.Data = digestBytes(file.Data)
		part.File = &file
	}
	return part
}

func digestText(value string) string {
	if value == "" {
		return ""
	}
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, value)
	return strconv.Itoa(len(value)) + ":" + hex.EncodeToString(hasher.Sum(nil))
}

func digestBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	digest := sha256.Sum256(value)
	return digest[:]
}
