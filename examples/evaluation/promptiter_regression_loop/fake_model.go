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
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// fakeRole distinguishes the three model consumers so a single fakeModel type can
// emit role-appropriate deterministic output.
type fakeRole int

const (
	fakeRoleCandidate fakeRole = iota
	fakeRoleJudge
	fakeRoleWorker
)

// fakeScenario selects which deterministic outcome the fake optimizer drives the
// regression loop toward. It lets us exercise all three gate paths without an API:
//   - happy:      optimization succeeds and the candidate is accepted (gain met)
//   - no-gain:    optimization yields no measurable improvement (candidate rejected)
//   - regression: optimization regresses on the validation set (candidate rejected)
type fakeScenario string

const (
	scenarioHappy      fakeScenario = "happy"
	scenarioNoGain     fakeScenario = "no-gain"
	scenarioRegression fakeScenario = "regression"
)

func parseScenario(s string) fakeScenario {
	switch fakeScenario(s) {
	case scenarioNoGain, scenarioRegression:
		return fakeScenario(s)
	default:
		return scenarioHappy
	}
}

// fakeModel is a deterministic, offline model implementation. It requires no API
// key and always returns the same output for a given request, which lets the whole
// regression loop run end-to-end in a sandbox.
type fakeModel struct {
	name     string
	role     fakeRole
	scenario fakeScenario
}

func newFakeModel(name string, role fakeRole, scenario fakeScenario) *fakeModel {
	return &fakeModel{name: name, role: role, scenario: scenario}
}

// Info implements model.Model.
func (m *fakeModel) Info() model.Info {
	return model.Info{Name: m.name, ContextWindow: 128000}
}

// GenerateContent implements model.Model by returning a single buffered response.
func (m *fakeModel) GenerateContent(_ context.Context, request *model.Request) (<-chan *model.Response, error) {
	resp := m.generate(request)
	ch := make(chan *model.Response, 1)
	ch <- resp
	close(ch)
	return ch, nil
}

func (m *fakeModel) generate(request *model.Request) *model.Response {
	content := m.respond(request)
	stop := "stop"
	return &model.Response{
		Model: m.name,
		Done:  true,
		Choices: []model.Choice{
			{
				FinishReason: &stop,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			},
		},
	}
}

func (m *fakeModel) respond(request *model.Request) string {
	system := systemText(request)
	userText := lastUserText(request)

	// Structured output requests are routed by their schema name. This covers the
	// judge (rubric_critic_scores) and the three PromptIter worker agents.
	if request.StructuredOutput != nil && request.StructuredOutput.JSONSchema != nil {
		return m.structured(request, system, userText)
	}

	switch m.role {
	case fakeRoleCandidate:
		return m.candidateResponse(system, userText)
	case fakeRoleJudge:
		// The judge is only ever invoked with a structured output schema; if it is
		// somehow called without one, return a neutral pass.
		return m.judgeFromText(userText)
	default:
		return "{}"
	}
}

func (m *fakeModel) structured(request *model.Request, system, userText string) string {
	switch request.StructuredOutput.JSONSchema.Name {
	case "rubric_critic_scores":
		return m.judgeFromText(userText)
	case "BackwardResult":
		return m.backwardFrom(request)
	case "aggregatedGradientProposal":
		return m.aggregatedFrom(request)
	case "surfacePatchProposal":
		return m.optimizerFrom(request)
	default:
		return "{}"
	}
}

// ---------------------------------------------------------------------------
// Candidate
// ---------------------------------------------------------------------------

func (m *fakeModel) candidateResponse(system, userText string) string {
	topic := firstLine(userText)
	// The candidate recognises the instruction it was given and adapts its output:
	//   - an instruction asking for 【数据面板】/【战术分析】 sections  -> high quality
	//   - an instruction asking for a terse "极简" summary               -> degraded
	//   - anything else (including the baseline instruction)            -> plain
	// This makes the score fully determined by what the optimizer produced.
	switch {
	case strings.Contains(system, "数据面板") || strings.Contains(system, "战术分析"):
		return boostedCommentary(topic)
	case strings.Contains(system, "极简") || strings.Contains(system, "退化"):
		return degradedCommentary(topic)
	default:
		return baselineCommentary(topic)
	}
}

func baselineCommentary(topic string) string {
	return fmt.Sprintf("关于%s，本场比赛十分精彩，双方球员都拼尽全力，最终比分非常接近，主场球队表现更胜一筹并取得了胜利。", clip(topic, 24))
}

func boostedCommentary(topic string) string {
	t := clip(topic, 24)
	return fmt.Sprintf(`【战报】
关于%s，本场比赛双方展开激烈对决。主场作战的一方凭借稳健的防守与高效转换进攻，逐渐掌握场上节奏，并在第三节中段拉开分差，最终将优势保持到终场。

【数据面板】
核心球员砍下高分并送出多次助攻，篮板与抢断数据同样亮眼；替补席贡献了关键火力，整体命中率保持稳定，三分线外的效率尤为突出，罚球线上也几乎没有失误。

【战术分析】
主队通过挡拆外弹与弱侧转移不断制造空位，防守端则采用包夹持球人的策略限制对手核心的发挥。关键时刻的轮转、协防与篮板保护，成为决定胜负的胜负手，也为后续回合奠定了心理优势。`, t)
}

// degradedCommentary is a deliberately poor response used by the regression
// scenario: it is short, off-structure, and tagged so the fake judge scores it 0.
func degradedCommentary(topic string) string {
	return fmt.Sprintf("【退化】关于%s的比赛，简单说一句：主队赢了，比赛就这样。", clip(topic, 24))
}

// ---------------------------------------------------------------------------
// Judge (llm_rubric_critic)
// ---------------------------------------------------------------------------

var (
	rubricIDRe   = regexp.MustCompile(`(?m)^([A-Za-z0-9_\-]+):\s`)
	finalAnswerRe = regexp.MustCompile(`(?is)<final_answer>(.*?)</final_answer>`)
)

func (m *fakeModel) judgeFromText(userText string) string {
	ids := extractRubricIDs(userText)
	if len(ids) == 0 {
		ids = []string{"1"}
	}
	finalAnswer := ""
	if m2 := finalAnswerRe.FindStringSubmatch(userText); m2 != nil {
		finalAnswer = m2[1]
	}
	// Prefer the explicit candidate answer; fall back to the whole message.
	text := finalAnswer
	if strings.TrimSpace(text) == "" {
		text = userText
	}
	q := judgeQuality(text)
	var scores []rubricScore
	for _, id := range ids {
		scores = append(scores, rubricScore{ID: id, Score: q, Reason: judgeReason(q)})
	}
	out := rubricOutput{RubricScores: scores}
	b, _ := json.Marshal(out)
	return string(b)
}

type rubricScore struct {
	ID     string  `json:"id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

type rubricOutput struct {
	RubricScores []rubricScore `json:"rubricScores"`
}

// judgeQuality derives a deterministic 0/0.5/1.0 score from the candidate response
// the judge actually sees: responses with 【数据面板】 score high, tagged 【退化】
// responses score low, and everything else scores mid. This keeps the judge score
// perfectly aligned with what the candidate produced.
func judgeQuality(text string) float64 {
	if strings.Contains(text, "【数据面板】") {
		return 1.0
	}
	if strings.Contains(text, "【退化】") {
		return 0.0
	}
	return 0.5
}

func judgeReason(q float64) string {
	switch q {
	case 1.0:
		return "回复结构完整、数据详实，满足评分维度。"
	case 0.0:
		return "回复严重缺失，未满足评分维度。"
	default:
		return "回复基本完整但信息密度不足。"
	}
}

func extractRubricIDs(text string) []string {
	matches := rubricIDRe.FindAllStringSubmatch(text, -1)
	seen := make(map[string]struct{})
	var ids []string
	for _, m := range matches {
		id := m[1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// ---------------------------------------------------------------------------
// Worker agents (backwarder / aggregator / optimizer)
// ---------------------------------------------------------------------------

var surfaceIDRe = regexp.MustCompile(`"SurfaceID":\s*"([^"]+)"`)

func (m *fakeModel) backwardFrom(request *model.Request) string {
	surfaceID := ""
	if m2 := surfaceIDRe.FindStringSubmatch(joinMessages(request)); m2 != nil {
		surfaceID = m2[1]
	}
	if surfaceID == "" {
		surfaceID = "responder"
	}
	out := backwardResult{
		Gradients: []gradientItem{
			{
				SurfaceID: surfaceID,
				Severity:  "high",
				Gradient:  "候选回复应补充【数据面板】与【战术分析】结构化板块，并保证字数在 350-850 之间，以提升信息密度与可读性。",
			},
		},
		Upstream: []string{},
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (m *fakeModel) aggregatedFrom(_ *model.Request) string {
	out := aggregatedGradientProposal{
		Gradients: []gradientProposal{
			{
				Severity: "high",
				Gradient: "候选回复应补充【数据面板】与【战术分析】结构化板块，并保证字数在 350-850 之间。",
			},
		},
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (m *fakeModel) optimizerFrom(_ *model.Request) string {
	var instruction, reason string
	switch m.scenario {
	case scenarioNoGain:
		// A neutral instruction that triggers no behaviour change in the candidate,
		// so the candidate scores the same as baseline and the gate rejects (no gain).
		instruction = "你是一名体育评论员。请照常撰写比赛评论即可，无需额外结构。"
		reason = "聚合梯度不显著，保持原指令以观测是否有自然增益。"
	case scenarioRegression:
		// An instruction that drives the candidate into its degraded branch, so the
		// candidate scores below baseline and the gate rejects (regression).
		instruction = "你是一名体育评论员。请用极简一句话概括比赛，不要展开任何细节。"
		reason = "为演示回归守卫，故意给出会劣化输出的指令。"
	default: // scenarioHappy
		instruction = "你是一名资深体育评论员。请生成一篇结构清晰、数据详实的中文战报，" +
			"必须包含【战报】【数据面板】【战术分析】三个板块，字数控制在 350-850 字。"
		reason = "根据聚合梯度，补充结构化板块约束可显著提升评测分数。"
	}
	out := surfacePatchProposal{
		Value:  surfaceValue{Text: instruction},
		Reason: reason,
	}
	b, _ := json.Marshal(out)
	return string(b)
}

type backwardResult struct {
	Gradients []gradientItem `json:"Gradients"`
	Upstream  []string       `json:"Upstream"`
}

type gradientItem struct {
	SurfaceID string `json:"SurfaceID"`
	Severity  string `json:"Severity"`
	Gradient  string `json:"Gradient"`
}

type aggregatedGradientProposal struct {
	Gradients []gradientProposal `json:"Gradients"`
}

type gradientProposal struct {
	Severity string `json:"Severity"`
	Gradient string `json:"Gradient"`
}

type surfacePatchProposal struct {
	Value  surfaceValue `json:"Value"`
	Reason string       `json:"Reason"`
}

type surfaceValue struct {
	Text string `json:"Text"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func systemText(request *model.Request) string {
	for _, msg := range request.Messages {
		if msg.Role == model.RoleSystem {
			return msg.Content
		}
	}
	return ""
}

func lastUserText(request *model.Request) string {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == model.RoleUser {
			return request.Messages[i].Content
		}
	}
	return ""
}

func joinMessages(request *model.Request) string {
	var sb strings.Builder
	for _, msg := range request.Messages {
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexAny(text, "\n\r"); idx >= 0 {
		text = text[:idx]
	}
	return text
}

func clip(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max])
}
