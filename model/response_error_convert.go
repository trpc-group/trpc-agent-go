//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"errors"
	"strconv"
)

type errorTyper interface {
	ErrorType() string
}

type errorCoder interface {
	ErrorCode() string
}

type codeStringer interface {
	Code() string
}

type codeInter interface {
	Code() int
}

type codeInt32er interface {
	Code() int32
}

type codeInt64er interface {
	Code() int64
}

// ResponseErrorFromError converts an error into a ResponseError.
//
// This helper is primarily used when an internal workflow error needs to be
// serialized into an event stream. It attempts to preserve structured fields
// (type/code) when the error carries them.
func ResponseErrorFromError(err error, fallbackType string) *ResponseError {
	if err == nil {
		return nil
	}

	var respErr *ResponseError
	if errors.As(err, &respErr) && respErr != nil {
		clone := *respErr
		if clone.Message == "" {
			clone.Message = err.Error()
		}
		if clone.Type == "" {
			clone.Type = fallbackType
		}
		return &clone
	}

	out := &ResponseError{
		Message: err.Error(),
		Type:    fallbackType,
	}
	if t := errorTypeFromError(err); t != "" {
		out.Type = t
	}
	if code := errorCodeFromError(err); code != "" {
		out.Code = stringPtr(code)
	}
	return out
}

func errorTypeFromError(err error) string {
	var typed errorTyper
	if !errors.As(err, &typed) || typed == nil {
		return ""
	}
	return typed.ErrorType()
}

func errorCodeFromError(err error) string {
	var coded errorCoder
	if errors.As(err, &coded) && coded != nil {
		if code := coded.ErrorCode(); code != "" {
			return code
		}
	}

	var codeString codeStringer
	if errors.As(err, &codeString) && codeString != nil {
		if code := codeString.Code(); code != "" {
			return code
		}
	}

	var codeInt codeInter
	if errors.As(err, &codeInt) && codeInt != nil {
		return strconv.Itoa(codeInt.Code())
	}

	var codeInt32 codeInt32er
	if errors.As(err, &codeInt32) && codeInt32 != nil {
		return strconv.FormatInt(int64(codeInt32.Code()), 10)
	}

	var codeInt64 codeInt64er
	if errors.As(err, &codeInt64) && codeInt64 != nil {
		return strconv.FormatInt(codeInt64.Code(), 10)
	}

	return ""
}

func stringPtr(s string) *string {
	return &s
}
