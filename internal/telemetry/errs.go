//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/errs"
)

// FormatResponseErrorLabel converts a response error into a telemetry label.
func FormatResponseErrorLabel(respErr *model.ResponseError, fallback string) string {
	if respErr == nil {
		return fallback
	}
	label := fallback
	if respErr.Type != "" {
		label = respErr.Type
	}
	if respErr.Code != nil && *respErr.Code != "" {
		return fmt.Sprintf("%s_%s", label, *respErr.Code)
	}
	return label
}

// ToErrorType converts an error to an error type.
func ToErrorType(err error, errorType string) string {
	return FormatResponseErrorLabel(errs.ToResponseError(err), errorType)
}
