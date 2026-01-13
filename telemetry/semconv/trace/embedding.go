//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package trace

const (
	// KeyGenAIRequestEncodingFormats is the attribute key for request encoding formats.
	KeyGenAIRequestEncodingFormats = "gen_ai.request.encoding_formats"

	// KeyGenAIEmbeddingsDimensionCount is the attribute key for embeddings dimension count.
	KeyGenAIEmbeddingsDimensionCount = "gen_ai.embeddings.dimension.count"

	// KeyGenAIEmbeddingsRequest is the attribute key for embeddings request.
	KeyGenAIEmbeddingsRequest = "gen_ai.embeddings.request"
	// KeyGenAIEmbeddingsResponse is the attribute key for embeddings response.
	KeyGenAIEmbeddingsResponse = "gen_ai.embeddings.response"
)
