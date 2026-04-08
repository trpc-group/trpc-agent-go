//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package debugrecorder provides an opt-in, file-based debug recorder
// for OpenClaw runtime and channels.
package debugrecorder

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

const (
	modeFull Mode = "full"
	modeSafe Mode = "safe"

	defaultDateDirLayout = "20060102"
	defaultTimeLayout    = "150405"

	defaultTraceDirPerm = 0o700
	defaultFilePerm     = 0o600

	defaultAttachmentsDir = "attachments"
	defaultBySessionDir   = "by-session"

	eventsFileName     = "events.jsonl"
	eventsGzipFileName = eventsFileName + ".gz"
	metaFileName       = "meta.json"
	resultFileName     = "result.json"
	traceRefName       = "trace.json"

	KindTraceStart  = "trace.start"
	KindTraceEnd    = "trace.end"
	KindText        = "text"
	KindError       = "error"
	KindGatewayReq  = "gateway.request"
	KindGatewayRsp  = "gateway.response"
	KindGatewayRun  = "gateway.run.start"
	KindModelReq    = "model.chat.request"
	KindRunnerEvent = "runner.event"

	ProviderOpenAIChatCompletions = "openai.chat.completions"

	KindTelegramMessage    = "telegram.message"
	KindTelegramAttachment = "telegram.attachment"

	errEmptyDir  = "debug recorder: empty dir"
	errEmptyKind = "debug trace: empty kind"

	modelRequestDataURLPrefix   = "data:"
	modelRequestBase64Delimiter = ";base64,"
	modelRequestDefaultMIMEType = "application/octet-stream"
	modelRequestInlineBlobName  = "inline"
	modelRequestFieldBlob       = "blob"
	modelRequestFieldData       = "data"
	modelRequestFieldFileData   = "file_data"
	modelRequestFieldMIMEType   = "mime_type"
	modelRequestFieldURL        = "url"

	maxTraceBaseLen     = 96
	maxSafeComponentLen = 64
	traceSuffixBytes    = 4

	gzipCompressionLevel = gzip.DefaultCompression
)

type Mode string

func ParseMode(raw string) (Mode, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return modeFull, nil
	}
	switch Mode(v) {
	case modeFull, modeSafe:
		return Mode(v), nil
	default:
		return "", fmt.Errorf("debug recorder: unsupported mode: %s", raw)
	}
}

type Recorder struct {
	dir  string
	mode Mode
	now  func() time.Time
}

func New(dir string, mode Mode) (*Recorder, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New(errEmptyDir)
	}
	if mode == "" {
		mode = modeFull
	}
	if mode != modeFull && mode != modeSafe {
		return nil, fmt.Errorf("debug recorder: unsupported mode: %s", mode)
	}
	if err := os.MkdirAll(dir, defaultTraceDirPerm); err != nil {
		return nil, fmt.Errorf("debug recorder: mkdir: %w", err)
	}
	return &Recorder{
		dir:  dir,
		mode: mode,
		now:  time.Now,
	}, nil
}

func (r *Recorder) Dir() string {
	if r == nil {
		return ""
	}
	return r.dir
}

func (r *Recorder) Mode() Mode {
	if r == nil {
		return ""
	}
	return r.mode
}

type TraceStart struct {
	AppName   string `json:"app_name,omitempty"`
	Channel   string `json:"channel,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	Source    string `json:"source,omitempty"`
}

type TraceEnd struct {
	Status   string        `json:"status,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	Error    string        `json:"error,omitempty"`
}

type Trace struct {
	root string
	mode Mode

	startedAt time.Time
	metaPath  string
	traceRef  string
	traceID   string

	mu     sync.Mutex
	events *os.File
	closed bool
}

func (r *Recorder) Start(start TraceStart) (*Trace, error) {
	if r == nil {
		return nil, errors.New("debug recorder: nil")
	}

	now := r.now()
	root, err := r.newTraceDir(now, start)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, defaultTraceDirPerm); err != nil {
		return nil, fmt.Errorf("debug recorder: mkdir trace: %w", err)
	}
	traceRef, err := r.writeSessionIndex(root, now, start)
	if err != nil {
		return nil, err
	}

	meta := struct {
		StartedAt time.Time  `json:"started_at"`
		Mode      Mode       `json:"mode"`
		Start     TraceStart `json:"start"`
		TraceID   string     `json:"trace_id,omitempty"`
		Version   string     `json:"version"`
	}{
		StartedAt: now,
		Mode:      r.mode,
		Start:     start,
		TraceID:   strings.TrimSpace(start.TraceID),
		Version:   "v1",
	}
	metaPath := filepath.Join(root, metaFileName)
	if err := writeJSONFile(metaPath, meta); err != nil {
		return nil, err
	}

	events, err := os.OpenFile(
		filepath.Join(root, eventsFileName),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		defaultFilePerm,
	)
	if err != nil {
		return nil, fmt.Errorf("debug recorder: open events: %w", err)
	}

	t := &Trace{
		root:      root,
		mode:      r.mode,
		startedAt: now,
		metaPath:  metaPath,
		traceRef:  traceRef,
		traceID:   strings.TrimSpace(start.TraceID),
		events:    events,
	}
	_ = t.Record(KindTraceStart, start)
	return t, nil
}

func (r *Recorder) newTraceDir(
	now time.Time,
	start TraceStart,
) (string, error) {
	dateDir := now.Format(defaultDateDirLayout)
	timeDir := now.Format(defaultTimeLayout)

	channel := safeComponent(start.Channel)
	if channel == "" {
		channel = "unknown"
	}
	req := safeComponent(start.RequestID)
	if req == "" {
		req = "request"
	}

	base := fmt.Sprintf("%s_%s_%s", timeDir, channel, req)
	base = strings.Trim(base, "._-")
	if base == "" {
		base = timeDir
	}
	if len(base) > maxTraceBaseLen {
		base = base[:maxTraceBaseLen]
	}

	dir := filepath.Join(r.dir, dateDir, base)
	if _, err := os.Stat(dir); err != nil && os.IsNotExist(err) {
		return dir, nil
	}
	suffix, err := randomHex(traceSuffixBytes)
	if err != nil {
		return "", err
	}
	return filepath.Join(r.dir, dateDir, base+"_"+suffix), nil
}

type traceRef struct {
	TraceDir  string    `json:"trace_dir"`
	StartedAt time.Time `json:"started_at"`
	Channel   string    `json:"channel,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

func (r *Recorder) writeSessionIndex(
	root string,
	now time.Time,
	start TraceStart,
) (string, error) {
	indexRoot, err := r.newSessionIndexDir(now, start)
	if err != nil {
		return "", err
	}
	if indexRoot == "" {
		return "", nil
	}
	if err := os.MkdirAll(indexRoot, defaultTraceDirPerm); err != nil {
		return "", fmt.Errorf(
			"debug recorder: mkdir session index: %w",
			err,
		)
	}
	rel, err := filepath.Rel(indexRoot, root)
	if err != nil {
		return "", fmt.Errorf(
			"debug recorder: session index rel: %w",
			err,
		)
	}
	ref := traceRef{
		TraceDir:  rel,
		StartedAt: now,
		Channel:   strings.TrimSpace(start.Channel),
		SessionID: strings.TrimSpace(start.SessionID),
		RequestID: strings.TrimSpace(start.RequestID),
		MessageID: strings.TrimSpace(start.MessageID),
		TraceID:   strings.TrimSpace(start.TraceID),
	}
	refPath := filepath.Join(indexRoot, traceRefName)
	if err := writeJSONFile(refPath, ref); err != nil {
		return "", err
	}
	return refPath, nil
}

func (r *Recorder) newSessionIndexDir(
	now time.Time,
	start TraceStart,
) (string, error) {
	session := sessionIndexComponent(start)
	if session == "" {
		return "", nil
	}
	dateDir := now.Format(defaultDateDirLayout)
	base := sessionIndexBase(now, start)
	dir := filepath.Join(r.dir, defaultBySessionDir, session, dateDir, base)
	if _, err := os.Stat(dir); err != nil && os.IsNotExist(err) {
		return dir, nil
	}
	suffix, err := randomHex(traceSuffixBytes)
	if err != nil {
		return "", err
	}
	return dir + "_" + suffix, nil
}

func sessionIndexComponent(start TraceStart) string {
	if session := safeComponent(start.SessionID); session != "" {
		return session
	}
	if user := safeComponent(start.UserID); user != "" {
		return "user_" + user
	}
	if req := safeComponent(start.RequestID); req != "" {
		return "request_" + req
	}
	return ""
}

func sessionIndexBase(now time.Time, start TraceStart) string {
	var parts []string
	parts = append(parts, now.Format(defaultTimeLayout))
	if msg := safeComponent(start.MessageID); msg != "" {
		parts = append(parts, msg)
	} else if req := safeComponent(start.RequestID); req != "" {
		parts = append(parts, req)
	}
	base := strings.Join(parts, "_")
	base = strings.Trim(base, "._-")
	if len(base) > maxTraceBaseLen {
		base = base[:maxTraceBaseLen]
	}
	if base == "" {
		return now.Format(defaultTimeLayout)
	}
	return base
}

func (t *Trace) Dir() string {
	if t == nil {
		return ""
	}
	return t.root
}

func (t *Trace) Mode() Mode {
	if t == nil {
		return ""
	}
	return t.mode
}

type record struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Payload any       `json:"payload,omitempty"`
}

func (t *Trace) Record(kind string, payload any) error {
	if t == nil {
		return nil
	}
	if strings.TrimSpace(kind) == "" {
		return errors.New(errEmptyKind)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	rec := record{
		Time:    time.Now(),
		Kind:    kind,
		Payload: payload,
	}
	enc := json.NewEncoder(t.events)
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("debug trace: write record: %w", err)
	}
	return nil
}

func (t *Trace) RecordText(text string) error {
	return t.Record(KindText, strings.TrimSpace(text))
}

func (t *Trace) RecordError(err error) error {
	if err == nil {
		return nil
	}
	return t.Record(KindError, err.Error())
}

type BlobRef struct {
	Ref    string `json:"ref,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int    `json:"size"`
	Name   string `json:"name,omitempty"`
}

type RequestSummary struct {
	Channel   string `json:"channel,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text,omitempty"`

	RequestSystemPrompt string `json:"request_system_prompt,omitempty"`

	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`

	ContentParts []ContentPartSummary `json:"content_parts,omitempty"`
}

type ContentPartSummary struct {
	Type string `json:"type,omitempty"`

	Text     string                `json:"text,omitempty"`
	Image    *ImagePartSummary     `json:"image,omitempty"`
	Audio    *AudioPartSummary     `json:"audio,omitempty"`
	File     *FilePartSummary      `json:"file,omitempty"`
	Location *gwproto.LocationPart `json:"location,omitempty"`
	Link     *gwproto.LinkPart     `json:"link,omitempty"`
}

type ImagePartSummary struct {
	URL    string  `json:"url,omitempty"`
	Data   BlobRef `json:"data,omitempty"`
	Detail string  `json:"detail,omitempty"`
	Format string  `json:"format,omitempty"`
}

type AudioPartSummary struct {
	URL    string  `json:"url,omitempty"`
	Data   BlobRef `json:"data,omitempty"`
	Format string  `json:"format,omitempty"`
}

type FilePartSummary struct {
	Filename string  `json:"filename,omitempty"`
	Data     BlobRef `json:"data,omitempty"`
	FileID   string  `json:"file_id,omitempty"`
	Format   string  `json:"format,omitempty"`
	URL      string  `json:"url,omitempty"`
}

func SummarizeRequest(
	t *Trace,
	req gwproto.MessageRequest,
) (RequestSummary, error) {
	out := RequestSummary{
		Channel:   strings.TrimSpace(req.Channel),
		From:      strings.TrimSpace(req.From),
		To:        strings.TrimSpace(req.To),
		Thread:    strings.TrimSpace(req.Thread),
		MessageID: strings.TrimSpace(req.MessageID),
		Text:      strings.TrimSpace(req.Text),
		RequestSystemPrompt: strings.TrimSpace(
			req.RequestSystemPrompt,
		),
		UserID:    strings.TrimSpace(req.UserID),
		SessionID: strings.TrimSpace(req.SessionID),
		RequestID: strings.TrimSpace(req.RequestID),
	}

	if len(req.ContentParts) == 0 {
		return out, nil
	}

	out.ContentParts = make([]ContentPartSummary, 0, len(req.ContentParts))
	for i := range req.ContentParts {
		part := req.ContentParts[i]
		entry := ContentPartSummary{
			Type: strings.TrimSpace(string(part.Type)),
		}

		if part.Text != nil {
			entry.Text = strings.TrimSpace(*part.Text)
		}
		if part.Image != nil {
			entry.Image = &ImagePartSummary{
				URL:    strings.TrimSpace(part.Image.URL),
				Detail: strings.TrimSpace(part.Image.Detail),
				Format: strings.TrimSpace(part.Image.Format),
			}
			if len(part.Image.Data) > 0 {
				name := fmt.Sprintf("image_%d", i)
				if entry.Image.Format != "" {
					name = name + "." + entry.Image.Format
				}
				ref, err := t.StoreBlob(name, part.Image.Data)
				if err != nil {
					return RequestSummary{}, err
				}
				entry.Image.Data = ref
			}
		}
		if part.Audio != nil {
			entry.Audio = &AudioPartSummary{
				URL:    strings.TrimSpace(part.Audio.URL),
				Format: strings.TrimSpace(part.Audio.Format),
			}
			if len(part.Audio.Data) > 0 {
				name := fmt.Sprintf("audio_%d", i)
				if entry.Audio.Format != "" {
					name = name + "." + entry.Audio.Format
				}
				ref, err := t.StoreBlob(name, part.Audio.Data)
				if err != nil {
					return RequestSummary{}, err
				}
				entry.Audio.Data = ref
			}
		}
		if part.File != nil {
			entry.File = &FilePartSummary{
				Filename: strings.TrimSpace(part.File.Filename),
				FileID:   strings.TrimSpace(part.File.FileID),
				Format:   strings.TrimSpace(part.File.Format),
				URL:      strings.TrimSpace(part.File.URL),
			}
			if len(part.File.Data) > 0 {
				name := entry.File.Filename
				if name == "" {
					name = fmt.Sprintf("file_%d", i)
				}
				ref, err := t.StoreBlob(name, part.File.Data)
				if err != nil {
					return RequestSummary{}, err
				}
				entry.File.Data = ref
			}
		}
		if part.Location != nil {
			entry.Location = part.Location
		}
		if part.Link != nil {
			entry.Link = part.Link
		}

		out.ContentParts = append(out.ContentParts, entry)
	}
	return out, nil
}

func (t *Trace) StoreBlob(name string, data []byte) (BlobRef, error) {
	if t == nil {
		return BlobRef{}, nil
	}
	sum := sha256.Sum256(data)
	shaHex := hex.EncodeToString(sum[:])

	ref := BlobRef{
		SHA256: shaHex,
		Size:   len(data),
		Name:   strings.TrimSpace(name),
	}
	if t.mode == modeSafe || len(data) == 0 {
		return ref, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ref, nil
	}

	rel := filepath.Join(defaultAttachmentsDir, shaHex)
	dst := filepath.Join(t.root, rel)
	ref.Ref = rel

	if _, err := os.Stat(dst); err == nil {
		return ref, nil
	}
	if err := os.MkdirAll(
		filepath.Dir(dst),
		defaultTraceDirPerm,
	); err != nil {
		return BlobRef{}, fmt.Errorf("debug trace: mkdir: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), "blob-*")
	if err != nil {
		return BlobRef{}, fmt.Errorf("debug trace: temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return BlobRef{}, fmt.Errorf("debug trace: write blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return BlobRef{}, fmt.Errorf("debug trace: close blob: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Remove(tmp.Name())
		return BlobRef{}, fmt.Errorf("debug trace: rename blob: %w", err)
	}
	return ref, nil
}

func (t *Trace) Close(end TraceEnd) error {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	if t.events != nil {
		enc := json.NewEncoder(t.events)
		_ = enc.Encode(record{
			Time:    time.Now(),
			Kind:    KindTraceEnd,
			Payload: end,
		})
	}

	_ = writeJSONFile(filepath.Join(t.root, resultFileName), end)

	if t.events == nil {
		return nil
	}
	eventsPath := filepath.Join(t.root, eventsFileName)
	err := t.events.Close()
	t.events = nil
	if err != nil {
		return err
	}
	return compressEventsFile(eventsPath)
}

func (t *Trace) SetTraceID(traceID string) error {
	if t == nil {
		return nil
	}

	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.traceID == traceID {
		return nil
	}

	if err := writeTraceIDJSON(t.metaPath, traceID); err != nil {
		return err
	}
	if err := writeTraceIDJSON(t.traceRef, traceID); err != nil {
		return err
	}
	t.traceID = traceID
	return nil
}

type traceKey struct{}
type recorderKey struct{}

func WithTrace(ctx context.Context, t *Trace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, traceKey{}, t)
}

func TraceFromContext(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(traceKey{}).(*Trace)
	return v
}

func WithRecorder(ctx context.Context, r *Recorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderKey{}, r)
}

func RecorderFromContext(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(recorderKey{}).(*Recorder)
	return v
}

type modelRequestRecord struct {
	Provider string `json:"provider,omitempty"`
	Request  any    `json:"request,omitempty"`
}

type inlineDataSummary struct {
	MIMEType string  `json:"mime_type,omitempty"`
	Blob     BlobRef `json:"blob,omitempty"`
}

func RecordModelRequest(
	ctx context.Context,
	provider string,
	payload any,
) error {
	trace := TraceFromContext(ctx)
	provider = strings.TrimSpace(provider)
	if trace == nil || provider == "" || payload == nil {
		return nil
	}

	sanitized, err := sanitizeModelRequestPayload(
		trace,
		provider,
		payload,
	)
	if err != nil {
		return err
	}

	return trace.Record(
		KindModelReq,
		modelRequestRecord{
			Provider: provider,
			Request:  sanitized,
		},
	)
}

type modelRequestPayloadSanitizer struct {
	trace           *Trace
	inlineBlobCount int
}

func sanitizeModelRequestPayload(
	trace *Trace,
	provider string,
	payload any,
) (any, error) {
	switch provider {
	case ProviderOpenAIChatCompletions:
		sanitizer := &modelRequestPayloadSanitizer{trace: trace}
		return sanitizer.walk(payload)
	default:
		return payload, nil
	}
}

func (s *modelRequestPayloadSanitizer) walk(
	value any,
) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			replaced, ok, err := s.replaceInlineData(
				key,
				child,
			)
			if err != nil {
				return nil, err
			}
			if ok {
				out[key] = replaced
				continue
			}

			next, err := s.walk(child)
			if err != nil {
				return nil, err
			}
			out[key] = next
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i := range v {
			next, err := s.walk(v[i])
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	default:
		return value, nil
	}
}

func (s *modelRequestPayloadSanitizer) replaceInlineData(
	key string,
	value any,
) (any, bool, error) {
	if !isInlineDataField(key) {
		return nil, false, nil
	}

	raw, ok := value.(string)
	if !ok {
		return nil, false, nil
	}

	mimeType, data, ok := parseDataURL(raw)
	if !ok {
		return nil, false, nil
	}

	ref, err := s.trace.StoreBlob(
		s.nextInlineBlobName(mimeType),
		data,
	)
	if err != nil {
		return nil, false, err
	}

	return inlineDataSummary{
		MIMEType: mimeType,
		Blob:     ref,
	}, true, nil
}

func (s *modelRequestPayloadSanitizer) nextInlineBlobName(
	mimeType string,
) string {
	s.inlineBlobCount++
	name := modelRequestInlineBlobName + "_" +
		strconv.Itoa(s.inlineBlobCount)
	if ext := fileExtForMIMEType(mimeType); ext != "" {
		name += ext
	}
	return name
}

func isInlineDataField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case modelRequestFieldData,
		modelRequestFieldFileData,
		modelRequestFieldURL:
		return true
	default:
		return false
	}
}

func parseDataURL(raw string) (string, []byte, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, modelRequestDataURLPrefix) {
		return "", nil, false
	}

	header, encoded, ok := strings.Cut(raw, ",")
	if !ok {
		return "", nil, false
	}
	if !strings.HasSuffix(header, modelRequestBase64Delimiter[:len(
		modelRequestBase64Delimiter,
	)-1]) {
		return "", nil, false
	}

	mimeType := strings.TrimPrefix(header, modelRequestDataURLPrefix)
	mimeType = strings.TrimSuffix(
		mimeType,
		modelRequestBase64Delimiter[:len(
			modelRequestBase64Delimiter,
		)-1],
	)
	if strings.TrimSpace(mimeType) == "" {
		mimeType = modelRequestDefaultMIMEType
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, false
	}
	return mimeType, data, true
}

func fileExtForMIMEType(mimeType string) string {
	exts, err := mime.ExtensionsByType(
		strings.TrimSpace(mimeType),
	)
	if err != nil || len(exts) == 0 {
		return ""
	}
	return exts[0]
}

func writeJSONFile(path string, v any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("debug recorder: empty json path")
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("debug recorder: marshal: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, defaultFilePerm); err != nil {
		return fmt.Errorf("debug recorder: write file: %w", err)
	}
	return nil
}

func ResolveEventsFilePath(traceDir string) (string, bool, error) {
	traceDir = strings.TrimSpace(traceDir)
	if traceDir == "" {
		return "", false, errors.New(
			"debug recorder: empty trace dir",
		)
	}

	eventsPath := filepath.Join(traceDir, eventsFileName)
	info, err := os.Stat(eventsPath)
	switch {
	case err == nil && !info.IsDir():
		return eventsPath, false, nil
	case err == nil:
		return "", false, fmt.Errorf(
			"debug recorder: events path is a directory",
		)
	case !errors.Is(err, os.ErrNotExist):
		return "", false, fmt.Errorf(
			"debug recorder: stat events: %w",
			err,
		)
	}

	gzipPath := filepath.Join(traceDir, eventsGzipFileName)
	info, err = os.Stat(gzipPath)
	switch {
	case err == nil && !info.IsDir():
		return gzipPath, true, nil
	case err == nil:
		return "", false, fmt.Errorf(
			"debug recorder: compressed events path is a directory",
		)
	case errors.Is(err, os.ErrNotExist):
		return "", false, fmt.Errorf(
			"debug recorder: events file not found",
		)
	default:
		return "", false, fmt.Errorf(
			"debug recorder: stat compressed events: %w",
			err,
		)
	}
}

func ReadEventsFile(traceDir string) ([]byte, error) {
	path, compressed, err := ResolveEventsFilePath(traceDir)
	if err != nil {
		return nil, err
	}
	if !compressed {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf(
				"debug recorder: read events: %w",
				readErr,
			)
		}
		return raw, nil
	}
	return readGzipFile(path)
}

func compressEventsFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("debug recorder: empty events path")
	}

	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("debug recorder: open events: %w", err)
	}
	defer src.Close()

	temp, err := os.CreateTemp(
		filepath.Dir(path),
		eventsGzipFileName+".*.tmp",
	)
	if err != nil {
		return fmt.Errorf(
			"debug recorder: create compressed events: %w",
			err,
		)
	}

	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		if keepTemp {
			return
		}
		_ = os.Remove(tempPath)
	}()

	hasher := sha256.New()
	writer, err := gzip.NewWriterLevel(
		temp,
		gzipCompressionLevel,
	)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf(
			"debug recorder: create gzip writer: %w",
			err,
		)
	}

	size, err := io.Copy(writer, io.TeeReader(src, hasher))
	if err != nil {
		_ = writer.Close()
		_ = temp.Close()
		return fmt.Errorf(
			"debug recorder: gzip events: %w",
			err,
		)
	}
	if err := writer.Close(); err != nil {
		_ = temp.Close()
		return fmt.Errorf(
			"debug recorder: close gzip writer: %w",
			err,
		)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf(
			"debug recorder: close compressed events: %w",
			err,
		)
	}

	if err := verifyGzipFile(tempPath, hasher.Sum(nil), size); err != nil {
		return err
	}

	gzipPath := filepath.Join(
		filepath.Dir(path),
		eventsGzipFileName,
	)
	if err := os.Remove(gzipPath); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(
			"debug recorder: remove old compressed events: %w",
			err,
		)
	}
	if err := os.Rename(tempPath, gzipPath); err != nil {
		return fmt.Errorf(
			"debug recorder: rename compressed events: %w",
			err,
		)
	}
	keepTemp = true

	if err := os.Remove(path); err != nil {
		return fmt.Errorf(
			"debug recorder: remove raw events: %w",
			err,
		)
	}
	return nil
}

func verifyGzipFile(path string, wantHash []byte, wantSize int64) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf(
			"debug recorder: open compressed events: %w",
			err,
		)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf(
			"debug recorder: open gzip reader: %w",
			err,
		)
	}
	defer reader.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, reader)
	if err != nil {
		return fmt.Errorf(
			"debug recorder: verify compressed events: %w",
			err,
		)
	}
	if size != wantSize {
		return fmt.Errorf(
			"debug recorder: gzip verification size mismatch: "+
				"got %d want %d",
			size,
			wantSize,
		)
	}

	if !bytes.Equal(hasher.Sum(nil), wantHash) {
		return errors.New(
			"debug recorder: gzip verification hash mismatch",
		)
	}
	return nil
}

func readGzipFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf(
			"debug recorder: open compressed events: %w",
			err,
		)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf(
			"debug recorder: open gzip reader: %w",
			err,
		)
	}
	defer reader.Close()

	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf(
			"debug recorder: read compressed events: %w",
			err,
		)
	}
	return raw, nil
}

func writeTraceIDJSON(path string, traceID string) error {
	path = strings.TrimSpace(path)
	traceID = strings.TrimSpace(traceID)
	if path == "" || traceID == "" {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("debug recorder: read file: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("debug recorder: unmarshal json: %w", err)
	}
	payload["trace_id"] = traceID
	return writeJSONFile(path, payload)
}

func safeComponent(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "._-")
	if len(out) > maxSafeComponentLen {
		out = out[:maxSafeComponentLen]
	}
	return out
}

func randomHex(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", errors.New("debug recorder: invalid rand bytes")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("debug recorder: rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
