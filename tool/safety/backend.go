//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

func normalizeBackend(backend Backend) Backend {
	switch backend {
	case BackendWorkspace, BackendHost, BackendCode, BackendSkill, BackendUnknown:
		return backend
	default:
		return BackendUnknown
	}
}
