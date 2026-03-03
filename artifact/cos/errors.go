//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cos

import "errors"

const (
	msgEmptyFilename = "cos artifact: filename cannot be empty"
	msgInvalidName   = "cos artifact: filename contains invalid characters"
	msgNilArtifact   = "cos artifact: artifact cannot be nil"
	msgEmptySession  = "cos artifact: session info fields cannot be empty"
)

// Sentinel errors for input validation.
var (
	ErrEmptyFilename    error
	ErrInvalidFilename  error
	ErrNilArtifact      error
	ErrEmptySessionInfo error
)

func init() {
	ErrEmptyFilename = errors.New(msgEmptyFilename)
	ErrInvalidFilename = errors.New(msgInvalidName)
	ErrNilArtifact = errors.New(msgNilArtifact)
	ErrEmptySessionInfo = errors.New(msgEmptySession)
}
