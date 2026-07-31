//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type inputs struct {
	Train      evalSet
	Validation evalSet
	Metrics    metricConfig
	Config     optimizationConfig
	Prompt     string
}

func loadInputs(dataDir string) (*inputs, error) {
	var loaded inputs
	if err := readJSON(dataDir+"/train.evalset.json", &loaded.Train); err != nil {
		return nil, err
	}
	if err := readJSON(dataDir+"/validation.evalset.json", &loaded.Validation); err != nil {
		return nil, err
	}
	if err := readJSON(dataDir+"/metrics.json", &loaded.Metrics); err != nil {
		return nil, err
	}
	if err := readJSON(dataDir+"/promptiter.json", &loaded.Config); err != nil {
		return nil, err
	}
	prompt, err := os.ReadFile(dataDir + "/baseline_prompt.txt")
	if err != nil {
		return nil, fmt.Errorf("read baseline prompt: %w", err)
	}
	loaded.Prompt = strings.TrimSpace(string(prompt))
	if err := validateInputs(&loaded); err != nil {
		return nil, err
	}
	return &loaded, nil
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s: trailing JSON data", path)
	}
	return nil
}

func validateInputs(loaded *inputs) error {
	if loaded == nil {
		return errors.New("inputs are nil")
	}
	if strings.TrimSpace(loaded.Prompt) == "" {
		return errors.New("baseline prompt is empty")
	}
	if err := validateEvalSet("train", loaded.Train); err != nil {
		return err
	}
	if err := validateEvalSet("validation", loaded.Validation); err != nil {
		return err
	}
	if len(loaded.Config.Candidates) == 0 {
		return errors.New("promptiter candidates are empty")
	}
	if loaded.Config.Gate.MaxCalls <= 0 || loaded.Config.Gate.MaxEstimatedTokens <= 0 {
		return errors.New("gate budgets must be positive")
	}
	return nil
}

func validateEvalSet(role string, set evalSet) error {
	if strings.TrimSpace(set.Name) == "" {
		return fmt.Errorf("%s eval set name is empty", role)
	}
	if len(set.Cases) == 0 {
		return fmt.Errorf("%s eval set cases are empty", role)
	}
	seen := make(map[string]struct{}, len(set.Cases))
	for _, evalCase := range set.Cases {
		if strings.TrimSpace(evalCase.ID) == "" {
			return fmt.Errorf("%s eval case id is empty", role)
		}
		if _, ok := seen[evalCase.ID]; ok {
			return fmt.Errorf("%s eval case %q is duplicated", role, evalCase.ID)
		}
		seen[evalCase.ID] = struct{}{}
		if len(evalCase.Required) == 0 {
			return fmt.Errorf("%s eval case %q has no required signals", role, evalCase.ID)
		}
		if strings.TrimSpace(evalCase.FailureCategory) == "" {
			return fmt.Errorf("%s eval case %q failure category is empty", role, evalCase.ID)
		}
	}
	return nil
}
