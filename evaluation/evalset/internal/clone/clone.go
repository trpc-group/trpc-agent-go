//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clone provides functions to clone evaluation sets and cases.
package clone

import (
	"encoding/json"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// CloneEvalCase clones an evaluation case.
func CloneEvalCase(evalCase *evalset.EvalCase) (*evalset.EvalCase, error) {
	if evalCase == nil {
		return nil, errors.New("eval case is nil")
	}
	data, err := json.Marshal(evalCase)
	if err != nil {
		return nil, err
	}
	var clonedEvalCase evalset.EvalCase
	if err := json.Unmarshal(data, &clonedEvalCase); err != nil {
		return nil, err
	}
	return &clonedEvalCase, nil
}

// CloneEvalSet clones an evaluation set.
func CloneEvalSet(evalSet *evalset.EvalSet) (*evalset.EvalSet, error) {
	if evalSet == nil {
		return nil, errors.New("eval set is nil")
	}
	data, err := json.Marshal(evalSet)
	if err != nil {
		return nil, err
	}
	var clonedEvalSet evalset.EvalSet
	if err := json.Unmarshal(data, &clonedEvalSet); err != nil {
		return nil, err
	}
	return &clonedEvalSet, nil
}
