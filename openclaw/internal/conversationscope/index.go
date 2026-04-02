//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationscope

import (
	"context"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const storageUserStatePrefix = "openclaw.conversation_storage_user:"

var storageUserStateValue = []byte("1")

// RememberIndexedStorageUser records one storage user scope seen for the
// canonical user so cleanup flows can enumerate persisted conversation scopes.
func RememberIndexedStorageUser(
	ctx context.Context,
	svc session.Service,
	appName string,
	canonicalUserID string,
	storageUserID string,
) error {
	if svc == nil {
		return nil
	}
	appName = strings.TrimSpace(appName)
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	storageUserID = strings.TrimSpace(storageUserID)
	if appName == "" ||
		canonicalUserID == "" ||
		storageUserID == "" ||
		canonicalUserID == storageUserID {
		return nil
	}
	return svc.UpdateUserState(
		ctx,
		session.UserKey{
			AppName: appName,
			UserID:  canonicalUserID,
		},
		session.StateMap{
			storageUserStateKey(storageUserID): storageUserStateValue,
		},
	)
}

// ListIndexedStorageUsers lists extra persisted storage scopes remembered for
// the canonical user.
func ListIndexedStorageUsers(
	ctx context.Context,
	svc session.Service,
	appName string,
	canonicalUserID string,
) ([]string, error) {
	if svc == nil {
		return nil, nil
	}
	appName = strings.TrimSpace(appName)
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	if appName == "" || canonicalUserID == "" {
		return nil, nil
	}
	state, err := svc.ListUserStates(
		ctx,
		session.UserKey{
			AppName: appName,
			UserID:  canonicalUserID,
		},
	)
	if err != nil {
		return nil, err
	}
	return indexedStorageUsersFromState(state), nil
}

// DeleteIndexedStorageUser removes one remembered storage scope index.
func DeleteIndexedStorageUser(
	ctx context.Context,
	svc session.Service,
	appName string,
	canonicalUserID string,
	storageUserID string,
) error {
	if svc == nil {
		return nil
	}
	appName = strings.TrimSpace(appName)
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	key := storageUserStateKey(storageUserID)
	if appName == "" || canonicalUserID == "" || key == "" {
		return nil
	}
	return svc.DeleteUserState(
		ctx,
		session.UserKey{
			AppName: appName,
			UserID:  canonicalUserID,
		},
		key,
	)
}

func indexedStorageUsersFromState(state session.StateMap) []string {
	if len(state) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(state))
	out := make([]string, 0, len(state))
	for key, value := range state {
		if value == nil || !strings.HasPrefix(key, storageUserStatePrefix) {
			continue
		}
		storageUserID := strings.TrimSpace(
			strings.TrimPrefix(key, storageUserStatePrefix),
		)
		if storageUserID == "" {
			continue
		}
		if _, ok := seen[storageUserID]; ok {
			continue
		}
		seen[storageUserID] = struct{}{}
		out = append(out, storageUserID)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func storageUserStateKey(storageUserID string) string {
	storageUserID = strings.TrimSpace(storageUserID)
	if storageUserID == "" {
		return ""
	}
	return storageUserStatePrefix + storageUserID
}
