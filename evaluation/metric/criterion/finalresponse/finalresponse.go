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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// FinalResponseCriterion provides comparison rules for final response messages.
type FinalResponseCriterion struct {
	// Text compares the final response content as plain text.
	Text *text.TextCriterion `json:"text,omitempty"`
	// JSON compares the final response content as JSON.
	JSON *cjson.JSONCriterion `json:"json,omitempty"`
	// Rouge scores the final response content with ROUGE.
	Rouge *crouge.RougeCriterion `json:"rouge,omitempty"`
	// Compare allows overriding the built-in matching logic.
	Compare func(actual, expected *evalset.Invocation) (bool, error) `json:"-"`
}

// New creates a FinalResponseCriterion with the provided options.
func New(opt ...Option) *FinalResponseCriterion {
	opts := newOptions(opt...)
	return &FinalResponseCriterion{
		Text:    opts.text,
		JSON:    opts.json,
		Rouge:   opts.rouge,
		Compare: opts.compare,
	}
}

// Match compares the final responses of actual and expected invocations using the provided context.
func (c *FinalResponseCriterion) Match(ctx context.Context, actual, expected *evalset.Invocation) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("final response criterion is nil")
	}
	if c.Compare != nil {
		return c.Compare(actual, expected)
	}
	if c.Text == nil && c.JSON == nil && c.Rouge == nil {
		return false, fmt.Errorf("final response criterion must configure text, json, or rouge")
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

	mismatchMessages := make([]string, 0, 3)

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
	if c.Rouge != nil {
		if err := matchContentAsRouge(ctx, actual.FinalResponse.Content, expected.FinalResponse.Content, c.Rouge); err != nil {
			mismatchMessages = append(mismatchMessages, err.Error())
		}
	}

	if len(mismatchMessages) > 0 {
		return false, errors.New(strings.Join(mismatchMessages, "; "))
	}
	return true, nil
}

// matchContentAsText compares two strings using a TextCriterion.
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

// matchContentAsJSON parses and compares two JSON strings using a JSONCriterion.
func matchContentAsJSON(actual, expected string, criterion *cjson.JSONCriterion) error {
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

// matchContentAsRouge scores and validates two strings using a RougeCriterion.
func matchContentAsRouge(ctx context.Context, actual, expected string, criterion *crouge.RougeCriterion) error {
	if criterion == nil || criterion.Ignore {
		return nil
	}
	result, err := criterion.Match(ctx, expected, actual)
	if err != nil {
		return fmt.Errorf("rouge mismatch: %w", err)
	}
	if !result.Passed {
		return fmt.Errorf("rouge mismatch: %s", result.Reason())
	}
	return nil
}

// parseContentAsJSON parses a JSON string into an untyped Go value.
func parseContentAsJSON(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	var v any
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
