//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// summarizerManager implements the SummarizerManager interface.
type summarizerManager struct {
	mu            sync.RWMutex
	summarizer    SessionSummarizer
	cache         map[string]map[string]map[string]*SessionSummary // app -> user -> sessionID -> summary
	autoSummarize bool
}

// NewManager creates a new summarizer manager.
func NewManager(summarizer SessionSummarizer, opts ...ManagerOption) SummarizerManager {
	m := &summarizerManager{
		summarizer:    summarizer,
		autoSummarize: true, // Default to true
		cache:         make(map[string]map[string]map[string]*SessionSummary),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// SetSummarizer sets the summarizer to use.
func (m *summarizerManager) SetSummarizer(summarizer SessionSummarizer, force bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.summarizer == nil || force {
		m.summarizer = summarizer
	}
}

// Summarize creates a session summary without modifying events.
func (m *summarizerManager) Summarize(ctx context.Context, sess *session.Session, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.summarizer == nil {
		return errors.New("no summarizer configured")
	}

	// Check if summarization is needed.
	shouldSummarize := m.summarizer.ShouldSummarize(sess) || force
	if !shouldSummarize {
		return nil
	}

	// Generate summary without modifying events.
	summaryText, err := m.summarizer.Summarize(ctx, sess, 0) // Use default window size.
	if err != nil {
		return fmt.Errorf("failed to create summary: %w", err)
	}

	if summaryText != "" {
		// Cache the summary.
		appName := sess.AppName
		userID := sess.UserID
		if m.cache[appName] == nil {
			m.cache[appName] = make(map[string]map[string]*SessionSummary)
		}
		if m.cache[appName][userID] == nil {
			m.cache[appName][userID] = make(map[string]*SessionSummary)
		}

		m.cache[appName][userID][sess.ID] = &SessionSummary{
			ID:        sess.ID,
			Summary:   summaryText,
			CreatedAt: time.Now(),
			Metadata:  map[string]any{},
		}
	}

	return nil
}

// GetSummary retrieves a summary for a session.
func (m *summarizerManager) GetSummary(sess *session.Session) (*SessionSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.cache == nil {
		return nil, errors.New("no cache available")
	}

	appName := sess.AppName
	userID := sess.UserID

	if m.cache[appName] == nil || m.cache[appName][userID] == nil {
		return nil, fmt.Errorf("no summary found for session %s", sess.ID)
	}

	summary, exists := m.cache[appName][userID][sess.ID]
	if !exists {
		return nil, fmt.Errorf("no summary found for session %s", sess.ID)
	}

	return summary, nil
}

// Metadata returns metadata about the summarizer configuration.
func (m *summarizerManager) Metadata() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.summarizer == nil {
		return map[string]any{
			metadataKeySummarizerConfigured: false,
		}
	}

	metadata := m.summarizer.Metadata()
	metadata[metadataKeyAutoSummarize] = m.autoSummarize

	// Add cache statistics.
	totalSummaries := 0
	for _, appCache := range m.cache {
		for _, userCache := range appCache {
			totalSummaries += len(userCache)
		}
	}
	metadata[metadataKeyCachedSummaries] = totalSummaries

	return metadata
}

// ShouldSummarize checks if a session should be summarized.
func (m *summarizerManager) ShouldSummarize(sess *session.Session) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.summarizer == nil || !m.autoSummarize {
		return false
	}

	return m.summarizer.ShouldSummarize(sess)
}
