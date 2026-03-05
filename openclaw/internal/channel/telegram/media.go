//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	downloadFailedMessage = "Failed to download attachment."
	attachmentTooLargeMsg = "Attachment is too large to process."
	unsupportedMediaMsg   = "Unsupported attachment format."

	defaultAttachmentName = "attachment"
	defaultPhotoName      = "photo"
	defaultVoiceName      = "voice"
	defaultAudioName      = "audio"
	defaultVideoName      = "video"

	audioFormatWAV = "wav"
	audioFormatMP3 = "mp3"
)

const (
	attachmentKindPhoto    = "photo"
	attachmentKindDocument = "document"
	attachmentKindVideo    = "video"
	attachmentKindVoice    = "voice"
	attachmentKindAudio    = "audio"
)

type userError struct {
	userMessage string
	err         error
}

func (e *userError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *userError) Unwrap() error { return e.err }

func (c *Channel) buildGatewayRequest(
	ctx context.Context,
	fromID string,
	thread string,
	requestID string,
	msg tgapi.Message,
) (gwclient.MessageRequest, error) {
	maxBytes := c.maxDownloadBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownloadBytes
	}

	text := joinMessageText(msg.Text, msg.Caption)

	req := gwclient.MessageRequest{
		Channel:   channelID,
		From:      fromID,
		Thread:    thread,
		MessageID: strconv.Itoa(msg.MessageID),
		Text:      text,
		UserID:    fromID,
		RequestID: requestID,
	}

	parts := make([]gwproto.ContentPart, 0, 4)
	var err error

	parts, err = c.appendPhotoPart(ctx, parts, msg.Photo, maxBytes)
	if err != nil {
		return gwclient.MessageRequest{}, err
	}
	parts, err = c.appendDocumentPart(ctx, parts, msg.Document, maxBytes)
	if err != nil {
		return gwclient.MessageRequest{}, err
	}
	parts, err = c.appendVideoPart(ctx, parts, msg.Video, maxBytes)
	if err != nil {
		return gwclient.MessageRequest{}, err
	}
	parts, err = c.appendVoicePart(ctx, parts, msg.Voice, maxBytes)
	if err != nil {
		return gwclient.MessageRequest{}, err
	}
	parts, err = c.appendAudioPart(ctx, parts, msg.Audio, maxBytes)
	if err != nil {
		return gwclient.MessageRequest{}, err
	}

	req.ContentParts = parts
	if strings.TrimSpace(req.Text) == "" && len(req.ContentParts) == 0 {
		return gwclient.MessageRequest{}, errors.New("telegram: empty message")
	}
	return req, nil
}

func joinMessageText(a, b string) string {
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

func (c *Channel) appendPhotoPart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	photos []tgapi.PhotoSize,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if len(photos) == 0 {
		return parts, nil
	}

	trace := debugrecorder.TraceFromContext(ctx)

	p := photos[len(photos)-1]
	fileID := strings.TrimSpace(p.FileID)
	if fileID == "" {
		return parts, nil
	}
	if p.FileSize > maxBytes && p.FileSize > 0 {
		uerr := &userError{
			userMessage: attachmentTooLargeMsg,
			err:         tgapi.ErrFileTooLarge,
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindPhoto,
			FileID:       fileID,
			ReportedSize: p.FileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	file, data, err := c.bot.DownloadFileByID(ctx, fileID, maxBytes)
	if err != nil {
		mapped := mapDownloadError(err)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindPhoto,
			FileID:       fileID,
			ReportedSize: p.FileSize,
			UserMessage:  userMessageFromErr(mapped),
			Error:        err.Error(),
		})
		return nil, mapped
	}

	format := inferImageFormat(file.FilePath, data)
	if format == "" {
		uerr := &userError{
			userMessage: unsupportedMediaMsg,
			err:         errors.New("telegram: empty image format"),
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindPhoto,
			FileID:       fileID,
			ReportedSize: p.FileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	name := defaultPhotoName + "." + format
	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindPhoto,
		FileID:       fileID,
		Name:         name,
		Format:       format,
		ReportedSize: p.FileSize,
		Blob:         ref,
	})

	parts = append(parts, gwproto.ContentPart{
		Type: gwproto.PartTypeImage,
		Image: &gwproto.ImagePart{
			Data:   data,
			Format: format,
		},
	})
	return parts, nil
}

func (c *Channel) appendDocumentPart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	doc *tgapi.Document,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if doc == nil {
		return parts, nil
	}

	trace := debugrecorder.TraceFromContext(ctx)

	fileID := strings.TrimSpace(doc.FileID)
	if fileID == "" {
		return parts, nil
	}
	if doc.FileSize > maxBytes && doc.FileSize > 0 {
		uerr := &userError{
			userMessage: attachmentTooLargeMsg,
			err:         tgapi.ErrFileTooLarge,
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindDocument,
			FileID:       fileID,
			Name:         strings.TrimSpace(doc.FileName),
			MimeType:     strings.TrimSpace(doc.MimeType),
			ReportedSize: doc.FileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	file, data, err := c.bot.DownloadFileByID(ctx, fileID, maxBytes)
	if err != nil {
		mapped := mapDownloadError(err)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindDocument,
			FileID:       fileID,
			Name:         strings.TrimSpace(doc.FileName),
			MimeType:     strings.TrimSpace(doc.MimeType),
			ReportedSize: doc.FileSize,
			UserMessage:  userMessageFromErr(mapped),
			Error:        err.Error(),
		})
		return nil, mapped
	}

	name := fallbackFilename(doc.FileName, file.FilePath, defaultAttachmentName)
	mimeType := strings.TrimSpace(doc.MimeType)

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindDocument,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: doc.FileSize,
		Blob:         ref,
	})

	parts = append(parts, gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			Filename: name,
			Data:     data,
			Format:   mimeType,
		},
	})
	return parts, nil
}

func (c *Channel) appendVideoPart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	video *tgapi.Video,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if video == nil {
		return parts, nil
	}

	trace := debugrecorder.TraceFromContext(ctx)

	fileID := strings.TrimSpace(video.FileID)
	if fileID == "" {
		return parts, nil
	}
	if video.FileSize > maxBytes && video.FileSize > 0 {
		uerr := &userError{
			userMessage: attachmentTooLargeMsg,
			err:         tgapi.ErrFileTooLarge,
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindVideo,
			FileID:       fileID,
			Name:         strings.TrimSpace(video.FileName),
			MimeType:     strings.TrimSpace(video.MimeType),
			ReportedSize: video.FileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	file, data, err := c.bot.DownloadFileByID(ctx, fileID, maxBytes)
	if err != nil {
		mapped := mapDownloadError(err)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindVideo,
			FileID:       fileID,
			Name:         strings.TrimSpace(video.FileName),
			MimeType:     strings.TrimSpace(video.MimeType),
			ReportedSize: video.FileSize,
			UserMessage:  userMessageFromErr(mapped),
			Error:        err.Error(),
		})
		return nil, mapped
	}

	name := fallbackFilename(video.FileName, file.FilePath, defaultVideoName)
	mimeType := strings.TrimSpace(video.MimeType)

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindVideo,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: video.FileSize,
		Blob:         ref,
	})

	parts = append(parts, gwproto.ContentPart{
		Type: gwproto.PartTypeVideo,
		File: &gwproto.FilePart{
			Filename: name,
			Data:     data,
			Format:   mimeType,
		},
	})
	return parts, nil
}

func (c *Channel) appendVoicePart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	voice *tgapi.Voice,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if voice == nil {
		return parts, nil
	}

	trace := debugrecorder.TraceFromContext(ctx)

	fileID := strings.TrimSpace(voice.FileID)
	if fileID == "" {
		return parts, nil
	}
	if voice.FileSize > maxBytes && voice.FileSize > 0 {
		uerr := &userError{
			userMessage: attachmentTooLargeMsg,
			err:         tgapi.ErrFileTooLarge,
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindVoice,
			FileID:       fileID,
			MimeType:     strings.TrimSpace(voice.MimeType),
			ReportedSize: voice.FileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	file, data, err := c.bot.DownloadFileByID(ctx, fileID, maxBytes)
	if err != nil {
		mapped := mapDownloadError(err)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindVoice,
			FileID:       fileID,
			MimeType:     strings.TrimSpace(voice.MimeType),
			ReportedSize: voice.FileSize,
			UserMessage:  userMessageFromErr(mapped),
			Error:        err.Error(),
		})
		return nil, mapped
	}

	name := fallbackFilename("", file.FilePath, defaultVoiceName)
	mimeType := strings.TrimSpace(voice.MimeType)

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindVoice,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: voice.FileSize,
		Blob:         ref,
	})

	parts = append(parts, gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			Filename: name,
			Data:     data,
			Format:   mimeType,
		},
	})
	return parts, nil
}

func (c *Channel) appendAudioPart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	audio *tgapi.Audio,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if audio == nil {
		return parts, nil
	}

	trace := debugrecorder.TraceFromContext(ctx)

	fileID := strings.TrimSpace(audio.FileID)
	if fileID == "" {
		return parts, nil
	}
	if audio.FileSize > maxBytes && audio.FileSize > 0 {
		uerr := &userError{
			userMessage: attachmentTooLargeMsg,
			err:         tgapi.ErrFileTooLarge,
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindAudio,
			FileID:       fileID,
			Name:         strings.TrimSpace(audio.FileName),
			MimeType:     strings.TrimSpace(audio.MimeType),
			ReportedSize: audio.FileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	file, data, err := c.bot.DownloadFileByID(ctx, fileID, maxBytes)
	if err != nil {
		mapped := mapDownloadError(err)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindAudio,
			FileID:       fileID,
			Name:         strings.TrimSpace(audio.FileName),
			MimeType:     strings.TrimSpace(audio.MimeType),
			ReportedSize: audio.FileSize,
			UserMessage:  userMessageFromErr(mapped),
			Error:        err.Error(),
		})
		return nil, mapped
	}

	format := inferAudioFormat(audio.FileName, file.FilePath, audio.MimeType)
	if format != "" {
		name := defaultAudioName + "." + format
		ref := storeBlob(trace, name, data)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindAudio,
			FileID:       fileID,
			Name:         name,
			Format:       format,
			MimeType:     strings.TrimSpace(audio.MimeType),
			ReportedSize: audio.FileSize,
			Blob:         ref,
		})
		parts = append(parts, gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				Data:   data,
				Format: format,
			},
		})
		return parts, nil
	}

	name := fallbackFilename(audio.FileName, file.FilePath, defaultAudioName)
	mimeType := strings.TrimSpace(audio.MimeType)

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindAudio,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: audio.FileSize,
		Blob:         ref,
	})

	parts = append(parts, gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			Filename: name,
			Data:     data,
			Format:   mimeType,
		},
	})
	return parts, nil
}

func mapDownloadError(err error) error {
	if errors.Is(err, tgapi.ErrFileTooLarge) {
		return &userError{
			userMessage: attachmentTooLargeMsg,
			err:         err,
		}
	}
	return &userError{
		userMessage: downloadFailedMessage,
		err:         err,
	}
}

type telegramAttachmentSummary struct {
	Kind         string                `json:"kind,omitempty"`
	FileID       string                `json:"file_id,omitempty"`
	Name         string                `json:"name,omitempty"`
	Format       string                `json:"format,omitempty"`
	MimeType     string                `json:"mime_type,omitempty"`
	ReportedSize int64                 `json:"reported_size,omitempty"`
	UserMessage  string                `json:"user_message,omitempty"`
	Error        string                `json:"error,omitempty"`
	Blob         debugrecorder.BlobRef `json:"blob,omitempty"`
}

func recordAttachment(
	trace *debugrecorder.Trace,
	summary telegramAttachmentSummary,
) {
	if trace == nil {
		return
	}
	_ = trace.Record(debugrecorder.KindTelegramAttachment, summary)
}

func storeBlob(
	trace *debugrecorder.Trace,
	name string,
	data []byte,
) debugrecorder.BlobRef {
	if trace == nil {
		return debugrecorder.BlobRef{}
	}
	ref, err := trace.StoreBlob(name, data)
	if err != nil {
		_ = trace.RecordError(err)
		return debugrecorder.BlobRef{}
	}
	return ref
}

func userMessageFromErr(err error) string {
	var uerr *userError
	if errors.As(err, &uerr) {
		return strings.TrimSpace(uerr.userMessage)
	}
	return ""
}

func fallbackFilename(primary, filePath, fallback string) string {
	name := strings.TrimSpace(primary)
	if name != "" {
		return name
	}
	base := strings.TrimSpace(path.Base(strings.TrimSpace(filePath)))
	if base != "" && base != "." && base != "/" {
		return base
	}
	return fallback
}

func inferImageFormat(filePath string, data []byte) string {
	if v := imageFormatFromExt(path.Ext(filePath)); v != "" {
		return v
	}
	if v := imageFormatFromContentType(http.DetectContentType(data)); v != "" {
		return v
	}
	return ""
}

func imageFormatFromExt(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".gif":
		return "gif"
	case ".webp":
		return "webp"
	default:
		return ""
	}
}

func imageFormatFromContentType(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if !strings.HasPrefix(ct, "image/") {
		return ""
	}
	format := strings.TrimPrefix(ct, "image/")
	format = strings.TrimSpace(format)
	if format == "jpg" {
		return "jpeg"
	}
	return format
}

func inferAudioFormat(filename, filePath, mimeType string) string {
	if v := audioFormatFromExt(path.Ext(filename)); v != "" {
		return v
	}
	if v := audioFormatFromExt(path.Ext(filePath)); v != "" {
		return v
	}
	return audioFormatFromMimeType(mimeType)
}

func audioFormatFromExt(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".wav":
		return audioFormatWAV
	case ".mp3":
		return audioFormatMP3
	default:
		return ""
	}
}

func audioFormatFromMimeType(mimeType string) string {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	switch mt {
	case "audio/wav", "audio/x-wav":
		return audioFormatWAV
	case "audio/mpeg", "audio/mp3":
		return audioFormatMP3
	default:
		return ""
	}
}
