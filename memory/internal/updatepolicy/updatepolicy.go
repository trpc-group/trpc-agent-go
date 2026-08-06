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

// Value identifies an update policy configured by a built-in extractor.
type Value string

type provider interface {
	ConfiguredUpdatePolicy() Value
}

// From returns the policy carried by a built-in extractor.
func From(value any) Value {
	provider, ok := value.(provider)
	if !ok {
		return ""
	}
	return provider.ConfiguredUpdatePolicy()
}
