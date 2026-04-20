//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newAskUserQuestionTool() (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in askUserQuestionInput) (askUserQuestionOutput, error) {
			if len(in.Questions) == 0 {
				return askUserQuestionOutput{}, fmt.Errorf("at least one question is required")
			}
			if len(in.Questions) > 4 {
				return askUserQuestionOutput{}, fmt.Errorf("at most 4 questions are allowed")
			}
			if err := validateAskUserQuestions(in.Questions); err != nil {
				return askUserQuestionOutput{}, err
			}
			answers := in.Answers
			if answers == nil {
				answers = map[string]string{}
			}
			return askUserQuestionOutput{
				Questions:   in.Questions,
				Answers:     answers,
				Annotations: in.Annotations,
			}, nil
		},
		function.WithName(toolAskUser),
		function.WithDescription(askUserDescription()),
	), nil
}

func validateAskUserQuestions(questions []askUserQuestion) error {
	seenQuestions := map[string]struct{}{}
	for _, question := range questions {
		text := strings.TrimSpace(question.Question)
		if text == "" {
			return fmt.Errorf("question text is required")
		}
		if _, ok := seenQuestions[text]; ok {
			return fmt.Errorf("question texts must be unique")
		}
		seenQuestions[text] = struct{}{}
		if len(question.Options) < 2 || len(question.Options) > 4 {
			return fmt.Errorf("question %q must have 2-4 options", text)
		}
		seenOptions := map[string]struct{}{}
		for _, option := range question.Options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				return fmt.Errorf("question %q has an option with empty label", text)
			}
			if _, ok := seenOptions[label]; ok {
				return fmt.Errorf("option labels must be unique within question %q", text)
			}
			seenOptions[label] = struct{}{}
		}
	}
	return nil
}

func askUserDescription() string {
	return `Ask the user a bounded multiple-choice clarification question set.

Usage:
- Use this tool only when the next step is blocked on user preference or clarification.
- Each question must be unique and include 2-4 unique options.
- This tool is for collecting structured user choices rather than free-form conversation.`
}
