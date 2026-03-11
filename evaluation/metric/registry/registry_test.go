//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package registry

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	criterionrouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	criteriontext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

type whitespaceTokenizer struct{}

// Tokenize splits text on whitespace without normalization.
func (whitespaceTokenizer) Tokenize(text string) []string {
	return strings.Fields(text)
}

func TestRegistryResolve_TextCompareName(t *testing.T) {
	reg := New()
	err := reg.RegisterTextCompare("trim_equal", func(actual, expected string) (bool, error) {
		return strings.TrimSpace(actual) == strings.TrimSpace(expected), nil
	})
	require.NoError(t, err)

	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Text: &criteriontext.TextCriterion{CompareName: "trim_equal"},
			},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	require.NotNil(t, evalMetric.Criterion.FinalResponse.Text.Compare)
	ok, err := evalMetric.Criterion.FinalResponse.Text.Match(" value ", "value")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRegistryResolve_RougeTokenizerName(t *testing.T) {
	reg := New()
	err := reg.RegisterRougeTokenizer("whitespace", whitespaceTokenizer{})
	require.NoError(t, err)

	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Rouge: &criterionrouge.RougeCriterion{TokenizerName: "whitespace"},
			},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	assert.NotNil(t, evalMetric.Criterion.FinalResponse.Rouge.Tokenizer)
}

func TestRegistryResolve_ToolTrajectoryNestedCompareNames(t *testing.T) {
	reg := New()
	err := reg.RegisterTextCompare("case_insensitive_equal", func(actual, expected string) (bool, error) {
		return strings.EqualFold(actual, expected), nil
	})
	require.NoError(t, err)
	err = reg.RegisterJSONCompare("allow_delta", func(actual, expected any) (bool, error) {
		actualMap, actualOK := actual.(map[string]any)
		expectedMap, expectedOK := expected.(map[string]any)
		if !actualOK || !expectedOK {
			return false, nil
		}
		return actualMap["value"] == expectedMap["value"], nil
	})
	require.NoError(t, err)

	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{
				DefaultStrategy: &tooltrajectory.ToolTrajectoryStrategy{
					Name:      &criteriontext.TextCriterion{CompareName: "case_insensitive_equal"},
					Arguments: &criterionjson.JSONCriterion{CompareName: "allow_delta"},
				},
			},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	strategy := evalMetric.Criterion.ToolTrajectory.DefaultStrategy
	require.NotNil(t, strategy.Name.Compare)
	require.NotNil(t, strategy.Arguments.Compare)
	ok, err := strategy.Name.Match("Tool", "tool")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = strategy.Arguments.Match(map[string]any{"value": 1}, map[string]any{"value": 1})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRegistryResolve_PrefersExplicitCompare(t *testing.T) {
	reg := New()
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				CompareName: "missing",
				Compare: func(actual, expected *evalset.Invocation) (bool, error) {
					return true, nil
				},
				Text: &criteriontext.TextCriterion{CompareName: "also_missing"},
			},
		},
	}

	err := reg.Resolve(evalMetric)
	require.NoError(t, err)
}

func TestRegistryResolve_TopLevelCompareNameSkipsNestedResolution(t *testing.T) {
	reg := New()
	err := reg.RegisterFinalResponseCompare("always_pass", func(actual, expected *evalset.Invocation) (bool, error) {
		return true, nil
	})
	require.NoError(t, err)

	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				CompareName: "always_pass",
				Text:        &criteriontext.TextCriterion{CompareName: "missing"},
			},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	require.NotNil(t, evalMetric.Criterion.FinalResponse.Compare)
	assert.Nil(t, evalMetric.Criterion.FinalResponse.Text.Compare)
}

func TestRegistryResolve_MissingRegistration(t *testing.T) {
	reg := New()
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Text: &criteriontext.TextCriterion{CompareName: "missing"},
			},
		},
	}

	err := reg.Resolve(evalMetric)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.Contains(t, err.Error(), "text compare missing not found")
}

func TestRegistryValidation(t *testing.T) {
	reg := New()

	assert.ErrorContains(t, reg.RegisterTextCompare("", func(actual, expected string) (bool, error) {
		return true, nil
	}), "text compare name is empty")
	assert.ErrorContains(t, reg.RegisterJSONCompare("", func(actual, expected any) (bool, error) {
		return true, nil
	}), "json compare name is empty")
	assert.ErrorContains(t, reg.RegisterToolTrajectoryCompare("", func(actual, expected *evalset.Invocation) (bool, error) {
		return true, nil
	}), "tool trajectory compare name is empty")
	assert.ErrorContains(t, reg.RegisterFinalResponseCompare("", func(actual, expected *evalset.Invocation) (bool, error) {
		return true, nil
	}), "final response compare name is empty")
	assert.ErrorContains(t, reg.RegisterRougeTokenizer("", whitespaceTokenizer{}), "rouge tokenizer name is empty")
	assert.ErrorContains(t, reg.Resolve(nil), "eval metric is nil")
}

func TestRegistryValidation_NilImplementations(t *testing.T) {
	reg := New()
	var tokenizer criterionrouge.Tokenizer
	assert.ErrorContains(t, reg.RegisterTextCompare("text", nil), "text compare is nil")
	assert.ErrorContains(t, reg.RegisterJSONCompare("json", nil), "json compare is nil")
	assert.ErrorContains(t, reg.RegisterToolTrajectoryCompare("tool", nil), "tool trajectory compare is nil")
	assert.ErrorContains(t, reg.RegisterFinalResponseCompare("final", nil), "final response compare is nil")
	assert.ErrorContains(t, reg.RegisterRougeTokenizer("rouge", tokenizer), "rouge tokenizer is nil")
}

func TestRegistryResolve_NilCriterion(t *testing.T) {
	reg := New()
	err := reg.Resolve(&metric.EvalMetric{MetricName: "metric"})
	assert.NoError(t, err)
}

func TestRegistryResolve_RegisteredFunctionsCanBeUsed(t *testing.T) {
	reg := New()
	err := reg.RegisterToolTrajectoryCompare("invocation_id_equal", func(actual, expected *evalset.Invocation) (bool, error) {
		return actual.InvocationID == expected.InvocationID, nil
	})
	require.NoError(t, err)

	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{CompareName: "invocation_id_equal"},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	ok, err := evalMetric.Criterion.ToolTrajectory.Match(
		&evalset.Invocation{InvocationID: "same"},
		&evalset.Invocation{InvocationID: "same"},
	)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRegistryResolve_RegisteredRougeTokenizerCanBeUsed(t *testing.T) {
	reg := New()
	err := reg.RegisterRougeTokenizer("whitespace", whitespaceTokenizer{})
	require.NoError(t, err)

	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Rouge: &criterionrouge.RougeCriterion{
					RougeType:     "rouge1",
					TokenizerName: "whitespace",
				},
			},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	result, err := evalMetric.Criterion.FinalResponse.Rouge.Match(context.Background(), "a-b", "a")
	require.NoError(t, err)
	assert.InDelta(t, 0.0, result.Value, 1e-12)
}

func TestRegistryResolve_ToolSpecificStrategyAndResultCompareNames(t *testing.T) {
	reg := New()
	err := reg.RegisterTextCompare("trim_equal", func(actual, expected string) (bool, error) {
		return strings.TrimSpace(actual) == strings.TrimSpace(expected), nil
	})
	require.NoError(t, err)
	err = reg.RegisterJSONCompare("match_result", func(actual, expected any) (bool, error) {
		actualMap, actualOK := actual.(map[string]any)
		expectedMap, expectedOK := expected.(map[string]any)
		if !actualOK || !expectedOK {
			return false, nil
		}
		return actualMap["status"] == expectedMap["status"], nil
	})
	require.NoError(t, err)
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{
				ToolStrategy: map[string]*tooltrajectory.ToolTrajectoryStrategy{
					"search": {
						Name:   &criteriontext.TextCriterion{CompareName: "trim_equal"},
						Result: &criterionjson.JSONCriterion{CompareName: "match_result"},
					},
				},
			},
		},
	}

	err = reg.Resolve(evalMetric)
	require.NoError(t, err)
	strategy := evalMetric.Criterion.ToolTrajectory.ToolStrategy["search"]
	require.NotNil(t, strategy.Name.Compare)
	require.NotNil(t, strategy.Result.Compare)
	ok, err := strategy.Name.Match(" search ", "search")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = strategy.Result.Match(map[string]any{"status": "ok"}, map[string]any{"status": "ok"})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRegistryResolve_MissingNamedImplementations(t *testing.T) {
	reg := New()
	tests := []struct {
		name    string
		metric  *metric.EvalMetric
		wantErr string
	}{
		{
			name: "missing_json_compare",
			metric: &metric.EvalMetric{
				Criterion: &criterion.Criterion{
					FinalResponse: &finalresponse.FinalResponseCriterion{
						JSON: &criterionjson.JSONCriterion{CompareName: "missing_json"},
					},
				},
			},
			wantErr: "json compare missing_json not found",
		},
		{
			name: "missing_tool_compare",
			metric: &metric.EvalMetric{
				Criterion: &criterion.Criterion{
					ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{CompareName: "missing_tool"},
				},
			},
			wantErr: "tool trajectory compare missing_tool not found",
		},
		{
			name: "missing_final_response_compare",
			metric: &metric.EvalMetric{
				Criterion: &criterion.Criterion{
					FinalResponse: &finalresponse.FinalResponseCriterion{CompareName: "missing_final"},
				},
			},
			wantErr: "final response compare missing_final not found",
		},
		{
			name: "missing_rouge_tokenizer",
			metric: &metric.EvalMetric{
				Criterion: &criterion.Criterion{
					FinalResponse: &finalresponse.FinalResponseCriterion{
						Rouge: &criterionrouge.RougeCriterion{TokenizerName: "missing_tokenizer"},
					},
				},
			},
			wantErr: "rouge tokenizer missing_tokenizer not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := reg.Resolve(tc.metric)
			require.Error(t, err)
			assert.True(t, errors.Is(err, os.ErrNotExist))
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
