//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package loss

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestSeverityRank(t *testing.T) {
	assert.Equal(t, 0, SeverityRank(promptiter.LossSeverityP0))
	assert.Equal(t, 1, SeverityRank(promptiter.LossSeverityP1))
	assert.Equal(t, 2, SeverityRank(promptiter.LossSeverityP2))
	assert.Equal(t, 3, SeverityRank(promptiter.LossSeverityP3))
	assert.Equal(t, 4, SeverityRank(promptiter.LossSeverity("unknown")))
}
