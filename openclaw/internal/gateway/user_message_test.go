//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

type staticFetcher struct {
	resp fetched
	err  error
}

func (f staticFetcher) Fetch(
	_ context.Context,
	_ string,
	_ int64,
) (fetched, error) {
	return f.resp, f.err
}

func TestNormalizeContentPart_FileID(t *testing.T) {
	t.Parallel()

	s := &Server{}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			Filename: "a.txt",
			FileID:   "file-1",
		},
	}
	out, _, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, model.ContentTypeFile, out.Type)
	require.NotNil(t, out.File)
	require.Equal(t, "a.txt", out.File.Name)
	require.Equal(t, "file-1", out.File.FileID)
}

func TestNormalizeContentPart_FileData_InferMimeType(t *testing.T) {
	t.Parallel()

	s := &Server{}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			Filename: "a.txt",
			Data:     []byte("hello"),
		},
	}
	out, _, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.File)
	require.Equal(t, "text/plain", out.File.MimeType)
}

func TestNormalizeContentPart_FileURL_DefaultMimeType(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data:     []byte("data"),
				Filename: "payload.unknownext",
			},
		},
	}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			URL: "https://example.com/payload.unknownext",
		},
	}
	out, _, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.File)
	require.Equal(t, mimeOctetStream, out.File.MimeType)
}

func TestNormalizeContentPart_ImageData(t *testing.T) {
	t.Parallel()

	s := &Server{}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeImage,
		Image: &gwproto.ImagePart{
			Data:   []byte{0x1, 0x2},
			Format: "png",
		},
	}
	out, _, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Image)
	require.Equal(t, "png", out.Image.Format)
	require.Equal(t, imageDetailAuto, out.Image.Detail)
}

func TestNormalizeContentPart_AudioData(t *testing.T) {
	t.Parallel()

	s := &Server{}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeAudio,
		Audio: &gwproto.AudioPart{
			Data:   []byte("data"),
			Format: "mp3",
		},
	}
	out, _, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Audio)
	require.Equal(t, "mp3", out.Audio.Format)
}

func TestNormalizeContentPart_AudioURL_InferFromContentType(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data:        []byte("data"),
				ContentType: "audio/mpeg",
			},
		},
	}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeAudio,
		Audio: &gwproto.AudioPart{
			URL: "https://example.com/voice",
		},
	}
	out, _, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Audio)
	require.Equal(t, audioFormatMP3, out.Audio.Format)
}

func TestNormalizeContentPart_Link(t *testing.T) {
	t.Parallel()

	s := &Server{}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeLink,
		Link: &gwproto.LinkPart{
			Title: "Docs",
			URL:   "https://example.com",
		},
	}
	out, text, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, model.ContentTypeText, out.Type)
	require.NotNil(t, out.Text)
	require.Contains(t, *out.Text, "https://example.com")
	require.Equal(t, *out.Text, text)
}

func TestNormalizeContentPart_Location(t *testing.T) {
	t.Parallel()

	s := &Server{}
	part := gwproto.ContentPart{
		Type: gwproto.PartTypeLocation,
		Location: &gwproto.LocationPart{
			Name:      "Somewhere",
			Latitude:  1.2,
			Longitude: 3.4,
		},
	}
	out, text, err := s.normalizeContentPart(context.Background(), part)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, model.ContentTypeText, out.Type)
	require.NotNil(t, out.Text)
	require.Contains(t, *out.Text, "latitude=")
	require.Contains(t, *out.Text, "longitude=")
	require.Equal(t, *out.Text, text)
}

func TestNormalizeUserMessage_MissingText(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeUserMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing text")
}

func TestFilenameFromHeaders_FallbackURL(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("https://example.com/a.txt")
	require.NoError(t, err)

	resp := &http.Response{
		Header: http.Header{
			"Content-Disposition": []string{"bad header"},
		},
	}
	require.Equal(t, "a.txt", filenameFromHeaders(resp, u))
}

func TestReadLimited_ContentTooLarge(t *testing.T) {
	t.Parallel()

	_, err := readLimited(strings.NewReader("abc"), 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
}

func TestNormalizeContentPart_TextErrors(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: gwproto.PartTypeText},
	)
	require.Error(t, err)

	_, _, err = s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeText,
			Text: strPtr(" "),
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_AudioUnsupportedFormat(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				Data:   []byte("data"),
				Format: "aac",
			},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_AudioURL_MissingFormat(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data: []byte("data"),
			},
		},
	}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				URL: "https://example.com/voice",
			},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_FileData_MissingFilename(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeFile,
			File: &gwproto.FilePart{
				Data: []byte("data"),
			},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_Link_MissingURL(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeLink,
			Link: &gwproto.LinkPart{Title: "t"},
		},
	)
	require.Error(t, err)
}

func TestURLPartFetcher_Fetch_InvalidURL(t *testing.T) {
	t.Parallel()

	f := newURLPartFetcher()
	_, err := f.Fetch(context.Background(), "http://\x00", 1)
	require.Error(t, err)
}

func TestURLPartFetcher_Fetch_UnsupportedScheme(t *testing.T) {
	t.Parallel()

	f := newURLPartFetcher()
	_, err := f.Fetch(context.Background(), "file://example.com/a", 1)
	require.Error(t, err)
}
