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

	"trpc.group/trpc-go/trpc-agent-go/telemetry/errs"
)

func ToErrorType(err error, errorType string) string {
	e := errs.ToResponseError(err)
	if e == nil {
		return errorType
	}
	if e.Type != "" {
		errorType = e.Type
	}
	if e.Code != nil {
		return fmt.Sprintf("%s_%s", errorType, *e.Code)
	}
	return errorType
}
