//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"

// EncodeStateDeltaMetadata converts Event.StateDelta into A2A metadata.
func EncodeStateDeltaMetadata(stateDelta map[string][]byte) map[string]any {
	return ia2a.EncodeStateDeltaMetadata(stateDelta)
}

// DecodeStateDeltaMetadata restores Event.StateDelta from encoded A2A metadata.
func DecodeStateDeltaMetadata(raw any) map[string][]byte {
	return ia2a.DecodeStateDeltaMetadata(raw)
}
