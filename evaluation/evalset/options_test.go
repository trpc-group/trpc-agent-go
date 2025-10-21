//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalset

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type customLocator struct{}

func (c *customLocator) Build(baseDir, appName, evalSetID string) string {
	return baseDir + "/" + appName + "/" + evalSetID
}

func (c *customLocator) List(baseDir, appName string) ([]string, error) {
	return []string{baseDir, appName}, nil
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()
	assert.Equal(t, defaultBaseDir, opts.BaseDir)
	assert.IsType(t, &locator{}, opts.Locator)
}

func TestNewOptionsWithOverrides(t *testing.T) {
	loc := &customLocator{}
	opts := NewOptions(
		WithBaseDir("/tmp/evals"),
		WithLocator(loc),
	)
	assert.Equal(t, "/tmp/evals", opts.BaseDir)
	assert.Equal(t, loc, opts.Locator)
}
