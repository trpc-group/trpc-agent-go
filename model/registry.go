//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

// RegisterModelContextWindow registers a model's context window size.
// This allows users to add custom models or override existing mappings.
func RegisterModelContextWindow(modelName string, contextWindowSize int) {
	imodel.ModelMutex.Lock()
	defer imodel.ModelMutex.Unlock()
	imodel.ModelContextWindows[modelName] = contextWindowSize
}

// RegisterModelContextWindows registers multiple models' context window sizes in batch.
// This is more efficient than calling RegisterModelContextWindow multiple times.
func RegisterModelContextWindows(models map[string]int) {
	imodel.ModelMutex.Lock()
	defer imodel.ModelMutex.Unlock()
	for modelName, contextWindowSize := range models {
		imodel.ModelContextWindows[modelName] = contextWindowSize
	}
}
