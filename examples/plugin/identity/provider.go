//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/plugin/identity"
)

// newDemoProvider returns an identity.Provider that fabricates a user
// identity in-process. A real implementation typically turns the given
// userID into credentials by looking them up in a session store, a
// secrets service, or an OAuth token exchange — but the important shape is
// the same: one Resolve call per invocation, producing a populated
// *identity.Identity that downstream tools consume via the context helpers.
//
// The provider lives behind the identity.Provider interface, so swapping
// in a real backend is a one-line change at the Runner construction site.
func newDemoProvider() identity.Provider {
	return identity.ProviderFunc(func(
		_ context.Context,
		resolvedUserID string,
		resolvedSessionID string,
	) (*identity.Identity, error) {
		_ = resolvedSessionID // a real provider would typically use it.
		token := fmt.Sprintf("demo-token-for-%s", resolvedUserID)
		return &identity.Identity{
			UserID: resolvedUserID,
			Token:  token,
			Headers: map[string]string{
				"Authorization": "Bearer " + token,
				"X-User-Id":     resolvedUserID,
			},
			EnvVars: map[string]string{
				"USER_ACCESS_TOKEN": token,
				"USER_ID":           resolvedUserID,
			},
		}, nil
	})
}
