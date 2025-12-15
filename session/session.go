//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package session provides the core session functionality.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// StateMap is a map of state key-value pairs.
type StateMap map[string][]byte

var (
	// ErrAppNameRequired is the error for app name required.
	ErrAppNameRequired = errors.New("appName is required")
	// ErrUserIDRequired is the error for user id required.
	ErrUserIDRequired = errors.New("userID is required")
	// ErrSessionIDRequired is the error for session id required.
	ErrSessionIDRequired = errors.New("sessionID is required")
)

// SummaryFilterKeyAllContents is the filter key representing
// the full-session summary with no filtering applied.
const SummaryFilterKeyAllContents = ""

// Session is the interface that all sessions must implement.
type Session struct {
	ID       string                 `json:"id"`      // ID is the session id.
	AppName  string                 `json:"appName"` // AppName is the app name.
	UserID   string                 `json:"userID"`  // UserID is the user id.
	State    StateMap               `json:"state"`   // State is the session state with delta support.
	Events   []event.Event          `json:"events"`  // Events is the session events.
	EventMu  sync.RWMutex           `json:"-"`
	Tracks   map[Track]*TrackEvents `json:"tracks,omitempty"` // Tracks stores track events.
	TracksMu sync.RWMutex           `json:"-"`
	// Summaries holds filter-aware summaries. The key is the event filter key.
	SummariesMu sync.RWMutex        `json:"-"`                   // SummariesMu is the read-write mutex for Summaries.
	Summaries   map[string]*Summary `json:"summaries,omitempty"` // Summaries is the filter-aware summaries.
	UpdatedAt   time.Time           `json:"updatedAt"`           // UpdatedAt is the last update time.
	CreatedAt   time.Time           `json:"createdAt"`           // CreatedAt is the creation time.

	// Hash is the pre-computed slot hash value for asynchronous task dispatching.
	// It is calculated once during session creation using murmur3 hash of
	// "appName:userID:sessionID" and remains immutable throughout the session's lifecycle.
	// This field is computed once during session creation and never modified.
	Hash int `json:"-"`
}

// Clone returns a copy of the session.
func (sess *Session) Clone() *Session {
	sess.EventMu.RLock()
	copiedSess := &Session{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		State:     make(StateMap), // Create new state to avoid reference sharing.
		Events:    make([]event.Event, len(sess.Events)),
		UpdatedAt: sess.UpdatedAt,
		CreatedAt: sess.CreatedAt, // Add missing CreatedAt field.
		Hash:      sess.Hash,
	}
	// Copy events.
	copy(copiedSess.Events, sess.Events)
	sess.EventMu.RUnlock()

	// Copy track events.
	sess.TracksMu.RLock()
	if len(sess.Tracks) > 0 {
		copiedSess.Tracks = make(map[Track]*TrackEvents, len(sess.Tracks))
		for track, events := range sess.Tracks {
			history := &TrackEvents{
				Track: events.Track,
			}
			if len(events.Events) > 0 {
				history.Events = make([]TrackEvent, len(events.Events))
				copy(history.Events, events.Events)
			}
			copiedSess.Tracks[track] = history
		}
	}
	sess.TracksMu.RUnlock()

	// Copy state.
	if sess.State != nil {
		for k, v := range sess.State {
			copiedValue := make([]byte, len(v))
			copy(copiedValue, v)
			copiedSess.State[k] = copiedValue
		}
	}

	// Copy summaries.
	sess.SummariesMu.RLock()
	if sess.Summaries != nil {
		copiedSess.Summaries = make(map[string]*Summary, len(sess.Summaries))
		for b, sum := range sess.Summaries {
			if sum == nil {
				continue
			}
			// Shallow copy is fine since Summary is immutable after write.
			copied := *sum
			copiedSess.Summaries[b] = &copied
		}
	}
	sess.SummariesMu.RUnlock()

	return copiedSess
}

// SessionOptions is the options for a session.
type SessionOptions func(*Session)

// WithSessionEvents is the option for the session events.
func WithSessionEvents(events []event.Event) SessionOptions {
	return func(sess *Session) {
		sess.Events = events
	}
}

// WithSessionSummaries is the option for the session summaries.
func WithSessionSummaries(summaries map[string]*Summary) SessionOptions {
	return func(sess *Session) {
		sess.Summaries = summaries
	}
}

// WithSessionState is the option for the session state.
func WithSessionState(state StateMap) SessionOptions {
	return func(sess *Session) {
		sess.State = state
	}
}

// WithSessionCreatedAt is the option for the session createdAt.
func WithSessionCreatedAt(createdAt time.Time) SessionOptions {
	return func(sess *Session) {
		sess.CreatedAt = createdAt
	}
}

// WithSessionUpdatedAt is the option for the session updatedAt.
func WithSessionUpdatedAt(updatedAt time.Time) SessionOptions {
	return func(sess *Session) {
		sess.UpdatedAt = updatedAt
	}
}

// NewSession creates a new session.
func NewSession(appName, userID, sessionID string, options ...SessionOptions) *Session {
	hashKey := fmt.Sprintf("%s:%s:%s", appName, userID, sessionID)
	hash := int(murmur3.Sum32([]byte(hashKey)))

	sess := &Session{
		ID:        sessionID,
		AppName:   appName,
		UserID:    userID,
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
		Summaries: make(map[string]*Summary),
		State:     make(StateMap),

		Hash: hash,
	}
	for _, o := range options {
		o(sess)
	}

	return sess
}

// GetEvents returns the session events.
func (sess *Session) GetEvents() []event.Event {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	eventsCopy := make([]event.Event, len(sess.Events))
	copy(eventsCopy, sess.Events)
	return eventsCopy
}

// GetEventCount returns the session event count.
func (sess *Session) GetEventCount() int {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	return len(sess.Events)
}

// AppendTrackEvent appends a track event to the session.
func (sess *Session) AppendTrackEvent(event *TrackEvent, opts ...Option) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if event == nil {
		return fmt.Errorf("track event is nil")
	}
	if sess.State == nil {
		sess.State = make(StateMap)
	}
	if err := ensureTrackExists(sess.State, event.Track); err != nil {
		return fmt.Errorf("ensure track indexed: %w", err)
	}
	sess.TracksMu.Lock()
	defer sess.TracksMu.Unlock()
	if sess.Tracks == nil {
		sess.Tracks = make(map[Track]*TrackEvents)
	}
	trackEvents, ok := sess.Tracks[event.Track]
	if !ok || trackEvents == nil {
		trackEvents = &TrackEvents{Track: event.Track}
		sess.Tracks[event.Track] = trackEvents
	}
	trackEvents.Events = append(trackEvents.Events, *event)
	sess.UpdatedAt = time.Now()
	return nil
}

// GetTrackEvents returns the track events snapshot.
func (sess *Session) GetTrackEvents(track Track) (*TrackEvents, error) {
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	if sess.Tracks == nil {
		return nil, fmt.Errorf("tracks is empty")
	}
	trackEvents, ok := sess.Tracks[track]
	if !ok || trackEvents == nil {
		return nil, fmt.Errorf("track events not found: %s", track)
	}
	copied := &TrackEvents{Track: trackEvents.Track}
	if len(trackEvents.Events) > 0 {
		copied.Events = make([]TrackEvent, len(trackEvents.Events))
		copy(copied.Events, trackEvents.Events)
	}
	return copied, nil
}

// EnsureEventStartWithUser filters events to ensure they start with RoleUser.
// It removes events from the beginning until it finds the first event from RoleUser.
func (sess *Session) EnsureEventStartWithUser() {
	if sess == nil || len(sess.Events) == 0 {
		log.Info("session is nil or has no events")
		return
	}
	// Find the first event that starts with RoleUser
	startIndex := -1
	for i, event := range sess.Events {
		if event.Response != nil && event.IsUserMessage() {
			startIndex = i
			break
		}
		// If event has no response or choices, continue to next event
	}

	// If no user event found, clear all events
	if startIndex == -1 {
		sess.Events = []event.Event{}
		return
	}

	// Keep events starting from the first user event
	if startIndex > 0 {
		sess.Events = sess.Events[startIndex:]
	}
}

// UpdateUserSession updates the user session with the given event and options.
func (sess *Session) UpdateUserSession(event *event.Event, opts ...Option) {
	if sess == nil || event == nil {
		log.Info("session or event is nil")
		return
	}
	if event.Response != nil && !event.IsPartial && event.IsValidContent() {
		sess.EventMu.Lock()
		sess.Events = append(sess.Events, *event)

		// Apply filtering options
		sess.ApplyEventFiltering(opts...)
		sess.EventMu.Unlock()
	}

	sess.UpdatedAt = time.Now()
	if sess.State == nil {
		sess.State = make(StateMap)
	}
	sess.ApplyEventStateDelta(event)
}

// ApplyEventFiltering applies event number and time filtering to session events
// It ensures that the filtered events still contain at least one user message.
func (sess *Session) ApplyEventFiltering(opts ...Option) {
	if sess == nil {
		log.Info("session is nil")
		return
	}
	originalEvents := sess.Events
	opt := applyOptions(opts...)

	// Apply event time filter - keep events after the specified time
	if !opt.EventTime.IsZero() {
		startIndex := -1
		for i, e := range sess.Events {
			if e.Timestamp.After(opt.EventTime) || e.Timestamp.Equal(opt.EventTime) {
				startIndex = i
				break
			}
		}
		if startIndex >= 0 {
			sess.Events = sess.Events[startIndex:]
		} else {
			// No events after the specified time, clear all events
			sess.Events = []event.Event{}
		}
	}

	// Apply event number limit
	if opt.EventNum > 0 && len(sess.Events) > opt.EventNum {
		sess.Events = sess.Events[len(sess.Events)-opt.EventNum:]
	}

	// check if has user message
	for i := 0; i < len(sess.Events); i++ {
		if sess.Events[i].IsUserMessage() {
			sess.Events = sess.Events[i:]
			return
		}
	}
	// find the last user message from original events
	for i := len(originalEvents) - 1; i >= 0; i-- {
		if originalEvents[i].IsUserMessage() {
			sess.Events = append([]event.Event{originalEvents[i]}, sess.Events...)
			return
		}
	}

	sess.Events = []event.Event{}
}

// ApplyEventStateDelta merges the state delta of the event into the session state.
func (sess *Session) ApplyEventStateDelta(e *event.Event) {
	if sess == nil || e == nil {
		log.Info("session or event is nil")
		return
	}
	if sess.State == nil {
		sess.State = make(StateMap)
	}
	for key, value := range e.StateDelta {
		sess.State[key] = value
	}
}

// ApplyEventStateDeltaMap merges the state delta of the event into the session state.
func ApplyEventStateDeltaMap(state StateMap, e *event.Event) {
	if state == nil || e == nil {
		log.Info("state or event is nil")
		return
	}

	for key, value := range e.StateDelta {
		state[key] = value
	}
}

func applyOptions(opts ...Option) *Options {
	opt := &Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

// Summary represents a concise, structured summary of a conversation branch.
// It is stored on the session object rather than in the StateMap.
type Summary struct {
	Summary   string    `json:"summary"`          // Summary is the concise conversation summary.
	Topics    []string  `json:"topics,omitempty"` // Topics is the optional topics list.
	UpdatedAt time.Time `json:"updated_at"`       // UpdatedAt is the update timestamp in UTC.
}

// Options is the options for getting a session.
type Options struct {
	EventNum  int       // EventNum is the number of recent events.
	EventTime time.Time // EventTime is the after time.
}

// Option is the option for a session.
type Option func(*Options)

// WithEventNum is the option for the number of recent events.
func WithEventNum(num int) Option {
	return func(o *Options) {
		o.EventNum = num
	}
}

// WithEventTime is the option for the time of the recent events.
func WithEventTime(time time.Time) Option {
	return func(o *Options) {
		o.EventTime = time
	}
}

// SummaryOption is the option for getting session summary.
type SummaryOption func(*SummaryOptions)

// SummaryOptions is the options for getting session summary.
type SummaryOptions struct {
	// FilterKey specifies which filter's summary to retrieve.
	// When empty (SummaryFilterKeyAllContents), retrieves the full-session summary.
	FilterKey string
}

// WithSummaryFilterKey sets the filter key for summary retrieval.
// When empty (SummaryFilterKeyAllContents), retrieves the full-session summary.
// Use this option to get summaries for specific event filters (e.g., "user-messages").
func WithSummaryFilterKey(filterKey string) SummaryOption {
	return func(o *SummaryOptions) {
		o.FilterKey = filterKey
	}
}

// Service is the interface that all session services must implement.
type Service interface {
	// CreateSession creates a new session.
	CreateSession(ctx context.Context, key Key, state StateMap, options ...Option) (*Session, error)

	// GetSession gets a session.
	GetSession(ctx context.Context, key Key, options ...Option) (*Session, error)

	// ListSessions lists all sessions by user scope of session key.
	ListSessions(ctx context.Context, userKey UserKey, options ...Option) ([]*Session, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context, key Key, options ...Option) error

	// UpdateAppState updates the state by target scope and key.
	UpdateAppState(ctx context.Context, appName string, state StateMap) error

	// DeleteAppState deletes the state by target scope and key.
	DeleteAppState(ctx context.Context, appName string, key string) error

	// GetState gets the state by target scope and key.
	ListAppStates(ctx context.Context, appName string) (StateMap, error)

	// UpdateUserState updates the state by target scope and key.
	UpdateUserState(ctx context.Context, userKey UserKey, state StateMap) error

	// GetUserState gets the state by target scope and key.
	ListUserStates(ctx context.Context, userKey UserKey) (StateMap, error)

	// DeleteUserState deletes the state by target scope and key.
	DeleteUserState(ctx context.Context, userKey UserKey, key string) error

	// UpdateSessionState updates the session-level state directly without appending an event.
	// This is useful for state initialization, correction, or synchronization scenarios
	// where event history is not needed.
	// Keys with app: or user: prefixes are not allowed (use UpdateAppState/UpdateUserState instead).
	// Keys with temp: prefix are allowed as they represent session-scoped ephemeral state.
	UpdateSessionState(ctx context.Context, key Key, state StateMap) error

	// AppendEvent appends an event to a session.
	AppendEvent(ctx context.Context, session *Session, event *event.Event, options ...Option) error

	// CreateSessionSummary triggers summarization for the session.
	// When filterKey is non-empty, implementations should limit work to the
	// matching branch using hierarchical rules consistent with event.Filter.
	// Implementations should preserve original events and store summaries on
	// the session object. The operation should be non-blocking for the main
	// flow where possible. Implementations may group deltas by branch internally.
	CreateSessionSummary(ctx context.Context, sess *Session, filterKey string, force bool) error

	// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
	// This method provides a non-blocking way to trigger summary generation.
	// When async processing is enabled, the job will be processed by background workers.
	// When async processing is disabled or unavailable, it falls back to synchronous processing.
	// The method validates session parameters before enqueueing and returns appropriate errors.
	EnqueueSummaryJob(ctx context.Context, sess *Session, filterKey string, force bool) error

	// GetSessionSummaryText returns the latest summary text for the session if any.
	// The boolean indicates whether a summary exists.
	// When no options are provided, returns the full-session summary (SummaryFilterKeyAllContents).
	// Use WithSummaryFilterKey to specify a different filter key.
	GetSessionSummaryText(ctx context.Context, sess *Session, opts ...SummaryOption) (string, bool)

	// Close closes the service.
	Close() error
}

// Key is the key for a session.
type Key struct {
	AppName   string // app name
	UserID    string // user id
	SessionID string // session id
}

// CheckSessionKey checks if a session key is valid.
func (s *Key) CheckSessionKey() error {
	return checkSessionKey(s.AppName, s.UserID, s.SessionID)
}

// CheckUserKey checks if a user key is valid.
func (s *Key) CheckUserKey() error {
	return checkUserKey(s.AppName, s.UserID)
}

// UserKey is the key for a user.
type UserKey struct {
	AppName string // app name
	UserID  string // user id
}

// CheckUserKey checks if a user key is valid.
func (s *UserKey) CheckUserKey() error {
	return checkUserKey(s.AppName, s.UserID)
}

func checkSessionKey(appName, userID, sessionID string) error {
	if appName == "" {
		return ErrAppNameRequired
	}
	if userID == "" {
		return ErrUserIDRequired
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	return nil
}

func checkUserKey(appName, userID string) error {
	if appName == "" {
		return ErrAppNameRequired
	}
	if userID == "" {
		return ErrUserIDRequired
	}
	return nil
}
