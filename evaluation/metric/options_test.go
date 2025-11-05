//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package metric

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type stubLocator struct{}

func (s *stubLocator) Build(baseDir, appName, evalSetID string) string {
	return baseDir + "/" + appName + "/" + evalSetID
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()
	assert.Equal(t, defaultBaseDir, opts.BaseDir)
	assert.IsType(t, &locator{}, opts.Locator)
}

func TestNewOptionsOverrides(t *testing.T) {
	loc := &stubLocator{}
	opts := NewOptions(
		WithBaseDir("/tmp/metrics"),
		WithLocator(loc),
	)
	assert.Equal(t, "/tmp/metrics", opts.BaseDir)
	assert.Equal(t, loc, opts.Locator)
}
