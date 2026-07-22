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
	llmMaxDiffBytes    = 48 << 10
	llmMaxDiffChunks   = 4
	llmMaxRuleFindings = 32
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
		mdl = newFakeReviewModel(firstAddedAnchor(cfg.ParsedDiff))
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

	chunks, truncated := splitDiffForLLM(cfg.DiffRaw, llmMaxDiffBytes)
	var allFindings []Finding
	for i, chunk := range chunks {
		runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		events, err := r.Run(runCtx, llmReviewUserID, fmt.Sprintf("review-%s-%d", cfg.TaskID, i+1), model.NewUserMessage(buildLLMReviewPrompt(cfg, chunk, i+1, len(chunks), truncated)))
		cancel()
		if err != nil {
			return nil, fmt.Errorf("run llm review agent: %w", err)
		}
		findings, err := parseLLMFindings(collectAssistantText(events))
		if err != nil {
			return nil, err
		}
		allFindings = append(allFindings, findings...)
	}
	allFindings = validateLLMFindings(allFindings, cfg.ParsedDiff)
	if provider == "fake" && len(allFindings) == 0 {
		if anchor := firstAddedAnchor(cfg.ParsedDiff); anchor.file != "" && anchor.line > 0 {
			allFindings = append(allFindings, Finding{
				Severity:       SeverityMedium,
				Category:       "testing",
				File:           anchor.file,
				Line:           anchor.line,
				Title:          "Fake model supplemental review item",
				Evidence:       "fake-model run through llmagent and code-review skill",
				Recommendation: "Use a real model in production or keep this as deterministic CI coverage.",
				Confidence:     0.54,
				Source:         "llm",
				RuleID:         "llm/fake-model/supplemental",
			})
		}
	}
	if truncated {
		allFindings = append(allFindings, Finding{
			Severity:       SeverityLow,
			Category:       "llm_review",
			File:           "",
			Line:           0,
			Title:          "LLM review input was truncated to stay within budget",
			Evidence:       fmt.Sprintf("Model review used %d chunk(s), capped at %d total chunk(s) and %d bytes per request; some content was omitted.", len(chunks), llmMaxDiffChunks, llmMaxDiffBytes),
			Recommendation: "Inspect omitted files or hunks manually, or rerun with a narrower diff when supplemental model review is required.",
			Confidence:     0.55,
			Source:         "llm",
			RuleID:         "llm/input-truncated",
		})
	}
	return DedupeFindings(allFindings), nil
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

func buildLLMReviewPrompt(cfg LLMReviewConfig, diffChunk string, chunkIndex, chunkCount int, truncated bool) string {
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
		for i, f := range cfg.RuleFindings {
			if i >= llmMaxRuleFindings {
				fmt.Fprintf(&b, "- %d additional rule finding(s) omitted to keep model input bounded\n", len(cfg.RuleFindings)-i)
				break
			}
			fmt.Fprintf(&b, "- [%s] %s:%d %s (%s)\n",
				f.Severity, truncate(f.File, 160), f.Line, truncate(f.Title, 160), truncate(f.RuleID, 96))
		}
		b.WriteString("\n")
	}
	if chunkCount > 1 {
		fmt.Fprintf(&b, "Chunk: %d of %d\n", chunkIndex, chunkCount)
	}
	if truncated {
		b.WriteString("Some diff content was omitted to stay within the configured model input budget. Treat omitted content as incomplete analysis.\n\n")
	}
	b.WriteString("Unified diff:\n```diff\n")
	b.WriteString(redactSecrets(diffChunk))
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
	step   atomic.Int32
	anchor fakeReviewAnchor
}

type fakeReviewAnchor struct {
	file string
	line int
}

func newFakeReviewModel(anchor fakeReviewAnchor) *fakeReviewModel {
	return &fakeReviewModel{anchor: anchor}
}

func (m *fakeReviewModel) Info() model.Info {
	return model.Info{Name: "cr-agent-fake-model"}
}

func (m *fakeReviewModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	step := m.step.Add(1)
	content := "[]"
	if step == 1 && m.anchor.file != "" && m.anchor.line > 0 {
		content = fmt.Sprintf(
			`[{"severity":"medium","category":"testing","file":%q,"line":%d,"title":"Fake model supplemental review item","evidence":"fake-model run through llmagent and code-review skill","recommendation":"Use a real model in production or keep this as deterministic CI coverage.","confidence":0.54,"source":"llm","rule_id":"llm/fake-model/supplemental"}]`,
			m.anchor.file,
			m.anchor.line,
		)
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

func firstAddedAnchor(pd ParsedDiff) fakeReviewAnchor {
	for _, h := range pd.Hunks {
		for _, line := range h.Lines {
			if line.Kind == '+' && line.NewLine > 0 && h.File != "" {
				return fakeReviewAnchor{file: h.File, line: line.NewLine}
			}
		}
	}
	return fakeReviewAnchor{}
}

func filepathJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathSeparator))
}

func splitDiffForLLM(raw string, maxBytes int) ([]string, bool) {
	return splitDiffForLLMWithLimit(raw, maxBytes, llmMaxDiffChunks)
}

func splitDiffForLLMWithLimit(raw string, maxBytes, maxChunks int) ([]string, bool) {
	if maxBytes <= 0 || len(raw) <= maxBytes {
		return []string{raw}, false
	}
	if maxChunks <= 0 {
		return []string{truncate(raw, maxBytes)}, true
	}
	sections := splitDiffSections(raw)
	var chunks []string
	var current strings.Builder
	var truncated bool
	for _, section := range sections {
		if len(section) > maxBytes {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			sub, subTruncated := splitLargeDiffSection(section, maxBytes)
			chunks = append(chunks, sub...)
			truncated = truncated || subTruncated
			continue
		}
		addedLen := len(section)
		if current.Len() > 0 {
			addedLen++
		}
		if current.Len() > 0 && current.Len()+addedLen > maxBytes {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(section)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		return []string{truncate(raw, maxBytes)}, true
	}
	if len(chunks) > maxChunks {
		return chunks[:maxChunks], true
	}
	return chunks, truncated
}

func splitDiffSections(raw string) []string {
	lines := strings.Split(raw, "\n")
	var sections []string
	var current []string
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") && len(current) > 0 {
			sections = append(sections, strings.Join(current, "\n"))
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		sections = append(sections, strings.Join(current, "\n"))
	}
	return sections
}

func splitLargeDiffSection(section string, maxBytes int) ([]string, bool) {
	if len(section) <= maxBytes {
		return []string{section}, false
	}
	lines := strings.Split(section, "\n")
	headerEnd := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			headerEnd = i
			break
		}
	}
	header := strings.Join(lines[:headerEnd], "\n")
	if len(header) >= maxBytes {
		return []string{truncate(section, maxBytes)}, true
	}
	var chunks []string
	var current strings.Builder
	current.WriteString(header)
	truncated := false
	for i := headerEnd; i < len(lines); {
		hunkStart := i
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
			i++
		}
		hunk := strings.Join(lines[hunkStart:i], "\n")
		addedLen := len(hunk)
		if current.Len() > 0 {
			addedLen++
		}
		if current.Len()+addedLen > maxBytes {
			if current.Len() > len(header) {
				chunks = append(chunks, current.String())
				current.Reset()
				current.WriteString(header)
			}
			addedLen = len(hunk)
			if current.Len() > 0 {
				addedLen++
			}
			if current.Len()+addedLen > maxBytes {
				room := maxBytes - current.Len() - 1
				if room > 0 {
					current.WriteByte('\n')
					current.WriteString(truncate(hunk, room))
				}
				chunks = append(chunks, current.String())
				current.Reset()
				current.WriteString(header)
				truncated = true
				continue
			}
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(hunk)
	}
	if current.Len() > len(header) {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		return []string{truncate(section, maxBytes)}, true
	}
	return chunks, truncated
}
