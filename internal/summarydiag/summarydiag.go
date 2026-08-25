//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package summarydiag holds shared presentation helpers for the four stable
// session-summary diagnostic records. The helpers format already-collected
// metadata; they do not select, persist, cascade, inject, or compact summaries.
package summarydiag

import "unicode/utf8"

const (
	// SchemaVersion is the diagnostic record schema. Bump it when the field
	// set or field semantics of a stable record name change incompatibly.
	SchemaVersion = 1

	// FilterKeyMaxRunes is the diagnostic display budget for a filter key,
	// including the truncation marker when a key is shortened. It matches
	// the VARCHAR(255) filter_key column used by the common
	// session_summaries schema (Postgres, MySQL, and pgvector). Longer keys
	// are truncated in logs only; stored keys and queries are unchanged.
	FilterKeyMaxRunes = 255

	// truncatedMarker is appended to a displayed filter key after truncation
	// so the change is visible in the value itself. The accompanying
	// *_truncated boolean is the authoritative signal.
	truncatedMarker = "..."
)

// FormatFilterKey prepares a filter key for diagnostic display. The original
// business key is not modified. Empty keys stay empty so logs still show
// filter_key="". A key within FilterKeyMaxRunes is returned unchanged. A
// longer key is cut at a UTF-8 rune boundary so the displayed value,
// including truncatedMarker, stays within FilterKeyMaxRunes. truncated is
// the authoritative signal that the logged value is not the original key.
func FormatFilterKey(key string) (display string, truncated bool) {
	prefixLimit := filterKeyPrefixLimit()
	n := 0
	prefixEnd := 0
	for i := range key {
		if n == prefixLimit {
			prefixEnd = i
		}
		if n == FilterKeyMaxRunes {
			return key[:prefixEnd] + truncatedMarker, true
		}
		n++
	}
	return key, false
}

// filterKeyPrefixLimit is how many original runes fit when the marker must
// also be included in FilterKeyMaxRunes.
func filterKeyPrefixLimit() int {
	limit := FilterKeyMaxRunes - utf8.RuneCountInString(truncatedMarker)
	if limit < 0 {
		return 0
	}
	return limit
}
