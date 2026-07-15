//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import "trpc.group/trpc-go/trpc-agent-go/session/internal/schemaversion"

// ObserveSchemaVersions registers a callback for observing the canonical schema
// versions of persistent session backends created in this process. Versions
// registered before the callback is installed are reported immediately.
func ObserveSchemaVersions(reporter func(modulePath, version string)) {
	schemaversion.Observe(reporter)
}
