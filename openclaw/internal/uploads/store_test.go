//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package uploads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreSave(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	saved, err := store.Save(context.Background(), Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:abc",
	}, "../李光耀回忆录.pdf", []byte("pdf-bytes"))
	require.NoError(t, err)

	require.NotEmpty(t, saved.Path)
	require.Equal(t, HostRef(saved.Path), saved.HostRef)

	data, err := os.ReadFile(saved.Path)
	require.NoError(t, err)
	require.Equal(t, []byte("pdf-bytes"), data)

	require.Contains(t, saved.Name, "李光耀回忆录.pdf")
	require.NotContains(t, saved.Name, "..")
}

func TestNewStore_EmptyStateDir(t *testing.T) {
	t.Parallel()

	_, err := NewStore(" ")
	require.Error(t, err)
}

func TestStoreSave_Deduplicates(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	scope := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	first, err := store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("same"),
	)
	require.NoError(t, err)

	second, err := store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("same"),
	)
	require.NoError(t, err)

	require.Equal(t, first.Path, second.Path)
}

func TestStoreDeleteUser(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root)
	require.NoError(t, err)

	saved, err := store.Save(context.Background(), Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}, "report.pdf", []byte("x"))
	require.NoError(t, err)

	require.NoError(t, store.DeleteUser(
		context.Background(),
		"telegram",
		"u1",
	))

	_, err = os.Stat(filepath.Dir(saved.Path))
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestPathFromHostRef(t *testing.T) {
	t.Parallel()

	absPath := filepath.Join(t.TempDir(), "report.pdf")

	path, ok := PathFromHostRef(HostRef(absPath))
	require.True(t, ok)
	require.Equal(t, absPath, path)

	path, ok = PathFromHostRef(absPath)
	require.True(t, ok)
	require.Equal(t, absPath, path)

	_, ok = PathFromHostRef("file-123")
	require.False(t, ok)
}

func TestStoreListScopeAndListAll(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	scopeA := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	scopeB := Scope{
		Channel:   "telegram",
		UserID:    "u2",
		SessionID: "s2",
	}

	first, err := store.Save(
		context.Background(),
		scopeA,
		"report.pdf",
		[]byte("a"),
	)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	second, err := store.Save(
		context.Background(),
		scopeB,
		"clip.mp4",
		[]byte("bb"),
	)
	require.NoError(t, err)

	scopeFiles, err := store.ListScope(scopeA, 10)
	require.NoError(t, err)
	require.Len(t, scopeFiles, 1)
	require.Equal(t, first.Name, scopeFiles[0].Name)
	require.Equal(t, first.Path, scopeFiles[0].Path)
	require.Equal(t, scopeA, scopeFiles[0].Scope)
	require.Equal(t, "", scopeFiles[0].MimeType)
	require.Contains(
		t,
		scopeFiles[0].RelativePath,
		"telegram/u1/s1/",
	)

	allFiles, err := store.ListAll(10)
	require.NoError(t, err)
	require.Len(t, allFiles, 2)
	require.Equal(t, second.Path, allFiles[0].Path)
	require.Equal(t, scopeB, allFiles[0].Scope)
	require.Equal(t, first.Path, allFiles[1].Path)
}

func TestStoreSaveWithMetadataAndList(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	scope := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	saved, err := store.SaveWithMetadata(
		context.Background(),
		scope,
		"video-note",
		"video/mp4",
		[]byte("mp4"),
	)
	require.NoError(t, err)

	metaPath := metadataPath(saved.Path)
	data, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "video/mp4")

	files, err := store.ListScope(scope, 10)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "video/mp4", files[0].MimeType)
	require.Equal(t, "video-note", files[0].Name)
	require.Equal(t, "", files[0].Source)
}

func TestStoreSaveWithInfo_PersistsSource(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	scope := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	saved, err := store.SaveWithInfo(
		context.Background(),
		scope,
		"frame.png",
		FileMetadata{
			MimeType: "image/png",
			Source:   SourceDerived,
		},
		[]byte("png"),
	)
	require.NoError(t, err)

	metaPath := metadataPath(saved.Path)
	data, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "image/png")
	require.Contains(t, string(data), SourceDerived)

	files, err := store.ListScope(scope, 10)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "image/png", files[0].MimeType)
	require.Equal(t, SourceDerived, files[0].Source)
}

func TestStoreScopeDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root)
	require.NoError(t, err)

	got := store.ScopeDir(Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:abc",
	})
	require.NotEmpty(t, got)
	require.True(
		t,
		strings.HasPrefix(got, filepath.Join(root, defaultUploadsDir)),
	)
	require.Contains(t, got, filepath.Join("telegram", "u1"))
}

func TestStoreAnnotate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root)
	require.NoError(t, err)

	scope := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	saved, err := store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	err = store.Annotate(saved.Path, FileMetadata{
		MimeType: "application/pdf",
		Source:   SourceDerived,
	})
	require.NoError(t, err)

	files, err := store.ListScope(scope, 10)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "application/pdf", files[0].MimeType)
	require.Equal(t, SourceDerived, files[0].Source)
}

func TestSanitizeHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultChannelDir, sanitizeDirToken(" ", defaultChannelDir))
	require.Equal(t, "telegram_dm_1", sanitizeDirToken(
		"telegram:dm:1",
		defaultSessionDir,
	))
	require.Equal(t, defaultFileName, sanitizeFileName("../"))
	require.Contains(t, sanitizeFileName("../报告.pdf"), "报告.pdf")
	require.True(t, IsMetadataPath("/tmp/report.pdf"+metadataSuffix))
	require.Equal(t, KindImage, KindFromMeta("frame", "image/png"))
	require.Equal(t, KindAudio, KindFromMeta("voice", "audio/ogg"))
	require.Equal(t, KindVideo, KindFromMeta("video-note", "video/mp4"))
	require.Equal(t, KindPDF, KindFromMeta("report", "application/pdf"))
	require.Equal(t, KindFile, KindFromMeta("notes", ""))
	require.Equal(
		t,
		"video.mp4",
		PreferredName("file_10.mp4", "video/mp4"),
	)
	require.Equal(
		t,
		"audio.ogg",
		PreferredName("file_11.ogg", "audio/ogg"),
	)
	require.Equal(
		t,
		"document.pdf",
		PreferredName("file_12.pdf", "application/pdf"),
	)
	require.Equal(
		t,
		"scan.jpg",
		PreferredName("scan.jpg", "image/jpeg"),
	)
	require.Equal(
		t,
		"audio.oga",
		PreferredName(
			"3a2a69871c9515b0d3a1d886-"+
				"3a2a69871c9515b0d3a1d886-file_11.oga",
			"audio/ogg",
		),
	)
	require.Equal(
		t,
		"report.pdf",
		StoredDisplayName(
			"702b5bcd905ee561174e9b03-"+
				"00ed39bb50144ce9ebef3aab-report.pdf",
		),
	)
	require.Equal(
		t,
		SourceInbound,
		sanitizeMetadataSource(" InBound "),
	)
	require.Equal(
		t,
		SourceDerived,
		sanitizeMetadataSource("derived"),
	)
	require.Equal(
		t,
		"",
		sanitizeMetadataSource("else"),
	)
}
