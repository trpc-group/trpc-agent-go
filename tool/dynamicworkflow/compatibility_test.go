//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package dynamicworkflow_test

import "trpc.group/trpc-go/trpc-agent-go/tool/dynamicworkflow"

// Keep the original positional literal source-compatible.
var _ dynamicworkflow.Runtime = dynamicworkflow.LocalRunner{"python3"}
