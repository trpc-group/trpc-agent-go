//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package failover

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type failureError struct {
	failures []failureRecord
}

type failureRecord struct {
	candidate string
	message   string
	errType   string
}

func newFailureError(failures []failureRecord) error {
	return &failureError{failures: append([]failureRecord(nil), failures...)}
}

func (e *failureError) Error() string {
	return buildFailureMessage(e.failures)
}

func buildFailureMessage(failures []failureRecord) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		if failure.candidate == "" {
			parts = append(parts, failure.message)
			continue
		}
		parts = append(
			parts,
			fmt.Sprintf(
				"candidate model %q failed before the first non-error chunk: %s",
				failure.candidate,
				failure.message,
			),
		)
	}
	return strings.Join(parts, "; ")
}

func failuresFromError(err error, fallback []failureRecord) []failureRecord {
	var wrapped *failureError
	if errors.As(err, &wrapped) && wrapped != nil {
		return append([]failureRecord(nil), wrapped.failures...)
	}
	return appendFailure(fallback, "", err.Error(), "")
}

func appendFailure(
	failures []failureRecord,
	candidateName string,
	message string,
	errType string,
) []failureRecord {
	next := append([]failureRecord(nil), failures...)
	next = append(next, failureRecord{
		candidate: candidateName,
		message:   message,
		errType:   errType,
	})
	return next
}

func buildFailureResponse(failures []failureRecord) *model.Response {
	return &model.Response{
		Error: &model.ResponseError{
			Message: buildFailureMessage(failures),
			Type:    failureResponseType(failures),
		},
		Timestamp: time.Now(),
		Done:      true,
	}
}

func failureResponseType(failures []failureRecord) string {
	for i := len(failures) - 1; i >= 0; i-- {
		if failures[i].errType != "" {
			return failures[i].errType
		}
	}
	return model.ErrorTypeAPIError
}
