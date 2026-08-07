//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redacttext"

const Placeholder = redacttext.Placeholder

var privateKeyPattern = redacttext.PrivateKeyPattern

type Result = redacttext.Result

// Text redacts suspected secrets from a string.
func Text(in string) Result {
	return redacttext.Text(in)
}

// ContainsSecret reports whether the text contains a supported secret shape.
func ContainsSecret(in string) bool {
	return redacttext.ContainsSecret(in)
}
