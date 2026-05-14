//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package length defines length-based content criteria.
package length

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// LengthCriterion validates that content length is within a configured range.
type LengthCriterion struct {
	// Ignore skips length validation when true.
	Ignore bool `json:"ignore,omitempty"`
	// Min is the inclusive minimum number of Unicode code points.
	Min *int `json:"min,omitempty"`
	// Max is the inclusive maximum number of Unicode code points.
	Max *int `json:"max,omitempty"`
}

// New creates a LengthCriterion with the provided options.
func New(opt ...Option) *LengthCriterion {
	opts := newOptions(opt...)
	return &LengthCriterion{
		Ignore: opts.ignore,
		Min:    opts.min,
		Max:    opts.max,
	}
}

// Match validates that content length is within the configured inclusive range.
func (c *LengthCriterion) Match(content string) (bool, error) {
	if c == nil || c.Ignore {
		return true, nil
	}
	if err := c.validate(); err != nil {
		return false, err
	}
	actualLength := utf8.RuneCountInString(content)
	if c.Min != nil && actualLength < *c.Min {
		return false, fmt.Errorf("actual length %d is less than min %d, expected range %s",
			actualLength, *c.Min, c.rangeString())
	}
	if c.Max != nil && actualLength > *c.Max {
		return false, fmt.Errorf("actual length %d is greater than max %d, expected range %s",
			actualLength, *c.Max, c.rangeString())
	}
	return true, nil
}

func (c *LengthCriterion) validate() error {
	if c.Min == nil && c.Max == nil {
		return fmt.Errorf("length criterion must configure min or max")
	}
	if c.Min != nil && *c.Min < 0 {
		return fmt.Errorf("min length must be non-negative")
	}
	if c.Max != nil && *c.Max < 0 {
		return fmt.Errorf("max length must be non-negative")
	}
	if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
		return fmt.Errorf("min length must be less than or equal to max length")
	}
	return nil
}

func (c *LengthCriterion) rangeString() string {
	min := "0"
	if c.Min != nil {
		min = strconv.Itoa(*c.Min)
	}
	rightBracket := "]"
	max := "+inf"
	if c.Max != nil {
		max = strconv.Itoa(*c.Max)
	} else {
		rightBracket = ")"
	}
	return fmt.Sprintf("[%s, %s%s", min, max, rightBracket)
}
