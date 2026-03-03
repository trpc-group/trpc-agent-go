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
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

type recordingFetcher struct {
	gotURL string
	gotMax int64
	resp   fetched
	err    error
}

func (f *recordingFetcher) Fetch(
	_ context.Context,
	rawURL string,
	maxBytes int64,
) (fetched, error) {
	f.gotURL = rawURL
	f.gotMax = maxBytes
	return f.resp, f.err
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("boom")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (rt roundTripperFunc) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	return rt(req)
}

type staticResolver struct {
	addrs []net.IPAddr
	err   error
}

func (r staticResolver) LookupIPAddr(
	_ context.Context,
	_ string,
) ([]net.IPAddr, error) {
	return r.addrs, r.err
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

func TestReadLimited_NilReader(t *testing.T) {
	t.Parallel()

	_, err := readLimited(nil, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil reader")
}

func TestReadLimited_InvalidMaxBytes(t *testing.T) {
	t.Parallel()

	_, err := readLimited(strings.NewReader("a"), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid max bytes")
}

func TestReadLimited_ReadError(t *testing.T) {
	t.Parallel()

	_, err := readLimited(errorReader{}, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read body")
}

func TestNormalizeContentType(t *testing.T) {
	t.Parallel()

	require.Empty(t, normalizeContentType(" "))
	require.Equal(
		t,
		"text/plain",
		normalizeContentType("text/plain; charset=utf-8"),
	)
}

func TestValidatePublicHost_ResolverPublic(t *testing.T) {
	t.Parallel()

	const host = "example.com"
	r := staticResolver{
		addrs: []net.IPAddr{
			{IP: net.ParseIP("192.0.2.1")},
		},
	}
	require.NoError(
		t,
		validatePublicHost(context.Background(), host, r),
	)
}

func TestValidatePublicHost_ResolverPrivate(t *testing.T) {
	t.Parallel()

	const host = "example.com"
	r := staticResolver{
		addrs: []net.IPAddr{
			{IP: net.ParseIP("127.0.0.1")},
		},
	}
	err := validatePublicHost(context.Background(), host, r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private address")
}

func TestValidatePublicHost_ResolverError(t *testing.T) {
	t.Parallel()

	const host = "example.com"
	r := staticResolver{
		err: errors.New("dns failure"),
	}
	err := validatePublicHost(context.Background(), host, r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve host")
}

func TestValidatePublicHost_ResolverNoAddresses(t *testing.T) {
	t.Parallel()

	const host = "example.com"
	r := staticResolver{}
	err := validatePublicHost(context.Background(), host, r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no addresses")
}

func TestFilenameFromHeaders_NilArgs(t *testing.T) {
	t.Parallel()

	require.Empty(t, filenameFromHeaders(nil, nil))
}

func TestFilenameFromHeaders_ContentDispositionFilename(t *testing.T) {
	t.Parallel()

	const rawURL = "https://example.com/a.bin"
	u, err := url.Parse(rawURL)
	require.NoError(t, err)

	resp := &http.Response{
		Header: http.Header{
			"Content-Disposition": []string{
				"attachment; filename=\"b.txt\"",
			},
		},
	}
	require.Equal(t, "b.txt", filenameFromHeaders(resp, u))
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

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	_, err := f.Fetch(context.Background(), "http://\x00", 1)
	require.Error(t, err)
}

func TestURLPartFetcher_Fetch_UnsupportedScheme(t *testing.T) {
	t.Parallel()

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	_, err := f.Fetch(context.Background(), "file://example.com/a", 1)
	require.Error(t, err)
}

func TestURLPartFetcher_Fetch_MissingHost(t *testing.T) {
	t.Parallel()

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	_, err := f.Fetch(context.Background(), "http:///a", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing url host")
}

func TestURLPartFetcher_Fetch_UnexpectedStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		},
	))
	t.Cleanup(srv.Close)

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	_, err := f.Fetch(context.Background(), srv.URL, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected status")
}

func TestURLPartFetcher_Fetch_TooManyRedirects(t *testing.T) {
	t.Parallel()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, srv.URL, http.StatusFound)
		},
	))
	t.Cleanup(srv.Close)

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	f.maxRedirects = 0
	_, err := f.Fetch(context.Background(), srv.URL, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too many redirects")
}

func TestURLPartFetcher_Fetch_UsesDefaultClient(t *testing.T) {
	t.Parallel()

	const okBody = "ok"
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, okBody)
		},
	))
	t.Cleanup(srv.Close)

	f := &urlPartFetcher{
		client:       nil,
		maxRedirects: defaultMaxRedirects,
		policy: partURLPolicy{
			allowPrivate: true,
		},
	}
	resp, err := f.Fetch(context.Background(), srv.URL, 1<<10)
	require.NoError(t, err)
	require.Equal(t, []byte(okBody), resp.Data)
}

func TestURLPartFetcher_Fetch_TransportError(t *testing.T) {
	t.Parallel()

	f := &urlPartFetcher{
		client: &http.Client{
			Transport: roundTripperFunc(
				func(_ *http.Request) (*http.Response, error) {
					return nil, errors.New("transport error")
				},
			),
		},
		maxRedirects: defaultMaxRedirects,
		policy: partURLPolicy{
			allowPrivate: true,
		},
	}
	_, err := f.Fetch(context.Background(), "https://192.0.2.1/a", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetch:")
}

func TestURLPartFetcher_Fetch_InvalidMaxBytes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "hello")
		},
	))
	t.Cleanup(srv.Close)

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	_, err := f.Fetch(context.Background(), srv.URL, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid max bytes")
}

func TestURLPartFetcher_Fetch_ContentTooLarge(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "abc")
		},
	))
	t.Cleanup(srv.Close)

	f := newURLPartFetcher(partURLPolicy{allowPrivate: true})
	_, err := f.Fetch(context.Background(), srv.URL, 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
}

func TestValidatingFetcher_Fetch_MissingFetcher(t *testing.T) {
	t.Parallel()

	f := validatingFetcher{
		next: nil,
		policy: partURLPolicy{
			allowPrivate: true,
		},
	}
	_, err := f.Fetch(context.Background(), "https://192.0.2.1/a", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing fetcher")
}

func TestValidatingFetcher_Fetch_Delegates(t *testing.T) {
	t.Parallel()

	const (
		rawURL   = "https://192.0.2.1/a"
		maxBytes = 123
	)
	next := &recordingFetcher{
		resp: fetched{
			Data: []byte("data"),
		},
	}
	f := validatingFetcher{
		next:   next,
		policy: partURLPolicy{},
	}
	out, err := f.Fetch(context.Background(), rawURL, maxBytes)
	require.NoError(t, err)
	require.Equal(t, []byte("data"), out.Data)
	require.Equal(t, rawURL, next.gotURL)
	require.Equal(t, int64(maxBytes), next.gotMax)
}

func TestFetchContentPart_DefaultMaxBytes(t *testing.T) {
	t.Parallel()

	const rawURL = "https://example.com/a"
	fetcher := &recordingFetcher{
		resp: fetched{
			Data: []byte("data"),
		},
	}
	s := &Server{
		partFetcher:  fetcher,
		maxPartBytes: 0,
	}
	out, err := s.fetchContentPart(context.Background(), rawURL)
	require.NoError(t, err)
	require.Equal(t, []byte("data"), out.Data)
	require.Equal(t, rawURL, fetcher.gotURL)
	require.Equal(t, defaultMaxContentPartBytes, fetcher.gotMax)
}

func TestFetchContentPart_MissingURL(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: &recordingFetcher{},
	}
	_, err := s.fetchContentPart(context.Background(), " ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing url")
}

func TestNormalizeContentPart_UnsupportedType(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: "unknown"},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_ImageURL(t *testing.T) {
	t.Parallel()

	const rawURL = "https://example.com/a.png"
	s := &Server{}
	out, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeImage,
			Image: &gwproto.ImagePart{
				URL: rawURL,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Image)
	require.Equal(t, rawURL, out.Image.URL)
	require.Equal(t, imageDetailAuto, out.Image.Detail)
}

func TestNormalizeContentPart_ImageErrors(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: gwproto.PartTypeImage},
	)
	require.Error(t, err)

	_, _, err = s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeImage,
			Image: &gwproto.ImagePart{
				Data: []byte{0x1},
			},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_AudioErrors(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: gwproto.PartTypeAudio},
	)
	require.Error(t, err)

	_, _, err = s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type:  gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{},
		},
	)
	require.Error(t, err)

	_, _, err = s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				Data: []byte("data"),
			},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_AudioURL_InferFromFilename(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data:     []byte("data"),
				Filename: "voice.wav",
			},
		},
	}
	out, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				URL: "https://example.com/voice.wav",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Audio)
	require.Equal(t, audioFormatWAV, out.Audio.Format)
}

func TestNormalizeContentPart_AudioURL_ExplicitFormat(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data: []byte("data"),
			},
		},
	}
	out, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				URL:    "https://example.com/voice",
				Format: audioFormatWAV,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Audio)
	require.Equal(t, audioFormatWAV, out.Audio.Format)
}

func TestNormalizeContentPart_AudioURL_UnsupportedFormat(t *testing.T) {
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
				URL:    "https://example.com/voice",
				Format: "aac",
			},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_FileErrors(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: gwproto.PartTypeFile},
	)
	require.Error(t, err)

	_, _, err = s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeFile,
			File: &gwproto.FilePart{},
		},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_FileURL_DefaultName(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data: []byte("data"),
			},
		},
	}
	out, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeFile,
			File: &gwproto.FilePart{
				URL: "https://example.com/attachment",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.File)
	require.Equal(t, "attachment", out.File.Name)
}

func TestNormalizeContentPart_FileURL_UsesContentType(t *testing.T) {
	t.Parallel()

	s := &Server{
		partFetcher: staticFetcher{
			resp: fetched{
				Data:        []byte("data"),
				ContentType: "text/plain; charset=utf-8",
			},
		},
	}
	out, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeFile,
			File: &gwproto.FilePart{
				URL: "https://example.com/payload",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.File)
	require.Equal(t, "text/plain", out.File.MimeType)
}

func TestNormalizeContentPart_FileData_UsesFormat(t *testing.T) {
	t.Parallel()

	s := &Server{}
	out, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{
			Type: gwproto.PartTypeFile,
			File: &gwproto.FilePart{
				Filename: "payload",
				Data:     []byte("data"),
				Format:   "application/json",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.File)
	require.Equal(t, "application/json", out.File.MimeType)
}

func TestNormalizeContentPart_LinkErrors(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: gwproto.PartTypeLink},
	)
	require.Error(t, err)
}

func TestNormalizeContentPart_LocationErrors(t *testing.T) {
	t.Parallel()

	s := &Server{}
	_, _, err := s.normalizeContentPart(
		context.Background(),
		gwproto.ContentPart{Type: gwproto.PartTypeLocation},
	)
	require.Error(t, err)
}
