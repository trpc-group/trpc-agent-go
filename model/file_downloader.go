//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import "context"

// FileDownloader is an optional interface implemented by models that can
// download a previously uploaded file by file_id.
//
// It enables tools (for example, skill_run) to stage user-uploaded file inputs
// into an execution workspace when the conversation references files by ID
// rather than embedding raw bytes.
type FileDownloader interface {
	// DownloadFile downloads file content referenced by fileID.
	//
	// Implementations should return the raw bytes and a best-effort MIME type.
	DownloadFile(ctx context.Context, fileID string) ([]byte, string, error)
}
