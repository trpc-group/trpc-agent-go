//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	llmReviewAppName   = "skills-code-review-agent"
	llmReviewAgentName = "code-review-agent"
	llmReviewUserID    = "review-user"
)

type LLMReviewConfig struct {
	TaskID       string
	DiffRaw      string
	ParsedDiff   ParsedDiff
	InputSummary DiffSummary
	RuleFindings []Finding
	FakeModel    bool
	Provider     string
	Model        string
	BaseURL      string
	Timeout      time.Duration
}

func RunLLMReview(ctx context.Context, cfg LLMReviewConfig) ([]Finding, error) {
	provider := normalizeModelProvider(cfg.Provider, cfg.FakeModel)
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	repo, err := skill.NewFSRepository(filepathJoin(exampleDir(), "skills"))
	if err != nil {
		return nil, fmt.Errorf("load skills repo: %w", err)
	}

	maxTokens := 2048
	temperature := 0.1
	var mdl model.Model
	switch provider {
	case "fake":
		mdl = newFakeReviewModel()
	default:
		modelName, opts, err := openAICompatibleModelOptions(provider, cfg.Model, cfg.BaseURL)
		if err != nil {
			return nil, err
		}
		mdl = openai.New(modelName, opts...)
	}
	agt := llmagent.New(
		llmReviewAgentName,
		llmagent.WithModel(mdl),
		llmagent.WithSkills(repo),
		llmagent.WithSkillToolProfile(llmagent.SkillToolProfileKnowledgeOnly),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
		llmagent.WithInstruction(llmReviewInstruction()),
		llmagent.WithDescription("Go code review agent using the code-review Skill."),
	)
	r := runner.NewRunner(
		llmReviewAppName,
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	events, err := r.Run(runCtx, llmReviewUserID, "review-"+cfg.TaskID, model.NewUserMessage(buildLLMReviewPrompt(cfg)))
	if err != nil {
		return nil, fmt.Errorf("run llm review agent: %w", err)
	}
	findings, err := parseLLMFindings(collectAssistantText(events))
	if err != nil {
		return nil, err
	}
	return validateLLMFindings(findings, cfg.ParsedDiff), nil
}

func normalizeModelProvider(provider string, fake bool) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if fake || provider == "" && fake {
		return "fake"
	}
	if provider == "" {
		return "openai"
	}
	switch provider {
	case "fake", "openai", "openai-compatible", "http", "deepseek":
		return provider
	default:
		return provider
	}
}

func openAICompatibleModelOptions(provider, modelName, baseURL string) (string, []openai.Option, error) {
	provider = normalizeModelProvider(provider, false)
	baseURL = firstNonEmpty(baseURL, os.Getenv("OPENAI_BASE_URL"))
	apiKey := os.Getenv("OPENAI_API_KEY")
	switch provider {
	case "openai":
		if strings.TrimSpace(modelName) == "" {
			modelName = "gpt-4o-mini"
		}
	case "openai-compatible", "http":
		if strings.TrimSpace(baseURL) == "" {
			return "", nil, fmt.Errorf("%s provider requires --model-base-url or OPENAI_BASE_URL", provider)
		}
		if strings.TrimSpace(modelName) == "" {
			modelName = "openai-compatible-review-model"
		}
	case "deepseek":
		baseURL = firstNonEmpty(baseURL, "https://api.deepseek.com")
		apiKey = firstNonEmpty(os.Getenv("DEEPSEEK_API_KEY"), apiKey)
		if strings.TrimSpace(modelName) == "" {
			modelName = "deepseek-chat"
		}
	default:
		return "", nil, fmt.Errorf("unsupported model provider %q; use fake, openai, openai-compatible, http, or deepseek", provider)
	}
	if strings.TrimSpace(apiKey) == "" {
		return "", nil, fmt.Errorf("%s provider requires OPENAI_API_KEY or provider-specific API key; use --fake-model or --model-provider fake for deterministic local testing", provider)
	}
	opts := []openai.Option{openai.WithAPIKey(apiKey)}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return modelName, opts, nil
}

func llmReviewInstruction() string {
	return strings.TrimSpace(`
You are a Go code review agent.

Workflow:
1. Load the "code-review" skill when rule references are needed.
2. Analyze the unified diff and the deterministic rule-engine findings provided by the user.
3. Return ONLY a JSON array of additional findings not already covered by rule findings.

Each finding object must include:
severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id.

Use source="llm". Severity must be critical, high, medium, or low. Confidence is 0.0-1.0.
If there are no additional issues, return [].
Do not wrap the JSON in markdown fences.
`)
}

func buildLLMReviewPrompt(cfg LLMReviewConfig) string {
	var b strings.Builder
	b.WriteString("Review this Go change set.\n\n")
	fmt.Fprintf(&b, "Input summary: files=%d go_files=%d added=%d deleted=%d\n\n",
		cfg.InputSummary.FilesChanged,
		cfg.InputSummary.GoFiles,
		cfg.InputSummary.AddedLines,
		cfg.InputSummary.DeletedLines,
	)
	if len(cfg.RuleFindings) > 0 {
		b.WriteString("Rule-engine findings already detected:\n")
		for _, f := range cfg.RuleFindings {
			fmt.Fprintf(&b, "- [%s] %s:%d %s (%s)\n",
				f.Severity, f.File, f.Line, f.Title, f.RuleID)
		}
		b.WriteString("\n")
	}
	b.WriteString("Unified diff:\n```diff\n")
	b.WriteString(redactSecrets(cfg.DiffRaw))
	b.WriteString("\n```\n")
	return b.String()
}

func collectAssistantText(events <-chan *event.Event) string {
	var last string
	for evt := range events {
		if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		msg := evt.Response.Choices[0].Message
		if msg.Role == model.RoleAssistant && strings.TrimSpace(msg.Content) != "" {
			last = msg.Content
		}
	}
	return last
}

func parseLLMFindings(content string) ([]Finding, error) {
	content = strings.TrimSpace(stripCodeFence(content))
	if content == "" {
		return nil, nil
	}
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		return nil, nil
	}
	var findings []Finding
	if err := json.Unmarshal([]byte(content[start:end+1]), &findings); err != nil {
		return nil, fmt.Errorf("decode llm findings: %w", err)
	}
	for i := range findings {
		findings[i].Source = "llm"
		findings[i].Evidence = redactSecrets(findings[i].Evidence)
		findings[i].Recommendation = redactSecrets(findings[i].Recommendation)
	}
	return findings, nil
}

func validateLLMFindings(items []Finding, pd ParsedDiff) []Finding {
	added := addedLineAnchors(pd)
	var out []Finding
	for _, f := range items {
		f.Source = "llm"
		if f.RuleID == "" {
			f.RuleID = "llm/supplemental"
		}
		if validLLMFinding(f, added) {
			out = append(out, f)
			continue
		}
		out = append(out, Finding{
			Severity:       SeverityLow,
			Category:       "llm_review",
			File:           f.File,
			Line:           f.Line,
			Title:          "LLM finding could not be anchored to the diff",
			Evidence:       redactSecrets(firstNonEmpty(f.Title, f.Evidence, "malformed or unanchored model finding")),
			Recommendation: "Review this model output manually before treating it as actionable.",
			Confidence:     0.50,
			Source:         "llm",
			RuleID:         "llm/malformed-finding",
		})
	}
	return out
}

func addedLineAnchors(pd ParsedDiff) map[string]map[int]bool {
	anchors := map[string]map[int]bool{}
	for _, h := range pd.Hunks {
		for _, line := range h.Lines {
			if line.Kind != '+' || line.NewLine <= 0 {
				continue
			}
			if anchors[h.File] == nil {
				anchors[h.File] = map[int]bool{}
			}
			anchors[h.File][line.NewLine] = true
		}
	}
	return anchors
}

func validLLMFinding(f Finding, added map[string]map[int]bool) bool {
	if !validSeverity(f.Severity) || f.Confidence < 0 || f.Confidence > 1 {
		return false
	}
	if strings.TrimSpace(f.Category) == "" ||
		strings.TrimSpace(f.File) == "" ||
		f.Line <= 0 ||
		strings.TrimSpace(f.Title) == "" ||
		strings.TrimSpace(f.Evidence) == "" ||
		strings.TrimSpace(f.Recommendation) == "" {
		return false
	}
	return added[f.File][f.Line]
}

func validSeverity(s Severity) bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	default:
		return false
	}
}

func stripCodeFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 3 {
		return content
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		return content
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func bucketSupplementalFindings(items []Finding) (findings, warnings, needsHuman []Finding) {
	for _, f := range items {
		if f.RuleID == "" {
			f.RuleID = "llm/supplemental"
		}
		if f.Source == "" {
			f.Source = "llm"
		}
		switch {
		case f.Confidence < 0.55:
			warnings = append(warnings, f)
		case f.Confidence < 0.70 || f.Severity == SeverityLow:
			needsHuman = append(needsHuman, f)
		default:
			findings = append(findings, f)
		}
	}
	return findings, warnings, needsHuman
}

type fakeReviewModel struct {
	step atomic.Int32
}

func newFakeReviewModel() *fakeReviewModel {
	return &fakeReviewModel{}
}

func (m *fakeReviewModel) Info() model.Info {
	return model.Info{Name: "cr-agent-fake-model"}
}

func (m *fakeReviewModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	step := m.step.Add(1)
	content := "[]"
	if step == 1 {
		content = `[{"severity":"medium","category":"testing","file":"service/handler.go","line":4,"title":"Fake model supplemental review item","evidence":"fake-model run through llmagent and code-review skill","recommendation":"Use a real model in production or keep this as deterministic CI coverage.","confidence":0.54,"source":"llm","rule_id":"llm/fake-model/supplemental"}]`
	}
	rsp := &model.Response{
		ID:      "fake-review",
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
		case ch <- rsp:
		}
	}()
	return ch, nil
}

func filepathJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathSeparator))
}
