//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func nonNilContext(ctx context.Context) context.Context {
	if isNilValue(ctx) {
		return context.Background()
	}
	return ctx
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func invalidConfiguredRuleMatch() Match {
	return newMatch(
		tool.PermissionActionAsk,
		RiskLevelHigh,
		"rule.invalid",
		"a configured safety rule is nil",
		"Remove or replace the invalid rule before execution.",
	)
}
