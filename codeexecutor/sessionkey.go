//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SessionWorkspaceKey derives a filesystem-safe, fixed-length workspace key
// from the session identity triple (appName, userID, sessionID).
//
// The triple is normalized with length prefixes (injective encoding) before
// hashing, so distinct triples always produce distinct hash inputs and
// separator-collision attacks (e.g. app="a/b" vs user="b") cannot alias two
// sessions onto one workspace. The output is "sess-" followed by 32 lowercase
// hex characters: a single path segment that is safe on POSIX and Windows
// (no separators, no drive-letter colons, no reserved device names) and never
// exceeds filesystem name-length limits regardless of input size.
//
// A session with an empty or whitespace-only ID has no stable identity;
// SessionWorkspaceKey returns "" for such inputs (fail-closed).
func SessionWorkspaceKey(app, user, id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	canonical := fmt.Sprintf("%d|%s|%d|%s|%d|%s",
		len(app), app, len(user), user, len(id), id)
	sum := sha256.Sum256([]byte(canonical))
	return "sess-" + hex.EncodeToString(sum[:16])
}

// LegacySessionWorkspaceKey reproduces the pre-encoding-change workspace key
// format ("app/user/id" when both app and user are non-empty, otherwise just
// "id"). The codeexecutor/sandbox runtime migrates its PerSession
// workspaces from this key automatically at CreateWorkspace time; this
// helper is intended for diagnostics and for WorkspaceManager
// implementations outside the sandbox that persist by this key and predate
// the encoding change. Callers on the sandbox runtime must not run a
// concurrent migration with this key — the runtime already does it.
// Returns "" when id is empty or whitespace-only.
func LegacySessionWorkspaceKey(app, user, id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	if app != "" && user != "" {
		return app + "/" + user + "/" + id
	}
	return id
}
