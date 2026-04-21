//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summaryscope stores temporary scope metadata used while evaluating
// summary thresholds for filtered session branches.
package summaryscope

import "trpc.group/trpc-go/trpc-agent-go/session"

const serviceMetaScopeFilterKey = "summary:scope_filter_key"

// SetScopeFilterKey marks sess as representing the specified summary branch scope.
// It mutates session.ServiceMeta directly and is not goroutine-safe, so callers
// must use it only on non-shared sessions (for example, the temporary summary
// session created in buildFilterSession) or before handing a session to
// concurrent readers. The "summary:" prefix in the internal key is required to
// avoid collisions with other ServiceMeta entries. Callers that need to apply
// scoped metadata to a live shared session should use a different approach,
// such as protecting ServiceMeta with a mutex or avoiding in-place mutation.
func SetScopeFilterKey(sess *session.Session, filterKey string) {
	if sess == nil || filterKey == "" {
		return
	}
	if sess.ServiceMeta == nil {
		sess.ServiceMeta = make(map[string]string)
	}
	sess.ServiceMeta[serviceMetaScopeFilterKey] = filterKey
}

// GetScopeFilterKey returns the temporary summary branch scope stored on sess.
func GetScopeFilterKey(sess *session.Session) string {
	if sess == nil || sess.ServiceMeta == nil {
		return ""
	}
	return sess.ServiceMeta[serviceMetaScopeFilterKey]
}
