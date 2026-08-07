//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

type resultKey struct {
	EvalSetID  string
	EvalCaseID string
	MetricName string
}

type catalog struct {
	EvalSetID   string
	EvalCaseIDs []string
	MetricNames []string
	ResultKeys  map[resultKey]struct{}
}

func buildCatalog(evalSetData, metricData []byte) (*catalog, error) {
	var set evalset.EvalSet
	if err := json.Unmarshal(evalSetData, &set); err != nil {
		return nil, fmt.Errorf("decode evaluation set: %w", err)
	}
	if strings.TrimSpace(set.EvalSetID) == "" {
		return nil, errors.New("evaluation set ID is empty")
	}
	if len(set.EvalCases) == 0 {
		return nil, errors.New("evaluation set has no evaluation cases")
	}

	caseIDs := make([]string, 0, len(set.EvalCases))
	seenCases := make(map[string]struct{}, len(set.EvalCases))
	for i, evalCase := range set.EvalCases {
		if evalCase == nil {
			return nil, fmt.Errorf("null evaluation case at index %d", i)
		}
		if strings.TrimSpace(evalCase.EvalID) == "" {
			return nil, fmt.Errorf("evaluation case ID at index %d is empty", i)
		}
		if _, ok := seenCases[evalCase.EvalID]; ok {
			return nil, fmt.Errorf("duplicate evaluation case ID %q", evalCase.EvalID)
		}
		seenCases[evalCase.EvalID] = struct{}{}
		caseIDs = append(caseIDs, evalCase.EvalID)
	}

	var rawMetrics []json.RawMessage
	if err := json.Unmarshal(metricData, &rawMetrics); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}
	if len(rawMetrics) == 0 {
		return nil, errors.New("metric list has no metrics")
	}
	for i, raw := range rawMetrics {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("null metric at index %d", i)
		}
	}

	var metrics []metric.EvalMetric
	if err := json.Unmarshal(metricData, &metrics); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}
	metricNames := make([]string, 0, len(metrics))
	seenMetrics := make(map[string]struct{}, len(metrics))
	for i, evalMetric := range metrics {
		if strings.TrimSpace(evalMetric.MetricName) == "" {
			return nil, fmt.Errorf("metric name at index %d is empty", i)
		}
		if _, ok := seenMetrics[evalMetric.MetricName]; ok {
			return nil, fmt.Errorf("duplicate metric name %q", evalMetric.MetricName)
		}
		seenMetrics[evalMetric.MetricName] = struct{}{}
		metricNames = append(metricNames, evalMetric.MetricName)
	}

	keys := make(map[resultKey]struct{}, len(caseIDs)*len(metricNames))
	for _, evalCaseID := range caseIDs {
		for _, metricName := range metricNames {
			keys[resultKey{
				EvalSetID:  set.EvalSetID,
				EvalCaseID: evalCaseID,
				MetricName: metricName,
			}] = struct{}{}
		}
	}
	return &catalog{
		EvalSetID:   set.EvalSetID,
		EvalCaseIDs: caseIDs,
		MetricNames: metricNames,
		ResultKeys:  keys,
	}, nil
}

func fingerprintInputs(inputs ...[]byte) string {
	hash := sha256.New()
	var length [8]byte
	for _, input := range inputs {
		binary.BigEndian.PutUint64(length[:], uint64(len(input)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(input)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
