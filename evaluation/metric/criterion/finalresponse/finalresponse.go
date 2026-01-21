//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finalresponse defines criteria for comparing agent final responses.
package finalresponse

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// FinalResponseCriterion provides comparison rules for final response messages.
type FinalResponseCriterion struct {
	// Text compares the final response content as plain text.
	Text *text.TextCriterion `json:"text,omitempty"`
	// JSON compares the final response content as JSON.
	JSON *criterionjson.JSONCriterion `json:"json,omitempty"`
	// Compare allows overriding the built-in matching logic.
	Compare func(actual, expected *evalset.Invocation) (bool, error) `json:"-"`
}

// New creates a FinalResponseCriterion with the provided options.
func New(opt ...Option) *FinalResponseCriterion {
	opts := newOptions(opt...)
	return &FinalResponseCriterion{
		Text:    opts.text,
		JSON:    opts.json,
		Compare: opts.compare,
	}
}

// Match compares the final responses of actual and expected invocations.
func (c *FinalResponseCriterion) Match(actual, expected *evalset.Invocation) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("final response criterion is nil")
	}
	if c.Compare != nil {
		return c.Compare(actual, expected)
	}
	if c.Text == nil && c.JSON == nil {
		return false, fmt.Errorf("final response criterion must configure text and/or json")
	}
	if actual == nil || expected == nil {
		return false, fmt.Errorf("actual or expected invocation is nil")
	}
	if actual.FinalResponse == nil && expected.FinalResponse == nil {
		return true, nil
	}
	if actual.FinalResponse == nil || expected.FinalResponse == nil {
		return false, fmt.Errorf("actual or expected final response is nil")
	}

	mismatchMessages := make([]string, 0, 2)

	if c.JSON != nil {
		if err := matchContentAsJSON(actual.FinalResponse.Content, expected.FinalResponse.Content, c.JSON); err != nil {
			mismatchMessages = append(mismatchMessages, err.Error())
		}
	}
	if c.Text != nil {
		if err := matchContentAsText(actual.FinalResponse.Content, expected.FinalResponse.Content, c.Text); err != nil {
			mismatchMessages = append(mismatchMessages, err.Error())
		}
	}

	if len(mismatchMessages) > 0 {
		return false, errors.New(strings.Join(mismatchMessages, "; "))
	}
	return true, nil
}

func matchContentAsText(actual, expected string, criterion *text.TextCriterion) error {
	if criterion == nil || criterion.Ignore {
		return nil
	}
	ok, err := criterion.Match(actual, expected)
	if err != nil {
		return fmt.Errorf("text mismatch: %w", err)
	}
	if !ok {
		return fmt.Errorf("text mismatch")
	}
	return nil
}

func matchContentAsJSON(actual, expected string, criterion *criterionjson.JSONCriterion) error {
	if criterion == nil || criterion.Ignore {
		return nil
	}
	actualValue, err := parseContentAsJSON(actual)
	if err != nil {
		return fmt.Errorf("parse actual final response as json: %w", err)
	}
	expectedValue, err := parseContentAsJSON(expected)
	if err != nil {
		return fmt.Errorf("parse expected final response as json: %w", err)
	}
	ok, err := criterion.Match(actualValue, expectedValue)
	if err != nil {
		return fmt.Errorf("json mismatch: %w", err)
	}
	if !ok {
		return fmt.Errorf("json mismatch")
	}
	return nil
}

func parseContentAsJSON(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	var v any
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
