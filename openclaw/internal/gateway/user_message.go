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
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

const (
	defaultMaxContentPartBytes int64 = 8 << 20

	defaultContentPartTimeout = 15 * time.Second
	defaultMaxRedirects       = 5

	headerUserAgent = "User-Agent"

	contentPartUserAgent = "trpc-agent-go/openclaw-gateway"

	imageDetailAuto = "auto"

	mimeOctetStream = "application/octet-stream"

	audioFormatWAV = "wav"
	audioFormatMP3 = "mp3"

	errContentPartTooLarge = "content part too large"
)

type partFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxBytes int64) (fetched, error)
}

type fetched struct {
	Data        []byte
	ContentType string
	Filename    string
}

type partURLPolicy struct {
	allowPrivate    bool
	allowedPatterns []string
	resolver        hostResolver
}

type hostResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

func (p partURLPolicy) Validate(
	ctx context.Context,
	u *url.URL,
) error {
	if u == nil {
		return errors.New("nil url")
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return errors.New("unsupported url scheme")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return errors.New("missing url host")
	}

	if len(p.allowedPatterns) > 0 &&
		!matchesAnyPattern(u, p.allowedPatterns) {
		return errors.New("url does not match any allowed pattern")
	}
	if p.allowPrivate {
		return nil
	}
	return validatePublicHost(ctx, host, p.resolver)
}

func matchesAnyPattern(u *url.URL, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if matchPattern(u, pattern) {
			return true
		}
	}
	return false
}

func matchPattern(u *url.URL, pattern string) bool {
	var host, prefix string
	if idx := strings.Index(pattern, "/"); idx != -1 {
		host = pattern[:idx]
		prefix = pattern[idx:]
	} else {
		host = pattern
	}

	if !matchHost(u.Hostname(), host) {
		return false
	}
	if prefix == "" {
		return true
	}
	uPath := u.Path
	if uPath == "" {
		uPath = "/"
	}
	if !strings.HasPrefix(uPath, "/") {
		uPath = "/" + uPath
	}
	if !strings.HasPrefix(uPath, prefix) {
		return false
	}
	if len(uPath) == len(prefix) {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return true
	}
	return uPath[len(prefix)] == '/'
}

func matchHost(hostname, target string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	target = strings.ToLower(strings.TrimSpace(target))
	if hostname == "" || target == "" {
		return false
	}
	if hostname == target {
		return true
	}
	return strings.HasSuffix(hostname, "."+target)
}

func validatePublicHost(
	ctx context.Context,
	host string,
	resolver hostResolver,
) error {
	if strings.EqualFold(host, "localhost") {
		return errors.New("url host resolves to private address")
	}
	ip, err := netip.ParseAddr(host)
	if err == nil {
		if isPrivateOrLocalIP(ip) {
			return fmt.Errorf(
				"url host resolves to private address: %s",
				host,
			)
		}
		return nil
	}

	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	if len(addrs) == 0 {
		return errors.New("resolve host: no addresses")
	}
	for _, addr := range addrs {
		parsed, ok := netip.AddrFromSlice(addr.IP)
		if !ok {
			continue
		}
		if isPrivateOrLocalIP(parsed) {
			return fmt.Errorf(
				"url host resolves to private address: %s",
				host,
			)
		}
	}
	return nil
}

func isPrivateOrLocalIP(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}

type validatingFetcher struct {
	next   partFetcher
	policy partURLPolicy
}

func (f validatingFetcher) Fetch(
	ctx context.Context,
	rawURL string,
	maxBytes int64,
) (fetched, error) {
	if f.next == nil {
		return fetched{}, errors.New("missing fetcher")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fetched{}, fmt.Errorf("parse url: %w", err)
	}
	if err := f.policy.Validate(ctx, parsed); err != nil {
		return fetched{}, err
	}
	return f.next.Fetch(ctx, rawURL, maxBytes)
}

type urlPartFetcher struct {
	client       *http.Client
	maxRedirects int
	policy       partURLPolicy
}

func newURLPartFetcher(policy partURLPolicy) *urlPartFetcher {
	return &urlPartFetcher{
		client: &http.Client{
			Timeout: defaultContentPartTimeout,
		},
		maxRedirects: defaultMaxRedirects,
		policy:       policy,
	}
}

func (f *urlPartFetcher) Fetch(
	ctx context.Context,
	rawURL string,
	maxBytes int64,
) (fetched, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fetched{}, fmt.Errorf("parse url: %w", err)
	}
	if err := f.policy.Validate(ctx, parsed); err != nil {
		return fetched{}, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		rawURL,
		nil,
	)
	if err != nil {
		return fetched{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set(headerUserAgent, contentPartUserAgent)

	c := f.client
	if c == nil {
		c = http.DefaultClient
	}

	copied := *c
	copied.CheckRedirect = func(
		req *http.Request,
		via []*http.Request,
	) error {
		if len(via) > f.maxRedirects {
			return errors.New("too many redirects")
		}
		return f.policy.Validate(req.Context(), req.URL)
	}

	resp, err := copied.Do(req)
	if err != nil {
		return fetched{}, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return fetched{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return fetched{}, err
	}

	contentType := normalizeContentType(resp.Header.Get(headerContentType))
	filename := filenameFromHeaders(resp, parsed)
	return fetched{
		Data:        body,
		ContentType: contentType,
		Filename:    filename,
	}, nil
}

func normalizeContentType(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	mediaType := strings.Split(raw, ";")[0]
	return strings.TrimSpace(mediaType)
}

func filenameFromHeaders(resp *http.Response, u *url.URL) string {
	if resp == nil || u == nil {
		return ""
	}
	_, params, err := mime.ParseMediaType(
		resp.Header.Get("Content-Disposition"),
	)
	if err == nil {
		name := strings.TrimSpace(params["filename"])
		if name != "" {
			return name
		}
	}
	name := strings.TrimSpace(path.Base(u.Path))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil reader")
	}
	if maxBytes <= 0 {
		return nil, errors.New("invalid max bytes")
	}
	limited := io.LimitReader(r, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New(errContentPartTooLarge)
	}
	return body, nil
}

func inboundFromRequest(
	req gwproto.MessageRequest,
	text string,
) InboundMessage {
	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = defaultChannelName
	}
	return InboundMessage{
		Channel:   channel,
		From:      strings.TrimSpace(req.From),
		To:        strings.TrimSpace(req.To),
		Thread:    strings.TrimSpace(req.Thread),
		MessageID: strings.TrimSpace(req.MessageID),
		Text:      strings.TrimSpace(text),
	}
}

func (s *Server) normalizeUserMessage(
	ctx context.Context,
	req gwproto.MessageRequest,
) (model.Message, string, error) {
	text := strings.TrimSpace(req.Text)
	parts, partsText, err := s.normalizeContentParts(
		ctx,
		req.ContentParts,
	)
	if err != nil {
		return model.Message{}, "", err
	}

	mentionText := strings.TrimSpace(joinText(text, partsText))
	if text == "" && len(parts) == 0 {
		return model.Message{}, "", errors.New("missing text")
	}
	return model.Message{
		Role:         model.RoleUser,
		Content:      text,
		ContentParts: parts,
	}, mentionText, nil
}

func joinText(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}

func (s *Server) normalizeContentParts(
	ctx context.Context,
	parts []gwproto.ContentPart,
) ([]model.ContentPart, string, error) {
	if len(parts) == 0 {
		return nil, "", nil
	}
	out := make([]model.ContentPart, 0, len(parts))
	textParts := make([]string, 0, len(parts))

	for i, part := range parts {
		normalized, text, err := s.normalizeContentPart(ctx, part)
		if err != nil {
			return nil, "", fmt.Errorf("content_parts[%d]: %w", i, err)
		}
		if normalized == nil {
			continue
		}
		out = append(out, *normalized)
		if text != "" {
			textParts = append(textParts, text)
		}
	}
	return out, strings.Join(textParts, "\n"), nil
}

func (s *Server) normalizeContentPart(
	ctx context.Context,
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	maxBytes := s.maxPartBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxContentPartBytes
	}

	switch part.Type {
	case gwproto.PartTypeText:
		return normalizeTextPart(part)
	case gwproto.PartTypeImage:
		if part.Image != nil &&
			len(part.Image.Data) > 0 &&
			int64(len(part.Image.Data)) > maxBytes {
			return nil, "", errors.New(errContentPartTooLarge)
		}
		return normalizeImagePart(part)
	case gwproto.PartTypeAudio, gwproto.PartTypeVoice:
		if part.Audio != nil &&
			len(part.Audio.Data) > 0 &&
			int64(len(part.Audio.Data)) > maxBytes {
			return nil, "", errors.New(errContentPartTooLarge)
		}
		return s.normalizeAudioPart(ctx, part)
	case gwproto.PartTypeFile, gwproto.PartTypeVideo:
		if part.File != nil &&
			len(part.File.Data) > 0 &&
			int64(len(part.File.Data)) > maxBytes {
			return nil, "", errors.New(errContentPartTooLarge)
		}
		return s.normalizeFilePart(ctx, part)
	case gwproto.PartTypeLink:
		return normalizeLinkPart(part)
	case gwproto.PartTypeLocation:
		return normalizeLocationPart(part)
	default:
		return nil, "", errors.New("unsupported content part type")
	}
}

func normalizeTextPart(
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	if part.Text == nil {
		return nil, "", errors.New("missing text")
	}
	text := strings.TrimSpace(*part.Text)
	if text == "" {
		return nil, "", errors.New("empty text")
	}
	return &model.ContentPart{
		Type: model.ContentTypeText,
		Text: &text,
	}, text, nil
}

func normalizeImagePart(
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	if part.Image == nil {
		return nil, "", errors.New("missing image")
	}
	detail := strings.TrimSpace(part.Image.Detail)
	if detail == "" {
		detail = imageDetailAuto
	}

	if strings.TrimSpace(part.Image.URL) != "" {
		return &model.ContentPart{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				URL:    strings.TrimSpace(part.Image.URL),
				Detail: detail,
			},
		}, "", nil
	}

	if len(part.Image.Data) == 0 {
		return nil, "", errors.New("missing image url or data")
	}
	format := strings.TrimSpace(part.Image.Format)
	if format == "" {
		return nil, "", errors.New("missing image format")
	}
	return &model.ContentPart{
		Type: model.ContentTypeImage,
		Image: &model.Image{
			Data:   part.Image.Data,
			Detail: detail,
			Format: format,
		},
	}, "", nil
}

func (s *Server) normalizeAudioPart(
	ctx context.Context,
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	if part.Audio == nil {
		return nil, "", errors.New("missing audio")
	}

	if strings.TrimSpace(part.Audio.URL) != "" {
		return s.normalizeAudioURL(ctx, part.Audio)
	}
	if len(part.Audio.Data) == 0 {
		return nil, "", errors.New("missing audio url or data")
	}
	format := strings.TrimSpace(part.Audio.Format)
	if format == "" {
		return nil, "", errors.New("missing audio format")
	}
	if !isSupportedAudioFormat(format) {
		return nil, "", errors.New("unsupported audio format")
	}
	return &model.ContentPart{
		Type: model.ContentTypeAudio,
		Audio: &model.Audio{
			Data:   part.Audio.Data,
			Format: format,
		},
	}, "", nil
}

func (s *Server) normalizeAudioURL(
	ctx context.Context,
	audio *gwproto.AudioPart,
) (*model.ContentPart, string, error) {
	f, err := s.fetchContentPart(ctx, audio.URL)
	if err != nil {
		return nil, "", err
	}
	format := strings.TrimSpace(audio.Format)
	if format == "" {
		format = inferAudioFormat(f)
	}
	if format == "" {
		return nil, "", errors.New("missing audio format")
	}
	if !isSupportedAudioFormat(format) {
		return nil, "", errors.New("unsupported audio format")
	}
	return &model.ContentPart{
		Type: model.ContentTypeAudio,
		Audio: &model.Audio{
			Data:   f.Data,
			Format: format,
		},
	}, "", nil
}

func inferAudioFormat(f fetched) string {
	ext := strings.ToLower(path.Ext(f.Filename))
	switch ext {
	case ".wav":
		return audioFormatWAV
	case ".mp3":
		return audioFormatMP3
	}
	switch normalizeContentType(f.ContentType) {
	case "audio/wav", "audio/x-wav":
		return audioFormatWAV
	case "audio/mpeg", "audio/mp3":
		return audioFormatMP3
	}
	return ""
}

func isSupportedAudioFormat(format string) bool {
	switch format {
	case audioFormatWAV, audioFormatMP3:
		return true
	default:
		return false
	}
}

func (s *Server) normalizeFilePart(
	ctx context.Context,
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	if part.File == nil {
		return nil, "", errors.New("missing file")
	}

	if strings.TrimSpace(part.File.FileID) != "" {
		return normalizeFileID(part.File), "", nil
	}
	if strings.TrimSpace(part.File.URL) != "" {
		return s.normalizeFileURL(ctx, part.File)
	}
	if len(part.File.Data) == 0 {
		return nil, "", errors.New("missing file url, data, or file_id")
	}
	return normalizeFileData(part.File)
}

func normalizeFileID(file *gwproto.FilePart) *model.ContentPart {
	name := strings.TrimSpace(file.Filename)
	id := strings.TrimSpace(file.FileID)
	return &model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{
			Name:   name,
			FileID: id,
		},
	}
}

func (s *Server) normalizeFileURL(
	ctx context.Context,
	file *gwproto.FilePart,
) (*model.ContentPart, string, error) {
	f, err := s.fetchContentPart(ctx, file.URL)
	if err != nil {
		return nil, "", err
	}
	name := strings.TrimSpace(file.Filename)
	if name == "" {
		name = strings.TrimSpace(f.Filename)
	}
	if name == "" {
		name = "attachment"
	}
	mimeType := normalizeContentType(file.Format)
	if mimeType == "" {
		mimeType = normalizeContentType(f.ContentType)
	}
	if mimeType == "" {
		mimeType = inferMimeTypeFromName(name)
	}
	if mimeType == "" {
		mimeType = mimeOctetStream
	}
	return &model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{
			Name:     name,
			Data:     f.Data,
			MimeType: mimeType,
		},
	}, "", nil
}

func normalizeFileData(
	file *gwproto.FilePart,
) (*model.ContentPart, string, error) {
	name := strings.TrimSpace(file.Filename)
	if name == "" {
		return nil, "", errors.New("missing filename")
	}
	mimeType := normalizeContentType(file.Format)
	if mimeType == "" {
		mimeType = inferMimeTypeFromName(name)
	}
	if mimeType == "" {
		mimeType = mimeOctetStream
	}
	return &model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{
			Name:     name,
			Data:     file.Data,
			MimeType: mimeType,
		},
	}, "", nil
}

func inferMimeTypeFromName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return ""
	}
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return ""
	}
	return normalizeContentType(mimeType)
}

func normalizeLinkPart(
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	if part.Link == nil {
		return nil, "", errors.New("missing link")
	}
	linkURL := strings.TrimSpace(part.Link.URL)
	if linkURL == "" {
		return nil, "", errors.New("missing link url")
	}
	title := strings.TrimSpace(part.Link.Title)
	text := strings.TrimSpace(joinText(title, linkURL))
	if text == "" {
		return nil, "", errors.New("empty link")
	}
	return &model.ContentPart{
		Type: model.ContentTypeText,
		Text: &text,
	}, text, nil
}

func normalizeLocationPart(
	part gwproto.ContentPart,
) (*model.ContentPart, string, error) {
	if part.Location == nil {
		return nil, "", errors.New("missing location")
	}
	name := strings.TrimSpace(part.Location.Name)
	location := fmt.Sprintf(
		"%s\nlatitude=%v\nlongitude=%v",
		name,
		part.Location.Latitude,
		part.Location.Longitude,
	)
	location = strings.TrimSpace(location)
	return &model.ContentPart{
		Type: model.ContentTypeText,
		Text: &location,
	}, location, nil
}

func (s *Server) fetchContentPart(
	ctx context.Context,
	rawURL string,
) (fetched, error) {
	fetcher := s.partFetcher
	if fetcher == nil {
		fetcher = newURLPartFetcher(partURLPolicy{})
	}
	maxBytes := s.maxPartBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxContentPartBytes
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fetched{}, errors.New("missing url")
	}
	return fetcher.Fetch(ctx, rawURL, maxBytes)
}
