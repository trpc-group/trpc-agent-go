//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package surface

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestIsSupportedType(t *testing.T) {
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeInstruction))
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeGlobalInstruction))
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeFewShot))
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeModel))
	assert.False(t, IsSupportedType(promptiter.SurfaceType("unknown")))
}
