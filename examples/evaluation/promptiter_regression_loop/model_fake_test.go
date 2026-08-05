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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func fakeRequest(contents ...string) *model.Request {
	messages := make([]model.Message, 0, len(contents))
	for _, content := range contents {
		messages = append(messages, model.Message{Role: model.RoleUser, Content: content})
	}
	return &model.Request{Messages: messages}
}

func callFake(t *testing.T, m *fakeModel, request *model.Request) string {
	t.Helper()
	ch, err := m.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	response, ok := <-ch
	require.True(t, ok)
	require.Empty(t, response.Error)
	require.Len(t, response.Choices, 1)
	return response.Choices[0].Message.Content
}

func TestFakeModel_CandidateGood(t *testing.T) {
	m := newFakeModel("scripted")
	request := fakeRequest(
		stageGood+" 严格输出 JSON",
		`{"caseId":"validation_02_ice_hockey","split":"validation","headline":"雪原狼加时绝杀湖湾骑士","source":"East Ice Center 2026-02-14"}`,
	)
	content := callFake(t, m, request)
	require.Equal(t, `{"headline":"雪原狼加时绝杀湖湾骑士","source":"East Ice Center 2026-02-14"}`, content)

	// GOOD fixes train_01 but leaves train_02/train_03 degraded so the next
	// optimization round still has train failures to drive gradients.
	train01 := callFake(t, m, fakeRequest(
		stageGood+" 严格输出 JSON",
		`{"caseId":"train_01_basketball","split":"train","headline":"掘金128比104大胜开拓者","source":"NBA 2026-01-12"}`,
	))
	require.Equal(t, `{"headline":"掘金128比104大胜开拓者","source":"NBA 2026-01-12"}`, train01)

	train02 := callFake(t, m, fakeRequest(
		stageGood+" 严格输出 JSON",
		`{"caseId":"train_02_tennis","split":"train","headline":"陈林五盘逆转晋级","source":"上海大师赛 2026-03-04"}`,
	))
	require.Contains(t, train02, degradedHeadline)
}

func TestFakeModel_CandidateOverfitTrainVsValidation(t *testing.T) {
	m := newFakeModel("scripted")
	trainRequest := fakeRequest(
		stageOverfit+" 只对训练样本",
		`{"caseId":"train_02_tennis","split":"train","headline":"陈林五盘逆转晋级","source":"上海大师赛 2026-03-04"}`,
	)
	require.Equal(t, `{"headline":"陈林五盘逆转晋级","source":"上海大师赛 2026-03-04"}`, callFake(t, m, trainRequest))

	// The key validation case must never regress even under the overfit stage.
	keyCase := callFake(t, m, fakeRequest(
		stageOverfit+" 只对训练样本",
		`{"caseId":"validation_02_ice_hockey","split":"validation","headline":"雪原狼加时绝杀湖湾骑士","source":"East Ice Center 2026-02-14"}`,
	))
	require.Equal(t, `{"headline":"雪原狼加时绝杀湖湾骑士","source":"East Ice Center 2026-02-14"}`, keyCase)

	// Non-key validation cases regress under the overfit stage.
	regressed := callFake(t, m, fakeRequest(
		stageOverfit+" 只对训练样本",
		`{"caseId":"validation_01_baseball","split":"validation","headline":"红人九下再见安打逆转小熊","source":"MLB 2026-05-01"}`,
	))
	require.NotEqual(t, `{"headline":"红人九下再见安打逆转小熊","source":"MLB 2026-05-01"}`, regressed)
	require.Contains(t, regressed, degradedHeadline)
}

func TestFakeModel_CandidateBaselineAndIneffective(t *testing.T) {
	m := newFakeModel("scripted")
	baseline := callFake(t, m, fakeRequest(
		"根据输入生成一条中文体育头条",
		`{"caseId":"train_01_basketball","split":"train","headline":"掘金128比104大胜开拓者","source":"NBA 2026-01-12"}`,
	))
	require.NotContains(t, baseline, "headline")

	ineffective := callFake(t, m, fakeRequest(
		stageIneffective+" 输出固定文案",
		`{"caseId":"validation_02_ice_hockey","split":"validation","headline":"雪原狼加时绝杀湖湾骑士","source":"East Ice Center 2026-02-14"}`,
	))
	require.NotContains(t, ineffective, "headline")
}

func TestFakeModel_OptimizerReturnsPlanInOrder(t *testing.T) {
	m := newFakeModel("scripted")
	request := fakeRequest("Optimize one PromptIter surface from the provided current value and aggregated gradients.")
	for i, expected := range optimizerPlan {
		content := callFake(t, m, request)
		var proposal struct {
			Value map[string]string `json:"Value"`
		}
		require.NoError(t, json.Unmarshal([]byte(content), &proposal))
		require.Equal(t, expected, proposal.Value["Text"], "optimizer call %d", i+1)
	}
	// The plan is finite and repeats the last entry afterwards.
	_ = callFake(t, m, request)
}

func TestFakeModel_BackwarderAndAggregatorJSON(t *testing.T) {
	m := newFakeModel("scripted")
	backwarder := callFake(t, m, fakeRequest("Compute PromptIter backward attribution for one step."))
	var backward struct {
		Gradients []struct {
			SurfaceID string `json:"SurfaceID"`
			Severity  string `json:"Severity"`
			Gradient  string `json:"Gradient"`
		} `json:"Gradients"`
		Upstream []any `json:"Upstream"`
	}
	require.NoError(t, json.Unmarshal([]byte(backwarder), &backward))
	require.Len(t, backward.Gradients, 1)
	require.Equal(t, "candidate#instruction", backward.Gradients[0].SurfaceID)
	require.Equal(t, "P1", backward.Gradients[0].Severity)
	require.Len(t, backward.Upstream, 0)

	aggregator := callFake(t, m, fakeRequest("Aggregate PromptIter gradients for a single surface."))
	var aggregated struct {
		Gradients []struct {
			Severity string `json:"Severity"`
			Gradient string `json:"Gradient"`
		} `json:"Gradients"`
	}
	require.NoError(t, json.Unmarshal([]byte(aggregator), &aggregated))
	require.Len(t, aggregated.Gradients, 1)
	require.Equal(t, "P1", aggregated.Gradients[0].Severity)
}

func TestFakeModel_CallCount(t *testing.T) {
	m := newFakeModel("scripted")
	require.Equal(t, 0, m.CallCount())
	callFake(t, m, fakeRequest("hello"))
	callFake(t, m, fakeRequest("hello"))
	require.Equal(t, 2, m.CallCount())
}
