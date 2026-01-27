// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package errs provides helpers to convert between the agent ResponseError
package errs

import "trpc.group/trpc-go/trpc-agent-go/model"

// ToResponseError converts an error to a ResponseError.
var ToResponseError = func(err error) *model.ResponseError {
	if err == nil {
		return nil
	}
	return &model.ResponseError{
		Message: err.Error(),
	}
}
