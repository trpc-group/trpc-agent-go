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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	scenarioImprovement = "improvement"
	scenarioNoop        = "noop"
	scenarioOverfit     = "overfit"
	scenarioMultiRound  = "multi_round"

	profileBaseline    = "[PROFILE:baseline]"
	profileGeneralized = "[PROFILE:generalized]"
	profileNoop        = "[PROFILE:noop]"
	profileOverfit     = "[PROFILE:overfit]"
)

var expectedAnswers = map[string]string{
	"train-weather-01":       `{"answer":"上海天气晴，25°C"}`,
	"train-distance-01":      `{"answer":"北京到天津约120公里"}`,
	"train-scope-01":         `{"answer":"城市信息服务每日09:00-18:00开放"}`,
	"validation-weather-01":  `{"answer":"广州天气多云，28°C"}`,
	"validation-distance-01": `{"answer":"苏州到杭州约160公里"}`,
	"validation-scope-01":    `{"answer":"紧急情况请拨打当地应急电话"}`,
}

type accountingRecorder struct {
	mu      sync.Mutex
	records []modelCallRecord
}

type modelCallRecord struct {
	Stage            string `json:"stage"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
	LatencyMS        int64  `json:"latencyMs"`
}

type accountingSummary struct {
	ModelCalls       int               `json:"modelCalls"`
	PromptTokens     int               `json:"promptTokens"`
	CompletionTokens int               `json:"completionTokens"`
	TotalTokens      int               `json:"totalTokens"`
	WallLatencyMS    int64             `json:"wallLatencyMs"`
	Cost             *float64          `json:"cost"`
	CostStatus       string            `json:"costStatus"`
	ByStage          []modelCallRecord `json:"byCall"`
}

func (r *accountingRecorder) add(record modelCallRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
}

func (r *accountingRecorder) summary(wallLatency time.Duration) accountingSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := accountingSummary{
		ModelCalls:    len(r.records),
		WallLatencyMS: wallLatency.Milliseconds(),
		CostStatus:    "not_configured",
		ByStage:       append([]modelCallRecord(nil), r.records...),
	}
	for _, record := range r.records {
		summary.PromptTokens += record.PromptTokens
		summary.CompletionTokens += record.CompletionTokens
		summary.TotalTokens += record.TotalTokens
	}
	return summary
}

type deterministicModel struct {
	kind            string
	scenario        string
	targetSurfaceID string
	recorder        *accountingRecorder
	mu              sync.RWMutex
	stage           string
	callCount       int
	optimizerCount  int
}

func newCandidateModel(scenario string, recorder *accountingRecorder) *deterministicModel {
	return &deterministicModel{kind: "candidate", scenario: scenario, recorder: recorder, stage: "evaluation"}
}

func newWorkerModel(scenario, surfaceID string, recorder *accountingRecorder) *deterministicModel {
	return &deterministicModel{kind: "worker", scenario: scenario, targetSurfaceID: surfaceID, recorder: recorder}
}

func (m *deterministicModel) Info() model.Info {
	return model.Info{Name: "deterministic-" + m.kind}
}

func (m *deterministicModel) setStage(stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stage = stage
}

func (m *deterministicModel) currentStage() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stage
}

func (m *deterministicModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	startedAt := time.Now()
	if request == nil {
		return nil, errors.New("deterministic model request is nil")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	prompt := joinMessages(request.Messages)
	var (
		responseMessage model.Message
		stage           string
		err             error
	)
	if m.kind == "candidate" {
		stage = m.currentStage()
		responseMessage, err = candidateMessage(prompt, request.Messages)
	} else {
		var content string
		content, stage, err = m.workerContent(prompt)
		responseMessage = model.NewAssistantMessage(content)
	}
	if err != nil {
		return nil, err
	}
	completionJSON, err := json.Marshal(responseMessage)
	if err != nil {
		return nil, fmt.Errorf("marshal deterministic response message: %w", err)
	}
	promptTokens := approximateTokens(prompt)
	completionTokens := approximateTokens(string(completionJSON))
	usage := &model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
	if m.recorder != nil {
		m.recorder.add(modelCallRecord{
			Stage:            stage,
			Model:            m.Info().Name,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			LatencyMS:        time.Since(startedAt).Milliseconds(),
		})
	}
	response := &model.Response{
		ID:      m.nextResponseID(),
		Object:  model.ObjectTypeChatCompletion,
		Model:   m.Info().Name,
		Done:    true,
		Usage:   usage,
		Choices: []model.Choice{{Message: responseMessage}},
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *deterministicModel) nextResponseID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	return fmt.Sprintf("fake-%s-%d", m.kind, m.callCount)
}

func candidateMessage(prompt string, messages []model.Message) (model.Message, error) {
	caseID := ""
	for id := range expectedAnswers {
		if strings.Contains(prompt, "case:"+id) {
			caseID = id
			break
		}
	}
	if caseID == "" {
		return model.Message{}, errors.New("candidate model could not identify fixture case")
	}
	correct, err := correctForProfile(prompt, caseID)
	if err != nil {
		return model.Message{}, err
	}
	if correct {
		if toolCall, ok := expectedToolCall(caseID); ok && !containsToolResponse(messages) {
			return model.Message{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{toolCall},
			}, nil
		}
		return model.NewAssistantMessage(expectedAnswers[caseID]), nil
	}
	if strings.Contains(prompt, profileOverfit) {
		return model.NewAssistantMessage(`{"answer":"仅适用于训练样本"}`), nil
	}
	return model.NewAssistantMessage(`{"answer":"信息不足"}`), nil
}

func correctForProfile(prompt, caseID string) (bool, error) {
	switch {
	case strings.Contains(prompt, profileGeneralized):
		return true, nil
	case strings.Contains(prompt, profileOverfit):
		return strings.HasPrefix(caseID, "train-"), nil
	case strings.Contains(prompt, profileBaseline), strings.Contains(prompt, profileNoop):
		switch caseID {
		case "train-weather-01", "validation-weather-01", "validation-scope-01":
			return true, nil
		default:
			return false, nil
		}
	default:
		return false, errors.New("candidate model could not identify prompt profile")
	}
}

func expectedToolCall(caseID string) (model.ToolCall, bool) {
	var name string
	var arguments string
	switch caseID {
	case "train-weather-01":
		name, arguments = "weather_lookup", `{"city":"Shanghai"}`
	case "validation-weather-01":
		name, arguments = "weather_lookup", `{"city":"Guangzhou"}`
	case "train-distance-01":
		name, arguments = "distance_lookup", `{"from":"Beijing","to":"Tianjin"}`
	case "validation-distance-01":
		name, arguments = "distance_lookup", `{"from":"Suzhou","to":"Hangzhou"}`
	default:
		return model.ToolCall{}, false
	}
	return model.ToolCall{
		Type: "function",
		ID:   "call-" + caseID,
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: []byte(arguments),
		},
	}, true
}

func containsToolResponse(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role == model.RoleTool {
			return true
		}
	}
	return false
}

func (m *deterministicModel) workerContent(prompt string) (string, string, error) {
	switch {
	case strings.Contains(prompt, "Compute PromptIter backward attribution"):
		content, err := m.backwardContent(prompt)
		return content, "promptiter.backward", err
	case strings.Contains(prompt, "Aggregate PromptIter gradients"):
		payload := map[string]any{
			"Gradients": []map[string]any{{"Severity": "P1", "Gradient": "make the response rules general and exact"}},
		}
		content, err := marshalJSON(payload)
		return content, "promptiter.aggregate", err
	case strings.Contains(prompt, "Optimize one PromptIter surface"):
		text := m.nextOptimizedInstruction()
		payload := map[string]any{
			"Value":  map[string]any{"Text": text},
			"Reason": "apply the deterministic regression scenario through PromptIter",
		}
		content, err := marshalJSON(payload)
		return content, "promptiter.optimize", err
	default:
		return "", "promptiter.unknown", errors.New("worker model received an unknown PromptIter request")
	}
}

func (m *deterministicModel) nextOptimizedInstruction() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.optimizerCount++
	if m.scenario == scenarioMultiRound && m.optimizerCount == 1 {
		return optimizedInstruction(scenarioNoop)
	}
	if m.scenario == scenarioMultiRound {
		return optimizedInstruction(scenarioImprovement)
	}
	return optimizedInstruction(m.scenario)
}

func (m *deterministicModel) backwardContent(prompt string) (string, error) {
	requestStart := strings.Index(prompt, "Request JSON:")
	if requestStart < 0 {
		return "", errors.New("backward request JSON marker is missing")
	}
	requestJSON, ok := strings.CutPrefix(prompt[requestStart+len("Request JSON:"):], "\n")
	if !ok {
		requestJSON = strings.TrimSpace(prompt[strings.Index(prompt, "Request JSON:")+len("Request JSON:"):])
	}
	var request struct {
		GradientSurfaces []struct {
			SurfaceID string
		}
		Predecessors []struct {
			StepID string
		}
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(requestJSON)), &request); err != nil {
		return "", fmt.Errorf("decode backward request: %w", err)
	}
	payload := map[string]any{"Gradients": []any{}, "Upstream": []any{}}
	if len(request.GradientSurfaces) > 0 {
		gradients := make([]map[string]any, 0, len(request.GradientSurfaces))
		for _, surface := range request.GradientSurfaces {
			if surface.SurfaceID == "" {
				continue
			}
			gradients = append(gradients, map[string]any{
				"SurfaceID": surface.SurfaceID,
				"Severity":  "P1",
				"Gradient":  "produce exact structured answers for unseen cases",
			})
		}
		payload["Gradients"] = gradients
	} else if len(request.Predecessors) > 0 {
		payload["Upstream"] = []map[string]any{{
			"PredecessorStepID": request.Predecessors[0].StepID,
			"Gradients":         []map[string]any{{"Severity": "P1", "Gradient": "propagate response mismatch"}},
		}}
	} else {
		return "", errors.New("backward request has neither gradient surfaces nor predecessors")
	}
	return marshalJSON(payload)
}

func optimizedInstruction(scenario string) string {
	base := "你是城市服务助手。对每个已知城市服务请求输出精确、规范化的 JSON 答案。\n"
	switch scenario {
	case scenarioNoop:
		return base + profileNoop
	case scenarioOverfit:
		return base + profileOverfit
	default:
		return base + profileGeneralized
	}
}

func joinMessages(messages []model.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(string(message.Role))
		builder.WriteByte(':')
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func approximateTokens(text string) int {
	count := utf8.RuneCountInString(text) / 4
	if count < 1 {
		return 1
	}
	return count
}

func marshalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
