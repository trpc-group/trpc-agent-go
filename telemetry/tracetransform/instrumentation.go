//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Copyright The OpenTelemetry Authors
// Copyright (C) 2025 Tencent. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//

package tracetransform

import (
	"go.opentelemetry.io/otel/sdk/instrumentation"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// InstrumentationScope transforms an OpenTelemetry instrumentation.Scope into an OTLP InstrumentationScope.
func InstrumentationScope(il instrumentation.Scope) *commonpb.InstrumentationScope {
	if il == (instrumentation.Scope{}) {
		return nil
	}
	return &commonpb.InstrumentationScope{
		Name:    il.Name,
		Version: il.Version,
	}
}
