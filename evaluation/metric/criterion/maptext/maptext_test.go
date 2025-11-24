//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package maptext

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

func TestMapTextCriterionJSONRoundTrip(t *testing.T) {
	criterion := &MapTextCriterion{
		TextCriterion: &text.TextCriterion{
			Ignore:        true,
			MatchStrategy: text.TextMatchStrategyExact,
		},
	}
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"textCriterion":{"ignore":true,"matchStrategy":"exact"}}`, string(data))

	var decoded MapTextCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.NotNil(t, decoded.TextCriterion)
	assert.Equal(t, criterion.TextCriterion.Ignore, decoded.TextCriterion.Ignore)
	assert.Equal(t, criterion.TextCriterion.MatchStrategy, decoded.TextCriterion.MatchStrategy)
}
