//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package xml

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestXMLCriterionJSONRoundTrip(t *testing.T) {
	criterion := New(WithIgnore(true), WithValid(true))
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"ignore":true,"valid":true}`, string(data))

	var decoded XMLCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.True(t, decoded.Ignore)
	assert.True(t, decoded.Valid)
}

func TestXMLCriterionMatchValid(t *testing.T) {
	cases := []string{
		`<root><child>value</child></root>`,
		`<?xml version="1.0"?><root xmlns:h="urn:test"><h:child /></root>`,
		`<!-- comment --><root/>`,
	}

	for _, content := range cases {
		ok, err := New(WithValid(true)).Match(content, "")
		assert.True(t, ok, content)
		assert.NoError(t, err, content)
	}
}

func TestXMLCriterionMatchInvalid(t *testing.T) {
	cases := []string{
		``,
		`   `,
		`<root>`,
		`<root></other>`,
		`<a/><b/>`,
		`text<root/>`,
	}

	for _, content := range cases {
		ok, err := New(WithValid(true)).Match(content, "")
		assert.False(t, ok, content)
		assert.Error(t, err, content)
	}
}

func TestXMLCriterionRequiresValidOrCompare(t *testing.T) {
	ok, err := New().Match(`<root/>`, "")
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestXMLCriterionCompare(t *testing.T) {
	called := false
	criterion := New(WithValid(true), WithCompare(func(actual, expected string) (bool, error) {
		called = true
		return actual == expected, nil
	}))
	ok, err := criterion.Match(`<root/>`, `<root/>`)
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestXMLCriterionIgnore(t *testing.T) {
	ok, err := (&XMLCriterion{Ignore: true}).Match("", "")
	assert.True(t, ok)
	assert.NoError(t, err)
}
