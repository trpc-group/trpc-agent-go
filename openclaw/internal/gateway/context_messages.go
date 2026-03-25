//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
)

const personaContextHeader = "Active preset persona for this chat:"

func (s *Server) injectedContextMessages(
	ctx context.Context,
	userID string,
	sessionID string,
	requestSystemPrompt string,
) []model.Message {
	out := make([]model.Message, 0, 4)
	if msg := requestSystemPromptMessage(requestSystemPrompt); msg != nil {
		out = append(out, *msg)
	}
	if msg := s.personaContextMessage(
		userID,
		sessionID,
	); msg != nil {
		out = append(out, *msg)
	}
	out = append(out, s.memoryFileContextMessages(ctx, userID)...)
	out = append(out, s.uploadContextMessages(userID, sessionID)...)
	return out
}

func requestSystemPromptMessage(prompt string) *model.Message {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	msg := model.NewSystemMessage(prompt)
	return &msg
}

func (s *Server) personaContextMessage(
	userID string,
	sessionID string,
) *model.Message {
	if s == nil || s.personaStore == nil {
		return nil
	}

	scopeKey := persona.ScopeKeyFromSession(
		channelFromSessionID(sessionID),
		userID,
		sessionID,
	)
	if strings.TrimSpace(scopeKey) == "" {
		return nil
	}

	preset, err := s.personaStore.Get(scopeKey)
	if err != nil || preset.ID == persona.PresetDefault {
		return nil
	}
	if strings.TrimSpace(preset.Prompt) == "" {
		return nil
	}

	msg := model.NewSystemMessage(buildPersonaContextText(preset))
	return &msg
}

func buildPersonaContextText(preset persona.Preset) string {
	lines := []string{
		personaContextHeader,
		"- id: " + preset.ID,
		"- name: " + preset.Name,
		preset.Prompt,
	}
	return strings.Join(lines, "\n")
}

func (s *Server) memoryFileContextMessages(
	ctx context.Context,
	userID string,
) []model.Message {
	if s == nil || s.memoryFileStore == nil {
		return nil
	}

	appName := strings.TrimSpace(s.appName)
	if appName == "" {
		return nil
	}
	path, err := s.memoryFileStore.EnsureMemory(
		ctx,
		appName,
		userID,
	)
	if err != nil {
		return nil
	}
	text, err := s.memoryFileStore.ReadFile(
		path,
		memoryfile.ReadLimit,
	)
	if err != nil {
		return nil
	}
	content := memoryfile.BuildContextText(text)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return []model.Message{model.NewSystemMessage(content)}
}
