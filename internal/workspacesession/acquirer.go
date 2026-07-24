//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspacesession

import (
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// NormalizeAcquirer returns nil when reg is nil or a non-nil interface value
// whose dynamic value is nil (a typed nil, e.g. (*WorkspaceRegistry)(nil)).
// Such a value would otherwise pass a reg == nil check and panic when Acquire
// is called on it. Other values are returned unchanged.
func NormalizeAcquirer(
	reg codeexecutor.WorkspaceAcquirer,
) codeexecutor.WorkspaceAcquirer {
	if reg == nil {
		return nil
	}
	switch v := reflect.ValueOf(reg); v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map,
		reflect.Ptr, reflect.Slice, reflect.UnsafePointer:
		if v.IsNil() {
			return nil
		}
	}
	return reg
}
