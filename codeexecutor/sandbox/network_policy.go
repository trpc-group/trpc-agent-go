//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// NetworkMode describes network access.
type NetworkMode string

const (
	// NetworkRestricted blocks outbound networking when the backend can enforce
	// it. Linux v1 uses an isolated network namespace.
	NetworkRestricted NetworkMode = "restricted"
	// NetworkEnabled allows the command to use the host network.
	NetworkEnabled NetworkMode = "enabled"
)

// NetworkPolicy describes network access for a profile.
type NetworkPolicy struct {
	Mode NetworkMode
}
