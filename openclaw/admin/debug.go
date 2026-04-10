//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

type debugStatus struct {
	Enabled      bool               `json:"enabled"`
	BySessionDir string             `json:"by_session_dir,omitempty"`
	SessionCount int                `json:"session_count"`
	TraceCount   int                `json:"trace_count"`
	Error        string             `json:"error,omitempty"`
	Sessions     []debugSessionView `json:"sessions,omitempty"`
	RecentTraces []debugTraceView   `json:"recent_traces,omitempty"`
}

type debugSessionView struct {
	SessionID   string    `json:"session_id,omitempty"`
	TraceCount  int       `json:"trace_count"`
	LastTraceAt time.Time `json:"last_trace_at,omitempty"`
	Channel     string    `json:"channel,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	TracePath   string    `json:"trace_path,omitempty"`
	LangfuseURL string    `json:"langfuse_url,omitempty"`
	MetaURL     string    `json:"meta_url,omitempty"`
	EventsURL   string    `json:"events_url,omitempty"`
	ResultURL   string    `json:"result_url,omitempty"`
}

type debugTraceView struct {
	SessionID   string    `json:"session_id,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	Channel     string    `json:"channel,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	TracePath   string    `json:"trace_path,omitempty"`
	LangfuseURL string    `json:"langfuse_url,omitempty"`
	MetaURL     string    `json:"meta_url,omitempty"`
	EventsURL   string    `json:"events_url,omitempty"`
	ResultURL   string    `json:"result_url,omitempty"`
}

type debugTraceRef struct {
	TraceDir  string    `json:"trace_dir"`
	StartedAt time.Time `json:"started_at"`
	Channel   string    `json:"channel,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

func (s *Service) debugStatus() debugStatus {
	return s.buildDebugStatus("")
}

func (s *Service) debugStatusForSession(sessionID string) debugStatus {
	return s.buildDebugStatus(sessionID)
}

func (s *Service) buildDebugStatus(sessionFilter string) debugStatus {
	root := strings.TrimSpace(s.cfg.DebugDir)
	status := debugStatus{}
	if root == "" {
		return status
	}
	status.BySessionDir = filepath.Join(root, debugBySessionDir)
	if _, err := os.Stat(root); err == nil {
		status.Enabled = true
	}
	if _, err := os.Stat(status.BySessionDir); err != nil {
		if os.IsNotExist(err) {
			return status
		}
		status.Error = err.Error()
		return status
	}

	traces, err := s.loadDebugTraces(root, sessionFilter)
	if err != nil {
		status.Error = err.Error()
	}
	status.Enabled = true
	status.TraceCount = len(traces)
	status.RecentTraces = limitDebugTraces(traces, maxDebugTraceRows)

	sessions := make(map[string]*debugSessionView)
	for _, trace := range traces {
		entry := sessions[trace.SessionID]
		if entry == nil {
			entry = &debugSessionView{
				SessionID: trace.SessionID,
			}
			sessions[trace.SessionID] = entry
		}
		entry.TraceCount++
		if trace.StartedAt.After(entry.LastTraceAt) {
			entry.LastTraceAt = trace.StartedAt
			entry.Channel = trace.Channel
			entry.RequestID = trace.RequestID
			entry.TraceID = trace.TraceID
			entry.TracePath = trace.TracePath
			entry.LangfuseURL = trace.LangfuseURL
			entry.MetaURL = trace.MetaURL
			entry.EventsURL = trace.EventsURL
			entry.ResultURL = trace.ResultURL
		}
	}

	status.SessionCount = len(sessions)
	if len(sessions) == 0 {
		return status
	}

	items := make([]debugSessionView, 0, len(sessions))
	for _, item := range sessions {
		if item == nil {
			continue
		}
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastTraceAt.Equal(items[j].LastTraceAt) {
			return items[i].SessionID < items[j].SessionID
		}
		return items[i].LastTraceAt.After(items[j].LastTraceAt)
	})
	if len(items) > maxDebugSessionRows {
		items = items[:maxDebugSessionRows]
	}
	status.Sessions = items
	return status
}

func (s *Service) loadDebugTraces(
	root string,
	sessionFilter string,
) ([]debugTraceView, error) {
	bySessionRoot := filepath.Join(root, debugBySessionDir)
	items := make([]debugTraceView, 0)
	var firstErr error
	walkErr := filepath.WalkDir(
		bySessionRoot,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d == nil || d.IsDir() ||
				d.Name() != debugMetaTraceRefName {
				return nil
			}
			trace, ok, err := s.readDebugTrace(
				root,
				bySessionRoot,
				path,
				sessionFilter,
			)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return nil
			}
			if !ok {
				return nil
			}
			items = append(items, trace)
			return nil
		},
	)
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			if items[i].SessionID == items[j].SessionID {
				return items[i].TracePath < items[j].TracePath
			}
			return items[i].SessionID < items[j].SessionID
		}
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	return items, firstErr
}

const debugMetaTraceRefName = "trace.json"

func (s *Service) readDebugTrace(
	root string,
	bySessionRoot string,
	refPath string,
	sessionFilter string,
) (debugTraceView, bool, error) {
	rel, err := filepath.Rel(bySessionRoot, refPath)
	if err != nil {
		return debugTraceView{}, false, err
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) == 0 {
		return debugTraceView{}, false, nil
	}
	sessionID := strings.TrimSpace(parts[0])
	if sessionFilter != "" && sessionID != sessionFilter {
		return debugTraceView{}, false, nil
	}

	data, err := os.ReadFile(refPath)
	if err != nil {
		return debugTraceView{}, false, err
	}
	var ref debugTraceRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return debugTraceView{}, false, err
	}

	indexDir := filepath.Dir(refPath)
	traceAbs := filepath.Clean(filepath.Join(indexDir, ref.TraceDir))
	traceRel, err := filepath.Rel(root, traceAbs)
	if err != nil {
		return debugTraceView{}, false, err
	}
	traceRel = filepath.ToSlash(traceRel)
	if strings.HasPrefix(traceRel, "../") || traceRel == ".." {
		return debugTraceView{}, false, nil
	}

	out := debugTraceView{
		SessionID: sessionID,
		StartedAt: ref.StartedAt,
		Channel:   strings.TrimSpace(ref.Channel),
		RequestID: strings.TrimSpace(ref.RequestID),
		MessageID: strings.TrimSpace(ref.MessageID),
		TraceID:   strings.TrimSpace(ref.TraceID),
		TracePath: traceRel,
	}
	out.LangfuseURL = s.langfuseTraceURL(out.TraceID)
	if fileExists(filepath.Join(traceAbs, debugMetaFileName)) {
		out.MetaURL = s.debugFileURL(traceRel, debugMetaFileName)
	}
	if _, _, err := debugrecorder.ResolveEventsFilePath(traceAbs); err == nil {
		out.EventsURL = s.debugFileURL(traceRel, debugEventsFileName)
	}
	if fileExists(filepath.Join(traceAbs, debugResultFileName)) {
		out.ResultURL = s.debugFileURL(traceRel, debugResultFileName)
	}
	return out, true, nil
}

func (s *Service) debugFileURL(tracePath string, name string) string {
	if !isAllowedDebugFile(name) || strings.TrimSpace(tracePath) == "" {
		return ""
	}
	values := url.Values{}
	values.Set(queryTrace, tracePath)
	values.Set(queryName, name)
	return routeDebugFile + "?" + values.Encode()
}

func limitDebugTraces(
	items []debugTraceView,
	limit int,
) []debugTraceView {
	if len(items) == 0 {
		return nil
	}
	if limit <= 0 || len(items) <= limit {
		out := make([]debugTraceView, len(items))
		copy(out, items)
		return out
	}
	out := make([]debugTraceView, limit)
	copy(out, items[:limit])
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info != nil && !info.IsDir()
}
