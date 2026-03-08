//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

func TestUploadContextName_HidesInboundPlaceholderNames(
	t *testing.T,
) {
	t.Parallel()

	require.Equal(
		t,
		"your audio message",
		uploadContextName(uploads.ListedFile{
			Name:     "file_11.oga",
			MimeType: "audio/ogg",
			Source:   uploads.SourceInbound,
		}),
	)
	require.Equal(
		t,
		"your video",
		uploadContextName(uploads.ListedFile{
			Name:     "file_10.mp4",
			MimeType: "video/mp4",
			Source:   uploads.SourceInbound,
		}),
	)
	require.Equal(
		t,
		"document.pdf",
		uploadContextName(uploads.ListedFile{
			Name:     "document.pdf",
			MimeType: "application/pdf",
			Source:   uploads.SourceInbound,
		}),
	)
}

func TestBuildUploadContextText_HidesPlaceholderNames(
	t *testing.T,
) {
	t.Parallel()

	text := buildUploadContextText([]uploads.ListedFile{{
		Name:     "file_11.oga",
		MimeType: "audio/ogg",
		Source:   uploads.SourceInbound,
	}})
	require.Contains(t, text, "your audio message")
	require.NotContains(t, text, "file_11.oga")
}
