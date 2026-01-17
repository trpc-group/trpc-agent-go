//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

// IntPtr returns a pointer to v.
func IntPtr(v int) *int { return &v }

// Float64Ptr returns a pointer to v.
func Float64Ptr(v float64) *float64 { return &v }

// BoolPtr returns a pointer to v.
func BoolPtr(v bool) *bool { return &v }

// StringPtr returns a pointer to v.
func StringPtr(v string) *string { return &v }
