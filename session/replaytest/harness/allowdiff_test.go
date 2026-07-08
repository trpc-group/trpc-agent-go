//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyScoreIsAllowed(t *testing.T) {
	v, expl := Classify("sqlite", Capabilities{}, Diff{
		Category: "memory", FieldPath: "memories[0].score",
		BaselineValue: "0.5", CompareValue: "0.5",
	})
	require.Equal(t, VerdictAllowedDiff, v)
	require.NotEmpty(t, expl)
}

func TestClassifyScoreWithinEpsilonIsAllowed(t *testing.T) {
	v, _ := Classify("sqlite", Capabilities{}, Diff{
		Category: "memory", FieldPath: "memories[0].score",
		BaselineValue: "0.5000000", CompareValue: "0.5000004",
	})
	require.Equal(t, VerdictAllowedDiff, v)
}

func TestClassifyScoreBeyondEpsilonIsInconsistent(t *testing.T) {
	v, _ := Classify("sqlite", Capabilities{}, Diff{
		Category: "memory", FieldPath: "memories[0].score",
		BaselineValue: "0.5", CompareValue: "0.9",
	})
	require.Equal(t, VerdictInconsistent, v)
}

func TestClassifySummaryTextIsInconsistent(t *testing.T) {
	v, _ := Classify("sqlite", Capabilities{}, Diff{Category: "summary", FieldPath: "summaries[\"\"].text"})
	require.Equal(t, VerdictInconsistent, v)
}

func TestClassifySQLiteEmptyScopedSummaryIsAllowed(t *testing.T) {
	v, expl := Classify("sqlite", Capabilities{}, Diff{
		Category:      "summary",
		FieldPath:     "summaries[\"agent-a/tool\"]",
		BaselineValue: "summary:",
		CompareValue:  missingValue,
	})
	require.Equal(t, VerdictAllowedDiff, v)
	require.NotEmpty(t, expl)
}

func TestClassifyEventPageUnsupported(t *testing.T) {
	v, expl := Classify("redis", Capabilities{SupportsEventPage: false},
		Diff{Category: "eventpage", FieldPath: "events[3]"})
	require.Equal(t, VerdictUnsupported, v)
	require.NotEmpty(t, expl)
}

func TestClassifyEventPageSupportedIsInconsistent(t *testing.T) {
	v, _ := Classify("postgres", Capabilities{SupportsEventPage: true},
		Diff{Category: "eventpage", FieldPath: "events[3]"})
	require.Equal(t, VerdictInconsistent, v)
}

func TestClassifyTTLUnsupported(t *testing.T) {
	v, _ := Classify("sqlite", Capabilities{SupportsTTL: false},
		Diff{Category: "ttl", FieldPath: "state.expired"})
	require.Equal(t, VerdictUnsupported, v)
}

func TestClassifyPlainStateIsInconsistent(t *testing.T) {
	v, _ := Classify("sqlite", Capabilities{}, Diff{Category: "state", FieldPath: "state.lang"})
	require.Equal(t, VerdictInconsistent, v)
}
