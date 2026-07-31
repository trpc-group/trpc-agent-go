//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	fakemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/model"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// LLMAnalyzer analyzes a parsed diff and returns findings.
type LLMAnalyzer func(context.Context, string, *input.DiffParseResult) ([]store.Finding, error)

func analyzeWithFakeModel(ctx context.Context, taskID string, diff *input.DiffParseResult) ([]store.Finding, error) {
	var content strings.Builder
	for _, file := range diff.Files {
		content.WriteString("File: ")
		content.WriteString(file.Path)
		content.WriteByte('\n')
		for _, hunk := range file.Hunks {
			for _, change := range hunk.Changes {
				content.WriteString(change.Content)
				content.WriteByte('\n')
			}
		}
	}
	response, err := fakemodel.NewFakeModel("fake-gpt").GenerateResponse(ctx, content.String())
	if err != nil {
		return nil, err
	}
	return parseAIFindings(response, taskID), nil
}

// NewOpenAIAnalyzer creates an analyzer backed by the configured OpenAI-compatible endpoint.
func NewOpenAIAnalyzer(modelName string) LLMAnalyzer {
	return NewOpenAIAnalyzerWithSkills(modelName, "")
}

// NewOpenAIAnalyzerWithSkills creates an analyzer with the code-review Skill repository.
func NewOpenAIAnalyzerWithSkills(modelName, skillsRoot string) LLMAnalyzer {
	return func(ctx context.Context, taskID string, diff *input.DiffParseResult) ([]store.Finding, error) {
		if os.Getenv("OPENAI_API_KEY") == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required")
		}
		if strings.TrimSpace(skillsRoot) == "" {
			return nil, fmt.Errorf("skills root is required for LLM analysis")
		}
		repo, err := skill.NewFSRepository(filepath.Clean(skillsRoot))
		if err != nil {
			return nil, fmt.Errorf("load skills repository: %w", err)
		}
		if _, err := repo.Get("code-review"); err != nil {
			return nil, fmt.Errorf("load code-review skill: %w", err)
		}
		mdl := openai.New(modelName)
		agent := llmagent.New("code-reviewer",
			llmagent.WithModel(mdl),
			llmagent.WithDescription("Go 代码审查 Agent"),
			llmagent.WithInstruction("先调用 skill_load 加载 code-review，再依据 Skill 规则审查；只返回包含 findings 数组的 JSON，不要返回其他内容。"),
			llmagent.WithSkills(repo),
			llmagent.WithSkillToolProfile(llmagent.SkillToolProfileFull),
		)
		events, err := runner.NewRunner("golens", agent).Run(ctx, "user", "review-"+taskID, model.NewUserMessage(BuildReviewPrompt(diff)))
		if err != nil {
			return nil, fmt.Errorf("LLM run failed: %w", err)
		}
		for event := range events {
			if event.Error != nil {
				return nil, event.Error
			}
			if event.Response != nil && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				if content != "" {
					return parseAIFindings(content, taskID), nil
				}
			}
		}
		log.Printf("LLM returned no response")
		return nil, fmt.Errorf("no response from LLM")
	}
}

// BuildReviewPrompt renders a redacted diff with file and line metadata.
func BuildReviewPrompt(diff *input.DiffParseResult) string {
	var content strings.Builder
	detector := safety.NewSecretDetector()
	for _, file := range diff.Files {
		content.WriteString(fmt.Sprintf("File: %s\n", file.Path))
		for _, hunk := range file.Hunks {
			content.WriteString(fmt.Sprintf("Hunk: @@ -%d,%d +%d,%d @@ %s\n", hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines, hunk.Header))
			for _, change := range hunk.Changes {
				prefix := " "
				if change.Type == "add" {
					prefix = "+"
				} else if change.Type == "delete" {
					prefix = "-"
				}
				content.WriteString(fmt.Sprintf("%s old=%d new=%d %s\n", prefix, change.OldLine, change.NewLine, detector.RedactText(change.Content)))
			}
		}
	}
	return content.String()
}
