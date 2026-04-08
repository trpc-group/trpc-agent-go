//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skills

import (
	iofs "io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	watchRefreshReasonManual = "manual"
	watchRefreshReasonWatch  = "watch"
)

var defaultWatchIgnoredNames = map[string]struct{}{
	".cache":        {},
	".git":          {},
	".mypy_cache":   {},
	".pytest_cache": {},
	".venv":         {},
	"__pycache__":   {},
	"build":         {},
	"dist":          {},
	"node_modules":  {},
	"venv":          {},
}

type WatchConfig struct {
	Enabled      bool
	Debounce     time.Duration
	WatchBundled bool
	BundledRoot  string
}

type WatchStatus struct {
	Enabled           bool       `json:"enabled"`
	WatchBundled      bool       `json:"watch_bundled"`
	DebounceMS        int        `json:"debounce_ms,omitempty"`
	Roots             []string   `json:"roots,omitempty"`
	Generation        int64      `json:"generation,omitempty"`
	LastRefreshAt     *time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshReason string     `json:"last_refresh_reason,omitempty"`
	LastChangedPath   string     `json:"last_changed_path,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
}

type WatchService struct {
	repo *Repository
	cfg  WatchConfig

	stateMu sync.RWMutex
	watchMu sync.RWMutex

	watcher *fsnotify.Watcher
	roots   []string
	watched map[string]struct{}

	done chan struct{}
	wg   sync.WaitGroup

	refreshMu sync.Mutex

	generation        int64
	lastRefreshAt     time.Time
	lastRefreshReason string
	lastChangedPath   string
	lastError         string
}

func NewWatchService(
	repo *Repository,
	roots []string,
	cfg WatchConfig,
) *WatchService {
	if repo == nil {
		return nil
	}

	svc := &WatchService{
		repo:    repo,
		cfg:     cfg,
		roots:   normalizeWatchRoots(roots, cfg),
		watched: map[string]struct{}{},
		done:    make(chan struct{}),
	}
	if !cfg.Enabled || len(svc.roots) == 0 {
		return svc
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		svc.recordError(err)
		log.Warnf("skills watcher init failed: %v", err)
		return svc
	}
	svc.watcher = watcher
	if err := svc.syncWatches(); err != nil {
		svc.recordError(err)
		log.Warnf("skills watcher sync failed: %v", err)
	}

	svc.wg.Add(1)
	go svc.loop()
	return svc
}

func (s *WatchService) Close() error {
	if s == nil {
		return nil
	}
	close(s.done)
	s.wg.Wait()

	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watcher == nil {
		return nil
	}
	err := s.watcher.Close()
	s.watcher = nil
	s.watched = map[string]struct{}{}
	return err
}

func (s *WatchService) Refresh() error {
	return s.refresh(watchRefreshReasonManual, "")
}

func (s *WatchService) Status() *WatchStatus {
	if s == nil {
		return nil
	}

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	status := &WatchStatus{
		Enabled:      s.cfg.Enabled,
		WatchBundled: s.cfg.WatchBundled,
		DebounceMS:   int(s.cfg.Debounce / time.Millisecond),
		Roots:        append([]string(nil), s.roots...),
		Generation:   s.generation,
		LastError:    strings.TrimSpace(s.lastError),
	}
	if !s.lastRefreshAt.IsZero() {
		ts := s.lastRefreshAt
		status.LastRefreshAt = &ts
	}
	status.LastRefreshReason = strings.TrimSpace(
		s.lastRefreshReason,
	)
	status.LastChangedPath = strings.TrimSpace(s.lastChangedPath)
	return status
}

func (s *WatchService) loop() {
	defer s.wg.Done()
	if s == nil || s.watcher == nil {
		return
	}

	var (
		timer       *time.Timer
		timerC      <-chan time.Time
		pendingPath string
	)
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerC = nil
	}
	resetTimer := func() {
		delay := s.cfg.Debounce
		if timer == nil {
			timer = time.NewTimer(delay)
			timerC = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}

	for {
		select {
		case <-s.done:
			stopTimer()
			return
		case event, ok := <-s.watcher.Events:
			if !ok {
				stopTimer()
				return
			}
			path, ok := s.relevantEvent(event)
			if !ok {
				continue
			}
			pendingPath = path
			resetTimer()
		case err, ok := <-s.watcher.Errors:
			if !ok || err == nil {
				continue
			}
			s.recordError(err)
			log.Warnf("skills watcher error: %v", err)
		case <-timerC:
			if err := s.refresh(
				watchRefreshReasonWatch,
				pendingPath,
			); err != nil {
				log.Warnf("skills watcher refresh failed: %v", err)
			}
			pendingPath = ""
			timer = nil
			timerC = nil
		}
	}
}

func (s *WatchService) relevantEvent(
	event fsnotify.Event,
) (string, bool) {
	path := filepath.Clean(strings.TrimSpace(event.Name))
	if path == "." || path == "" {
		return "", false
	}
	if isIgnoredWatchPath(path) {
		return "", false
	}
	if filepath.Base(path) == skillFileName {
		return path, true
	}

	for _, root := range s.roots {
		if path == root {
			return path, true
		}
		if event.Op&fsnotify.Create != 0 &&
			filepath.Dir(path) == root &&
			watchDirExists(path) {
			return path, true
		}
	}

	s.watchMu.RLock()
	defer s.watchMu.RUnlock()
	current := path
	for current != "." && current != "" {
		if _, ok := s.watched[current]; ok {
			return path, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", false
}

func (s *WatchService) refresh(
	reason string,
	changedPath string,
) error {
	if s == nil || s.repo == nil {
		return nil
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	err := s.repo.Refresh()
	if err == nil {
		if syncErr := s.syncWatches(); syncErr != nil {
			err = syncErr
		}
	}

	s.stateMu.Lock()
	s.lastRefreshAt = time.Now()
	s.lastRefreshReason = strings.TrimSpace(reason)
	s.lastChangedPath = strings.TrimSpace(changedPath)
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
		s.generation++
	}
	s.stateMu.Unlock()
	return err
}

func (s *WatchService) syncWatches() error {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()

	if s == nil || s.watcher == nil {
		return nil
	}

	want := s.desiredWatchDirs()
	for path := range s.watched {
		if _, ok := want[path]; ok {
			continue
		}
		if err := s.watcher.Remove(path); err != nil &&
			!isIgnorableWatchError(err) {
			return err
		}
		delete(s.watched, path)
	}
	for path := range want {
		if _, ok := s.watched[path]; ok {
			continue
		}
		if err := s.watcher.Add(path); err != nil {
			return err
		}
		s.watched[path] = struct{}{}
	}
	return nil
}

func (s *WatchService) desiredWatchDirs() map[string]struct{} {
	dirs := map[string]struct{}{}
	for _, root := range s.roots {
		if root == "" {
			continue
		}
		if watchDirExists(root) {
			for path := range collectWatchDirs(root) {
				dirs[path] = struct{}{}
			}
			continue
		}
		parent := nearestExistingWatchParent(root)
		if parent != "" {
			dirs[parent] = struct{}{}
		}
	}
	return dirs
}

func collectWatchDirs(root string) map[string]struct{} {
	dirs := map[string]struct{}{}
	_ = filepath.WalkDir(
		root,
		func(path string, d iofs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if path != root && isIgnoredWatchName(d.Name()) {
				return filepath.SkipDir
			}
			dirs[path] = struct{}{}
			return nil
		},
	)
	return dirs
}

func (s *WatchService) recordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.stateMu.Lock()
	s.lastError = err.Error()
	s.stateMu.Unlock()
}

func normalizeWatchRoots(
	roots []string,
	cfg WatchConfig,
) []string {
	bundled := normalizeWatchPath(cfg.BundledRoot)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		normalized := normalizeWatchPath(root)
		if normalized == "" {
			continue
		}
		if !cfg.WatchBundled && bundled != "" &&
			normalized == bundled {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func normalizeWatchPath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	if strings.Contains(path, "://") {
		u, err := url.Parse(path)
		if err != nil {
			return ""
		}
		switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
		case "file":
			if u.Host != "" && u.Host != "localhost" {
				return ""
			}
			path = filepath.FromSlash(u.Path)
		default:
			return ""
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil &&
		strings.TrimSpace(resolved) != "" {
		path = resolved
	}
	return filepath.Clean(path)
}

func nearestExistingWatchParent(path string) string {
	current := filepath.Clean(strings.TrimSpace(path))
	for current != "." && current != "" {
		parent := filepath.Dir(current)
		if parent == current {
			if watchDirExists(parent) {
				return parent
			}
			return ""
		}
		if watchDirExists(parent) {
			return parent
		}
		current = parent
	}
	return ""
}

func isIgnoredWatchPath(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(os.PathSeparator)) {
		if isIgnoredWatchName(part) {
			return true
		}
	}
	return false
}

func isIgnoredWatchName(name string) bool {
	_, ok := defaultWatchIgnoredNames[strings.TrimSpace(name)]
	return ok
}

func isIgnorableWatchError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "can't remove non-existent") ||
		strings.Contains(text, "file already closed")
}

func watchDirExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.IsDir()
}
