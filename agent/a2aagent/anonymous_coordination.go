//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	anonymousCookieRecordVersion        = 1
	anonymousCookieRecordStateKeySuffix = ".record.v1"
)

var errAnonymousCookieNotCaptured = errors.New(
	"anonymous A2A response did not establish a valid identity cookie",
)

type anonymousCookieRecordEnvelope struct {
	Version   int        `json:"version"`
	Value     string     `json:"value,omitempty"`
	Secure    bool       `json:"secure,omitempty"`
	Path      string     `json:"path,omitempty"`
	Domain    string     `json:"domain,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Deleted   bool       `json:"deleted,omitempty"`
}

func encodeAnonymousCookieRecord(record anonymousCookieRecord) ([]byte, error) {
	envelope := anonymousCookieRecordEnvelope{
		Version: anonymousCookieRecordVersion,
		Value:   strings.TrimSpace(record.value),
		Secure:  record.secure,
		Path:    strings.TrimSpace(record.path),
		Domain:  normalizeAnonymousCookieDomain(record.domain),
	}
	if !record.expires.IsZero() {
		expires := record.expires.UTC()
		envelope.ExpiresAt = &expires
	}
	return json.Marshal(envelope)
}

func decodeAnonymousCookieRecord(
	value []byte,
	scope anonymousCookieURLScope,
) (anonymousCookieRecord, bool) {
	var envelope anonymousCookieRecordEnvelope
	if len(value) == 0 || json.Unmarshal(value, &envelope) != nil ||
		envelope.Version != anonymousCookieRecordVersion || envelope.Deleted {
		return anonymousCookieRecord{}, false
	}
	record := anonymousCookieRecord{
		value:  strings.TrimSpace(envelope.Value),
		secure: envelope.Secure,
		path:   strings.TrimSpace(envelope.Path),
		domain: normalizeAnonymousCookieDomain(envelope.Domain),
	}
	if !isAnonymousUserIDCookieValue(record.value) {
		return anonymousCookieRecord{}, false
	}
	if record.path != "" && !strings.HasPrefix(record.path, "/") {
		return anonymousCookieRecord{}, false
	}
	if !anonymousCookiePathIntersectsScope(record.path, scope.path) {
		return anonymousCookieRecord{}, false
	}
	if record.domain != "" && scope.hostname != "" {
		host := strings.ToLower(strings.TrimSuffix(scope.hostname, "."))
		if host != record.domain && !strings.HasSuffix(host, "."+record.domain) {
			return anonymousCookieRecord{}, false
		}
	}
	if envelope.ExpiresAt != nil {
		record.expires = envelope.ExpiresAt.UTC()
		if !record.expires.After(time.Now()) {
			return anonymousCookieRecord{}, false
		}
	}
	return record, true
}

func anonymousCookiePathIntersectsScope(cookiePath, scopePath string) bool {
	if scopePath == "" {
		scopePath = "/"
	}
	if cookiePath == "" {
		cookiePath = "/"
	}
	return anonymousCookiePathMatches(scopePath, cookiePath) ||
		anonymousCookiePathMatches(cookiePath, scopePath)
}

func anonymousCookieTombstoneValue() []byte {
	// The envelope contains only fields supported by encoding/json.
	value, _ := json.Marshal(anonymousCookieRecordEnvelope{
		Version: anonymousCookieRecordVersion,
		Deleted: true,
	})
	return value
}

func (s *anonymousCookieState) canonicalStateKey() string {
	if s == nil || s.key == "" {
		return ""
	}
	return s.key + anonymousCookieRecordStateKeySuffix
}

func (s *anonymousCookieState) loadCanonicalRecord() (
	anonymousCookieRecord,
	bool,
	bool,
) {
	if s == nil || s.canonicalStateKey() == "" {
		return anonymousCookieRecord{}, false, false
	}
	seen := make(map[*session.Session]struct{}, 2)
	for _, sess := range []*session.Session{s.persistSession, s.session} {
		if sess == nil {
			continue
		}
		if _, ok := seen[sess]; ok {
			continue
		}
		seen[sess] = struct{}{}
		value, present := sess.GetState(s.canonicalStateKey())
		if !present {
			continue
		}
		record, ok := decodeAnonymousCookieRecord(value, s.scope)
		return record, ok, true
	}
	return anonymousCookieRecord{}, false, false
}

func (s *anonymousCookieState) stateInitializer() (
	session.StateInitializationService,
	session.Key,
	bool,
) {
	if s == nil || s.sessionService == nil {
		return nil, session.Key{}, false
	}
	initializer, ok := s.sessionService.(session.StateInitializationService)
	if !ok {
		return nil, session.Key{}, false
	}
	key, ok := s.persistentSessionKey()
	if !ok {
		return nil, session.Key{}, false
	}
	return initializer, key, true
}

func (s *anonymousCookieState) usesCanonicalRecord() bool {
	if _, _, present := s.loadCanonicalRecord(); present {
		return true
	}
	_, _, ok := s.stateInitializer()
	return ok
}

func (s *anonymousCookieState) legacyRecordForMigration() (
	anonymousCookieRecord,
	bool,
) {
	if _, _, present := s.loadCanonicalRecord(); present {
		return anonymousCookieRecord{}, false
	}
	return s.loadLegacyRecord()
}

func (s *anonymousCookieState) storeCanonicalValue(value []byte) error {
	if s == nil {
		return errors.New("store anonymous A2A cookie record: state is nil")
	}
	record, ok := decodeAnonymousCookieRecord(value, s.scope)
	if !ok {
		return errors.New("store anonymous A2A cookie record: value is invalid")
	}
	for _, sess := range uniqueAnonymousCookieSessions(s.session, s.persistSession) {
		sess.SetState(s.canonicalStateKey(), value)
		storeAnonymousCookieRecord(sess, s.key, record)
	}
	return nil
}

func (s *anonymousCookieState) legacyStateProjection(
	value []byte,
) (session.StateMap, error) {
	if s == nil || s.key == "" {
		return nil, errors.New("project anonymous A2A cookie record: state is unavailable")
	}
	var envelope anonymousCookieRecordEnvelope
	if len(value) == 0 || json.Unmarshal(value, &envelope) != nil ||
		envelope.Version != anonymousCookieRecordVersion {
		return nil, errors.New("project anonymous A2A cookie record: value is invalid")
	}
	if envelope.Deleted {
		return anonymousCookieClearedStateMap(s.key), nil
	}
	record, ok := decodeAnonymousCookieRecord(value, s.scope)
	if !ok {
		return nil, errors.New("project anonymous A2A cookie record: value is invalid")
	}
	return anonymousCookieRecordStateMap(s.key, record), nil
}

func (s *anonymousCookieState) legacyStateProjections() []session.StateInitializationProjection {
	keys := []string{
		s.key,
		s.key + anonymousUserIDCookieSecureKeySuffix,
		s.key + anonymousUserIDCookiePathKeySuffix,
		s.key + anonymousUserIDCookieDomainKeySuffix,
		s.key + anonymousUserIDCookieExpiryKeySuffix,
	}
	projections := make([]session.StateInitializationProjection, 0, len(keys))
	for _, stateKey := range keys {
		stateKey := stateKey
		projections = append(projections, session.StateInitializationProjection{
			StateKey: stateKey,
			Project: func(value []byte) ([]byte, error) {
				state, err := s.legacyStateProjection(value)
				if err != nil {
					return nil, err
				}
				return state[stateKey], nil
			},
		})
	}
	return projections
}

func (s *anonymousCookieState) storeRecordLocally(record anonymousCookieRecord) {
	if s == nil {
		return
	}
	canonical := s.usesCanonicalRecord()
	var encoded []byte
	if canonical {
		encoded, _ = encodeAnonymousCookieRecord(record)
	}
	for _, sess := range uniqueAnonymousCookieSessions(s.session, s.persistSession) {
		if encoded != nil {
			sess.SetState(s.canonicalStateKey(), encoded)
		}
		storeAnonymousCookieRecord(sess, s.key, record)
	}
}

func (s *anonymousCookieState) storeCanonicalTombstoneLocally() {
	if s == nil {
		return
	}
	for _, sess := range uniqueAnonymousCookieSessions(s.session, s.persistSession) {
		sess.SetState(s.canonicalStateKey(), anonymousCookieTombstoneValue())
	}
}

func (s *anonymousCookieState) syncFromPersistedSession(
	persisted *session.Session,
) {
	if s == nil || persisted == nil {
		return
	}
	canonicalValue, canonicalPresent := persisted.GetState(s.canonicalStateKey())
	if canonicalPresent {
		for _, sess := range uniqueAnonymousCookieSessions(s.session, s.persistSession) {
			sess.SetState(s.canonicalStateKey(), canonicalValue)
			if record, ok := decodeAnonymousCookieRecord(canonicalValue, s.scope); ok {
				storeAnonymousCookieRecord(sess, s.key, record)
			} else {
				clearAnonymousCookieState(sess, s.key)
			}
		}
		return
	}
	for _, sess := range uniqueAnonymousCookieSessions(s.session, s.persistSession) {
		sess.DeleteState(s.canonicalStateKey())
	}
	if record, ok := loadAnonymousCookieStateFromSession(persisted, s.key); ok {
		for _, sess := range uniqueAnonymousCookieSessions(s.session, s.persistSession) {
			storeAnonymousCookieRecord(sess, s.key, record)
		}
	}
}

func uniqueAnonymousCookieSessions(
	sessions ...*session.Session,
) []*session.Session {
	unique := make([]*session.Session, 0, len(sessions))
	seen := make(map[*session.Session]struct{}, len(sessions))
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		if _, ok := seen[sess]; ok {
			continue
		}
		seen[sess] = struct{}{}
		unique = append(unique, sess)
	}
	return unique
}

func (h *anonymousCookieHTTPReqHandler) cookieInitializationNeeded(
	cookie *anonymousCookieState,
) bool {
	if cookie == nil {
		return false
	}
	if _, _, coordinated := cookie.stateInitializer(); coordinated {
		_, valid, _ := cookie.loadCanonicalRecord()
		return !valid
	}
	_, valid := cookie.loadRecord()
	return !valid
}

func (h *anonymousCookieHTTPReqHandler) handleCoordinatedInitialization(
	ctx context.Context,
	httpClient *http.Client,
	req *http.Request,
	cookie *anonymousCookieState,
	initializer session.StateInitializationService,
	persistentKey session.Key,
) (*http.Response, error) {
	var (
		ownerResponse *http.Response
		ownerErr      error
	)
	value, didInitialize, err := initializer.LoadOrInitializeSessionState(
		ctx,
		persistentKey,
		cookie.canonicalStateKey(),
		func(value []byte) bool {
			_, ok := decodeAnonymousCookieRecord(value, h.scope)
			return ok
		},
		func(initializeCtx context.Context) ([]byte, error) {
			if err := initializeCtx.Err(); err != nil {
				return nil, err
			}
			if legacy, ok := cookie.legacyRecordForMigration(); ok {
				encoded, encodeErr := encodeAnonymousCookieRecord(legacy)
				if encodeErr != nil {
					return nil, encodeErr
				}
				if _, valid := decodeAnonymousCookieRecord(
					encoded,
					h.scope,
				); valid {
					return encoded, nil
				}
			}
			pendingSession := session.NewSession("", "", "")
			pendingCookie := newAnonymousCookieState(
				pendingSession,
				nil,
				nil,
				cookie.key,
				h.scope,
			)
			// The response body outlives this initializer callback, so its request
			// must remain bound to the invocation context after the lease is closed.
			ownerResponse, ownerErr = h.sendRequest(
				ctx,
				httpClient,
				req,
				pendingCookie,
			)
			if ownerErr != nil {
				closeAnonymousCookieResponse(ownerResponse)
				ownerResponse = nil
				return nil, ownerErr
			}
			if err := initializeCtx.Err(); err != nil {
				closeAnonymousCookieResponse(ownerResponse)
				ownerResponse = nil
				return nil, err
			}
			record, ok := pendingCookie.loadLegacyRecord()
			if !ok {
				return nil, errAnonymousCookieNotCaptured
			}
			return encodeAnonymousCookieRecord(record)
		},
		cookie.legacyStateProjections()...,
	)
	if err != nil {
		if errors.Is(err, errAnonymousCookieNotCaptured) &&
			!h.requireCoordination {
			if ownerResponse == nil && ownerErr == nil {
				return nil, fmt.Errorf(
					"coordinate anonymous A2A identity: initializer returned no response: %w",
					err,
				)
			}
			return ownerResponse, ownerErr
		}
		closeAnonymousCookieResponse(ownerResponse)
		return nil, fmt.Errorf("coordinate anonymous A2A identity: %w", err)
	}
	if err := cookie.storeCanonicalValue(value); err != nil {
		closeAnonymousCookieResponse(ownerResponse)
		return nil, err
	}
	if didInitialize && ownerResponse != nil {
		return ownerResponse, ownerErr
	}
	if ownerResponse != nil {
		closeAnonymousCookieResponse(ownerResponse)
		return nil, errors.New(
			"coordinate anonymous A2A identity: initializer returned a response without committing it",
		)
	}
	return h.sendRequest(ctx, httpClient, req, cookie)
}

func (h *anonymousCookieHTTPReqHandler) sendRequest(
	ctx context.Context,
	httpClient *http.Client,
	req *http.Request,
	cookie *anonymousCookieState,
) (*http.Response, error) {
	captureResult := &anonymousCookieCaptureResult{}
	// Session state owns anonymous identity; a shared user-supplied Jar must
	// not replay another local session's remote principal.
	requestClient := *httpClient
	requestClient.Jar = &anonymousCookieJar{
		ctx:    ctx,
		base:   httpClient.Jar,
		cookie: cookie,
		scope:  h.scope,
		result: captureResult,
	}
	requestClient.Transport = &anonymousCookieRoundTripper{
		base:   httpClient.Transport,
		cookie: cookie,
		scope:  h.scope,
		result: captureResult,
	}
	request := req.Clone(ctx)
	setAnonymousCookieHeader(request, cookie, h.scope)
	resp, err := h.next.Handle(ctx, &requestClient, request)
	h.captureResponseCookie(ctx, request, resp, cookie, captureResult)
	if captureErr := captureResult.error(); captureErr != nil {
		closeAnonymousCookieResponse(resp)
		if err != nil {
			return nil, errors.Join(err, captureErr)
		}
		return nil, captureErr
	}
	return resp, err
}

func closeAnonymousCookieResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}
