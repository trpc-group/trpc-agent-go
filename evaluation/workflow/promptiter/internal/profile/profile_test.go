//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestClonePreservesNilOverrides(t *testing.T) {
	profile := &promptiter.Profile{StructureID: "structure_1"}
	cloned := Clone(profile)
	assert.NotNil(t, cloned)
	if cloned == nil {
		return
	}
	assert.Equal(t, "structure_1", cloned.StructureID)
	assert.Nil(t, cloned.Overrides)
}
