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

// Sentinel errors for input validation.
var (
	ErrEmptyFilename = errors.New(
		"cos artifact: filename cannot be empty",
	)
	ErrInvalidFilename = errors.New(
		"cos artifact: filename contains invalid characters",
	)
	ErrNilArtifact = errors.New(
		"cos artifact: artifact cannot be nil",
	)
	ErrEmptySessionInfo = errors.New(
		"cos artifact: session info fields cannot be empty",
	)
)
