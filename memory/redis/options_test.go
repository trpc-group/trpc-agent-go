//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import "testing"

func TestWithDisableAutoMemoryOnExternalContext(t *testing.T) {
	opts := defaultOptions.clone()
	if opts.disableAutoMemoryOnExternalContext {
		t.Fatal("disable auto memory on external context should default to false")
	}

	WithDisableAutoMemoryOnExternalContext(true)(&opts)
	if !opts.disableAutoMemoryOnExternalContext {
		t.Fatal("disable auto memory on external context should be true")
	}

	WithDisableAutoMemoryOnExternalContext(false)(&opts)
	if opts.disableAutoMemoryOnExternalContext {
		t.Fatal("disable auto memory on external context should be false")
	}
}
