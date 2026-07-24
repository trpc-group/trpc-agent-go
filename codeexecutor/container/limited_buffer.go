//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package container

import "trpc.group/trpc-go/trpc-agent-go/codeexecutor/internal/outputlimit"

type limitedBuffer = outputlimit.Buffer

func newLimitedBuffer(limit int) limitedBuffer { return outputlimit.NewBuffer(limit) }
