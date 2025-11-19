//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"reflect"
	"time"
)

var (
	timeType = reflect.TypeOf(time.Time{})
)

// DeepCopier defines an interface for types that can perform deep copies of themselves.
type DeepCopier interface {
	// DeepCopy performs a deep copy of the object and returns a new copy.
	DeepCopy() any
}

// deepCopyAny performs a deep copy of common JSON-serializable Go types to
// avoid sharing mutable references (maps/slices) across goroutines.
func deepCopyAny(value any) any {
	if copier, ok := value.(DeepCopier); ok {
		return copier.DeepCopy()
	}

	visited := make(map[uintptr]any)
	if out, ok := deepCopyFastPath(value); ok {
		return out
	}
	return deepCopyReflect(reflect.ValueOf(value), visited)
}

// deepCopyFastPath handles common JSON-friendly types without reflection.
func deepCopyFastPath(value any) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(v))
		for k, vv := range v {
			copied[k] = deepCopyAny(vv)
		}
		return copied, true
	case []any:
		copied := make([]any, len(v))
		for i := range v {
			copied[i] = deepCopyAny(v[i])
		}
		return copied, true
	case []string:
		copied := make([]string, len(v))
		copy(copied, v)
		return copied, true
	case []int:
		copied := make([]int, len(v))
		copy(copied, v)
		return copied, true
	case []float64:
		copied := make([]float64, len(v))
		copy(copied, v)
		return copied, true
	case time.Time:
		return v, true
	}
	return nil, false
}

// deepCopyReflect performs a deep copy using reflection with cycle detection.
func deepCopyReflect(rv reflect.Value, visited map[uintptr]any) any {
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Interface:
		return copyInterface(rv, visited)
	case reflect.Ptr:
		return copyPointer(rv, visited)
	case reflect.Map:
		return copyMap(rv, visited)
	case reflect.Slice:
		return copySlice(rv, visited)
	case reflect.Array:
		return copyArray(rv, visited)
	case reflect.Struct:
		return copyStruct(rv, visited)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return reflect.Zero(rv.Type()).Interface()
	default:
		return rv.Interface()
	}
}

func copyInterface(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return nil
	}
	if copier, ok := rv.Interface().(DeepCopier); ok {
		return copier.DeepCopy()
	}
	return deepCopyReflect(rv.Elem(), visited)
}

func copyPointer(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return nil
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	if copier, ok := rv.Interface().(DeepCopier); ok {
		return copier.DeepCopy()
	}
	elem := rv.Elem()
	newPtr := reflect.New(elem.Type())
	visited[ptr] = newPtr.Interface()
	newPtr.Elem().Set(reflect.ValueOf(deepCopyReflect(elem, visited)))
	return newPtr.Interface()
}

func copyMap(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return reflect.Zero(rv.Type()).Interface()
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	newMap := reflect.MakeMapWithSize(rv.Type(), rv.Len())
	visited[ptr] = newMap.Interface()
	for _, mk := range rv.MapKeys() {
		mv := rv.MapIndex(mk)
		newMap.SetMapIndex(mk,
			reflect.ValueOf(deepCopyReflect(mv, visited)))
	}
	return newMap.Interface()
}

func copySlice(rv reflect.Value, visited map[uintptr]any) any {
	if rv.IsNil() {
		return reflect.Zero(rv.Type()).Interface()
	}
	ptr := rv.Pointer()
	if cached, ok := visited[ptr]; ok {
		return cached
	}
	l := rv.Len()
	newSlice := reflect.MakeSlice(rv.Type(), l, l)
	visited[ptr] = newSlice.Interface()
	for i := 0; i < l; i++ {
		newSlice.Index(i).Set(
			reflect.ValueOf(deepCopyReflect(rv.Index(i), visited)),
		)
	}
	return newSlice.Interface()
}

func copyArray(rv reflect.Value, visited map[uintptr]any) any {
	l := rv.Len()
	newArr := reflect.New(rv.Type()).Elem()
	for i := 0; i < l; i++ {
		elem := rv.Index(i)
		newArr.Index(i).Set(reflect.ValueOf(deepCopyReflect(elem, visited)))
	}
	return newArr.Interface()
}

func copyStruct(rv reflect.Value, visited map[uintptr]any) any {
	if copier, ok := rv.Interface().(DeepCopier); ok {
		return copier.DeepCopy()
	}
	if isTimeType(rv) {
		return copyTime(rv)
	}
	newStruct := reflect.New(rv.Type()).Elem()
	for i := 0; i < rv.NumField(); i++ {
		ft := rv.Type().Field(i)
		if ft.PkgPath != "" {
			continue
		}
		dstField := newStruct.Field(i)
		if !dstField.CanSet() {
			continue
		}
		srcField := rv.Field(i)
		copied := deepCopyReflect(srcField, visited)
		if copied == nil {
			dstField.Set(reflect.Zero(dstField.Type()))
			continue
		}
		srcVal := reflect.ValueOf(copied)
		if srcVal.Type().AssignableTo(dstField.Type()) {
			dstField.Set(srcVal)
		} else if srcVal.Type().ConvertibleTo(dstField.Type()) {
			dstField.Set(srcVal.Convert(dstField.Type()))
		} else {
			dstField.Set(reflect.Zero(dstField.Type()))
		}
	}
	return newStruct.Interface()
}

func isTimeType(value reflect.Value) bool {
	rt := value.Type()
	if rt == timeType {
		return true
	}

	if rt.ConvertibleTo(timeType) {
		return true
	}

	return false
}

func copyTime(value reflect.Value) any {
	if value.Type().ConvertibleTo(timeType) {
		timeVal := value.Convert(timeType).Interface()
		if t, ok := timeVal.(time.Time); ok {
			return reflect.ValueOf(t).Convert(value.Type()).Interface()
		}
	}
	return value.Interface()
}
