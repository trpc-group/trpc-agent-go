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
	Enabled    bool                `json:"enabled"`
	Root       string              `json:"root,omitempty"`
	FileCount  int                 `json:"file_count"`
	TotalBytes int64               `json:"total_bytes"`
	Error      string              `json:"error,omitempty"`
	Files      []uploadView        `json:"files,omitempty"`
	Sessions   []uploadSessionView `json:"sessions,omitempty"`
}

type uploadView struct {
	Channel      string    `json:"channel,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Name         string    `json:"name,omitempty"`
	RelativePath string    `json:"relative_path,omitempty"`
	Kind         string    `json:"kind,omitempty"`
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
	status.FileCount = len(listed)
	status.Files, status.TotalBytes = uploadViewsFromList(listed)
	status.Sessions = uploadSessionsFromList(listed)
	return status
}

func resolveUploadsRoot(stateDir string) string {
	root := strings.TrimSpace(stateDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, defaultUploadsDir)
}

func uploadViewsFromList(listed []uploads.ListedFile) ([]uploadView, int64) {
	files := make([]uploadView, 0, len(listed))
	var totalBytes int64
	for _, file := range listed {
		totalBytes += file.SizeBytes
		files = append(files, uploadView{
			Channel:      file.Scope.Channel,
			UserID:       file.Scope.UserID,
			SessionID:    file.Scope.SessionID,
			Name:         file.Name,
			RelativePath: file.RelativePath,
			Kind:         uploadKindFromName(file.Name),
			SizeBytes:    file.SizeBytes,
			ModifiedAt:   file.ModifiedAt,
			OpenURL:      uploadFileURL(file.RelativePath, false),
			DownloadURL:  uploadFileURL(file.RelativePath, true),
		})
	}
	if len(files) > maxUploadRows {
		files = files[:maxUploadRows]
	}
	return files, totalBytes
}

func uploadSessionsFromList(
	listed []uploads.ListedFile,
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
	if len(out) > maxUploadSessions {
		out = out[:maxUploadSessions]
	}
	return out
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func uploadKindFromName(name string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return "image"
	case ".mp3", ".wav", ".ogg", ".oga", ".m4a":
		return "audio"
	case ".mp4", ".mov", ".webm", ".mkv":
		return "video"
	case ".pdf":
		return "pdf"
	default:
		return "file"
	}
}

func uploadFileURL(rel string, download bool) string {
	values := url.Values{}
	values.Set(queryPath, filepath.ToSlash(strings.TrimSpace(rel)))
	if download {
		values.Set(queryDownload, "1")
	}
	return routeUploadFile + "?" + values.Encode()
}
