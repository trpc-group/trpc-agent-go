//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package errs

import (
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestToResponseError_Nil(t *testing.T) {
	if got := ToResponseError(nil); got != nil {
		t.Fatalf("ToResponseError(nil) = %#v, want nil", got)
	}
}

func TestToResponseError_PreservesOuterMessageAndStructuredFields(t *testing.T) {
	code := "429"
	err := fmt.Errorf("request failed: %w", &model.ResponseError{
		Message: "rate limit",
		Type:    model.ErrorTypeAPIError,
		Code:    &code,
	})

	got := ToResponseError(err)
	if got == nil {
		t.Fatal("ToResponseError() returned nil")
	}
	if got.Message != err.Error() {
		t.Fatalf("Message = %q, want %q", got.Message, err.Error())
	}
	if got.Type != model.ErrorTypeAPIError {
		t.Fatalf("Type = %q, want %q", got.Type, model.ErrorTypeAPIError)
	}
	if got.Code == nil || *got.Code != code {
		t.Fatalf("Code = %v, want %q", got.Code, code)
	}
}
