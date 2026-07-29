//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	JSONName     = "review_report.json"
	MarkdownName = "review_report.md"
)

// Published contains artifact records and report metadata for persistence.
type Published struct {
	Artifacts []review.ArtifactRecord
	Metadata  review.ReportMetadata
}

// Publish saves canonical JSON and Markdown through artifact.Service.
func Publish(
	ctx context.Context,
	service artifact.Service,
	session artifact.SessionInfo,
	taskID string,
	document Document,
) (Published, error) {
	if ctx == nil || service == nil {
		return Published{}, errors.New("publish report: context and artifact service are required")
	}
	if !validArtifactSegment(taskID) || document.Report.Task.ID != taskID ||
		session.SessionID != taskID || !validArtifactSegment(session.AppName) ||
		!validArtifactSegment(session.UserID) || !validArtifactSegment(session.SessionID) {
		return Published{}, errors.New("publish report: invalid artifact identity")
	}
	if err := verifyDocument(document); err != nil {
		return Published{}, err
	}
	jsonRecord, err := saveArtifact(ctx, service, session, taskID, JSONName, "application/json", document.JSON)
	if err != nil {
		return Published{}, err
	}
	markdownRecord, err := saveArtifact(ctx, service, session, taskID, MarkdownName, "text/markdown", document.Markdown)
	if err != nil {
		return Published{Artifacts: []review.ArtifactRecord{jsonRecord}}, err
	}
	return Published{
		Artifacts: []review.ArtifactRecord{jsonRecord, markdownRecord},
		Metadata: review.ReportMetadata{
			SchemaVersion:             review.SchemaVersion,
			TaskID:                    taskID,
			Digest:                    jsonRecord.Digest,
			JSONArtifactReference:     jsonRecord.Reference,
			MarkdownArtifactReference: markdownRecord.Reference,
		},
	}, nil
}

func verifyDocument(document Document) error {
	verified, err := Finalize(document.Report)
	if err != nil {
		return err
	}
	if !bytes.Equal(verified.JSON, document.JSON) ||
		!bytes.Equal(verified.Markdown, document.Markdown) {
		return errors.New("report document bytes are not canonical")
	}
	return nil
}

func validArtifactSegment(value string) bool {
	if value == "" || value == "." || value == ".." || redact.String(value) != value {
		return false
	}
	if strings.ContainsAny(value, "/\\\x00") {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func saveArtifact(
	ctx context.Context,
	service artifact.Service,
	session artifact.SessionInfo,
	taskID string,
	name string,
	mimeType string,
	data []byte,
) (review.ArtifactRecord, error) {
	revision, err := service.SaveArtifact(ctx, session, name, &artifact.Artifact{
		Data:     append([]byte(nil), data...),
		MimeType: mimeType,
		Name:     name,
	})
	if err != nil {
		return review.ArtifactRecord{}, fmt.Errorf("publish report artifact %s: %w", name, redact.Error(err))
	}
	digest := sha256.Sum256(data)
	return review.ArtifactRecord{
		SchemaVersion: review.SchemaVersion,
		TaskID:        taskID,
		Name:          name,
		Reference: fmt.Sprintf(
			"artifact://%s/%s/%s/%s?revision=%d",
			url.PathEscape(session.AppName),
			url.PathEscape(session.UserID),
			url.PathEscape(session.SessionID),
			url.PathEscape(name),
			revision,
		),
		Digest:   hex.EncodeToString(digest[:]),
		MIMEType: mimeType,
		Size:     int64(len(data)),
	}, nil
}

// WriteLocal atomically replaces the two report files in directory.
func WriteLocal(directory string, document Document) error {
	if directory == "" {
		return errors.New("write report: output directory is required")
	}
	if err := verifyDocument(document); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("write report directory: %w", err)
	}
	jsonPath := filepath.Join(directory, JSONName)
	markdownPath := filepath.Join(directory, MarkdownName)
	previousJSON, err := captureFile(jsonPath)
	if err != nil {
		return err
	}
	jsonTemporary, err := prepareTemporary(directory, document.JSON, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(jsonTemporary)
	markdownTemporary, err := prepareTemporary(directory, document.Markdown, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(markdownTemporary)
	if err := os.Rename(jsonTemporary, jsonPath); err != nil {
		return fmt.Errorf("replace json report file: %w", err)
	}
	if err := os.Rename(markdownTemporary, markdownPath); err != nil {
		if rollbackErr := restoreFile(jsonPath, previousJSON); rollbackErr != nil {
			return fmt.Errorf("replace markdown report file: %v; restore json report: %w",
				err, rollbackErr)
		}
		return fmt.Errorf("replace markdown report file: %w", err)
	}
	return syncDirectory(directory)
}

type fileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func captureFile(name string) (fileSnapshot, error) {
	info, err := os.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("inspect existing report file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{exists: true, mode: info.Mode()}, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("read existing report file: %w", err)
	}
	return fileSnapshot{exists: true, data: data, mode: info.Mode().Perm()}, nil
}

func restoreFile(name string, snapshot fileSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDirectory(filepath.Dir(name))
	}
	if !snapshot.mode.IsRegular() {
		return errors.New("previous report destination was not a regular file")
	}
	return atomicWrite(name, snapshot.data, snapshot.mode.Perm())
}

func prepareTemporary(directory string, data []byte, mode os.FileMode) (name string, err error) {
	temporary, err := os.CreateTemp(directory, ".review-report-*")
	if err != nil {
		return "", fmt.Errorf("write report temporary file: %w", err)
	}
	name = temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if err = temporary.Chmod(mode); err != nil {
		return "", fmt.Errorf("chmod report temporary file: %w", err)
	}
	if _, err = temporary.Write(data); err != nil {
		return "", fmt.Errorf("write report temporary file: %w", err)
	}
	if err = temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync report temporary file: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return "", fmt.Errorf("close report temporary file: %w", err)
	}
	return name, nil
}

func syncDirectory(directory string) error {
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open report directory for sync: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("sync report directory: %w", err)
	}
	return nil
}

func atomicWrite(destination string, data []byte, mode os.FileMode) (err error) {
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".review-report-*")
	if err != nil {
		return fmt.Errorf("write report temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	if err = temporary.Chmod(mode); err != nil {
		return fmt.Errorf("chmod report temporary file: %w", err)
	}
	if _, err = temporary.Write(data); err != nil {
		return fmt.Errorf("write report temporary file: %w", err)
	}
	if err = temporary.Sync(); err != nil {
		return fmt.Errorf("sync report temporary file: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return fmt.Errorf("close report temporary file: %w", err)
	}
	if err = os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("replace report file: %w", err)
	}
	return syncDirectory(directory)
}
