//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package attribution

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

type attributionCorpusEntry struct {
	ID       string                     `json:"id"`
	Expected regression.FailureCategory `json:"expected"`
	Case     regression.CaseResult      `json:"case"`
}

func TestFrozenAttributionCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/corpus.json")
	require.NoError(t, err)
	var corpus []attributionCorpusEntry
	require.NoError(t, json.Unmarshal(data, &corpus))
	require.NotEmpty(t, corpus)

	matrix := make(map[regression.FailureCategory]map[regression.FailureCategory]int)
	totals := make(map[regression.FailureCategory]int)
	correct := make(map[regression.FailureCategory]int)
	for _, entry := range corpus {
		actual, attributeErr := NewRules().Attribute(context.Background(), &entry.Case)
		require.NoError(t, attributeErr, entry.ID)
		require.NotEmpty(t, actual.Reason, entry.ID)
		require.NotEmpty(t, actual.Evidence, entry.ID)
		if matrix[entry.Expected] == nil {
			matrix[entry.Expected] = make(map[regression.FailureCategory]int)
		}
		matrix[entry.Expected][actual.Category]++
		totals[entry.Expected]++
		if actual.Category == entry.Expected {
			correct[entry.Expected]++
		}
	}

	categories := make([]string, 0, len(totals))
	for category := range totals {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)
	allCorrect := 0
	for _, name := range categories {
		category := regression.FailureCategory(name)
		allCorrect += correct[category]
		t.Logf("attribution category=%s correct=%d total=%d matrix=%v",
			category, correct[category], totals[category], matrix[category])
		require.GreaterOrEqual(t, float64(correct[category])/float64(totals[category]), .50,
			"category %s is below minimum accuracy", category)
	}
	require.GreaterOrEqual(t, float64(allCorrect)/float64(len(corpus)), .75)
}
