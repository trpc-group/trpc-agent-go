//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"sync/atomic"
	"unicode/utf8"
)

const defaultTruncateMarker = "…[truncated]…"

// observationMaxBytes stores the configured truncation threshold.
//
// Semantics:
// - < 0: truncation disabled
// - = 0: truncate everything
// - > 0: max byte length for a JSON leaf node (or a plain string value)
var observationMaxBytes atomic.Int64

func init() {
	// Default: truncation disabled unless configured.
	observationMaxBytes.Store(-1)
}

func setObservationMaxBytes(maxBytes *int) {
	if maxBytes == nil {
		observationMaxBytes.Store(-1)
		return
	}
	if *maxBytes < 0 {
		observationMaxBytes.Store(-1)
		return
	}
	observationMaxBytes.Store(int64(*maxBytes))
}

// getObservationMaxBytes returns the max byte length for each observation JSON leaf node.
func getObservationMaxBytes() int {
	return int(observationMaxBytes.Load())
}

// truncateObservationValue limits the size of Langfuse observation input/output.
//
// It is intentionally simple (scheme-agnostic): apply to the final string value.
// Truncation is disabled by default unless configured.
func truncateObservationValue(s string) string {
	maxBytes := getObservationMaxBytes()
	if maxBytes < 0 {
		return s
	}
	if maxBytes == 0 {
		return ""
	}
	return truncateStringBytes(s, maxBytes)
}

func truncateStringBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s)

	marker := []byte(defaultTruncateMarker)
	if len(marker) >= maxBytes {
		return string(safeUTF8Prefix(marker, maxBytes))
	}

	remaining := maxBytes - len(marker)
	// Prefer keeping more head than tail, but always keep both sides.
	headBytes := remaining * 2 / 3
	tailBytes := remaining - headBytes

	head := safeUTF8Prefix(b, headBytes)
	tail := safeUTF8Suffix(b, tailBytes)
	out := make([]byte, 0, len(head)+len(marker)+len(tail))
	out = append(out, head...)
	out = append(out, marker...)
	out = append(out, tail...)

	// Best-effort validity: should already be valid due to boundary trimming.
	if !utf8.Valid(out) {
		out = safeUTF8Prefix(b, maxBytes)
	}
	return string(out)
}

func safeUTF8Prefix(b []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	if n >= len(b) {
		return b
	}
	end := n
	// Back up from a UTF-8 continuation byte.
	for end > 0 && (b[end]&0xC0) == 0x80 {
		end--
	}
	return b[:end]
}

func safeUTF8Suffix(b []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	if n >= len(b) {
		return b
	}
	start := len(b) - n
	// Advance to a rune boundary (skip UTF-8 continuation bytes).
	for start < len(b) && (b[start]&0xC0) == 0x80 {
		start++
	}
	return b[start:]
}
