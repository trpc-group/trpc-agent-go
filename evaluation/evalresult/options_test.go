//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalresult

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockLocator struct{}

func (m *mockLocator) Build(baseDir, appName, evalSetID string) string {
	return baseDir + "/" + appName + "/" + evalSetID
}

func (m *mockLocator) List(baseDir, appName string) ([]string, error) {
	return []string{baseDir, appName}, nil
}

func withLocator(l Locator) Option {
	return func(o *Options) {
		o.Locator = l
	}
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()
	assert.Equal(t, defaultBaseDir, opts.BaseDir)
	assert.IsType(t, &locator{}, opts.Locator)
}

func TestNewOptionsOverride(t *testing.T) {
	loc := &mockLocator{}
	opts := NewOptions(
		WithBaseDir("/tmp/results"),
		withLocator(loc),
	)
	assert.Equal(t, "/tmp/results", opts.BaseDir)
	assert.Equal(t, loc, opts.Locator)
}
