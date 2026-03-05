//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	dmSessionStoreVersion = 1

	dmSessionStoreFilePrefix = "dm-session-"
	dmSessionStoreFileSuffix = ".json"

	dmSessionStoreDirPerm  = 0o700
	dmSessionStoreFilePerm = 0o600

	dmSessionStoreDayLayout = "20060102"

	dmSessionSaltBytes = 8

	dmSessionTempSuffixBytes = 8
)

type dmSessionResetPolicy struct {
	Idle  time.Duration
	Daily bool
}

type dmSessionStore struct {
	path string
	now  func() time.Time

	mu    sync.Mutex
	state dmSessionStoreState
}

type dmSessionStoreState struct {
	Version int `json:"version"`

	DMs map[string]*dmSessionEntry `json:"dms,omitempty"`
}

type dmSessionEntry struct {
	ActiveSessionID string `json:"active_session_id,omitempty"`

	LastActivityUnix int64 `json:"last_activity_unix,omitempty"`
	LastResetUnix    int64 `json:"last_reset_unix,omitempty"`

	LastDailyResetDay string `json:"last_daily_reset_day,omitempty"`
}

func newDMSessionStore(path string) (*dmSessionStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("telegram: empty dm session store path")
	}

	s := &dmSessionStore{
		path: path,
		now:  time.Now,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func dmSessionStorePath(stateDir string, bot BotInfo) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", errors.New("telegram: empty state dir")
	}
	filename := dmSessionStoreFilePrefix +
		offsetKey(bot) +
		dmSessionStoreFileSuffix
	return filepath.Join(stateDir, offsetStoreDir, filename), nil
}

func (s *dmSessionStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.state = dmSessionStoreState{
				Version: dmSessionStoreVersion,
				DMs:     make(map[string]*dmSessionEntry),
			}
			return nil
		}
		return fmt.Errorf("telegram: read dm session store: %w", err)
	}

	var state dmSessionStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("telegram: decode dm session store: %w", err)
	}
	if state.Version != dmSessionStoreVersion {
		return fmt.Errorf(
			"telegram: unexpected dm session store version: %d",
			state.Version,
		)
	}
	if state.DMs == nil {
		state.DMs = make(map[string]*dmSessionEntry)
	}
	s.state = state
	return nil
}

func (s *dmSessionStore) EnsureActiveSession(
	ctx context.Context,
	userID string,
	legacySessionID string,
	policy dmSessionResetPolicy,
) (sessionID string, rotated bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", false, errors.New("telegram: empty user id")
	}
	legacySessionID = strings.TrimSpace(legacySessionID)
	if legacySessionID == "" {
		return "", false, errors.New("telegram: empty legacy session id")
	}

	now := s.now()
	nowUnix := now.Unix()
	today := ""
	if policy.Daily {
		today = now.Format(dmSessionStoreDayLayout)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.state.DMs[userID]
	changed := false
	if entry == nil && (policy.Daily || policy.Idle > 0) {
		entry = &dmSessionEntry{}
		s.state.DMs[userID] = entry
		changed = true
	}

	shouldRotate := false
	if policy.Daily {
		if entry != nil && strings.TrimSpace(entry.LastDailyResetDay) == "" {
			entry.LastDailyResetDay = today
			changed = true
		} else if entry != nil && entry.LastDailyResetDay != today {
			shouldRotate = true
			entry.LastDailyResetDay = today
			changed = true
		}
	}

	if policy.Idle > 0 {
		if entry != nil && entry.LastActivityUnix > 0 {
			last := time.Unix(entry.LastActivityUnix, 0)
			if now.Sub(last) >= policy.Idle {
				shouldRotate = true
			}
		}
		if entry != nil && entry.LastActivityUnix != nowUnix {
			entry.LastActivityUnix = nowUnix
			changed = true
		}
	}

	active := legacySessionID
	if entry != nil && strings.TrimSpace(entry.ActiveSessionID) != "" {
		active = entry.ActiveSessionID
	}

	if shouldRotate {
		active = legacySessionID + ":" + randomHex(dmSessionSaltBytes)
		rotated = true
		if entry == nil {
			entry = &dmSessionEntry{}
			s.state.DMs[userID] = entry
			changed = true
		}
		entry.ActiveSessionID = active
		entry.LastResetUnix = nowUnix
		changed = true
	}

	if changed {
		if err := s.persistLocked(ctx); err != nil {
			return "", false, err
		}
	}

	return active, rotated, nil
}

func (s *dmSessionStore) Rotate(
	ctx context.Context,
	userID string,
	legacySessionID string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", errors.New("telegram: empty user id")
	}
	legacySessionID = strings.TrimSpace(legacySessionID)
	if legacySessionID == "" {
		return "", errors.New("telegram: empty legacy session id")
	}

	now := s.now()
	nowUnix := now.Unix()

	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.state.DMs[userID]
	if entry == nil {
		entry = &dmSessionEntry{}
		s.state.DMs[userID] = entry
	}

	sessionID := legacySessionID + ":" + randomHex(dmSessionSaltBytes)
	entry.ActiveSessionID = sessionID
	entry.LastResetUnix = nowUnix
	entry.LastActivityUnix = nowUnix

	if err := s.persistLocked(ctx); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *dmSessionStore) ForgetUser(
	ctx context.Context,
	userID string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, errors.New("telegram: empty user id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.state.DMs) == 0 {
		return false, nil
	}
	if _, ok := s.state.DMs[userID]; !ok {
		return false, nil
	}
	delete(s.state.DMs, userID)

	if err := s.persistLocked(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *dmSessionStore) persistLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, dmSessionStoreDirPerm); err != nil {
		return fmt.Errorf(
			"telegram: create dm session store dir: %w",
			err,
		)
	}

	payload := s.state
	payload.Version = dmSessionStoreVersion

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("telegram: encode dm session store: %w", err)
	}
	data = append(data, '\n')

	tmp := fmt.Sprintf(
		"%s.%s.tmp",
		s.path,
		randomHex(dmSessionTempSuffixBytes),
	)
	if err := os.WriteFile(tmp, data, dmSessionStoreFilePerm); err != nil {
		return fmt.Errorf("telegram: write dm session store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("telegram: rename dm session store: %w", err)
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "rand"
	}
	return hex.EncodeToString(b)
}
