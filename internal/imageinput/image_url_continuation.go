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
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionroute"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// UnavailableImageURLsStateKey stores URL-backed image input locations that
	// should no longer be sent to the model service for this session.
	UnavailableImageURLsStateKey = "__trpc_agent_unavailable_image_urls_v1"

	// DefaultUnavailableImageURLPlaceholder is used when a URL-backed image is
	// suppressed from a model request after a model-side image access failure.
	DefaultUnavailableImageURLPlaceholder = "[Image unavailable: the user attached an image here, but the model could not access or decode it. The image content was not observed.]"

	maxUnavailableImageURLRecords   = 128
	unavailableImageURLStateVersion = 2
)

var (
	urlFailureSignals = []string{
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
		"not-a-url",
		"not a url",
		"不符合",
		"安全要求",
		"base64",
		"cos",
		"下载",
		"拉取",
		"访问",
		"异常",
		"失败",
		"无效",
		"不支持",
	}

	imageFetchSignals = []string{
		"fetch",
		"retrieve",
		"download",
		"access",
		"下载",
		"拉取",
		"访问",
	}
)

// UnavailableImageURLRecord describes one event-scoped URL-backed image part
// that should be projected out of later model requests.
type UnavailableImageURLRecord struct {
	EventID      string    `json:"event_id,omitempty"`
	ChoiceIndex  int       `json:"choice_index"`
	PartIndex    int       `json:"part_index"`
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

type unavailableImageURLLocation struct {
	EventID     string
	ChoiceIndex int
	PartIndex   int
	URL         string
}

type imageURLMark struct {
	EventID     string
	ChoiceIndex int
	PartIndex   int
	URL         string
}

type imageURLRef struct {
	EventID      string
	RequestID    string
	InvocationID string
	ChoiceIndex  int
	PartIndex    int
	URL          string
	Message      model.Message
}

var (
	imageURLParamBracketPattern = regexp.MustCompile(
		`messages\[(\d+)\]\.content\[(\d+)\]`,
	)
	imageURLParamDotPattern = regexp.MustCompile(
		`messages\.(\d+)\.content\.(\d+)`,
	)
)

// IsImageURLFailure reports whether err/respErr looks like a provider-side
// image URL fetch, access, validation, or decode failure.
func IsImageURLFailure(err error, respErr *model.ResponseError) bool {
	text := imageFailureText(err, respErr)
	if !hasImageSignal(text) {
		return false
	}
	if hasURLSignal(text) {
		return containsAny(text, urlFailureSignals)
	}
	return containsAny(text, imageFetchSignals)
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
	text := imageFailureText(err, respErr)
	return hasURLSignal(text) && containsAny(text, urlFailureSignals)
}

// MarkUnavailableImageURLsFromRequest records event-scoped image URL locations
// from req that should be replaced in later model-facing request views.
func MarkUnavailableImageURLsFromRequest(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	cause error,
	respErr *model.ResponseError,
) (int, error) {
	if inv == nil || inv.Session == nil || req == nil {
		return 0, nil
	}
	markSession, marks, err := imageURLMarksToRecord(
		ctx,
		inv,
		req,
		cause,
		respErr,
	)
	if err != nil || len(marks) == 0 || markSession == nil {
		return 0, err
	}
	return writeUnavailableImageURLMarks(ctx, inv, markSession, marks, cause)
}

func imageURLMarksToRecord(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	cause error,
	respErr *model.ResponseError,
) (*session.Session, []imageURLMark, error) {
	markSession := inv.Session
	if routedSession, ok := routedImageURLFailureSession(inv); ok &&
		!sameSession(routedSession, inv.Session) {
		markSession = routedSession
	}
	marks := imageURLMarksToMarkInSession(inv, markSession, req, cause, respErr)
	if len(marks) == 0 {
		var sessionForMarks *session.Session
		var err error
		sessionForMarks, marks, err = imageURLMarksToMarkFromPersistedSession(
			ctx,
			inv,
			markSession,
			req,
			cause,
			respErr,
		)
		if err != nil {
			return nil, nil, err
		}
		if len(marks) > 0 {
			markSession = sessionForMarks
		}
	}
	return markSession, marks, nil
}

func writeUnavailableImageURLMarks(
	ctx context.Context,
	inv *agent.Invocation,
	markSession *session.Session,
	marks []imageURLMark,
	cause error,
) (int, error) {
	state := readUnavailableImageURLState(markSession)
	now := time.Now()
	seen := make(map[unavailableImageURLLocation]int, len(state.URLs))
	for i, record := range state.URLs {
		if !record.hasLocation() {
			continue
		}
		seen[record.location()] = i
	}
	written := 0
	for _, mark := range marks {
		if !mark.hasLocation() {
			continue
		}
		record := UnavailableImageURLRecord{
			EventID:      mark.EventID,
			ChoiceIndex:  mark.ChoiceIndex,
			PartIndex:    mark.PartIndex,
			URL:          mark.URL,
			RequestID:    inv.RunOptions.RequestID,
			InvocationID: inv.InvocationID,
			Error:        errorString(cause),
			MarkedAt:     now,
		}
		location := mark.location()
		if i, ok := seen[location]; ok {
			state.URLs[i] = record
			written++
			continue
		}
		seen[location] = len(state.URLs)
		state.URLs = append(state.URLs, record)
		written++
	}
	if written == 0 {
		return 0, nil
	}
	if len(state.URLs) > maxUnavailableImageURLRecords {
		state.URLs = state.URLs[len(state.URLs)-maxUnavailableImageURLRecords:]
	}
	state.Version = unavailableImageURLStateVersion
	raw, err := json.Marshal(state)
	if err != nil {
		return 0, err
	}
	if inv.SessionService != nil {
		key := session.Key{
			AppName:   markSession.AppName,
			UserID:    markSession.UserID,
			SessionID: markSession.ID,
		}
		if err := inv.SessionService.UpdateSessionState(
			ctx,
			key,
			session.StateMap{UnavailableImageURLsStateKey: raw},
		); err != nil {
			return 0, err
		}
	}
	markSession.SetState(UnavailableImageURLsStateKey, raw)
	return written, nil
}

func imageURLMarksToMarkFromPersistedSession(
	ctx context.Context,
	inv *agent.Invocation,
	baseSession *session.Session,
	req *model.Request,
	cause error,
	respErr *model.ResponseError,
) (*session.Session, []imageURLMark, error) {
	if inv == nil || baseSession == nil || inv.SessionService == nil {
		return nil, nil, nil
	}
	sess, err := inv.SessionService.GetSession(ctx, session.Key{
		AppName:   baseSession.AppName,
		UserID:    baseSession.UserID,
		SessionID: baseSession.ID,
	})
	if err != nil || sess == nil {
		return nil, nil, err
	}
	return sess, imageURLMarksToMarkInSession(inv, sess, req, cause, respErr), nil
}

func routedImageURLFailureSession(inv *agent.Invocation) (*session.Session, bool) {
	if inv == nil {
		return nil, false
	}
	routeEvt := &event.Event{
		RequestID:    inv.RunOptions.RequestID,
		InvocationID: inv.InvocationID,
		Branch:       inv.Branch,
		FilterKey:    inv.GetEventFilterKey(),
	}
	if parent := inv.GetParentInvocation(); parent != nil {
		routeEvt.ParentInvocationID = parent.InvocationID
	}
	return sessionroute.RouteEvent(inv, routeEvt)
}

func sameSession(a, b *session.Session) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AppName == b.AppName && a.UserID == b.UserID && a.ID == b.ID
}

// ProjectUnavailableImageURLs replaces session-marked event-scoped URL-backed
// image parts with a text placeholder in the supplied message.
func ProjectUnavailableImageURLs(
	sess *session.Session,
	evt event.Event,
	choiceIndex int,
	msg model.Message,
	placeholder string,
) model.Message {
	unavailable := unavailableImageURLLocationSet(sess)
	if evt.ID == "" || len(unavailable) == 0 || len(msg.ContentParts) == 0 {
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
		location := unavailableImageURLLocation{
			EventID:     evt.ID,
			ChoiceIndex: choiceIndex,
			PartIndex:   i,
			URL:         imageURL,
		}
		if _, ok := unavailable[location]; !ok {
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

func unavailableImageURLLocationSet(
	sess *session.Session,
) map[unavailableImageURLLocation]struct{} {
	state := readUnavailableImageURLState(sess)
	if len(state.URLs) == 0 {
		return nil
	}
	out := make(map[unavailableImageURLLocation]struct{}, len(state.URLs))
	for _, record := range state.URLs {
		if !record.hasLocation() {
			// URL-only draft records cannot be projected without widening scope.
			continue
		}
		out[record.location()] = struct{}{}
	}
	return out
}

func (r UnavailableImageURLRecord) hasLocation() bool {
	return r.EventID != "" && r.URL != "" && r.PartIndex >= 0
}

func (r UnavailableImageURLRecord) location() unavailableImageURLLocation {
	return unavailableImageURLLocation{
		EventID:     r.EventID,
		ChoiceIndex: r.ChoiceIndex,
		PartIndex:   r.PartIndex,
		URL:         r.URL,
	}
}

func (m imageURLMark) hasLocation() bool {
	return m.EventID != "" && m.URL != "" && m.PartIndex >= 0
}

func (m imageURLMark) location() unavailableImageURLLocation {
	return unavailableImageURLLocation{
		EventID:     m.EventID,
		ChoiceIndex: m.ChoiceIndex,
		PartIndex:   m.PartIndex,
		URL:         m.URL,
	}
}

func imageURLMarksToMarkInSession(
	inv *agent.Invocation,
	sess *session.Session,
	req *model.Request,
	cause error,
	respErr *model.ResponseError,
) []imageURLMark {
	if inv == nil || sess == nil {
		return nil
	}
	if marks := imageURLMarksFromResponseParam(
		sess,
		req,
		cause,
		respErr,
	); len(marks) > 0 {
		return marks
	}
	reqURLs := requestImageURLs(req)
	if len(reqURLs) == 0 {
		return nil
	}
	if mentioned := mentionedURLs(
		reqURLs,
		imageFailureText(cause, respErr),
	); len(mentioned) > 0 {
		return uniqueSessionImageURLMarks(sess, req, mentioned)
	}
	current := messageImageURLs(inv.Message)
	if len(current) > 0 {
		return currentInvocationImageURLMarks(
			inv,
			sess,
			intersectOrdered(current, reqURLs),
		)
	}
	return uniqueSessionImageURLMarks(sess, req, reqURLs)
}

func imageURLMarksFromResponseParam(
	sess *session.Session,
	req *model.Request,
	cause error,
	respErr *model.ResponseError,
) []imageURLMark {
	respErr = responseError(cause, respErr)
	if respErr == nil || respErr.Param == nil {
		return nil
	}
	messageIndex, partIndex, ok := parseImageURLParam(*respErr.Param)
	if !ok || req == nil || messageIndex < 0 || messageIndex >= len(req.Messages) {
		return nil
	}
	msg := req.Messages[messageIndex]
	if partIndex < 0 || partIndex >= len(msg.ContentParts) {
		return nil
	}
	part := msg.ContentParts[partIndex]
	if part.Type != model.ContentTypeImage || part.Image == nil {
		return nil
	}
	imageURL := strings.TrimSpace(part.Image.URL)
	if imageURL == "" {
		return nil
	}
	var matches []imageURLMark
	for _, ref := range sessionImageURLRefs(sess) {
		if ref.URL != imageURL || ref.PartIndex != partIndex {
			continue
		}
		if !messageContentEqual(ref.Message, msg) {
			continue
		}
		matches = append(matches, markFromRef(ref))
	}
	if len(matches) != 1 {
		return nil
	}
	return matches
}

func currentInvocationImageURLMarks(
	inv *agent.Invocation,
	sess *session.Session,
	urls []string,
) []imageURLMark {
	if inv == nil || sess == nil || len(urls) == 0 {
		return nil
	}
	allowed := urlSet(urls)
	var out []imageURLMark
	for _, ref := range sessionImageURLRefs(sess) {
		if _, ok := allowed[ref.URL]; !ok {
			continue
		}
		if inv.InvocationID != "" && ref.InvocationID != inv.InvocationID {
			continue
		}
		if !messageContentEqual(ref.Message, inv.Message) {
			continue
		}
		out = append(out, markFromRef(ref))
	}
	return dedupeImageURLMarks(out)
}

func uniqueSessionImageURLMarks(
	sess *session.Session,
	req *model.Request,
	urls []string,
) []imageURLMark {
	if sess == nil || req == nil || len(urls) == 0 {
		return nil
	}
	allowed := urlSet(urls)
	byURL := make(map[string][]imageURLMark, len(urls))
	for _, ref := range sessionImageURLRefs(sess) {
		if _, ok := allowed[ref.URL]; !ok {
			continue
		}
		if !requestContainsMessage(req, ref.Message) {
			continue
		}
		byURL[ref.URL] = append(byURL[ref.URL], markFromRef(ref))
	}
	var out []imageURLMark
	for _, imageURL := range urls {
		matches := dedupeImageURLMarks(byURL[imageURL])
		if len(matches) != 1 {
			continue
		}
		out = append(out, matches[0])
	}
	return out
}

func sessionImageURLRefs(sess *session.Session) []imageURLRef {
	if sess == nil {
		return nil
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	var refs []imageURLRef
	for _, evt := range sess.Events {
		if evt.ID == "" || evt.Response == nil {
			continue
		}
		for choiceIndex, choice := range evt.Response.Choices {
			msg := choice.Message
			for partIndex, part := range msg.ContentParts {
				if part.Type != model.ContentTypeImage || part.Image == nil {
					continue
				}
				imageURL := strings.TrimSpace(part.Image.URL)
				if imageURL == "" {
					continue
				}
				refs = append(refs, imageURLRef{
					EventID:      evt.ID,
					RequestID:    evt.RequestID,
					InvocationID: evt.InvocationID,
					ChoiceIndex:  choiceIndex,
					PartIndex:    partIndex,
					URL:          imageURL,
					Message:      msg,
				})
			}
		}
	}
	return refs
}

func markFromRef(ref imageURLRef) imageURLMark {
	return imageURLMark{
		EventID:     ref.EventID,
		ChoiceIndex: ref.ChoiceIndex,
		PartIndex:   ref.PartIndex,
		URL:         ref.URL,
	}
}

func requestContainsMessage(req *model.Request, msg model.Message) bool {
	if req == nil {
		return false
	}
	for _, reqMsg := range req.Messages {
		if messageContentEqual(reqMsg, msg) {
			return true
		}
	}
	return false
}

func messageContentEqual(a, b model.Message) bool {
	return a.Role == b.Role &&
		a.Content == b.Content &&
		a.ToolID == b.ToolID &&
		a.ToolName == b.ToolName &&
		reflect.DeepEqual(a.ContentParts, b.ContentParts) &&
		reflect.DeepEqual(a.ToolCalls, b.ToolCalls)
}

func responseError(
	err error,
	respErr *model.ResponseError,
) *model.ResponseError {
	var nested *model.ResponseError
	if errors.As(err, &nested) && nested != nil {
		return nested
	}
	return respErr
}

func parseImageURLParam(param string) (int, int, bool) {
	if param == "" {
		return 0, 0, false
	}
	if messageIndex, partIndex, ok := parseImageURLParamWithPattern(
		imageURLParamBracketPattern,
		param,
	); ok {
		return messageIndex, partIndex, true
	}
	return parseImageURLParamWithPattern(imageURLParamDotPattern, param)
}

func parseImageURLParamWithPattern(
	pattern *regexp.Regexp,
	param string,
) (int, int, bool) {
	matches := pattern.FindStringSubmatch(param)
	if len(matches) != 3 {
		return 0, 0, false
	}
	messageIndex, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, false
	}
	partIndex, err := strconv.Atoi(matches[2])
	if err != nil {
		return 0, 0, false
	}
	return messageIndex, partIndex, true
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
		if containsURLToken(text, strings.ToLower(imageURL)) {
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

func urlSet(urls []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(urls))
	for _, imageURL := range urls {
		if imageURL == "" {
			continue
		}
		seen[imageURL] = struct{}{}
	}
	return seen
}

func dedupeImageURLMarks(marks []imageURLMark) []imageURLMark {
	if len(marks) == 0 {
		return nil
	}
	seen := make(map[unavailableImageURLLocation]struct{}, len(marks))
	out := make([]imageURLMark, 0, len(marks))
	for _, mark := range marks {
		if !mark.hasLocation() {
			continue
		}
		location := mark.location()
		if _, ok := seen[location]; ok {
			continue
		}
		seen[location] = struct{}{}
		out = append(out, mark)
	}
	return out
}

func containsURLToken(text, imageURL string) bool {
	if text == "" || imageURL == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(text[start:], imageURL)
		if idx < 0 {
			return false
		}
		idx += start
		end := idx + len(imageURL)
		if isURLTokenBoundaryBefore(text, idx) &&
			isURLTokenBoundaryAfter(text, end) {
			return true
		}
		start = idx + 1
	}
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

func isURLTokenBoundaryBefore(text string, idx int) bool {
	if idx <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:idx])
	return isURLTokenBoundaryRune(r)
}

func isURLTokenBoundaryAfter(text string, idx int) bool {
	if idx >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[idx:])
	return isURLTokenBoundaryRune(r)
}

func isURLTokenBoundaryRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsControl(r) {
		return true
	}
	switch r {
	case '"', '\'', '`', '<', '>', '(', ')', '[', ']', '{', '}', ',', ';':
		return true
	default:
		return false
	}
}

func hasImageSignal(text string) bool {
	return strings.Contains(text, "image") || strings.Contains(text, "图片")
}

func hasURLSignal(text string) bool {
	return strings.Contains(text, "url") ||
		containsWordSignal(text, "uri") ||
		containsWordSignal(text, "link") ||
		strings.Contains(text, "链接") ||
		strings.Contains(text, "地址")
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsWordSignal(text, word string) bool {
	if text == "" || word == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(text[start:], word)
		if idx < 0 {
			return false
		}
		idx += start
		end := idx + len(word)
		if isWordBoundaryBefore(text, idx) && isWordBoundaryAfter(text, end) {
			return true
		}
		start = idx + 1
	}
}

func isWordBoundaryBefore(text string, idx int) bool {
	if idx <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:idx])
	return !unicode.IsLetter(r) && !unicode.IsNumber(r)
}

func isWordBoundaryAfter(text string, idx int) bool {
	if idx >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[idx:])
	return !unicode.IsLetter(r) && !unicode.IsNumber(r)
}

func readUnavailableImageURLState(sess *session.Session) unavailableImageURLState {
	if sess == nil {
		return unavailableImageURLState{Version: unavailableImageURLStateVersion}
	}
	raw, ok := sess.GetState(UnavailableImageURLsStateKey)
	if !ok || len(raw) == 0 {
		return unavailableImageURLState{Version: unavailableImageURLStateVersion}
	}
	var state unavailableImageURLState
	if err := json.Unmarshal(raw, &state); err != nil {
		return unavailableImageURLState{Version: unavailableImageURLStateVersion}
	}
	if state.Version == 0 {
		state.Version = unavailableImageURLStateVersion
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
