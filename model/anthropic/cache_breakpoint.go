//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package anthropic

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// cacheBreakpointLimit is the number of cache_control markers Anthropic accepts
// in one request. A fifth is rejected outright, so this is a budget, not a hint.
const cacheBreakpointLimit = 4

// defaultCacheControl is the marker placed when nothing constrains its TTL.
const defaultCacheControl = `{"type":"ephemeral"}`

// toolResultCacheBreakpointMiddleware places the tool-result breakpoint on the
// request body as it goes out.
//
// The three unconditional breakpoints are set while the typed request is built.
// This one is conditional on everything that can still change the request after
// that: the request callback, which may rewrite Messages and set top-level
// CacheControl; client and per-request options such as WithJSONSet, which the
// SDK applies to the serialized body, not the typed params; and any middleware
// registered ahead of this one. The body the SDK is about to send is the only
// vantage point from which all of those are visible, so the marker is placed and
// the budget enforced there. The SDK runs middleware on every attempt, from the
// original body, so a retry is marked the same way.
func (m *Model) toolResultCacheBreakpointMiddleware() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if !m.cacheMessages || req == nil || req.Body == nil {
			return next(req)
		}
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		placed := placeToolResultCacheBreakpoint(body)
		req.Body = io.NopCloser(bytes.NewReader(placed))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(placed)), nil
		}
		req.ContentLength = int64(len(placed))
		return next(req)
	}
}

// placeToolResultCacheBreakpoint marks the newest tool-result message of a
// serialized request, so a turn's tool output is cached in the request that
// carries it rather than the next one. The body comes back unchanged when there
// is nothing to mark, when no slot is left, or when a marker would conflict with
// one the caller placed.
//
// Without it every tool result crosses the cache boundary twice: sent uncached
// in the request carrying it, then written once the last-assistant breakpoint
// moves past it. It also halves the distance between cache entries, which
// matters because a breakpoint looks back a bounded number of blocks to find the
// previous one — one wide parallel-tool turn can otherwise put it out of range.
//
// This is the conditional breakpoint because losing it costs a delay, not a
// cache entry: the next request's last-assistant breakpoint writes these results
// anyway. A caller that spends the whole budget itself is still over it; forcing
// a fit would mean dropping its marker or the system and tools ones, trading a
// 400 that names the problem for a silent cache regression.
func placeToolResultCacheBreakpoint(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	list := messages.Array()
	if len(list) <= 1 {
		return body
	}
	idx := lastToolResultMessageIndex(list, lastAssistantMessageIndex(list))
	if idx < 0 {
		return body
	}
	// A tool-result message is all tool_result blocks, and the API accepts a
	// marker on any of them, so the last block is the one to mark.
	block := len(list[idx].Get("content").Array()) - 1
	path := fmt.Sprintf("messages.%d.content.%d.cache_control", idx, block)
	// A marker already on that block is the caller's, placed through the
	// callback or a request option: its TTL is what they asked for, and
	// replacing it could shorten a one-hour entry to five minutes or make the
	// TTLs along the prompt shorten and then lengthen, which is rejected.
	if gjson.GetBytes(body, path).IsObject() {
		return body
	}
	if countCacheBreakpoints(body) >= cacheBreakpointLimit {
		return body
	}
	cacheControl := defaultCacheControl
	if topLevel := gjson.GetBytes(body, "cache_control"); topLevel.IsObject() {
		// Top-level cache_control is automatic caching: the API adds a marker
		// of its own on the last cacheable block. When the tool result is that
		// block, the automatic marker already covers it and an explicit one
		// would be redundant at best and, with another TTL, rejected. Ahead of
		// it, the marker takes the same TTL to keep the ordering valid.
		if idx == len(list)-1 {
			return body
		}
		cacheControl = topLevel.Raw
	}
	placed, err := sjson.SetRawBytes(body, path, []byte(cacheControl))
	if err != nil {
		return body
	}
	return placed
}

// countCacheBreakpoints counts the cache_control markers a serialized request
// carries. Top-level cache_control counts as one: the API answers it with a
// marker of its own. A system prompt sent as a plain string has none.
func countCacheBreakpoints(body []byte) int {
	count := 0
	if gjson.GetBytes(body, "cache_control").IsObject() {
		count++
	}
	for _, block := range gjson.GetBytes(body, "system").Array() {
		if block.Get("cache_control").IsObject() {
			count++
		}
	}
	for _, t := range gjson.GetBytes(body, "tools").Array() {
		if t.Get("cache_control").IsObject() {
			count++
		}
	}
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		for _, block := range message.Get("content").Array() {
			if block.Get("cache_control").IsObject() {
				count++
			}
		}
	}
	return count
}

// lastAssistantMessageIndex is the serialized form of the rule
// findLastAssistantMessageIndex applies to the typed request: the last assistant
// message that is not the final message, so the breakpoint the two agree on is
// the same one.
func lastAssistantMessageIndex(messages []gjson.Result) int {
	for i := len(messages) - 2; i >= 0; i-- {
		if messages[i].Get("role").String() == "assistant" {
			return i
		}
	}
	return -1
}

// lastToolResultMessageIndex finds the newest message that carries nothing but
// tool results, searching only past minIndex (the last-assistant breakpoint, or
// -1 when there is none). convertMessages merges contiguous tool results into a
// single user message, so the match is the whole of the latest turn's tool
// output.
func lastToolResultMessageIndex(messages []gjson.Result, minIndex int) int {
	for i := len(messages) - 1; i > minIndex; i-- {
		if isToolResultMessage(messages[i]) {
			return i
		}
	}
	return -1
}

// isToolResultMessage reports whether every content block of a message is a
// tool result, which is the shape convertMessages produces when it merges a
// turn's tool results together. Text beside a result is the part that changes
// between requests, so a mixed message is not a candidate: marking there would
// invalidate the prefix the breakpoint protects.
func isToolResultMessage(message gjson.Result) bool {
	content := message.Get("content")
	if !content.IsArray() {
		return false
	}
	blocks := content.Array()
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Get("type").String() != "tool_result" {
			return false
		}
	}
	return true
}
