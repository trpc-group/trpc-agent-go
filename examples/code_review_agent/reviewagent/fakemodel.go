//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reviewagent

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// fakeModelName identifies the deterministic offline model.
const fakeModelName = "fake-review-model"

var (
	fakeFileRE  = regexp.MustCompile(`(?m)^FILE: (\S+)`)
	fakeAddedRE = regexp.MustCompile(`(?m)^\+ (\d+): (.*)$`)
)

// fakeModel is a deterministic model.Model implementation. It exercises the
// full agent orchestration (runner, session, event stream, JSON parsing)
// without any API key, producing stable output for tests and dry runs.
type fakeModel struct{}

func newFakeModel() *fakeModel { return &fakeModel{} }

// Info implements model.Model.
func (m *fakeModel) Info() model.Info { return model.Info{Name: fakeModelName} }

// GenerateContent implements model.Model with a canned structured reply
// derived only from the prompt content.
func (m *fakeModel) GenerateContent(_ context.Context, request *model.Request) (<-chan *model.Response, error) {
	prompt := lastUserMessage(request)
	reply := modelReply{Summary: "Deterministic fake-model review completed."}
	if file, line, evidence := firstAddedLine(prompt); file != "" {
		reply.Findings = append(reply.Findings, modelFinding{
			Severity:       "low",
			Category:       "model_review",
			File:           file,
			Line:           line,
			Title:          "Fake model flagged the first added line for demonstration",
			Evidence:       evidence,
			Recommendation: "Replace fake-model mode with llm mode for real model commentary.",
			Confidence:     0.55,
			RuleID:         "FAKE001",
		})
	}
	data, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:   "fake-review-response",
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(string(data)),
		}},
	}
	close(ch)
	return ch, nil
}

func lastUserMessage(request *model.Request) string {
	if request == nil {
		return ""
	}
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == model.RoleUser {
			return request.Messages[i].Content
		}
	}
	return ""
}

// firstAddedLine locates the first "FILE:" block and its first added line in
// the prompt built by BuildPrompt.
func firstAddedLine(prompt string) (file string, line int, evidence string) {
	fileMatch := fakeFileRE.FindStringSubmatchIndex(prompt)
	if fileMatch == nil {
		return "", 0, ""
	}
	file = prompt[fileMatch[2]:fileMatch[3]]
	added := fakeAddedRE.FindStringSubmatch(prompt[fileMatch[1]:])
	if added == nil {
		return "", 0, ""
	}
	line, err := strconv.Atoi(added[1])
	if err != nil {
		return "", 0, ""
	}
	return file, line, added[2]
}
