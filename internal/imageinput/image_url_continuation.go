//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package imageinput contains framework-side request projection helpers for
// multimodal image input continuation.
package imageinput

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// UnavailableImageURLsStateKey stores URL-backed image inputs that should
	// no longer be sent to the model service for this session.
	UnavailableImageURLsStateKey = "__trpc_agent_unavailable_image_urls_v1"

	// DefaultUnavailableImageURLPlaceholder is used when a URL-backed image is
	// suppressed from a model request after a model-side image access failure.
	DefaultUnavailableImageURLPlaceholder = "[Image unavailable: the user attached an image here, but the model could not access or decode it. The image content was not observed.]"

	maxUnavailableImageURLRecords = 128
)

// UnavailableImageURLRecord describes one URL-backed image that should be
// projected out of later model requests.
type UnavailableImageURLRecord struct {
	URL          string    `json:"url"`
	RequestID    string    `json:"request_id,omitempty"`
	InvocationID string    `json:"invocation_id,omitempty"`
	Error        string    `json:"error,omitempty"`
	MarkedAt     time.Time `json:"marked_at,omitempty"`
}

type unavailableImageURLState struct {
	Version int                         `json:"version"`
	URLs    []UnavailableImageURLRecord `json:"urls"`
}

// IsImageURLFailure reports whether err/respErr looks like a provider-side
// image URL fetch, access, validation, or decode failure.
func IsImageURLFailure(err error, respErr *model.ResponseError) bool {
	text := imageFailureText(err, respErr)
	if !strings.Contains(text, "image") && !strings.Contains(text, "图片") {
		return false
	}
	needles := []string{
		"url",
		"uri",
		"fetch",
		"retrieve",
		"download",
		"access",
		"decode",
		"invalid",
		"unsupported",
		"load",
		"read",
		"下载",
		"异常",
		"失败",
		"无效",
		"不支持",
	}
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// IsImageURLFailureForRequest reports whether err/respErr should suppress
// URL-backed image inputs from req in later model requests.
func IsImageURLFailureForRequest(
	err error,
	respErr *model.ResponseError,
	req *model.Request,
) bool {
	if IsImageURLFailure(err, respErr) {
		return true
	}
	if len(requestImageURLs(req)) == 0 {
		return false
	}
	return isURLInputFailure(imageFailureText(err, respErr))
}

// MarkUnavailableImageURLsFromRequest records image URLs from req that should
// be replaced in later model-facing request views.
func MarkUnavailableImageURLsFromRequest(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	cause error,
) (int, error) {
	if inv == nil || inv.Session == nil || req == nil {
		return 0, nil
	}
	urls := imageURLsToMark(inv, req, cause)
	if len(urls) == 0 {
		return 0, nil
	}
	state := readUnavailableImageURLState(inv.Session)
	now := time.Now()
	seen := make(map[string]int, len(state.URLs))
	for i, record := range state.URLs {
		seen[record.URL] = i
	}
	for _, imageURL := range urls {
		record := UnavailableImageURLRecord{
			URL:          imageURL,
			RequestID:    inv.RunOptions.RequestID,
			InvocationID: inv.InvocationID,
			Error:        errorString(cause),
			MarkedAt:     now,
		}
		if i, ok := seen[imageURL]; ok {
			state.URLs[i] = record
			continue
		}
		seen[imageURL] = len(state.URLs)
		state.URLs = append(state.URLs, record)
	}
	if len(state.URLs) > maxUnavailableImageURLRecords {
		state.URLs = state.URLs[len(state.URLs)-maxUnavailableImageURLRecords:]
	}
	state.Version = 1
	raw, err := json.Marshal(state)
	if err != nil {
		return 0, err
	}
	inv.Session.SetState(UnavailableImageURLsStateKey, raw)
	if inv.SessionService != nil {
		key := session.Key{
			AppName:   inv.Session.AppName,
			UserID:    inv.Session.UserID,
			SessionID: inv.Session.ID,
		}
		if err := inv.SessionService.UpdateSessionState(
			ctx,
			key,
			session.StateMap{UnavailableImageURLsStateKey: raw},
		); err != nil {
			return 0, err
		}
	}
	return len(urls), nil
}

// ProjectUnavailableImageURLs replaces session-marked URL-backed image parts
// with a text placeholder in the supplied message.
func ProjectUnavailableImageURLs(
	sess *session.Session,
	msg model.Message,
	placeholder string,
) model.Message {
	unavailable := UnavailableImageURLSet(sess)
	if len(unavailable) == 0 || len(msg.ContentParts) == 0 {
		return msg
	}
	var projected []model.ContentPart
	for i, part := range msg.ContentParts {
		if part.Type != model.ContentTypeImage || part.Image == nil {
			continue
		}
		imageURL := strings.TrimSpace(part.Image.URL)
		if imageURL == "" {
			continue
		}
		if _, ok := unavailable[imageURL]; !ok {
			continue
		}
		if projected == nil {
			projected = append([]model.ContentPart(nil), msg.ContentParts...)
		}
		projected[i] = NewUnavailableImageURLPlaceholderPart(placeholder)
	}
	if projected != nil {
		msg.ContentParts = projected
	}
	return msg
}

// NewUnavailableImageURLPlaceholderPart creates a text part for a suppressed
// image URL.
func NewUnavailableImageURLPlaceholderPart(placeholder string) model.ContentPart {
	if placeholder == "" {
		placeholder = DefaultUnavailableImageURLPlaceholder
	}
	text := placeholder
	return model.ContentPart{
		Type: model.ContentTypeText,
		Text: &text,
	}
}

// UnavailableImageURLSet returns the session's marked unavailable image URLs.
func UnavailableImageURLSet(sess *session.Session) map[string]struct{} {
	state := readUnavailableImageURLState(sess)
	if len(state.URLs) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(state.URLs))
	for _, record := range state.URLs {
		if record.URL == "" {
			continue
		}
		out[record.URL] = struct{}{}
	}
	return out
}

func imageURLsToMark(
	inv *agent.Invocation,
	req *model.Request,
	cause error,
) []string {
	reqURLs := requestImageURLs(req)
	if len(reqURLs) == 0 {
		return nil
	}
	if mentioned := mentionedURLs(
		reqURLs,
		strings.ToLower(errorString(cause)),
	); len(mentioned) > 0 {
		return mentioned
	}
	return reqURLs
}

func requestImageURLs(req *model.Request) []string {
	if req == nil {
		return nil
	}
	var urls []string
	for _, msg := range req.Messages {
		urls = append(urls, messageImageURLs(msg)...)
	}
	return dedupeOrdered(urls)
}

func messageImageURLs(msg model.Message) []string {
	var urls []string
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeImage || part.Image == nil {
			continue
		}
		imageURL := strings.TrimSpace(part.Image.URL)
		if imageURL == "" {
			continue
		}
		urls = append(urls, imageURL)
	}
	return dedupeOrdered(urls)
}

func mentionedURLs(urls []string, text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, imageURL := range urls {
		if strings.Contains(text, strings.ToLower(imageURL)) {
			out = append(out, imageURL)
		}
	}
	return dedupeOrdered(out)
}

func isURLInputFailure(text string) bool {
	if !strings.Contains(text, "url") &&
		!strings.Contains(text, "uri") &&
		!strings.Contains(text, "图片") {
		return false
	}
	needles := []string{
		"must be",
		"invalid",
		"unsupported",
		"malformed",
		"scheme",
		"fetch",
		"retrieve",
		"download",
		"access",
		"decode",
		"load",
		"read",
		"下载",
		"异常",
		"失败",
		"无效",
		"不支持",
	}
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func intersectOrdered(primary, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, imageURL := range allowed {
		allowedSet[imageURL] = struct{}{}
	}
	var out []string
	for _, imageURL := range primary {
		if _, ok := allowedSet[imageURL]; ok {
			out = append(out, imageURL)
		}
	}
	return dedupeOrdered(out)
}

func dedupeOrdered(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(urls))
	out := make([]string, 0, len(urls))
	for _, imageURL := range urls {
		if imageURL == "" {
			continue
		}
		if _, ok := seen[imageURL]; ok {
			continue
		}
		seen[imageURL] = struct{}{}
		out = append(out, imageURL)
	}
	return out
}

func readUnavailableImageURLState(sess *session.Session) unavailableImageURLState {
	if sess == nil {
		return unavailableImageURLState{Version: 1}
	}
	raw, ok := sess.GetState(UnavailableImageURLsStateKey)
	if !ok || len(raw) == 0 {
		return unavailableImageURLState{Version: 1}
	}
	var state unavailableImageURLState
	if err := json.Unmarshal(raw, &state); err != nil {
		return unavailableImageURLState{Version: 1}
	}
	if state.Version == 0 {
		state.Version = 1
	}
	return state
}

func imageFailureText(err error, respErr *model.ResponseError) string {
	parts := make([]string, 0, 5)
	if err != nil {
		parts = append(parts, err.Error())
	}
	var nested *model.ResponseError
	if errors.As(err, &nested) && nested != nil {
		respErr = nested
	}
	if respErr != nil {
		parts = append(parts, respErr.Message, respErr.Type)
		if respErr.Code != nil {
			parts = append(parts, *respErr.Code)
		}
		if respErr.Param != nil {
			parts = append(parts, *respErr.Param)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
