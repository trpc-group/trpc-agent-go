//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapCriterionCompareOverride(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Compare: func(actual, expected map[string]any) (bool, error) {
			called = true
			return true, nil
		},
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMapCriterionDeepEqualMismatch(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestMapCriterionDeepEqualSuccess(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreSkipsCompare(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Ignore: true,
		Compare: func(actual, expected map[string]any) (bool, error) {
			called = true
			return false, nil
		},
		MatchStrategy: JSONMatchStrategyExact,
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.False(t, called)
}

func TestJSONCriterionInvalidMatchStrategy(t *testing.T) {
	criterion := &JSONCriterion{
		MatchStrategy: JSONMatchStrategy("invalid"),
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTree(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"time": true,
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"city": "Shanghai",
		"time": "2025-03-01T12:00:00Z",
		"meta": map[string]any{
			"time": "12:00",
			"id":   "ticket-1",
		},
	}
	expected := map[string]any{
		"city": "Shanghai",
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeMismatch(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"time": true,
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"city": "Beijing",
		"time": "2025-03-01T12:00:00Z",
		"meta": map[string]any{
			"time": "12:00",
			"id":   "ticket-1",
		},
	}
	expected := map[string]any{
		"city": "Shanghai",
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTreeNoIgnoreConfigured(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{},
	}
	actual := map[string]any{
		"city": "Shanghai",
	}
	expected := map[string]any{
		"city": "Shanghai",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNestedOnlyChildIgnored(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":   "1",
			"time": "12:00",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNestedMismatchOnNonIgnored(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":   "1",
			"time": "12:00",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "2",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTreeOrderIndependentKeys(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"time": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
		"time": "12:00",
	}
	expected := map[string]any{
		"time": "ignored",
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNestedMapsWithExtraKeys(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":      "1",
			"time":    "12:00",
			"country": "CN",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}
