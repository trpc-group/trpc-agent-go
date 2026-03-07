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
	"fmt"
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
	defaultDocumentName   = "document"
	defaultPhotoName      = "photo"
	defaultVoiceName      = "voice"
	defaultAudioName      = "audio"
	defaultVideoName      = "video"
	defaultAnimationName  = "animation"
	defaultVideoNoteName  = "video-note"

	audioFormatWAV = "wav"
	audioFormatMP3 = "mp3"

	mimeImageJPEG = "image/jpeg"
	mimeImagePNG  = "image/png"
	mimeImageWEBP = "image/webp"

	mimeAudioMPEG = "audio/mpeg"
	mimeAudioWAV  = "audio/wav"
	mimeAudioOGG  = "audio/ogg"
	mimeVideoMP4  = "video/mp4"
	mimeImageGIF  = "image/gif"

	bytesPerKiB int64 = 1 << 10
	bytesPerMiB int64 = 1 << 20
)

const (
	attachmentKindPhoto     = "photo"
	attachmentKindDocument  = "document"
	attachmentKindVideo     = "video"
	attachmentKindVoice     = "voice"
	attachmentKindAudio     = "audio"
	attachmentKindAnimation = "animation"
	attachmentKindVideoNote = "video_note"
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
	sessionID string,
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
		SessionID: sessionID,
		RequestID: requestID,
	}

	parts := make([]gwproto.ContentPart, 0, 4)
	var err error

	parts, err = c.appendMessageParts(ctx, parts, &msg, maxBytes)
	if err != nil {
		return gwclient.MessageRequest{}, err
	}
	if len(parts) == 0 && hasTelegramAttachments(msg.ReplyToMessage) {
		parts, err = c.appendMessageParts(
			ctx,
			parts,
			msg.ReplyToMessage,
			maxBytes,
		)
		if err != nil {
			return gwclient.MessageRequest{}, err
		}
	}

	req.ContentParts = parts
	if strings.TrimSpace(req.Text) == "" && len(req.ContentParts) == 0 {
		return gwclient.MessageRequest{}, errors.New("telegram: empty message")
	}
	return req, nil
}

func (c *Channel) appendMessageParts(
	ctx context.Context,
	parts []gwproto.ContentPart,
	msg *tgapi.Message,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if msg == nil {
		return parts, nil
	}

	var err error

	parts, err = c.appendPhotoPart(ctx, parts, msg.Photo, maxBytes)
	if err != nil {
		return nil, err
	}
	parts, err = c.appendDocumentPart(ctx, parts, msg.Document, maxBytes)
	if err != nil {
		return nil, err
	}
	parts, err = c.appendVideoPart(ctx, parts, msg.Video, maxBytes)
	if err != nil {
		return nil, err
	}
	parts, err = c.appendAnimationPart(
		ctx,
		parts,
		msg.Animation,
		maxBytes,
	)
	if err != nil {
		return nil, err
	}
	parts, err = c.appendVideoNotePart(
		ctx,
		parts,
		msg.VideoNote,
		maxBytes,
	)
	if err != nil {
		return nil, err
	}
	parts, err = c.appendVoicePart(ctx, parts, msg.Voice, maxBytes)
	if err != nil {
		return nil, err
	}
	parts, err = c.appendAudioPart(ctx, parts, msg.Audio, maxBytes)
	if err != nil {
		return nil, err
	}
	return parts, nil
}

func hasTelegramAttachments(msg *tgapi.Message) bool {
	if msg == nil {
		return false
	}
	return len(msg.Photo) > 0 ||
		msg.Document != nil ||
		msg.Audio != nil ||
		msg.Voice != nil ||
		msg.Video != nil ||
		msg.Animation != nil ||
		msg.VideoNote != nil
}

func replyHasPhoto(msg *tgapi.Message) bool {
	return msg != nil && len(msg.Photo) > 0
}

func replyHasDocument(msg *tgapi.Message) bool {
	return msg != nil && msg.Document != nil
}

func replyHasAudio(msg *tgapi.Message) bool {
	return msg != nil && msg.Audio != nil
}

func replyHasVoice(msg *tgapi.Message) bool {
	return msg != nil && msg.Voice != nil
}

func replyHasVideo(msg *tgapi.Message) bool {
	return msg != nil && msg.Video != nil
}

func replyHasAnimation(msg *tgapi.Message) bool {
	return msg != nil && msg.Animation != nil
}

func replyHasVideoNote(msg *tgapi.Message) bool {
	return msg != nil && msg.VideoNote != nil
}

func incomingReplyTo(msg tgapi.Message) int {
	if msg.ReplyToMessage == nil {
		return 0
	}
	return msg.ReplyToMessage.MessageID
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
			userMessage: attachmentTooLargeMessage(maxBytes),
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
		mapped := mapDownloadError(err, maxBytes)
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
	parts = appendStoredFilePart(
		parts,
		name,
		mimeTypeForImageFormat(format),
		data,
	)
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
			userMessage: attachmentTooLargeMessage(maxBytes),
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
		mapped := mapDownloadError(err, maxBytes)
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

	name := fallbackDocumentFilename(
		doc.FileName,
		file.FilePath,
		strings.TrimSpace(doc.MimeType),
	)
	mimeType := normalizeMediaMIME(
		name,
		file.FilePath,
		doc.MimeType,
	)

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindDocument,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: doc.FileSize,
		Blob:         ref,
	})

	if imageFormat := inferImageFormat(name, data); imageFormat != "" {
		parts = append(parts, gwproto.ContentPart{
			Type: gwproto.PartTypeImage,
			Image: &gwproto.ImagePart{
				Data:   data,
				Format: imageFormat,
			},
		})
		parts = appendStoredFilePart(
			parts,
			name,
			normalizeMediaMIME(
				name,
				file.FilePath,
				mimeTypeForImageFormat(imageFormat),
			),
			data,
		)
		return parts, nil
	}

	if audioPart := c.audioModelPart(
		ctx,
		name,
		file.FilePath,
		mimeType,
		data,
	); audioPart != nil {
		parts = append(parts, *audioPart)
		parts = appendStoredFilePart(
			parts,
			name,
			normalizeMediaMIME(
				name,
				file.FilePath,
				mimeType,
			),
			data,
		)
		return parts, nil
	}

	if isVideoMedia(name, file.FilePath, mimeType) {
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

	parts = appendStoredFilePart(parts, name, mimeType, data)
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
			userMessage: attachmentTooLargeMessage(maxBytes),
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
		mapped := mapDownloadError(err, maxBytes)
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

	mimeType := normalizeMediaMIME(
		video.FileName,
		file.FilePath,
		video.MimeType,
	)
	name := fallbackMediaFilename(
		video.FileName,
		file.FilePath,
		defaultVideoName,
		mimeType,
	)

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

func (c *Channel) appendAnimationPart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	animation *tgapi.Animation,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if animation == nil {
		return parts, nil
	}
	return c.appendNamedVideoLikePart(
		ctx,
		parts,
		attachmentKindAnimation,
		strings.TrimSpace(animation.FileID),
		strings.TrimSpace(animation.FileName),
		strings.TrimSpace(animation.MimeType),
		animation.FileSize,
		defaultAnimationName,
		maxBytes,
	)
}

func (c *Channel) appendVideoNotePart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	videoNote *tgapi.VideoNote,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if videoNote == nil {
		return parts, nil
	}
	return c.appendNamedVideoLikePart(
		ctx,
		parts,
		attachmentKindVideoNote,
		strings.TrimSpace(videoNote.FileID),
		"",
		"",
		videoNote.FileSize,
		defaultVideoNoteName,
		maxBytes,
	)
}

func (c *Channel) appendNamedVideoLikePart(
	ctx context.Context,
	parts []gwproto.ContentPart,
	kind string,
	fileID string,
	fileName string,
	mimeType string,
	fileSize int64,
	fallbackName string,
	maxBytes int64,
) ([]gwproto.ContentPart, error) {
	if fileID == "" {
		return parts, nil
	}

	trace := debugrecorder.TraceFromContext(ctx)
	if fileSize > maxBytes && fileSize > 0 {
		uerr := &userError{
			userMessage: attachmentTooLargeMessage(maxBytes),
			err:         tgapi.ErrFileTooLarge,
		}
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         kind,
			FileID:       fileID,
			Name:         fileName,
			MimeType:     mimeType,
			ReportedSize: fileSize,
			UserMessage:  uerr.userMessage,
			Error:        uerr.Error(),
		})
		return nil, uerr
	}

	file, data, err := c.bot.DownloadFileByID(ctx, fileID, maxBytes)
	if err != nil {
		mapped := mapDownloadError(err, maxBytes)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         kind,
			FileID:       fileID,
			Name:         fileName,
			MimeType:     mimeType,
			ReportedSize: fileSize,
			UserMessage:  userMessageFromErr(mapped),
			Error:        err.Error(),
		})
		return nil, mapped
	}

	name := fallbackMediaFilename(
		fileName,
		file.FilePath,
		fallbackName,
		mimeType,
	)
	mimeType = normalizeMediaMIME(name, file.FilePath, mimeType)
	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         kind,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: fileSize,
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
			userMessage: attachmentTooLargeMessage(maxBytes),
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
		mapped := mapDownloadError(err, maxBytes)
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

	mimeType := normalizeMediaMIME(
		"",
		file.FilePath,
		voice.MimeType,
	)
	name := fallbackMediaFilename(
		"",
		file.FilePath,
		defaultVoiceName,
		mimeType,
	)

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindVoice,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: voice.FileSize,
		Blob:         ref,
	})

	if audioPart := c.audioModelPart(
		ctx,
		name,
		file.FilePath,
		mimeType,
		data,
	); audioPart != nil {
		parts = append(parts, *audioPart)
		parts = appendStoredFilePart(parts, name, mimeType, data)
		return parts, nil
	}

	parts = appendStoredFilePart(parts, name, mimeType, data)
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
			userMessage: attachmentTooLargeMessage(maxBytes),
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
		mapped := mapDownloadError(err, maxBytes)
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

	mimeType := normalizeMediaMIME(
		audio.FileName,
		file.FilePath,
		audio.MimeType,
	)
	name := fallbackMediaFilename(
		audio.FileName,
		file.FilePath,
		defaultAudioName,
		mimeType,
	)
	if audioPart := c.audioModelPart(
		ctx,
		name,
		file.FilePath,
		mimeType,
		data,
	); audioPart != nil {
		ref := storeBlob(trace, name, data)
		recordAttachment(trace, telegramAttachmentSummary{
			Kind:         attachmentKindAudio,
			FileID:       fileID,
			Name:         name,
			Format:       audioPart.Audio.Format,
			MimeType:     mimeType,
			ReportedSize: audio.FileSize,
			Blob:         ref,
		})
		parts = append(parts, *audioPart)
		parts = appendStoredFilePart(parts, name, mimeType, data)
		return parts, nil
	}

	ref := storeBlob(trace, name, data)
	recordAttachment(trace, telegramAttachmentSummary{
		Kind:         attachmentKindAudio,
		FileID:       fileID,
		Name:         name,
		MimeType:     mimeType,
		ReportedSize: audio.FileSize,
		Blob:         ref,
	})

	parts = appendStoredFilePart(parts, name, mimeType, data)
	return parts, nil
}

func mapDownloadError(err error, maxBytes int64) error {
	if errors.Is(err, tgapi.ErrFileTooLarge) {
		return &userError{
			userMessage: attachmentTooLargeMessage(maxBytes),
			err:         err,
		}
	}
	return &userError{
		userMessage: downloadFailedMessage,
		err:         err,
	}
}

func attachmentTooLargeMessage(maxBytes int64) string {
	if maxBytes <= 0 {
		return attachmentTooLargeMsg
	}
	return fmt.Sprintf(
		"%s (limit: %s).",
		attachmentTooLargeMsg,
		formatByteLimit(maxBytes),
	)
}

func formatByteLimit(maxBytes int64) string {
	switch {
	case maxBytes >= bytesPerMiB && maxBytes%bytesPerMiB == 0:
		return fmt.Sprintf("%d MiB", maxBytes/bytesPerMiB)
	case maxBytes >= bytesPerKiB && maxBytes%bytesPerKiB == 0:
		return fmt.Sprintf("%d KiB", maxBytes/bytesPerKiB)
	default:
		return fmt.Sprintf("%d bytes", maxBytes)
	}
}

func appendStoredFilePart(
	parts []gwproto.ContentPart,
	name string,
	mimeType string,
	data []byte,
) []gwproto.ContentPart {
	return append(parts, gwproto.ContentPart{
		Type: gwproto.PartTypeFile,
		File: &gwproto.FilePart{
			Filename: name,
			Data:     data,
			Format:   strings.TrimSpace(mimeType),
		},
	})
}

func mimeTypeForImageFormat(format string) string {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "jpeg", "jpg":
		return mimeImageJPEG
	case "png":
		return mimeImagePNG
	case "gif":
		return mimeImageGIF
	case "webp":
		return mimeImageWEBP
	default:
		return ""
	}
}

func mimeTypeForAudioFormat(format string) string {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case audioFormatMP3:
		return mimeAudioMPEG
	case audioFormatWAV:
		return mimeAudioWAV
	default:
		return ""
	}
}

func isSupportedAudioFormat(format string) bool {
	return strings.TrimSpace(format) != ""
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

func fallbackMediaFilename(
	primary string,
	filePath string,
	fallback string,
	mimeType string,
) string {
	name := strings.TrimSpace(primary)
	if name != "" {
		return name
	}

	base := strings.TrimSpace(path.Base(strings.TrimSpace(filePath)))
	if base != "" && base != "." && base != "/" &&
		!looksGeneratedTelegramFileName(base) {
		return base
	}

	ext := mediaExtFromPathOrMIME(filePath, mimeType)
	if ext == "" {
		return fallback
	}
	return fallback + ext
}

func fallbackDocumentFilename(
	primary string,
	filePath string,
	mimeType string,
) string {
	name := strings.TrimSpace(primary)
	if name != "" && !looksGeneratedTelegramFileName(name) {
		return name
	}
	fallback := documentFallbackBase(filePath, mimeType)
	return fallbackMediaFilename("", filePath, fallback, mimeType)
}

func documentFallbackBase(filePath string, mimeType string) string {
	contentType := strings.ToLower(strings.TrimSpace(mimeType))
	ext := strings.ToLower(path.Ext(strings.TrimSpace(filePath)))

	switch {
	case contentType == "application/pdf" || ext == ".pdf":
		return defaultDocumentName
	case contentType == mimeImageGIF || ext == ".gif":
		return defaultAnimationName
	case strings.HasPrefix(contentType, mimePrefixImage) ||
		ext == ".jpg" || ext == ".jpeg" ||
		ext == ".png" || ext == ".webp":
		return defaultPhotoName
	case strings.HasPrefix(contentType, mimePrefixVideo) ||
		ext == ".mp4" || ext == ".mov" ||
		ext == ".webm" || ext == ".mkv":
		return defaultVideoName
	case strings.HasPrefix(contentType, mimePrefixAudio) ||
		ext == ".mp3" || ext == ".wav" ||
		ext == ".ogg" || ext == ".oga" ||
		ext == ".m4a":
		return defaultAudioName
	default:
		return defaultDocumentName
	}
}

func looksGeneratedTelegramFileName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	dot := strings.Index(trimmed, ".")
	stem := trimmed
	if dot >= 0 {
		stem = trimmed[:dot]
	}
	if !strings.HasPrefix(stem, "file_") {
		return false
	}
	suffix := strings.TrimPrefix(stem, "file_")
	return suffix != "" && isDigitsOnly(suffix)
}

func mediaExtFromPathOrMIME(filePath, mimeType string) string {
	if ext := path.Ext(strings.TrimSpace(filePath)); ext != "" {
		if strings.EqualFold(ext, ".oga") {
			return ".ogg"
		}
		return strings.ToLower(ext)
	}
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case mimeAudioMPEG:
		return ".mp3"
	case mimeAudioWAV:
		return ".wav"
	case mimeAudioOGG:
		return ".ogg"
	case mimeVideoMP4:
		return ".mp4"
	case mimeImageGIF:
		return ".gif"
	default:
		return ""
	}
}

func normalizeMediaMIME(
	fileName string,
	filePath string,
	mimeType string,
) string {
	trimmed := strings.TrimSpace(mimeType)
	if trimmed != "" {
		return trimmed
	}
	if extType := typeFromExtension(path.Ext(fileName)); extType != "" {
		return extType
	}
	return typeFromExtension(path.Ext(filePath))
}

func isVideoMedia(
	fileName string,
	filePath string,
	mimeType string,
) bool {
	normalized := normalizeMediaMIME(fileName, filePath, mimeType)
	if strings.HasPrefix(normalized, mimePrefixVideo) {
		return true
	}
	switch mediaExtFromPathOrMIME(filePath, normalized) {
	case ".mp4", ".mov", ".webm", ".mkv":
		return true
	default:
		return false
	}
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
