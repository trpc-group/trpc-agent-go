//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package updatepolicy carries the built-in extractor's update policy across
// the memory package boundary without exposing policy discovery publicly.
package updatepolicy

// MetadataKey identifies the opaque policy value in extractor metadata.
const MetadataKey = "trpc-agent-go/memory-extractor/update-policy"

// Value identifies an update policy configured by a built-in extractor.
type Value string

type metadataProvider interface {
	Metadata() map[string]any
}

// From returns the policy carried by extractor metadata.
func From(value any) Value {
	provider, ok := value.(metadataProvider)
	if !ok {
		return ""
	}
	policy, _ := provider.Metadata()[MetadataKey].(Value)
	return policy
}
