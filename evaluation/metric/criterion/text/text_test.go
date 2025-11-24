//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package text

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTextCriterionJSONRoundTrip(t *testing.T) {
	criterion := &TextCriterion{
		Ignore:          true,
		CaseInsensitive: true,
		MatchStrategy:   TextMatchStrategyRegex,
	}
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"ignore":true,"caseInsensitive":true,"matchStrategy":"regex"}`, string(data))

	var decoded TextCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, criterion.Ignore, decoded.Ignore)
	assert.Equal(t, criterion.CaseInsensitive, decoded.CaseInsensitive)
	assert.Equal(t, criterion.MatchStrategy, decoded.MatchStrategy)
}
