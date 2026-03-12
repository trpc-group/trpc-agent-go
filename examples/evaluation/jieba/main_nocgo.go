//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build !cgo

package main

import (
	"context"
	"errors"
)

func run(ctx context.Context) error {
	_ = ctx
	return errors.New("the jieba example requires cgo; rebuild with CGO_ENABLED=1")
}
