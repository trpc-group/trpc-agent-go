//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// serializeVector converts a float64 embedding into the binary format
// accepted by MySQL 9.0+ VECTOR type (little-endian float32 sequence).
func serializeVector(embedding []float64) []byte {
	buf := make([]byte, 4*len(embedding))
	for i, v := range embedding {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(v)))
	}
	return buf
}

// vectorToString converts a float64 embedding to MySQL STRING_TO_VECTOR
// format: "[1.0, 2.0, 3.0, ...]". Used for MySQL 9.0+ vector queries.
func vectorToString(embedding []float64) string {
	parts := make([]string, len(embedding))
	for i, v := range embedding {
		parts[i] = fmt.Sprintf("%g", float32(v))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// cosineSimilarity computes the cosine similarity between two float64 vectors.
// Returns 0 if either vector has zero magnitude.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// deserializeVector converts a little-endian float32 binary blob back to
// float64 slice. Used for brute-force search on MySQL 8.x.
func deserializeVector(data []byte) ([]float64, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid vector blob length: %d (must be multiple of 4)", len(data))
	}
	n := len(data) / 4
	result := make([]float64, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4:])
		result[i] = float64(math.Float32frombits(bits))
	}
	return result, nil
}
