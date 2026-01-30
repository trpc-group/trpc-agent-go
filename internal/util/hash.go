//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package util

// stableHash32Offset is the FNV-1a 32-bit offset basis.
const stableHash32Offset uint32 = 2166136261

// stableHash32Prime is the FNV-1a 32-bit prime.
const stableHash32Prime uint32 = 16777619

// StableHash32 returns a deterministic 32-bit FNV-1a hash of the input.
func StableHash32(key string) uint32 {
	hash := stableHash32Offset
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= stableHash32Prime
	}
	return hash
}

// StableHashInt returns a deterministic non-negative int hash of the input.
func StableHashInt(key string) int {
	return int(StableHash32(key) & 0x7fffffff)
}

// StableHashIndex returns a deterministic bucket index in [0, n) for the input.
func StableHashIndex(key string, n int) int {
	if n <= 0 {
		return 0
	}
	return int(StableHash32(key) % uint32(n))
}
