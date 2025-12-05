// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// ModelProvider is a minimal abstraction used by the DSL runtime to
// resolve logical model identifiers (model IDs) into concrete model.Model
// instances. It intentionally does not prescribe where models come from:
// they may be constructed from environment variables, loaded from a
// platform-level model service, or hard-coded in application code.
package dsl

import "trpc.group/trpc-go/trpc-agent-go/model"

// ModelProvider resolves a logical model identifier into a model.Model.
// Implementations are responsible for any caching or configuration lookup.
type ModelProvider interface {
	// Get returns the model associated with the given ID or an error if
	// the model is not found or cannot be constructed.
	Get(name string) (model.Model, error)
}

