//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type accumulator struct {
	mu                    sync.Mutex
	finalized             bool
	capturedRunInputs     bool
	hasUserContent        bool
	userContent           model.Message
	hasFinalResponse      bool
	finalResponse         model.Message
	hasRunError           bool
	runError              model.ResponseError
	sessionInputState     map[string]any
	contextMessages       []model.Message
	tools                 []*evalset.Tool
	toolIDIdx             map[string]int
	intermediateResponses []model.Message
}

func newAccumulator() *accumulator {
	return &accumulator{
		toolIDIdx: make(map[string]int),
	}
}

func (a *accumulator) captureRunInputs(runtimeState map[string]any, contextMessages []model.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized || a.capturedRunInputs {
		return
	}
	a.sessionInputState = cloneStateMap(runtimeState)
	a.contextMessages = cloneValue("context messages", contextMessages)
	a.capturedRunInputs = true
}

func (a *accumulator) setUserContent(msg model.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	if a.hasUserContent {
		return
	}
	if !model.HasPayload(msg) {
		return
	}
	a.userContent = cloneValue("user content", msg)
	a.hasUserContent = true
}

func (a *accumulator) setFinalResponse(msg model.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	if !model.HasPayload(msg) {
		return
	}
	a.finalResponse = cloneValue("final response", msg)
	a.hasFinalResponse = true
}

func (a *accumulator) addIntermediateResponse(msg model.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	if msg.Role != model.RoleAssistant {
		return
	}
	if !model.HasPayload(msg) {
		return
	}
	a.intermediateResponses = append(a.intermediateResponses, cloneValue("intermediate response", msg))
}

func (a *accumulator) setRunError(err model.ResponseError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	a.runError = err
	a.hasRunError = true
}

func (a *accumulator) addToolCall(tc model.ToolCall) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	if tc.ID == "" {
		return
	}
	if _, exists := a.toolIDIdx[tc.ID]; exists {
		return
	}
	tool := &evalset.Tool{
		ID:        tc.ID,
		Name:      tc.Function.Name,
		Arguments: parseToolCallArguments(tc.Function.Arguments),
	}
	a.tools = append(a.tools, tool)
	a.toolIDIdx[tc.ID] = len(a.tools) - 1
}

func (a *accumulator) addToolResult(toolID, toolName, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	if toolID == "" {
		return
	}
	value := parseToolResultContent(content)
	if idx, ok := a.toolIDIdx[toolID]; ok {
		a.tools[idx].Result = value
		if a.tools[idx].Name == "" && toolName != "" {
			a.tools[idx].Name = toolName
		}
		return
	}
	tool := &evalset.Tool{
		ID:     toolID,
		Name:   toolName,
		Result: value,
	}
	a.tools = append(a.tools, tool)
	a.toolIDIdx[toolID] = len(a.tools) - 1
}

func (a *accumulator) isFinalized() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.finalized
}

func parseToolCallArguments(arguments []byte) any {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	err := json.Unmarshal([]byte(trimmed), &value)
	if err == nil {
		return value
	}
	log.Warnf("evalset recorder: parse tool call arguments as json failed: %v", err)
	return string(arguments)
}

func parseToolResultContent(content string) any {
	if content == "" {
		return ""
	}
	var value any
	err := json.Unmarshal([]byte(content), &value)
	if err == nil {
		return value
	}
	log.Warnf("evalset recorder: parse tool result content as json failed: %v", err)
	return content
}

type turnSnapshot struct {
	finalized             bool
	hasUserContent        bool
	userContent           model.Message
	hasFinalResponse      bool
	finalResponse         model.Message
	hasRunError           bool
	runError              model.ResponseError
	sessionInputState     map[string]any
	contextMessages       []model.Message
	tools                 []*evalset.Tool
	intermediateResponses []model.Message
}

func (a *accumulator) finalizeAndSnapshot() turnSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.finalized {
		a.finalized = true
	}
	s := turnSnapshot{
		finalized:             a.finalized,
		hasUserContent:        a.hasUserContent,
		userContent:           a.userContent,
		hasFinalResponse:      a.hasFinalResponse,
		finalResponse:         a.finalResponse,
		hasRunError:           a.hasRunError,
		runError:              a.runError,
		sessionInputState:     a.sessionInputState,
		contextMessages:       a.contextMessages,
		intermediateResponses: append([]model.Message(nil), a.intermediateResponses...),
	}
	if len(a.tools) > 0 {
		s.tools = cloneValue("tools", a.tools)
	}
	return s
}

func cloneStateMap(state map[string]any) map[string]any {
	if len(state) == 0 {
		return map[string]any{}
	}
	copied := make(map[string]any, len(state))
	for key, value := range state {
		copied[key] = normalizeStateValue(value)
	}
	return copied
}

func cloneValue[T any](name string, value T) T {
	payload, err := json.Marshal(value)
	if err != nil {
		log.Warnf("evalset recorder: clone %s failed: %v", name, err)
		return value
	}
	var cloned T
	if err := json.Unmarshal(payload, &cloned); err != nil {
		log.Warnf("evalset recorder: decode cloned %s failed: %v", name, err)
		return value
	}
	return cloned
}

func normalizeStateValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		log.Warnf("evalset recorder: normalize runtime state value failed: %v", err)
		return fmt.Sprint(value)
	}
	var normalized any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		log.Warnf("evalset recorder: decode normalized runtime state value failed: %v", err)
		return string(payload)
	}
	return normalized
}
