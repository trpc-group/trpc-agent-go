//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package criterion

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

func TestCriterionNewDefaults(t *testing.T) {
	c := New()
	assert.NotNil(t, c.ToolTrajectory)
}

func TestCriterionWithToolTrajectory(t *testing.T) {
	custom := tooltrajectory.New()
	c := New(WithToolTrajectory(custom))
	assert.Equal(t, custom, c.ToolTrajectory)
}

func TestCriterionWithFinalResponse(t *testing.T) {
	custom := cfinalresponse.New()
	c := New(WithFinalResponse(custom))
	assert.Equal(t, custom, c.FinalResponse)
}

func TestCriterionJSONRoundTrip(t *testing.T) {
	c := &Criterion{
		ToolTrajectory: tooltrajectory.New(),
		FinalResponse:  cfinalresponse.New(),
	}
	data, err := json.Marshal(c)
	assert.NoError(t, err)

	var decoded Criterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.NotNil(t, decoded.ToolTrajectory)
	assert.NotNil(t, decoded.FinalResponse)
}
