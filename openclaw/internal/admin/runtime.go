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
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	defaultUploadsDir = "uploads"
	maxUploadRows     = 12
	maxUploadSessions = 8
	maxExecRows       = 12
)

type uploadFilters struct {
	Channel   string
	UserID    string
	SessionID string
	Kind      string
	MimeType  string
	Source    string
}

type execStatus struct {
	Enabled      bool              `json:"enabled"`
	SessionCount int               `json:"session_count"`
	RunningCount int               `json:"running_count"`
	Sessions     []execSessionView `json:"sessions,omitempty"`
}

type execSessionView struct {
	SessionID string `json:"session_id,omitempty"`
	Command   string `json:"command,omitempty"`
	Status    string `json:"status,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	DoneAt    string `json:"done_at,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
}

type uploadsStatus struct {
	Enabled      bool                `json:"enabled"`
	Root         string              `json:"root,omitempty"`
	FileCount    int                 `json:"file_count"`
	TotalBytes   int64               `json:"total_bytes"`
	Error        string              `json:"error,omitempty"`
	KindCounts   []uploadKindCount   `json:"kind_counts,omitempty"`
	SourceCounts []uploadSourceCount `json:"source_counts,omitempty"`
	Files        []uploadView        `json:"files,omitempty"`
	Sessions     []uploadSessionView `json:"sessions,omitempty"`
}

type uploadKindCount struct {
	Kind  string `json:"kind,omitempty"`
	Count int    `json:"count"`
}

type uploadSourceCount struct {
	Source string `json:"source,omitempty"`
	Count  int    `json:"count"`
}

type uploadView struct {
	Channel      string    `json:"channel,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Name         string    `json:"name,omitempty"`
	RelativePath string    `json:"relative_path,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	MimeType     string    `json:"mime_type,omitempty"`
	Source       string    `json:"source,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at,omitempty"`
	OpenURL      string    `json:"open_url,omitempty"`
	DownloadURL  string    `json:"download_url,omitempty"`
}

type uploadSessionView struct {
	Channel      string    `json:"channel,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	FileCount    int       `json:"file_count"`
	TotalBytes   int64     `json:"total_bytes"`
	LastModified time.Time `json:"last_modified,omitempty"`
}

func (s *Service) execStatus() execStatus {
	if s == nil || s.cfg.Exec == nil {
		return execStatus{}
	}

	sessions := s.cfg.Exec.ListSessions()
	status := execStatus{
		Enabled:      true,
		SessionCount: len(sessions),
		Sessions:     make([]execSessionView, 0, len(sessions)),
	}
	for _, session := range sessions {
		if session.Status == "running" {
			status.RunningCount++
		}
		status.Sessions = append(
			status.Sessions,
			execSessionViewFromSession(session),
		)
	}
	if len(status.Sessions) > maxExecRows {
		status.Sessions = status.Sessions[:maxExecRows]
	}
	return status
}

func execSessionViewFromSession(
	session octool.ProcessSession,
) execSessionView {
	return execSessionView{
		SessionID: strings.TrimSpace(session.SessionID),
		Command:   strings.TrimSpace(session.Command),
		Status:    strings.TrimSpace(session.Status),
		StartedAt: strings.TrimSpace(session.StartedAt),
		DoneAt:    strings.TrimSpace(session.DoneAt),
		ExitCode:  session.ExitCode,
	}
}

func (s *Service) uploadsStatus() uploadsStatus {
	return s.uploadsStatusFiltered(
		uploadFilters{},
		maxUploadRows,
		maxUploadSessions,
	)
}

func (s *Service) uploadsStatusFiltered(
	filters uploadFilters,
	fileLimit int,
	sessionLimit int,
) uploadsStatus {
	root := resolveUploadsRoot(s.cfg.StateDir)
	status := uploadsStatus{Root: root}
	if root == "" {
		return status
	}
	if _, err := os.Stat(root); err != nil {
		if errorsIsNotExist(err) {
			return status
		}
		status.Error = err.Error()
		return status
	}

	status.Enabled = true
	store, err := uploads.NewStore(s.cfg.StateDir)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	listed, err := store.ListAll(0)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	listed = filterUploadList(listed, filters)
	status.FileCount = len(listed)
	status.Files, status.TotalBytes = uploadViewsFromList(
		listed,
		fileLimit,
	)
	status.Sessions = uploadSessionsFromList(listed, sessionLimit)
	status.KindCounts = uploadKindCountsFromList(listed)
	status.SourceCounts = uploadSourceCountsFromList(listed)
	return status
}

func resolveUploadsRoot(stateDir string) string {
	root := strings.TrimSpace(stateDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, defaultUploadsDir)
}

func uploadViewsFromList(
	listed []uploads.ListedFile,
	limit int,
) ([]uploadView, int64) {
	files := make([]uploadView, 0, len(listed))
	var totalBytes int64
	for _, file := range listed {
		totalBytes += file.SizeBytes
		name := uploads.PreferredName(file.Name, file.MimeType)
		if name == "" {
			name = file.Name
		}
		files = append(files, uploadView{
			Channel:      file.Scope.Channel,
			UserID:       file.Scope.UserID,
			SessionID:    file.Scope.SessionID,
			Name:         name,
			RelativePath: file.RelativePath,
			Kind:         uploadKindFromFile(file),
			MimeType:     file.MimeType,
			Source:       file.Source,
			SizeBytes:    file.SizeBytes,
			ModifiedAt:   file.ModifiedAt,
			OpenURL:      uploadFileURL(file.RelativePath, false),
			DownloadURL:  uploadFileURL(file.RelativePath, true),
		})
	}
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	return files, totalBytes
}

func uploadSessionsFromList(
	listed []uploads.ListedFile,
	limit int,
) []uploadSessionView {
	index := make(map[string]*uploadSessionView)
	for _, file := range listed {
		key := strings.Join([]string{
			file.Scope.Channel,
			file.Scope.UserID,
			file.Scope.SessionID,
		}, "\x00")
		view, ok := index[key]
		if !ok {
			view = &uploadSessionView{
				Channel:   file.Scope.Channel,
				UserID:    file.Scope.UserID,
				SessionID: file.Scope.SessionID,
			}
			index[key] = view
		}
		view.FileCount++
		view.TotalBytes += file.SizeBytes
		if file.ModifiedAt.After(view.LastModified) {
			view.LastModified = file.ModifiedAt
		}
	}

	out := make([]uploadSessionView, 0, len(index))
	for _, view := range index {
		out = append(out, *view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastModified.Equal(out[j].LastModified) {
			if out[i].Channel != out[j].Channel {
				return out[i].Channel < out[j].Channel
			}
			if out[i].UserID != out[j].UserID {
				return out[i].UserID < out[j].UserID
			}
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].LastModified.After(out[j].LastModified)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func uploadKindCountsFromList(
	listed []uploads.ListedFile,
) []uploadKindCount {
	if len(listed) == 0 {
		return nil
	}

	counts := make(map[string]int)
	for _, file := range listed {
		kind := uploadKindFromFile(file)
		counts[kind]++
	}

	out := make([]uploadKindCount, 0, len(counts))
	for kind, count := range counts {
		out = append(out, uploadKindCount{
			Kind:  kind,
			Count: count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func uploadSourceCountsFromList(
	listed []uploads.ListedFile,
) []uploadSourceCount {
	if len(listed) == 0 {
		return nil
	}

	counts := make(map[string]int)
	for _, file := range listed {
		source := strings.TrimSpace(file.Source)
		if source == "" {
			source = "unknown"
		}
		counts[source]++
	}

	out := make([]uploadSourceCount, 0, len(counts))
	for source, count := range counts {
		out = append(out, uploadSourceCount{
			Source: source,
			Count:  count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Source < out[j].Source
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func filterUploadList(
	listed []uploads.ListedFile,
	filters uploadFilters,
) []uploads.ListedFile {
	if len(listed) == 0 {
		return nil
	}

	channel := strings.TrimSpace(filters.Channel)
	userID := strings.TrimSpace(filters.UserID)
	sessionID := strings.TrimSpace(filters.SessionID)
	kind := strings.ToLower(strings.TrimSpace(filters.Kind))
	mimeType := strings.ToLower(strings.TrimSpace(filters.MimeType))
	source := strings.ToLower(strings.TrimSpace(filters.Source))
	if channel == "" && userID == "" && sessionID == "" &&
		kind == "" && mimeType == "" && source == "" {
		return listed
	}

	out := make([]uploads.ListedFile, 0, len(listed))
	for _, file := range listed {
		if channel != "" && file.Scope.Channel != channel {
			continue
		}
		if userID != "" && file.Scope.UserID != userID {
			continue
		}
		if sessionID != "" && file.Scope.SessionID != sessionID {
			continue
		}
		if kind != "" && uploadKindFromFile(file) != kind {
			continue
		}
		if mimeType != "" &&
			strings.ToLower(strings.TrimSpace(file.MimeType)) != mimeType {
			continue
		}
		if source != "" &&
			strings.ToLower(strings.TrimSpace(file.Source)) != source {
			continue
		}
		out = append(out, file)
	}
	return out
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func uploadKindFromName(name string) string {
	return uploads.KindFromMeta(name, "")
}

func uploadKindFromFile(file uploads.ListedFile) string {
	return uploads.KindFromMeta(file.Name, file.MimeType)
}

func uploadFileURL(rel string, download bool) string {
	values := url.Values{}
	values.Set(queryPath, filepath.ToSlash(strings.TrimSpace(rel)))
	if download {
		values.Set(queryDownload, "1")
	}
	return routeUploadFile + "?" + values.Encode()
}
