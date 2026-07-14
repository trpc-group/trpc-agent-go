//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmreview

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeReviewModel struct {
	step int
}

func newFakeReviewModel() *fakeReviewModel {
	return &fakeReviewModel{}
}

func (m *fakeReviewModel) Info() model.Info {
	return model.Info{Name: "cr-agent-fake-model"}
}

func (m *fakeReviewModel) GenerateContent(
	ctx context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.step++
	content := "[]"
	if m.step == 1 {
		content = `[{"severity":"medium","category":"testing","file":"review.go","line":1,"title":"Fake model supplemental check","evidence":"fake-model run","recommendation":"Replace with real LLM in production.","confidence":0.55,"rule_id":"LLM-001","source":"llm"}]`
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
