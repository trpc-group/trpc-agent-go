//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	criterionlength "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/length"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	criterionrouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	criterionxml "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/xml"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type stubRunner struct{}

func (stubRunner) Run(_ context.Context, _ string, _ string, _ model.Message, _ ...agent.RunOption) (<-chan *event.Event, error) {
	return nil, nil
}

func (stubRunner) Close() error { return nil }

var _ runner.Runner = (*stubRunner)(nil)

func intPtr(v int) *int { return &v }

func float64Ptr(v float64) *float64 { return &v }

func boolPtr(v bool) *bool { return &v }

func stringPtr(v string) *string { return &v }

func TestCloneEvalCase_NilInput(t *testing.T) {
	got, err := CloneEvalCase(nil)
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestCloneEvalSet_NilInput(t *testing.T) {
	got, err := CloneEvalSet(nil)
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestCloneEvalMetric_NilInput(t *testing.T) {
	got, err := CloneEvalMetric(nil)
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestCloneEvalMetric_DeepCopiesJudgeTemplate(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName:    "metric-1",
		EvaluatorName: "llm_judge_template",
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             "Question: {{question}}",
					ResponseScorerName: "single_score",
					VariableBindings: []*criterionllm.TemplateVariableBinding{
						{
							TemplateVariable: "question",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeActual,
								Field: criterionllm.TemplateVariableFieldUserContent,
							},
						},
					},
				},
			},
		},
	}

	dst, err := CloneEvalMetric(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	require.NotNil(t, dst.Criterion)
	require.NotNil(t, dst.Criterion.LLMJudge)
	require.NotNil(t, dst.Criterion.LLMJudge.Template)
	dst.Criterion.LLMJudge.Template.Prompt = "changed"
	assert.Equal(t, "Question: {{question}}", src.Criterion.LLMJudge.Template.Prompt)
	dst.Criterion.LLMJudge.Template.VariableBindings[0].TemplateVariable = "changed"
	assert.Equal(t, "question", src.Criterion.LLMJudge.Template.VariableBindings[0].TemplateVariable)
	dst.Criterion.LLMJudge.Template.VariableBindings[0].Source.Scope = criterionllm.TemplateVariableScopeExpected
	assert.Equal(t, criterionllm.TemplateVariableScopeActual, src.Criterion.LLMJudge.Template.VariableBindings[0].Source.Scope)
}

func TestCloneTemplateVariableHelpersHandleNil(t *testing.T) {
	assert.Nil(t, cloneTemplateVariableBindings(nil))
	assert.Nil(t, cloneTemplateVariableBinding(nil))
}

func TestCloneEvalMetric_PreservesNilTemplateBinding(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName:    "metric-1",
		EvaluatorName: "llm_judge_template",
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             "Question: {{question}}",
					ResponseScorerName: "single_score",
					VariableBindings: []*criterionllm.TemplateVariableBinding{
						nil,
					},
				},
			},
		},
	}
	dst, err := CloneEvalMetric(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	require.Len(t, dst.Criterion.LLMJudge.Template.VariableBindings, 1)
	assert.Nil(t, dst.Criterion.LLMJudge.Template.VariableBindings[0])
}

func TestCloneEvalSetResult_NilInput(t *testing.T) {
	got, err := CloneEvalSetResult(nil)
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestCloneEvalCase_DeepCopy(t *testing.T) {
	srcText := "hello"
	audioBytes := []byte{3, 2, 1}
	fileBytes := []byte{7, 8, 9}
	src := &evalset.EvalCase{
		EvalID: "case-1",
		ContextMessages: []*model.Message{
			{
				Role:    model.RoleUser,
				Content: "prompt",
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &srcText},
					{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte{1, 2, 3}}},
					{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: audioBytes, Format: "wav"}},
					{Type: model.ContentTypeFile, File: &model.File{Name: "input.txt", Data: fileBytes, MimeType: "text/plain"}},
				},
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "f",
							Arguments: []byte(`{"a":1}`),
						},
						Index: intPtr(1),
						ExtraFields: map[string]any{
							"k": []any{map[string]any{"x": "y"}},
						},
					},
				},
			},
		},
		Conversation: []*evalset.Invocation{
			{
				InvocationID: "inv-1",
				ContextMessages: []*model.Message{
					{
						Role:    model.RoleSystem,
						Content: "context",
					},
				},
				UserContent: &model.Message{
					Role:    model.RoleUser,
					Content: "user input",
				},
				FinalResponse: &model.Message{
					Role:    model.RoleAssistant,
					Content: "assistant output",
				},
				Tools: []*evalset.Tool{
					{
						ID:   "tool-1",
						Name: "tool",
						Arguments: map[string]any{
							"a":      1,
							"nested": map[string]any{"b": true},
							"list":   []any{"x", map[string]any{"y": []byte{4, 5}}},
						},
						Result: []any{"ok", []byte{6, 7, 8}},
					},
				},
				IntermediateResponses: []*model.Message{
					{
						Role:    model.RoleAssistant,
						Content: "thinking",
						ContentParts: []model.ContentPart{
							{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: []byte{9}, Format: "wav"}},
						},
					},
				},
				CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1, 0).UTC()},
			},
		},
		ActualConversation: []*evalset.Invocation{
			{
				InvocationID: "inv-actual-1",
				FinalResponse: &model.Message{
					Role:    model.RoleAssistant,
					Content: "final",
				},
			},
		},
		SessionInput: &evalset.SessionInput{
			AppName: "app",
			UserID:  "user",
			State: map[string]any{
				"flag":  true,
				"bytes": []byte{9, 8, 7},
			},
		},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1, 0).UTC()},
	}

	dst, err := CloneEvalCase(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	assertNotAliasedAndEqual(t, src, dst)

	*dst.ContextMessages[0].ContentParts[0].Text = "changed"
	assert.Equal(t, "hello", *src.ContextMessages[0].ContentParts[0].Text)

	dst.ContextMessages[0].ContentParts[1].Image.Data[0] = 99
	assert.Equal(t, byte(1), src.ContextMessages[0].ContentParts[1].Image.Data[0])

	dst.ContextMessages[0].ContentParts[2].Audio.Data[0] = 0
	assert.Equal(t, byte(3), src.ContextMessages[0].ContentParts[2].Audio.Data[0])

	dst.ContextMessages[0].ContentParts[3].File.Data[0] = 0
	assert.Equal(t, byte(7), src.ContextMessages[0].ContentParts[3].File.Data[0])

	dst.ContextMessages[0].ToolCalls[0].Function.Arguments[0] = 'X'
	assert.Equal(t, byte('{'), src.ContextMessages[0].ToolCalls[0].Function.Arguments[0])

	dst.ContextMessages[0].ToolCalls[0].ExtraFields["k"].([]any)[0].(map[string]any)["x"] = "changed"
	assert.Equal(t, "y", src.ContextMessages[0].ToolCalls[0].ExtraFields["k"].([]any)[0].(map[string]any)["x"])

	dst.Conversation[0].Tools[0].Arguments.(map[string]any)["a"] = 2
	assert.Equal(t, 1, src.Conversation[0].Tools[0].Arguments.(map[string]any)["a"])

	dst.Conversation[0].IntermediateResponses[0].ContentParts[0].Audio.Data[0] = 0
	assert.Equal(t, byte(9), src.Conversation[0].IntermediateResponses[0].ContentParts[0].Audio.Data[0])

	dst.SessionInput.State["bytes"].([]byte)[0] = 0
	assert.Equal(t, byte(9), src.SessionInput.State["bytes"].([]byte)[0])

	dst.CreationTimestamp.Time = time.Unix(2, 0).UTC()
	assert.Equal(t, time.Unix(1, 0).UTC(), src.CreationTimestamp.Time)
}

func TestCloneEvalSet_DeepCopy(t *testing.T) {
	src := &evalset.EvalSet{
		EvalSetID:         "set-1",
		Name:              "set-1",
		Description:       "desc",
		CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1, 0).UTC()},
		EvalCases: []*evalset.EvalCase{
			{EvalID: "case-1"},
			{EvalID: "case-2"},
		},
	}

	dst, err := CloneEvalSet(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	assertNotAliasedAndEqual(t, src, dst)

	dst.EvalCases[0].EvalID = "changed"
	assert.Equal(t, "case-1", src.EvalCases[0].EvalID)
}

func TestCloneEvalMetric_DeepCopyKeepsAPIKeyAndDropsJudgeRunnerOptions(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName: "metric-1",
		Threshold:  0.5,
		Criterion: &criterion.Criterion{
			ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{
				DefaultStrategy: &tooltrajectory.ToolTrajectoryStrategy{
					Name: &text.TextCriterion{
						CaseInsensitive: true,
					},
					Arguments: &criterionjson.JSONCriterion{
						IgnoreTree: map[string]any{"a": true},
					},
					Result: &criterionjson.JSONCriterion{
						OnlyTree: map[string]any{"b": true},
					},
				},
				ToolStrategy: map[string]*tooltrajectory.ToolTrajectoryStrategy{
					"tool": {
						Name: &text.TextCriterion{
							MatchStrategy: text.TextMatchStrategyContains,
						},
						Arguments: &criterionjson.JSONCriterion{
							OnlyTree: map[string]any{"x": true},
						},
						Result: &criterionjson.JSONCriterion{
							IgnoreTree: map[string]any{"y": true},
						},
					},
				},
				OrderSensitive: true,
				SubsetMatching: true,
			},
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Text: &text.TextCriterion{
					CaseInsensitive: true,
					Length: &criterionlength.LengthCriterion{
						Min: intPtr(1),
						Max: intPtr(10),
					},
				},
				JSON: &criterionjson.JSONCriterion{
					IgnoreTree: map[string]any{
						"a": map[string]any{"b": true},
					},
					OnlyTree: map[string]any{
						"x": []any{"y"},
					},
					NumberTolerance: float64Ptr(0.1),
				},
				Rouge: &criterionrouge.RougeCriterion{
					RougeType: "rouge1",
					Measure:   criterionrouge.RougeMeasureF1,
					Threshold: criterionrouge.Score{Precision: 0.1, Recall: 0.2, F1: 0.3},
				},
				XML: &criterionxml.XMLCriterion{},
			},
			LLMJudge: &criterionllm.LLMCriterion{
				Rubrics: []*criterionllm.Rubric{
					{
						ID: "r1",
						Content: &criterionllm.RubricContent{
							Text: "rubric",
						},
					},
				},
				JudgeModel: &criterionllm.JudgeModelOptions{
					ProviderName: "provider",
					ModelName:    "model",
					APIKey:       "secret",
					ExtraFields: map[string]any{
						"k": "v",
					},
					NumSamples: intPtr(2),
					Generation: &model.GenerationConfig{
						MaxTokens:        intPtr(100),
						Temperature:      float64Ptr(0.7),
						TopP:             float64Ptr(0.9),
						Stop:             []string{"s1"},
						PresencePenalty:  float64Ptr(1.1),
						FrequencyPenalty: float64Ptr(0.2),
						ReasoningEffort:  stringPtr("low"),
						ThinkingEnabled:  boolPtr(true),
						ThinkingTokens:   intPtr(10),
					},
				},
			},
		},
	}
	src.Criterion.LLMJudge.JudgeRunnerOptions = &criterionllm.JudgeRunnerOptions{Runner: stubRunner{}}

	dst, err := CloneEvalMetric(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	assertNotAliasedAndEqual(t, src, dst)

	require.NotNil(t, dst.Criterion.LLMJudge)
	require.NotNil(t, dst.Criterion.LLMJudge.JudgeModel)
	assert.Equal(t, "secret", dst.Criterion.LLMJudge.JudgeModel.APIKey)

	assert.Nil(t, dst.Criterion.LLMJudge.JudgeRunnerOptions)

	dst.Criterion.LLMJudge.JudgeModel.ExtraFields["k"] = "changed"
	assert.Equal(t, "v", src.Criterion.LLMJudge.JudgeModel.ExtraFields["k"])

	dst.Criterion.FinalResponse.JSON.IgnoreTree["a"].(map[string]any)["b"] = false
	assert.Equal(t, true, src.Criterion.FinalResponse.JSON.IgnoreTree["a"].(map[string]any)["b"])

	dst.Criterion.LLMJudge.Rubrics[0].Content.Text = "changed"
	assert.Equal(t, "rubric", src.Criterion.LLMJudge.Rubrics[0].Content.Text)

	dst.Criterion.LLMJudge.JudgeModel.Generation.Stop[0] = "changed"
	assert.Equal(t, "s1", src.Criterion.LLMJudge.JudgeModel.Generation.Stop[0])

	dst.Criterion.ToolTrajectory.ToolStrategy["tool"].Name.MatchStrategy = text.TextMatchStrategyExact
	assert.Equal(t, text.TextMatchStrategyContains, src.Criterion.ToolTrajectory.ToolStrategy["tool"].Name.MatchStrategy)

	dst.Criterion.FinalResponse.Rouge.RougeType = "rougeL"
	assert.Equal(t, "rouge1", src.Criterion.FinalResponse.Rouge.RougeType)

	*dst.Criterion.FinalResponse.Text.Length.Min = 2
	assert.Equal(t, 1, *src.Criterion.FinalResponse.Text.Length.Min)

	dst.Criterion.FinalResponse.XML.Ignore = true
	assert.False(t, src.Criterion.FinalResponse.XML.Ignore)
}

func TestCloneEvalSetResult_DeepCopy(t *testing.T) {
	src := &evalresult.EvalSetResult{
		EvalSetResultID:   "result-1",
		EvalSetResultName: "result-1",
		EvalSetID:         "set-1",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID: "set-1",
				EvalID:    "case-1",
				RunID:     1,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "metric-1",
						Score:      0.9,
						EvalStatus: status.EvalStatusPassed,
						Threshold:  0.5,
						Criterion: &criterion.Criterion{
							LLMJudge: &criterionllm.LLMCriterion{
								Rubrics: []*criterionllm.Rubric{
									{
										ID: "r1",
										Content: &criterionllm.RubricContent{
											Text: "rubric",
										},
									},
								},
								JudgeModel: &criterionllm.JudgeModelOptions{
									ProviderName: "provider",
									ModelName:    "model",
									APIKey:       "secret",
								},
							},
						},
						Details: &evalresult.EvalMetricResultDetails{
							Reason: "ok",
							Score:  0.9,
							RubricScores: []*evalresult.RubricScore{
								{
									ID:     "r1",
									Reason: "good",
									Score:  1,
								},
							},
						},
					},
				},
				EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
					{
						ActualInvocation: &evalset.Invocation{
							InvocationID: "inv-1",
							Tools: []*evalset.Tool{
								{
									ID:        "tool-1",
									Name:      "tool",
									Arguments: map[string]any{"k": "v"},
									Result:    map[string]any{"ok": true},
								},
							},
						},
						ExpectedInvocation: &evalset.Invocation{
							InvocationID: "inv-expected-1",
						},
						EvalMetricResults: []*evalresult.EvalMetricResult{
							{
								MetricName: "metric-1",
								Score:      0.9,
								EvalStatus: status.EvalStatusPassed,
								Threshold:  0.5,
							},
						},
					},
				},
				SessionID: "session",
				UserID:    "user",
			},
		},
		Summary: &evalresult.EvalSetResultSummary{
			OverallStatus: status.EvalStatusPassed,
			NumRuns:       1,
			RunStatusCounts: &evalresult.EvalStatusCounts{
				Passed: 1,
			},
			RunSummaries: []*evalresult.EvalSetRunSummary{
				{
					RunID:         1,
					OverallStatus: status.EvalStatusPassed,
					CaseStatusCounts: &evalresult.EvalStatusCounts{
						Passed: 1,
					},
				},
			},
		},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1, 0).UTC()},
	}

	dst, err := CloneEvalSetResult(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	assertNotAliasedAndEqual(t, src, dst)

	dst.EvalCaseResults[0].OverallEvalMetricResults[0].Details.RubricScores[0].Reason = "changed"
	assert.Equal(t, "good", src.EvalCaseResults[0].OverallEvalMetricResults[0].Details.RubricScores[0].Reason)

	dst.EvalCaseResults[0].EvalMetricResultPerInvocation[0].ActualInvocation.Tools[0].Arguments.(map[string]any)["k"] = "changed"
	assert.Equal(t, "v", src.EvalCaseResults[0].EvalMetricResultPerInvocation[0].ActualInvocation.Tools[0].Arguments.(map[string]any)["k"])

	dst.Summary.RunStatusCounts.Passed = 2
	assert.Equal(t, 1, src.Summary.RunStatusCounts.Passed)
}

func TestCloneEvalSetResult_DeepCopyFullSummary(t *testing.T) {
	src := &evalresult.EvalSetResult{
		EvalSetResultID:   "result-1",
		EvalSetResultName: "result-1",
		EvalSetID:         "set-1",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID: "set-1",
				EvalID:    "case-1",
				RunID:     1,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "metric-1",
						Score:      0.9,
						EvalStatus: status.EvalStatusPassed,
						Threshold:  0.5,
						Criterion: &criterion.Criterion{
							ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{
								DefaultStrategy: &tooltrajectory.ToolTrajectoryStrategy{
									Name: &text.TextCriterion{
										CaseInsensitive: true,
									},
								},
								ToolStrategy: map[string]*tooltrajectory.ToolTrajectoryStrategy{
									"tool": {
										Arguments: &criterionjson.JSONCriterion{
											OnlyTree: map[string]any{
												"x": true,
											},
										},
									},
								},
								OrderSensitive: true,
							},
							FinalResponse: &finalresponse.FinalResponseCriterion{
								Rouge: &criterionrouge.RougeCriterion{
									RougeType: "rouge1",
								},
							},
							LLMJudge: &criterionllm.LLMCriterion{
								JudgeModel: &criterionllm.JudgeModelOptions{
									ProviderName: "provider",
									ModelName:    "model",
									APIKey:       "secret",
								},
							},
						},
						Details: &evalresult.EvalMetricResultDetails{
							Reason: "ok",
							Score:  0.9,
							RubricScores: []*evalresult.RubricScore{
								nil,
								{
									ID:     "r1",
									Reason: "good",
									Score:  1,
								},
							},
						},
					},
					nil,
				},
				EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
					nil,
					{
						ActualInvocation: &evalset.Invocation{
							InvocationID: "inv-1",
							UserContent: &model.Message{
								Role:    model.RoleUser,
								Content: "user",
								ContentParts: []model.ContentPart{
									{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: []byte{1}, Format: "wav"}},
									{Type: model.ContentTypeFile, File: &model.File{Name: "f", Data: []byte{2}}},
								},
							},
							FinalResponse: &model.Message{
								Role:    model.RoleAssistant,
								Content: "assistant",
							},
							IntermediateResponses: []*model.Message{
								{
									Role:    model.RoleAssistant,
									Content: "intermediate",
								},
							},
							Tools: []*evalset.Tool{
								{
									ID:        "tool-1",
									Name:      "tool",
									Arguments: map[string]any{"k": []any{"v", map[string]any{"bytes": []byte{3}}}},
									Result:    map[string]any{"ok": true},
								},
							},
						},
						ExpectedInvocation: &evalset.Invocation{
							InvocationID: "inv-expected-1",
							Tools: []*evalset.Tool{
								{
									ID:        "tool-1",
									Name:      "tool",
									Arguments: map[string]any{"k": "v"},
									Result:    map[string]any{"ok": true},
								},
							},
						},
						EvalMetricResults: []*evalresult.EvalMetricResult{
							{
								MetricName: "metric-1",
								Score:      0.9,
								EvalStatus: status.EvalStatusPassed,
								Threshold:  0.5,
							},
							nil,
						},
					},
				},
				SessionID: "session",
				UserID:    "user",
			},
			nil,
		},
		Summary: &evalresult.EvalSetResultSummary{
			OverallStatus: status.EvalStatusPassed,
			NumRuns:       1,
			RunStatusCounts: &evalresult.EvalStatusCounts{
				Passed: 1,
				Failed: 1,
			},
			RunSummaries: []*evalresult.EvalSetRunSummary{
				nil,
				{
					RunID:         1,
					OverallStatus: status.EvalStatusPassed,
					CaseStatusCounts: &evalresult.EvalStatusCounts{
						Passed: 1,
					},
					MetricSummaries: []*evalresult.EvalMetricSummary{
						nil,
						{
							MetricName: "metric-1",
							StatusCounts: &evalresult.EvalStatusCounts{
								Passed: 1,
							},
						},
					},
				},
			},
			EvalCaseSummaries: []*evalresult.EvalCaseResultSummary{
				nil,
				{
					EvalID:        "case-1",
					OverallStatus: status.EvalStatusPassed,
					RunStatusCounts: &evalresult.EvalStatusCounts{
						Passed: 1,
					},
					MetricSummaries: []*evalresult.EvalMetricSummary{
						{
							MetricName: "metric-1",
							StatusCounts: &evalresult.EvalStatusCounts{
								Passed: 1,
							},
						},
					},
					RunSummaries: []*evalresult.EvalCaseRunSummary{
						nil,
						{
							RunID:           1,
							FinalEvalStatus: status.EvalStatusPassed,
							MetricResults: []*evalresult.EvalMetricRunSummary{
								nil,
								{
									MetricName: "metric-1",
									Score:      0.9,
									EvalStatus: status.EvalStatusPassed,
									Threshold:  0.5,
								},
							},
						},
					},
				},
			},
		},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1, 0).UTC()},
	}

	dst, err := CloneEvalSetResult(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	assertNotAliasedAndEqual(t, src, dst)

	dst.Summary.RunSummaries[1].MetricSummaries[1].StatusCounts.Passed = 2
	assert.Equal(t, 1, src.Summary.RunSummaries[1].MetricSummaries[1].StatusCounts.Passed)

	dst.EvalCaseResults[0].OverallEvalMetricResults[0].Details.RubricScores[1].Reason = "changed"
	assert.Equal(t, "good", src.EvalCaseResults[0].OverallEvalMetricResults[0].Details.RubricScores[1].Reason)

	dst.EvalCaseResults[0].EvalMetricResultPerInvocation[1].ActualInvocation.UserContent.ContentParts[0].Audio.Data[0] = 0
	assert.Equal(t, byte(1), src.EvalCaseResults[0].EvalMetricResultPerInvocation[1].ActualInvocation.UserContent.ContentParts[0].Audio.Data[0])
}

func TestCloneHelpers_NilInputs(t *testing.T) {
	assert.Nil(t, cloneBytes(nil))
	assert.Nil(t, cloneStringSlice(nil))
	assert.Nil(t, cloneBoolPtr(nil))
	assert.Nil(t, cloneStringPtr(nil))
	assert.Nil(t, cloneImage(nil))
	assert.Nil(t, cloneAudio(nil))
	assert.Nil(t, cloneFile(nil))
}

func TestCloneAny_UnsupportedTypes(t *testing.T) {
	_, err := cloneAny(make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value type")

	_, err = cloneAny(func() {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value type")

	var v int
	_, err = cloneAny(unsafe.Pointer(&v))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value type")
}

func TestCloneEvalCase_ErrorFromContextMessages(t *testing.T) {
	src := &evalset.EvalCase{
		EvalID: "case-err-context-messages",
		ContextMessages: []*model.Message{
			{
				Role:    model.RoleUser,
				Content: "prompt",
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name: "f",
						},
						ExtraFields: map[string]any{
							"bad": make(chan int),
						},
					},
				},
			},
		},
	}

	dst, err := CloneEvalCase(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalCase_ErrorFromConversation(t *testing.T) {
	src := &evalset.EvalCase{
		EvalID: "case-err-conversation",
		Conversation: []*evalset.Invocation{
			{
				InvocationID: "inv-1",
				Tools: []*evalset.Tool{
					{
						ID:        "tool-1",
						Name:      "tool",
						Arguments: map[string]any{"bad": make(chan int)},
					},
				},
			},
		},
	}

	dst, err := CloneEvalCase(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalCase_ErrorFromActualConversation(t *testing.T) {
	src := &evalset.EvalCase{
		EvalID:            "case-err-actual-conversation",
		Conversation:      []*evalset.Invocation{nil},
		SessionInput:      &evalset.SessionInput{},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(1, 0).UTC()},
		ActualConversation: []*evalset.Invocation{
			{
				InvocationID: "inv-actual-1",
				Tools: []*evalset.Tool{
					nil,
					{
						ID:        "tool-1",
						Name:      "tool",
						Arguments: map[string]any{"bad": func() {}},
					},
				},
			},
		},
	}

	dst, err := CloneEvalCase(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalCase_ErrorFromSessionInput(t *testing.T) {
	src := &evalset.EvalCase{
		EvalID: "case-err-session-input",
		SessionInput: &evalset.SessionInput{
			AppName: "app",
			UserID:  "user",
			State: map[string]any{
				"bad": make(chan int),
			},
		},
	}

	dst, err := CloneEvalCase(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalSet_ErrorFromEvalCase(t *testing.T) {
	src := &evalset.EvalSet{
		EvalSetID: "set-err",
		EvalCases: []*evalset.EvalCase{
			{EvalID: "ok"},
			{
				EvalID: "bad",
				ContextMessages: []*model.Message{
					{
						Role:    model.RoleUser,
						Content: "prompt",
						ToolCalls: []model.ToolCall{
							{
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name: "f",
								},
								ExtraFields: map[string]any{
									"bad": make(chan int),
								},
							},
						},
					},
				},
			},
		},
	}

	dst, err := CloneEvalSet(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalMetric_NilCriterion(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName: "metric-nil-criterion",
		Threshold:  0.5,
	}

	dst, err := CloneEvalMetric(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	assert.Nil(t, dst.Criterion)
}

func TestCloneEvalMetric_ErrorFromCriterionJSON(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName: "metric-err-json",
		Threshold:  0.5,
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				JSON: &criterionjson.JSONCriterion{
					IgnoreTree: map[string]any{
						"bad": make(chan int),
					},
				},
			},
		},
	}

	dst, err := CloneEvalMetric(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalMetric_ToolTrajectoryNilStrategies(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName: "metric-nil-strategies",
		Threshold:  0.5,
		Criterion: &criterion.Criterion{
			ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{
				ToolStrategy: map[string]*tooltrajectory.ToolTrajectoryStrategy{
					"tool": nil,
				},
			},
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Text: &text.TextCriterion{},
			},
			LLMJudge: &criterionllm.LLMCriterion{
				Rubrics: []*criterionllm.Rubric{
					{
						ID: "r1",
					},
				},
				JudgeModel: &criterionllm.JudgeModelOptions{
					ProviderName: "provider",
					ModelName:    "model",
					APIKey:       "secret",
					Generation:   &model.GenerationConfig{},
				},
			},
		},
	}

	dst, err := CloneEvalMetric(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	require.NotNil(t, dst.Criterion)
	require.NotNil(t, dst.Criterion.ToolTrajectory)
	assert.Nil(t, dst.Criterion.ToolTrajectory.DefaultStrategy)
	assert.Contains(t, dst.Criterion.ToolTrajectory.ToolStrategy, "tool")
	assert.Nil(t, dst.Criterion.ToolTrajectory.ToolStrategy["tool"])

	require.NotNil(t, dst.Criterion.LLMJudge)
	require.NotNil(t, dst.Criterion.LLMJudge.Rubrics)
	require.Len(t, dst.Criterion.LLMJudge.Rubrics, 1)
	assert.Nil(t, dst.Criterion.LLMJudge.Rubrics[0].Content)

	require.NotNil(t, dst.Criterion.LLMJudge.JudgeModel)
	assert.Nil(t, dst.Criterion.LLMJudge.JudgeModel.NumSamples)
	require.NotNil(t, dst.Criterion.LLMJudge.JudgeModel.Generation)
	assert.Nil(t, dst.Criterion.LLMJudge.JudgeModel.Generation.Stop)
	assert.Nil(t, dst.Criterion.LLMJudge.JudgeModel.Generation.ReasoningEffort)
	assert.Nil(t, dst.Criterion.LLMJudge.JudgeModel.Generation.ThinkingEnabled)
}

func TestCloneEvalMetric_ErrorFromJudgeModelExtraFields(t *testing.T) {
	src := &metric.EvalMetric{
		MetricName: "metric-err-extra-fields",
		Threshold:  0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeModel: &criterionllm.JudgeModelOptions{
					ProviderName: "provider",
					ModelName:    "model",
					APIKey:       "secret",
					ExtraFields: map[string]any{
						"bad": make(chan int),
					},
				},
			},
		},
	}

	dst, err := CloneEvalMetric(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneEvalSetResult_NilFields(t *testing.T) {
	src := &evalresult.EvalSetResult{
		EvalSetResultID:   "result-nil-fields",
		EvalSetResultName: "result-nil-fields",
		EvalSetID:         "set-1",
		Summary: &evalresult.EvalSetResultSummary{
			RunStatusCounts:   nil,
			RunSummaries:      nil,
			EvalCaseSummaries: nil,
		},
	}

	dst, err := CloneEvalSetResult(src)
	require.NoError(t, err)
	require.NotNil(t, dst)
	require.NotNil(t, dst.Summary)
	assert.Nil(t, dst.Summary.RunStatusCounts)
	assert.Nil(t, dst.Summary.RunSummaries)
	assert.Nil(t, dst.Summary.EvalCaseSummaries)
}

func TestCloneEvalSetResult_ErrorFromInvocationTools(t *testing.T) {
	src := &evalresult.EvalSetResult{
		EvalSetResultID: "result-err-invocation",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalID: "case-1",
				EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
					{
						ActualInvocation: &evalset.Invocation{
							InvocationID: "inv-1",
							Tools: []*evalset.Tool{
								{
									ID:        "tool-1",
									Name:      "tool",
									Arguments: map[string]any{"bad": make(chan int)},
								},
							},
						},
					},
				},
			},
		},
	}

	dst, err := CloneEvalSetResult(src)
	require.Error(t, err)
	assert.Nil(t, dst)
}

func assertNotAliasedAndEqual(t *testing.T, src, dst any) {
	t.Helper()
	require.NotNil(t, src)
	require.NotNil(t, dst)

	srcVal := reflect.ValueOf(src)
	dstVal := reflect.ValueOf(dst)
	require.Equal(t, srcVal.Type(), dstVal.Type())

	visited := make(map[visitKey]struct{})
	assertNotAliasedAndEqualValue(t, srcVal, dstVal, visited, "root")
}

type visitKey struct {
	src uintptr
	dst uintptr
	typ reflect.Type
}

func assertNotAliasedAndEqualValue(t *testing.T, src, dst reflect.Value, visited map[visitKey]struct{}, path string) {
	t.Helper()

	if !src.IsValid() || !dst.IsValid() {
		assert.Equal(t, src.IsValid(), dst.IsValid(), path)
		return
	}
	require.Equal(t, src.Type(), dst.Type(), path)

	switch src.Kind() {
	case reflect.Interface:
		if src.IsNil() {
			assert.True(t, dst.IsNil(), path)
			return
		}
		require.False(t, dst.IsNil(), path)
		assertNotAliasedAndEqualValue(t, src.Elem(), dst.Elem(), visited, path+".<iface>")
	case reflect.Pointer:
		if src.IsNil() {
			assert.True(t, dst.IsNil(), path)
			return
		}
		require.False(t, dst.IsNil(), path)
		key := visitKey{src: src.Pointer(), dst: dst.Pointer(), typ: src.Type()}
		if _, ok := visited[key]; ok {
			return
		}
		visited[key] = struct{}{}
		assert.NotEqual(t, src.Pointer(), dst.Pointer(), path)
		assertNotAliasedAndEqualValue(t, src.Elem(), dst.Elem(), visited, path+".*")
	case reflect.Map:
		if src.IsNil() {
			assert.True(t, dst.IsNil(), path)
			return
		}
		require.False(t, dst.IsNil(), path)
		key := visitKey{src: src.Pointer(), dst: dst.Pointer(), typ: src.Type()}
		if _, ok := visited[key]; ok {
			return
		}
		visited[key] = struct{}{}
		assert.NotEqual(t, src.Pointer(), dst.Pointer(), path)
		assert.Equal(t, src.Len(), dst.Len(), path)
		for _, k := range src.MapKeys() {
			srcValue := src.MapIndex(k)
			dstValue := dst.MapIndex(k)
			require.True(t, dstValue.IsValid(), path)
			assertNotAliasedAndEqualValue(t, srcValue, dstValue, visited, path+"["+valueSummary(k)+"]")
		}
	case reflect.Slice:
		if src.IsNil() {
			assert.True(t, dst.IsNil(), path)
			return
		}
		require.False(t, dst.IsNil(), path)
		if src.Len() > 0 {
			assert.NotEqual(t, src.Pointer(), dst.Pointer(), path)
		}
		require.Equal(t, src.Len(), dst.Len(), path)
		for i := 0; i < src.Len(); i++ {
			assertNotAliasedAndEqualValue(t, src.Index(i), dst.Index(i), visited, path+"["+valueSummary(reflect.ValueOf(i))+"]")
		}
	case reflect.Struct:
		if hasNoExportedFields(src.Type()) {
			assert.True(t, reflect.DeepEqual(src.Interface(), dst.Interface()), path)
			return
		}
		for i := 0; i < src.NumField(); i++ {
			field := src.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			if isJSONDash(field.Tag) {
				continue
			}
			assertNotAliasedAndEqualValue(t, src.Field(i), dst.Field(i), visited, path+"."+field.Name)
		}
	default:
		assert.True(t, reflect.DeepEqual(src.Interface(), dst.Interface()), path)
	}
}

func hasNoExportedFields(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).PkgPath == "" {
			return false
		}
	}
	return true
}

func isJSONDash(tag reflect.StructTag) bool {
	raw, ok := tag.Lookup("json")
	if !ok {
		return false
	}
	return strings.Split(raw, ",")[0] == "-"
}

func valueSummary(v reflect.Value) string {
	if !v.IsValid() {
		return "<invalid>"
	}
	if v.CanInterface() {
		switch v.Kind() {
		case reflect.String:
			return v.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return fmt.Sprintf("%d", v.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return fmt.Sprintf("%d", v.Uint())
		}
	}
	return v.Type().String()
}
