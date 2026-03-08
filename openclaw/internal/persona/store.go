//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	storeVersion = 1

	storeDirName  = "persona"
	storeFileName = "presets.json"
	storeTempExt  = ".tmp"

	storeDirPerm  = 0o700
	storeFilePerm = 0o600

	scopeKindDM     = "dm"
	scopeKindThread = "thread"
	scopeSep        = ":"

	PresetDefault    = "default"
	PresetGirlfriend = "girlfriend"
	PresetConcise    = "concise"
	PresetCoach      = "coach"
	PresetCreative   = "creative"
)

var ErrUnknownPreset = errors.New("persona: unknown preset")

type Preset struct {
	ID          string
	Name        string
	Description string
	Prompt      string
}

var presetList = []Preset{
	{
		ID:          PresetDefault,
		Name:        "Default",
		Description: "Use the normal assistant behavior.",
	},
	{
		ID:          PresetGirlfriend,
		Name:        "Girlfriend",
		Description: "Warm, playful, and affectionate companion tone.",
		Prompt: "Adopt a warm, playful, and affectionate companion " +
			"tone. Prefer natural, low-pressure language. Light " +
			"flirting is fine when the user's tone welcomes it. " +
			"Stay respectful, emotionally grounded, and honest. " +
			"Do not claim real-world exclusivity, dependency, or " +
			"obligations.",
	},
	{
		ID:          PresetConcise,
		Name:        "Concise",
		Description: "Direct, brief, and action-first replies.",
		Prompt: "Be direct, brief, and low-friction. Lead with the " +
			"answer or concrete action. Keep wording tight unless " +
			"the user explicitly asks for depth.",
	},
	{
		ID:          PresetCoach,
		Name:        "Coach",
		Description: "Structured, pragmatic, and goal-oriented.",
		Prompt: "Act like a pragmatic coach. Give clear structure, " +
			"challenge vague thinking, and convert goals into " +
			"concrete next steps. Stay supportive but do not " +
			"sugarcoat tradeoffs.",
	},
	{
		ID:          PresetCreative,
		Name:        "Creative",
		Description: "More imaginative, vivid, and idea-rich.",
		Prompt: "Lean imaginative, vivid, and idea-rich. Offer " +
			"varied angles, names, examples, and alternatives " +
			"when it helps the task.",
	},
}

var presetAliases = map[string]string{
	"":      PresetDefault,
	"none":  PresetDefault,
	"off":   PresetDefault,
	"reset": PresetDefault,
	"gf":    PresetGirlfriend,
}

type Store struct {
	path string

	mu    sync.Mutex
	state storeState
}

type storeState struct {
	Version int               `json:"version"`
	Scopes  map[string]string `json:"scopes,omitempty"`
}

func DefaultStorePath(stateDir string) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", errors.New("persona: empty state dir")
	}
	return filepath.Join(
		stateDir,
		storeDirName,
		storeFileName,
	), nil
}

func NewStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("persona: empty store path")
	}

	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func List() []Preset {
	out := make([]Preset, len(presetList))
	copy(out, presetList)
	return out
}

func Lookup(id string) (Preset, bool) {
	key := normalizePresetID(id)
	if alias, ok := presetAliases[key]; ok {
		key = alias
	}
	for _, preset := range presetList {
		if preset.ID == key {
			return preset, true
		}
	}
	return Preset{}, false
}

func DefaultPreset() Preset {
	preset, _ := Lookup(PresetDefault)
	return preset
}

func DMScopeKey(channel string, userID string) string {
	return buildScopeKey(channel, scopeKindDM, userID)
}

func ThreadScopeKey(channel string, thread string) string {
	return buildScopeKey(channel, scopeKindThread, thread)
}

func ScopeKeyFromSession(
	channel string,
	userID string,
	sessionID string,
) string {
	channel = normalizeChannel(channel)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return DMScopeKey(channel, userID)
	}

	parts := strings.Split(sessionID, scopeSep)
	if len(parts) < 3 {
		if strings.TrimSpace(userID) == "" {
			return sessionID
		}
		return DMScopeKey(channel, userID)
	}

	sessionChannel := normalizeChannel(parts[0])
	switch strings.TrimSpace(parts[1]) {
	case scopeKindThread:
		return sessionID
	case scopeKindDM:
		if strings.TrimSpace(userID) != "" {
			return DMScopeKey(sessionChannel, userID)
		}
		return strings.Join(parts[:3], scopeSep)
	default:
		if strings.TrimSpace(userID) == "" {
			return sessionID
		}
		return DMScopeKey(sessionChannel, userID)
	}
}

func (s *Store) Get(scopeKey string) (Preset, error) {
	scopeKey = normalizeScopeKey(scopeKey)
	if scopeKey == "" {
		return Preset{}, errors.New("persona: empty scope key")
	}

	if s == nil {
		return DefaultPreset(), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := normalizePresetID(s.state.Scopes[scopeKey])
	if id == "" {
		return DefaultPreset(), nil
	}
	preset, ok := Lookup(id)
	if !ok {
		return DefaultPreset(), nil
	}
	return preset, nil
}

func (s *Store) Set(
	ctx context.Context,
	scopeKey string,
	presetID string,
) (Preset, error) {
	if err := contextErr(ctx); err != nil {
		return Preset{}, err
	}

	scopeKey = normalizeScopeKey(scopeKey)
	if scopeKey == "" {
		return Preset{}, errors.New("persona: empty scope key")
	}

	preset, ok := Lookup(presetID)
	if !ok {
		return Preset{}, fmt.Errorf(
			"%w: %s",
			ErrUnknownPreset,
			strings.TrimSpace(presetID),
		)
	}
	if s == nil {
		return preset, errors.New("persona: nil store")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state.Scopes == nil {
		s.state.Scopes = make(map[string]string)
	}

	changed := false
	if preset.ID == PresetDefault {
		if _, ok := s.state.Scopes[scopeKey]; ok {
			delete(s.state.Scopes, scopeKey)
			changed = true
		}
	} else if s.state.Scopes[scopeKey] != preset.ID {
		s.state.Scopes[scopeKey] = preset.ID
		changed = true
	}

	if !changed {
		return preset, nil
	}
	if err := s.persistLocked(ctx); err != nil {
		return Preset{}, err
	}
	return preset, nil
}

func (s *Store) ForgetUser(
	ctx context.Context,
	channel string,
	userID string,
) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return nil
	}

	scopeKey := DMScopeKey(channel, userID)
	if scopeKey == "" {
		return errors.New("persona: empty scope key")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.state.Scopes[scopeKey]; !ok {
		return nil
	}
	delete(s.state.Scopes, scopeKey)
	return s.persistLocked(ctx)
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.state = storeState{
				Version: storeVersion,
				Scopes:  make(map[string]string),
			}
			return nil
		}
		return fmt.Errorf("persona: read store: %w", err)
	}

	var state storeState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("persona: decode store: %w", err)
	}
	if state.Version != storeVersion {
		return fmt.Errorf(
			"persona: unexpected store version: %d",
			state.Version,
		)
	}
	if state.Scopes == nil {
		state.Scopes = make(map[string]string)
	}
	s.state = state
	return nil
}

func (s *Store) persistLocked(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, storeDirPerm); err != nil {
		return fmt.Errorf("persona: create store dir: %w", err)
	}

	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("persona: encode store: %w", err)
	}

	tmpPath := s.path + storeTempExt
	if err := os.WriteFile(tmpPath, raw, storeFilePerm); err != nil {
		return fmt.Errorf("persona: write temp store: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("persona: replace store: %w", err)
	}
	return nil
}

func buildScopeKey(
	channel string,
	kind string,
	id string,
) string {
	channel = normalizeChannel(channel)
	id = strings.TrimSpace(id)
	if channel == "" || strings.TrimSpace(kind) == "" || id == "" {
		return ""
	}
	return strings.Join([]string{channel, kind, id}, scopeSep)
}

func normalizeChannel(channel string) string {
	return strings.TrimSpace(channel)
}

func normalizeScopeKey(scopeKey string) string {
	return strings.TrimSpace(scopeKey)
}

func normalizePresetID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
