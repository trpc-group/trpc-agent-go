//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLlmCriterion(t *testing.T) {
	crit := New("provider", "model", WithNumSamples(2), WithBaseURL("base"))
	require.NotNil(t, crit.JudgeModel)
	assert.Equal(t, "provider", crit.JudgeModel.ProviderName)
	assert.Equal(t, "model", crit.JudgeModel.ModelName)
	assert.Equal(t, 2, *crit.JudgeModel.NumSamples)
	assert.Equal(t, "base", crit.JudgeModel.BaseURL)
}

func TestJudgeModelAPIKeyOmittedFromJSON(t *testing.T) {
	crit := New("provider", "model", WithAPIKey("secret"))
	data, err := json.Marshal(crit)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "apiKey")
	assert.NotContains(t, string(data), "secret")

	var decoded LLMCriterion
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	require.NotNil(t, decoded.JudgeModel)
	assert.Equal(t, "", decoded.JudgeModel.APIKey)

	err = json.Unmarshal([]byte(`{"judgeModel":{"providerName":"p","modelName":"m","apiKey":"secret"}}`), &decoded)
	require.NoError(t, err)
	require.NotNil(t, decoded.JudgeModel)
	assert.Equal(t, "secret", decoded.JudgeModel.APIKey)
}

func TestJudgeModelEnvExpansion(t *testing.T) {
	t.Setenv("TEST_JUDGE_API_KEY", "secret")
	t.Setenv("TEST_JUDGE_BASE_URL", "https://example.com")
	t.Setenv("TEST_JUDGE_PROVIDER_NAME", "env-provider")
	t.Setenv("TEST_JUDGE_MODEL_NAME", "env-model")
	t.Setenv("TEST_JUDGE_VARIANT", "deepseek")

	var decoded LLMCriterion
	err := json.Unmarshal([]byte(`{"judgeModel":{"providerName":"${TEST_JUDGE_PROVIDER_NAME}","modelName":"${TEST_JUDGE_MODEL_NAME}","variant":"${TEST_JUDGE_VARIANT}","apiKey":"${TEST_JUDGE_API_KEY}","baseURL":"${TEST_JUDGE_BASE_URL}"}}`), &decoded)
	require.NoError(t, err)
	require.NotNil(t, decoded.JudgeModel)
	assert.Equal(t, "env-provider", decoded.JudgeModel.ProviderName)
	assert.Equal(t, "env-model", decoded.JudgeModel.ModelName)
	assert.Equal(t, "deepseek", decoded.JudgeModel.Variant)
	assert.Equal(t, "secret", decoded.JudgeModel.APIKey)
	assert.Equal(t, "https://example.com", decoded.JudgeModel.BaseURL)
}

func TestJudgeModelEnvExpansionPartialAPIKey(t *testing.T) {
	t.Setenv("TEST_JUDGE_API_KEY_PARTIAL", "secret")

	var decoded LLMCriterion
	err := json.Unmarshal([]byte(`{"judgeModel":{"providerName":"p","modelName":"m","apiKey":"prefix-${TEST_JUDGE_API_KEY_PARTIAL}-suffix"}}`), &decoded)
	require.NoError(t, err)
	require.NotNil(t, decoded.JudgeModel)
	assert.Equal(t, "prefix-secret-suffix", decoded.JudgeModel.APIKey)
}

func TestJudgeModelUnmarshalJSONFails(t *testing.T) {
	var opts JudgeModelOptions
	err := opts.UnmarshalJSON([]byte(`{"providerName":123}`))
	require.Error(t, err)
}
