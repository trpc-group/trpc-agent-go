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
	cxml "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/xml"
)

// CompareFunc defines custom final response comparison logic.
type CompareFunc func(actual, expected *evalset.Invocation) (bool, error)

// FinalResponseCriterion provides comparison rules for final response messages.
type FinalResponseCriterion struct {
	// Text compares the final response content as plain text.
	Text *text.TextCriterion `json:"text,omitempty"`
	// JSON compares the final response content as JSON.
	JSON *cjson.JSONCriterion `json:"json,omitempty"`
	// Rouge scores the final response content with ROUGE.
	Rouge *crouge.RougeCriterion `json:"rouge,omitempty"`
	// XML validates the final response content as XML.
	XML *cxml.XMLCriterion `json:"xml,omitempty"`
	// CompareName selects a registered comparison implementation by name.
	CompareName string `json:"compareName,omitempty"`
	// Compare allows overriding the built-in matching logic.
	Compare CompareFunc `json:"-"`
}

// New creates a FinalResponseCriterion with the provided options.
func New(opt ...Option) *FinalResponseCriterion {
	opts := newOptions(opt...)
	return &FinalResponseCriterion{
		Text:        opts.text,
		JSON:        opts.json,
		Rouge:       opts.rouge,
		XML:         opts.xml,
		CompareName: opts.compareName,
		Compare:     opts.compare,
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
	if !c.hasConfiguredCriterion() {
		return false, fmt.Errorf("final response criterion must configure text, json, rouge, or xml")
	}
	if actual == nil || expected == nil {
		return false, fmt.Errorf("actual or expected invocation is nil")
	}
	return c.matchFinalResponseContent(ctx, actual, expected)
}

func (c *FinalResponseCriterion) hasConfiguredCriterion() bool {
	return c.Text != nil || c.JSON != nil || c.Rouge != nil || c.XML != nil
}

func (c *FinalResponseCriterion) matchFinalResponseContent(ctx context.Context, actual, expected *evalset.Invocation) (bool, error) {
	actualMessage := actual.FinalResponse
	expectedMessage := expected.FinalResponse
	if actualMessage == nil && expectedMessage == nil {
		return true, nil
	}
	if actualMessage == nil {
		return false, fmt.Errorf("actual or expected final response is nil")
	}
	actualContent := actualMessage.Content
	expectedContent := ""
	if expectedMessage != nil {
		expectedContent = expectedMessage.Content
	}
	mismatchMessages := make([]string, 0, 5)
	appendMismatch := func(err error) {
		if err != nil {
			mismatchMessages = append(mismatchMessages, err.Error())
		}
	}
	if c.JSON != nil {
		appendMismatch(matchContentAsJSON(actualContent, expectedContent, c.JSON))
	}
	if c.Text != nil {
		appendMismatch(matchContentAsText(actualContent, expectedContent, c.Text))
	}
	if c.Rouge != nil {
		appendMismatch(matchContentAsRouge(ctx, actualContent, expectedContent, c.Rouge))
	}
	if c.XML != nil {
		appendMismatch(matchContentAsXML(actualContent, expectedContent, c.XML))
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
	ok, err := criterion.Match(json.RawMessage(actual), json.RawMessage(expected))
	if err != nil {
		return fmt.Errorf("json mismatch: %w", err)
	}
	if !ok {
		return fmt.Errorf("json mismatch")
	}
	return nil
}

// matchContentAsXML validates a string using an XMLCriterion.
func matchContentAsXML(actual, expected string, criterion *cxml.XMLCriterion) error {
	if criterion == nil || criterion.Ignore {
		return nil
	}
	ok, err := criterion.Match(actual, expected)
	if err != nil {
		return fmt.Errorf("xml mismatch: %w", err)
	}
	if !ok {
		return fmt.Errorf("xml mismatch")
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
