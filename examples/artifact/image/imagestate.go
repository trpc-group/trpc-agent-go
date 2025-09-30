//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

const generateImageStateKey = "generateImageStateKey"

type generateImageStateValue struct {
	ImageIDs []string `json:"image_ids"`
}
