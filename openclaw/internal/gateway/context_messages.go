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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
)

const personaContextHeader = "Active preset persona for this chat:"

func (s *Server) injectedContextMessages(
	userID string,
	sessionID string,
) []model.Message {
	out := make([]model.Message, 0, 2)
	if msg := s.personaContextMessage(
		userID,
		sessionID,
	); msg != nil {
		out = append(out, *msg)
	}
	out = append(out, s.uploadContextMessages(userID, sessionID)...)
	return out
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
