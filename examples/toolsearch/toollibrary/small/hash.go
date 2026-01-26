//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewHashGeneratorTool creates a hash generator tool.
func NewHashGeneratorTool() tool.CallableTool {
	return function.NewFunctionTool(
		generateHash,
		function.WithName("hash_generator"),
		function.WithDescription("Generate hash values from text using different algorithms: MD5, SHA1, or SHA256. Useful for data integrity verification and password hashing."),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Hash generation request",
			Required:    []string{"text", "algorithm"},
			Properties: map[string]*tool.Schema{
				"text": {
					Type:        "string",
					Description: "Text to hash",
				},
				"algorithm": {
					Type:        "string",
					Description: "Hash algorithm",
					Enum:        []any{"MD5", "SHA1", "SHA256"},
				},
			},
		}),
	)
}

type hashRequest struct {
	Text      string `json:"text"`
	Algorithm string `json:"algorithm"`
}

type hashResponse struct {
	OriginalText string `json:"original_text"`
	Algorithm    string `json:"algorithm"`
	Hash         string `json:"hash"`
	Message      string `json:"message"`
}

func generateHash(_ context.Context, req hashRequest) (hashResponse, error) {
	var hash string

	switch strings.ToUpper(req.Algorithm) {
	case "MD5":
		h := md5.New()
		h.Write([]byte(req.Text))
		hash = hex.EncodeToString(h.Sum(nil))
	case "SHA1":
		h := sha1.New()
		h.Write([]byte(req.Text))
		hash = hex.EncodeToString(h.Sum(nil))
	case "SHA256":
		h := sha256.New()
		h.Write([]byte(req.Text))
		hash = hex.EncodeToString(h.Sum(nil))
	default:
		return hashResponse{
			OriginalText: req.Text,
			Algorithm:    req.Algorithm,
			Hash:         "",
			Message:      "Error: Unsupported algorithm. Use MD5, SHA1, or SHA256",
		}, fmt.Errorf("unsupported algorithm: %s", req.Algorithm)
	}

	return hashResponse{
		OriginalText: req.Text,
		Algorithm:    strings.ToUpper(req.Algorithm),
		Hash:         hash,
		Message:      fmt.Sprintf("Generated %s hash successfully", strings.ToUpper(req.Algorithm)),
	}, nil
}
