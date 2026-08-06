//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fakemodel provides a deterministic, no-network way to drive the PromptIter
// regression-loop example so it runs with no API key and produces reproducible scores.
//
// Two pieces cooperate:
//
//   - ScriptedModel implements model.Model. It answers the candidate agent as a pure function
//     of (system instruction, user input), so swapping the instruction deterministically changes
//     the candidate's answers — and therefore its evaluation score.
//   - DeterministicBackwarder / DeterministicAggregator / DeterministicOptimizer implement the
//     PromptIter engine collaborator interfaces without any model. The optimizer proposes the
//     next instruction from a fixture-defined transition table, so the whole optimize loop is
//     reproducible. This mirrors the engine's own unit-test strategy (stub collaborators) while
//     still exercising the real engine loop.
//
// The scenarios (optimization succeeds / is ineffective / regresses the validation set) live in
// the fixture data, not in code.
package fakemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Fixture is the on-disk deterministic script for one example app.
type Fixture struct {
	// Candidate scripts the candidate agent's answers.
	Candidate CandidateScript `json:"candidate"`
	// Optimizer scripts the instruction proposed each optimization round.
	Optimizer OptimizerScript `json:"optimizer"`
}

// CandidateScript maps (instruction, input) signals to a fixed answer.
type CandidateScript struct {
	// Answers are evaluated in order; the first matching rule wins.
	Answers []AnswerRule `json:"answers"`
	// Default is returned when no rule matches.
	Default string `json:"default"`
}

// AnswerRule matches by substring on the system instruction and the user input.
// An empty match field matches anything.
type AnswerRule struct {
	InstructionContains string `json:"instructionContains"`
	InputContains       string `json:"inputContains"`
	Answer              string `json:"answer"`
}

// OptimizerScript defines how the deterministic optimizer rewrites the instruction.
type OptimizerScript struct {
	// Transitions are evaluated in order against the current instruction; first match wins.
	Transitions []Transition `json:"transitions"`
}

// Transition proposes a replacement instruction when the current instruction matches FromContains.
type Transition struct {
	// FromContains matches the current instruction by substring; empty matches anything.
	FromContains string `json:"fromContains"`
	// ToInstruction is the proposed replacement instruction text.
	ToInstruction string `json:"toInstruction"`
	// Reason explains why the patch is proposed (required by the engine's optimizer contract).
	Reason string `json:"reason"`
}

// LoadFixture reads and validates a fixture JSON file. Unknown fields are rejected so a misspelled
// key (e.g. "inputContain" instead of "inputContains") surfaces as an error instead of being
// silently ignored and producing a fixture that behaves unexpectedly. Trailing data after the first
// JSON value is also rejected, so a duplicated/concatenated document does not silently load only its
// first half.
func LoadFixture(path string) (*Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var fixture Fixture
	if err := dec.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse fixture %s: unexpected trailing data after JSON value", path)
	}
	return &fixture, nil
}

// ScriptedModel is a deterministic model.Model driven by a CandidateScript.
type ScriptedModel struct {
	name   string
	script CandidateScript
}

// NewScriptedModel builds a ScriptedModel with the given identifier and script.
func NewScriptedModel(name string, script CandidateScript) *ScriptedModel {
	return &ScriptedModel{name: name, script: script}
}

// GenerateContent returns the scripted answer for the request's instruction and input.
func (m *ScriptedModel) GenerateContent(_ context.Context, request *model.Request) (<-chan *model.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	instruction, input := extractInstructionAndInput(request.Messages)
	answer := m.script.resolve(instruction, input)
	stop := "stop"
	response := &model.Response{
		ID:     "fake-" + m.name,
		Object: model.ObjectTypeChatCompletion,
		Model:  m.name,
		Done:   true,
		Choices: []model.Choice{
			{
				Index:        0,
				Message:      model.Message{Role: model.RoleAssistant, Content: answer},
				FinishReason: &stop,
			},
		},
		Usage: &model.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0},
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

// Info returns the model identity.
func (m *ScriptedModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// resolve returns the first matching answer, or the default.
func (s CandidateScript) resolve(instruction, input string) string {
	for _, rule := range s.Answers {
		if rule.InstructionContains != "" && !strings.Contains(instruction, rule.InstructionContains) {
			continue
		}
		if rule.InputContains != "" && !strings.Contains(input, rule.InputContains) {
			continue
		}
		return rule.Answer
	}
	return s.Default
}

// extractInstructionAndInput joins system message content (the instruction surface) and returns
// the last user message content (the eval case input). If the flow does not emit a distinct
// system message, it falls back to all message content so instruction markers are still visible.
func extractInstructionAndInput(messages []model.Message) (instruction, input string) {
	var systemParts, allParts []string
	for _, msg := range messages {
		if msg.Content != "" {
			allParts = append(allParts, msg.Content)
		}
		switch msg.Role {
		case model.RoleSystem:
			if msg.Content != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case model.RoleUser:
			if msg.Content != "" {
				input = msg.Content
			}
		}
	}
	instruction = strings.Join(systemParts, "\n")
	if instruction == "" {
		instruction = strings.Join(allParts, "\n")
	}
	return instruction, input
}
