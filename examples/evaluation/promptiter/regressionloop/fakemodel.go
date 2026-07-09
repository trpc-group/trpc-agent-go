//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// scriptedModel is a deterministic model that never calls a real API. It maps
// one request to one static (or request-derived) assistant content, so the full
// PromptIter loop can run with no API key.
type scriptedModel struct {
	name    string
	respond func(*model.Request) (string, error)
}

// GenerateContent returns one non-streaming response built from respond.
func (m *scriptedModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("scripted model: request is nil")
	}
	content, err := m.respond(request)
	if err != nil {
		return nil, err
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:     "scripted-" + m.name,
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			},
		},
	}
	close(ch)
	return ch, nil
}

// Info identifies this scripted model instance.
func (m *scriptedModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// newStaticModel returns a scripted model that ignores the request and always
// replies with content. Used for the backward / aggregate / optimize workers.
func newStaticModel(name, content string) model.Model {
	return &scriptedModel{
		name: name,
		respond: func(*model.Request) (string, error) {
			return content, nil
		},
	}
}

// newCandidateModel returns the deterministic candidate model. Its answer
// depends on whether the system instruction carries the optimizer marker: under
// the optimized instruction it draws from optimizedGolds, otherwise from
// baselineGolds. A token miss yields the poor answer (a fail). Partitioning the
// two gold sets lets one model express every scenario — success (baseline empty,
// optimized full), ineffective (both empty), and overfit (a case that passes at
// baseline but not after optimization).
func newCandidateModel(name, marker, poor string, baselineGolds, optimizedGolds map[string]string) model.Model {
	baselineTokens := sortedTokens(baselineGolds)
	optimizedTokens := sortedTokens(optimizedGolds)
	return &scriptedModel{
		name: name,
		respond: func(request *model.Request) (string, error) {
			if strings.Contains(systemText(request), marker) {
				return lookupGold(userText(request), optimizedTokens, optimizedGolds, poor), nil
			}
			return lookupGold(userText(request), baselineTokens, baselineGolds, poor), nil
		},
	}
}

// sortedTokens returns gold keys sorted longest-first for deterministic,
// collision-safe substring matching (map iteration order is random).
func sortedTokens(golds map[string]string) []string {
	tokens := make([]string, 0, len(golds))
	for token := range golds {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i]) != len(tokens[j]) {
			return len(tokens[i]) > len(tokens[j])
		}
		return tokens[i] < tokens[j]
	})
	return tokens
}

// lookupGold returns the gold whose token is contained in user, else poor.
func lookupGold(user string, tokens []string, golds map[string]string, poor string) string {
	for _, token := range tokens {
		if strings.Contains(user, token) {
			return golds[token]
		}
	}
	return poor
}

// systemText concatenates all system-role message contents in the request.
func systemText(request *model.Request) string {
	var b strings.Builder
	for _, message := range request.Messages {
		if message.Role == model.RoleSystem {
			b.WriteString(message.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// userText concatenates all user-role message contents in the request.
func userText(request *model.Request) string {
	var b strings.Builder
	for _, message := range request.Messages {
		if message.Role == model.RoleUser {
			b.WriteString(message.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}
