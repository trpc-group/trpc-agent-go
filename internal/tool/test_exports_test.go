//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"reflect"

	apischema "trpc.group/trpc-go/trpc-agent-go/tool"
)

// Exported-only-for-tests helpers to exercise unexported logic from
// external (package tool_test) tests.

func HelperCheckRecursion(target, current reflect.Type) bool {
	return checkRecursion(target, current, make(map[reflect.Type]bool))
}

func HelperGenerateDefName(t reflect.Type) string {
	return generateDefName(t)
}

func HelperHandlePrimitiveType(t reflect.Type) *apischema.Schema {
	return handlePrimitiveType(t)
}

func HelperAppendRequiredField(
	required []string,
	field reflect.StructField,
	fieldSchema *apischema.Schema,
	fieldName string,
	isOmitEmpty bool,
) []string {
	return appendRequiredField(required, field, fieldSchema, fieldName, isOmitEmpty)
}
