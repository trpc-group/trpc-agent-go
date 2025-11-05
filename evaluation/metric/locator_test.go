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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocatorBuild(t *testing.T) {
	loc := &locator{}
	path := loc.Build("/tmp/base", "app", "set")
	assert.Equal(t, filepath.Join("/tmp/base", "app", "set"+defaultMetricsFileSuffix), path)
}
