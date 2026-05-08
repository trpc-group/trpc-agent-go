//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package xml defines XML-based content criteria.
package xml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// CompareFunc defines custom XML comparison logic.
type CompareFunc func(actual, expected string) (bool, error)

// XMLCriterion validates XML content.
type XMLCriterion struct {
	// Ignore skips XML validation when true.
	Ignore bool `json:"ignore,omitempty"`
	// Valid validates that the actual content is a well-formed XML document.
	Valid bool `json:"valid,omitempty"`
	// Compare overrides default validation when provided.
	Compare CompareFunc `json:"-"`
}

// New creates an XMLCriterion with the provided options.
func New(opt ...Option) *XMLCriterion {
	opts := newOptions(opt...)
	return &XMLCriterion{
		Ignore:  opts.ignore,
		Valid:   opts.valid,
		Compare: opts.compare,
	}
}

// Match compares or validates XML content using the configured rule.
func (c *XMLCriterion) Match(actual, expected string) (bool, error) {
	if c.Ignore {
		return true, nil
	}
	if c.Compare != nil {
		return c.Compare(actual, expected)
	}
	if c.Valid {
		return matchValid(actual)
	}
	return false, fmt.Errorf("xml criterion not configured")
}

func matchValid(content string) (bool, error) {
	if strings.TrimSpace(content) == "" {
		return false, fmt.Errorf("xml content is empty")
	}
	decoder := xml.NewDecoder(strings.NewReader(content))
	var depth int
	var rootCount int
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				rootCount++
				if rootCount > 1 {
					return false, fmt.Errorf("xml content must contain a single root element")
				}
			}
			depth++
		case xml.EndElement:
			depth--
			if depth < 0 {
				return false, fmt.Errorf("xml content has unexpected closing element")
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return false, fmt.Errorf("xml content has non-whitespace text outside the root element")
			}
		}
	}
	if rootCount == 0 {
		return false, fmt.Errorf("xml content must contain a root element")
	}
	if depth != 0 {
		return false, fmt.Errorf("xml content has unclosed elements")
	}
	return true, nil
}
