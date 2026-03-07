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
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
)

const (
	defaultUploadsDir = "uploads"
	maxUploadRows     = 12
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
	Enabled    bool         `json:"enabled"`
	Root       string       `json:"root,omitempty"`
	FileCount  int          `json:"file_count"`
	TotalBytes int64        `json:"total_bytes"`
	Error      string       `json:"error,omitempty"`
	Files      []uploadView `json:"files,omitempty"`
}

type uploadView struct {
	Name         string    `json:"name,omitempty"`
	RelativePath string    `json:"relative_path,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at,omitempty"`
	OpenURL      string    `json:"open_url,omitempty"`
	DownloadURL  string    `json:"download_url,omitempty"`
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
	files, totalBytes, err := listUploads(root)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.FileCount = len(files)
	status.TotalBytes = totalBytes
	if len(files) > maxUploadRows {
		files = files[:maxUploadRows]
	}
	status.Files = files
	return status
}

func resolveUploadsRoot(stateDir string) string {
	root := strings.TrimSpace(stateDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, defaultUploadsDir)
}

func listUploads(root string) ([]uploadView, int64, error) {
	files := make([]uploadView, 0)
	var totalBytes int64
	walkErr := filepath.WalkDir(
		root,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d == nil || d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			totalBytes += info.Size()
			kind := uploadKindFromName(d.Name())
			openURL := uploadFileURL(rel, false)
			files = append(files, uploadView{
				Name:         d.Name(),
				RelativePath: filepath.ToSlash(rel),
				Kind:         kind,
				SizeBytes:    info.Size(),
				ModifiedAt:   info.ModTime(),
				OpenURL:      openURL,
				DownloadURL:  uploadFileURL(rel, true),
			})
			return nil
		},
	)
	if walkErr != nil {
		return nil, 0, walkErr
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModifiedAt.Equal(files[j].ModifiedAt) {
			return files[i].RelativePath < files[j].RelativePath
		}
		return files[i].ModifiedAt.After(files[j].ModifiedAt)
	})
	return files, totalBytes, nil
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
