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
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeReviewModel struct {
	step atomic.Int32
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
	_ = m.step.Add(1)
	// Always return the supplemental finding so multi-step Agent calls
	// (skill_load then answer) still leave a valid last assistant message.
	content := `[{"severity":"medium","category":"testing","file":"internal/user/service.go","line":14,"title":"Fake model supplemental check","evidence":"fake-model run","recommendation":"Replace with real LLM in production.","confidence":0.55,"rule_id":"LLM-001","source":"llm"}]`
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
