//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import "trpc.group/trpc-go/trpc-agent-go/session/summary"

// externalBackends returns env-gated external backends. It is filled in by a
// later task; for now no external backends are wired.
func externalBackends(summarizer summary.SessionSummarizer) []*Backend {
	return nil
}
