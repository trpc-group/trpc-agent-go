//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responsejson

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestUnmarshalContent(t *testing.T) {
	var payload struct {
		Score float64 `json:"score"`
	}

	err := UnmarshalContent(makeResponse("```json\n{\"score\":0.5}\n```"), &payload)
	require.NoError(t, err)
	assert.Equal(t, 0.5, payload.Score)
}

func TestUnmarshalContentRejectsInvalidResponse(t *testing.T) {
	var payload struct {
		Score float64 `json:"score"`
	}

	err := UnmarshalContent(nil, &payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")

	err = UnmarshalContent(&model.Response{}, &payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no choices in response")

	err = UnmarshalContent(makeResponse(""), &payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response text")
}

func TestUnmarshalContentRejectsInvalidJSON(t *testing.T) {
	var payload struct {
		Score float64 `json:"score"`
	}

	err := UnmarshalContent(makeResponse("definitely not json"), &payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal response json")
}

func TestUnmarshalContentRepairsMalformedJSON(t *testing.T) {
	var payload struct {
		Score float64 `json:"score"`
	}

	err := UnmarshalContent(makeResponse("{score:0.5}"), &payload)
	require.NoError(t, err)
	assert.Equal(t, 0.5, payload.Score)
}

func TestUnmarshalContentIgnoresTrailingProse(t *testing.T) {
	var payload struct {
		Score float64 `json:"score"`
	}

	raw := `{"score":0.75}

Judge note: overall acceptable.`
	err := UnmarshalContent(makeResponse(raw), &payload)
	require.NoError(t, err)
	assert.Equal(t, 0.75, payload.Score)
}

func makeResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}
}
