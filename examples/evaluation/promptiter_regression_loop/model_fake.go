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
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Stage markers embedded in the candidate instruction. The scripted fake model
// reads the active marker and switches its output behaviour, which lets the
// example deterministically exercise the three required scenarios:
//   - STAGE_GOOD: instruction that generalises (train and validation both pass)
//   - STAGE_OVERFIT: instruction that improves train but regresses validation
//   - STAGE_INEFFECTIVE: instruction that does not improve anything
const (
	stageGood        = "[STAGE_GOOD]"
	stageOverfit     = "[STAGE_OVERFIT]"
	stageIneffective = "[STAGE_INEFFECTIVE]"
)

// optimizer markers are the instruction strings the scripted optimizer returns
// per call (one call per optimization round for the single target surface).
var optimizerPlan = []string{
	stageGood + " 严格输出包含 headline 与 source 字段的 JSON 对象,headline 必须原样使用输入中的 headline 字段",
	stageOverfit + " 仅对训练样本原样输出 headline 字段,验证样本一律输出固定文案",
	stageIneffective + " 无论输入是什么都输出同一句固定文案",
}

// fakeModel is a fully deterministic, scripted implementation of model.Model.
// It inspects the outgoing request and returns a fixed response for every role
// in the pipeline (candidate, backwarder, aggregator, optimizer), so the whole
// Evaluation + Optimization loop runs without any real model API key.
type fakeModel struct {
	// name is reported through Info and recorded in the audit report.
	name string

	// optimizerCalls counts how many optimizer requests have been served. It
	// selects which instruction the scripted optimizer proposes next, which is
	// exactly one step per optimization round.
	optimizerCalls int

	// calls is the total number of GenerateContent invocations, used for the
	// gate's model-call budget check.
	calls int

	mu sync.Mutex
}

func newFakeModel(name string) *fakeModel {
	return &fakeModel{name: name}
}

// Info implements model.Model.
func (m *fakeModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// GenerateContent implements model.Model and returns one scripted response.
func (m *fakeModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	content := m.respond(request)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		// Done marks the response as final; without it the flow treats every
		// response as incomplete and re-invokes the model forever.
		Done: true,
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

// CallCount returns the total number of GenerateContent invocations.
func (m *fakeModel) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// respond dispatches to the role detected from the request content.
func (m *fakeModel) respond(request *model.Request) string {
	text := requestText(request)
	switch {
	case strings.Contains(text, "Optimize one PromptIter surface"):
		return m.optimizerResponse()
	case strings.Contains(text, "Aggregate PromptIter gradients"):
		return aggregatorResponse()
	case strings.Contains(text, "Compute PromptIter backward attribution"):
		return backwarderResponse()
	default:
		return m.candidateResponse(text)
	}
}

// optimizerResponse returns the JSON proposal for the next optimization round.
func (m *fakeModel) optimizerResponse() string {
	m.mu.Lock()
	index := m.optimizerCalls
	m.optimizerCalls++
	m.mu.Unlock()
	if index < 0 || index >= len(optimizerPlan) {
		index = len(optimizerPlan) - 1
	}
	instruction := optimizerPlan[index]
	proposal, err := json.Marshal(struct {
		Value  map[string]string `json:"Value"`
		Reason string            `json:"Reason"`
	}{
		Value:  map[string]string{"Text": instruction},
		Reason: "scripted optimizer proposes the next deterministic candidate instruction",
	})
	if err != nil {
		return `{"Value":{"text":""},"Reason":"scripted optimizer failure"}`
	}
	return string(proposal)
}

// backwarderResponse returns a valid backwarder JSON result that attributes the
// failure to the single candidate instruction surface.
func backwarderResponse() string {
	return `{"Gradients":[{"SurfaceID":"candidate#instruction","Severity":"P1","Gradient":"final output does not satisfy the metric requirements"}],"Upstream":[]}`
}

// aggregatorResponse returns a valid aggregator JSON result for the single surface.
func aggregatorResponse() string {
	return `{"Gradients":[{"Severity":"P1","Gradient":"final output does not satisfy the metric requirements"}]}`
}

// candidateResponse produces the scripted candidate answer for one eval case.
//
// The candidate reads the case input JSON (which carries the case id, the split
// marker and the gold headline/source), then returns a card whose correctness
// depends on the active instruction stage. The stage mix is what makes the
// example deterministically exercise the three required scenarios while keeping
// the PromptIter engine busy (the optimizer only runs while the train set still
// has failures to fix):
//   - STAGE_GOOD: fixes the full validation set and train_01, but leaves
//     train_02/train_03 degraded so the next round still has gradients.
//   - STAGE_OVERFIT: fixes every train case while degrading all validation
//     cases except the key case, so validation regresses against the accepted
//     baseline (overfitting scenario).
//   - STAGE_INEFFECTIVE / baseline: a plain text line that is neither valid
//     JSON nor an exact match.
func (m *fakeModel) candidateResponse(text string) string {
	input := parseCaseInput(text)
	switch {
	case strings.Contains(text, stageGood):
		if input.split == "validation" || strings.HasPrefix(input.caseID, "train_01") {
			return buildCard(input.headline, input.source)
		}
		return buildCard(degradedHeadline, degradedSource)
	case strings.Contains(text, stageOverfit):
		if input.split == "train" || strings.HasPrefix(input.caseID, "validation_02") {
			return buildCard(input.headline, input.source)
		}
		return buildCard(degradedHeadline, degradedSource)
	default:
		return "头条:体育赛事迎来激烈对决,比赛过程跌宕起伏,最终分出胜负。"
	}
}

const (
	// degradedHeadline and degradedSource are the fixed degraded card values
	// used by the scripted candidate when an instruction stage does not cover
	// the current split/case.
	degradedHeadline = "固定文案头条"
	degradedSource   = "固定来源"
)

// caseInput is the parsed eval-case task JSON handed to the candidate.
type caseInput struct {
	caseID   string
	split    string
	headline string
	source   string
}

// parseCaseInput extracts the candidate task fields from the request text.
func parseCaseInput(text string) caseInput {
	// The user content is the compact JSON object; locate its opening brace and
	// parse it independently of surrounding instruction text.
	start := strings.Index(text, "{")
	if start < 0 {
		return caseInput{}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text[start:]), &raw); err != nil {
		return caseInput{}
	}
	out := caseInput{}
	if v, ok := raw["caseId"].(string); ok {
		out.caseID = v
	}
	if v, ok := raw["split"].(string); ok {
		out.split = v
	}
	if v, ok := raw["headline"].(string); ok {
		out.headline = v
	}
	if v, ok := raw["source"].(string); ok {
		out.source = v
	}
	return out
}

// buildCard renders the canonical JSON card string. Field order and spacing must
// match the references recorded in the evalset files for exact matching.
func buildCard(headline, source string) string {
	card, err := json.Marshal(struct {
		Headline string `json:"headline"`
		Source   string `json:"source"`
	}{Headline: headline, Source: source})
	if err != nil {
		return fmt.Sprintf(`{"headline":%q,"source":%q}`, headline, source)
	}
	return string(card)
}

// requestText joins every message content in the request for role detection.
func requestText(request *model.Request) string {
	if request == nil {
		return ""
	}
	parts := make([]string, 0, len(request.Messages))
	for _, msg := range request.Messages {
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n")
}
