//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"

// RedactSandboxSummary redacts secret-bearing string fields on a sandbox summary
// before it is appended to in-memory reports or persisted.
func RedactSandboxSummary(s review.SandboxRunSummary) review.SandboxRunSummary {
	s.Command = Redact(s.Command)
	s.Error = Redact(s.Error)
	s.StdoutSample = Redact(s.StdoutSample)
	s.StderrSample = Redact(s.StderrSample)
	s.Executor = Redact(s.Executor)
	s.Status = Redact(s.Status)
	return s
}
