//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package function

// LongRunner defines an interface for determining if an operation or process
// is expected to run for an extended period of time.
type LongRunner interface {
	// LongRunning returns true if the operation is expected to run for a long time.
	LongRunning() bool
}
