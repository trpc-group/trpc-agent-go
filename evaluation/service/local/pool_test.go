//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateEvalCaseInferencePoolRejectsNonPositiveSize(t *testing.T) {
	_, err := createEvalCaseInferencePool(0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool size must be greater than 0")
}
