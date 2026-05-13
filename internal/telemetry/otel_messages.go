//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/url"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	otelPartTypeBlob             = "blob"
	otelPartTypeFile             = "file"
	otelPartTypeReasoning        = "reasoning"
	otelPartTypeText             = "text"
	otelPartTypeToolCall         = "tool_call"
	otelPartTypeToolCallResponse = "tool_call_response"
	otelPartTypeURI              = "uri"

	otelModalityAudio = "audio"
	otelModalityFile  = "file"
	otelModalityImage = "image"
	otelModalityVideo = "video"
)

// OTelMessagePart is the OpenTelemetry-aligned message part payload.
type OTelMessagePart struct {
	Type      string          `json:"type"`
	Content   string          `json:"content,omitempty"`
	Modality  string          `json:"modality,omitempty"`
	MIMEType  string          `json:"mime_type,omitempty"`
	URI       string          `json:"uri,omitempty"`
	FileID    string          `json:"file_id,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	Detail    string          `json:"detail,omitempty"`
	Filename  string          `json:"filename,omitempty"`
}

// OTelInputMessage is the OpenTelemetry-aligned payload for gen_ai.input.messages.otel.
type OTelInputMessage struct {
	Role  model.Role        `json:"role"`
	Parts []OTelMessagePart `json:"parts"`
	Name  string            `json:"name,omitempty"`
}

// OTelOutputMessage is the OpenTelemetry-aligned payload for gen_ai.output.messages.otel.
type OTelOutputMessage struct {
	Role         model.Role        `json:"role"`
	Parts        []OTelMessagePart `json:"parts"`
	Name         string            `json:"name,omitempty"`
	FinishReason string            `json:"finish_reason,omitempty"`
}

func marshalOTelTelemetryMessages(messages []model.Message) ([]byte, error) {
	out := make([]OTelInputMessage, len(messages))
	for i, msg := range messages {
		out[i] = OTelInputMessage{
			Role:  normalizedOTelMessageRole(msg.Role, ""),
			Parts: otelPartsFromModelMessage(msg),
			Name:  strings.TrimSpace(msg.ToolName),
		}
	}
	return json.Marshal(out)
}

func marshalOTelTelemetryChoices(choices []model.Choice) ([]byte, error) {
	out := make([]OTelOutputMessage, len(choices))
	for i, choice := range choices {
		msg := choice.Message
		if isZeroOTelTelemetryMessage(msg) && !isZeroOTelTelemetryMessage(choice.Delta) {
			msg = choice.Delta
		}
		out[i] = OTelOutputMessage{
			Role:         normalizedOTelMessageRole(msg.Role, model.RoleAssistant),
			Parts:        otelPartsFromModelMessage(msg),
			Name:         strings.TrimSpace(msg.ToolName),
			FinishReason: derefString(choice.FinishReason),
		}
	}
	return json.Marshal(out)
}

func otelPartsFromModelMessage(msg model.Message) []OTelMessagePart {
	parts := make([]OTelMessagePart, 0, 1+len(msg.ContentParts)+len(msg.ToolCalls))
	if part, ok := otelPartFromToolCallResponse(msg); ok {
		parts = append(parts, part)
	} else {
		if msg.Content != "" {
			parts = append(parts, OTelMessagePart{Type: otelPartTypeText, Content: msg.Content})
		}
		parts = append(parts, otelPartsFromContentParts(msg.ContentParts)...)
	}
	if msg.ReasoningContent != "" {
		parts = append(parts, OTelMessagePart{Type: otelPartTypeReasoning, Content: msg.ReasoningContent})
	}
	for _, toolCall := range msg.ToolCalls {
		parts = append(parts, otelPartFromToolCall(toolCall))
	}
	if len(parts) == 0 {
		return []OTelMessagePart{}
	}
	return parts
}

func otelPartsFromContentParts(contentParts []model.ContentPart) []OTelMessagePart {
	parts := make([]OTelMessagePart, 0, len(contentParts))
	for _, contentPart := range contentParts {
		part, ok := otelPartFromContentPart(contentPart)
		if ok {
			parts = append(parts, part)
		}
	}
	return parts
}

func otelPartFromContentPart(contentPart model.ContentPart) (OTelMessagePart, bool) {
	switch contentPart.Type {
	case model.ContentTypeText:
		if contentPart.Text == nil {
			return OTelMessagePart{}, false
		}
		return OTelMessagePart{Type: otelPartTypeText, Content: *contentPart.Text}, true
	case model.ContentTypeImage:
		return otelPartFromImage(contentPart.Image)
	case model.ContentTypeAudio:
		if contentPart.Audio == nil || len(contentPart.Audio.Data) == 0 {
			return OTelMessagePart{}, false
		}
		return OTelMessagePart{
			Type:     otelPartTypeBlob,
			Modality: otelModalityAudio,
			MIMEType: normalizeFormatAsMIME(contentPart.Audio.Format, "audio"),
			Content:  base64.StdEncoding.EncodeToString(contentPart.Audio.Data),
		}, true
	case model.ContentTypeFile:
		return otelPartFromFile(contentPart.File)
	default:
		return OTelMessagePart{}, false
	}
}

func otelPartFromImage(image *model.Image) (OTelMessagePart, bool) {
	if image == nil {
		return OTelMessagePart{}, false
	}
	if image.URL != "" {
		return OTelMessagePart{
			Type:     otelPartTypeURI,
			Modality: otelModalityImage,
			MIMEType: imageMIMEType(image),
			URI:      image.URL,
			Detail:   strings.TrimSpace(image.Detail),
		}, true
	}
	if len(image.Data) == 0 {
		return OTelMessagePart{}, false
	}
	return OTelMessagePart{
		Type:     otelPartTypeBlob,
		Modality: otelModalityImage,
		MIMEType: imageMIMEType(image),
		Content:  base64.StdEncoding.EncodeToString(image.Data),
		Detail:   strings.TrimSpace(image.Detail),
	}, true
}

func otelPartFromFile(file *model.File) (OTelMessagePart, bool) {
	if file == nil {
		return OTelMessagePart{}, false
	}
	modality, mimeType := fileMetadata(file)
	if strings.TrimSpace(file.FileID) != "" {
		return OTelMessagePart{
			Type:     otelPartTypeFile,
			Modality: modality,
			MIMEType: mimeType,
			FileID:   strings.TrimSpace(file.FileID),
			Filename: strings.TrimSpace(file.Name),
		}, true
	}
	if len(file.Data) == 0 {
		return OTelMessagePart{}, false
	}
	return OTelMessagePart{
		Type:     otelPartTypeBlob,
		Modality: modality,
		MIMEType: mimeType,
		Content:  base64.StdEncoding.EncodeToString(file.Data),
		Filename: strings.TrimSpace(file.Name),
	}, true
}

func otelPartFromToolCall(toolCall model.ToolCall) OTelMessagePart {
	return OTelMessagePart{
		Type:      otelPartTypeToolCall,
		ID:        strings.TrimSpace(toolCall.ID),
		Name:      strings.TrimSpace(toolCall.Function.Name),
		Arguments: rawJSONOrJSONString(toolCall.Function.Arguments),
	}
}

func otelPartFromToolCallResponse(msg model.Message) (OTelMessagePart, bool) {
	if msg.Role != model.RoleTool {
		return OTelMessagePart{}, false
	}
	id := strings.TrimSpace(msg.ToolID)
	response := toolResponseRawMessage(msg)
	if id == "" && len(response) == 0 {
		return OTelMessagePart{}, false
	}
	return OTelMessagePart{
		Type:     otelPartTypeToolCallResponse,
		ID:       id,
		Response: response,
	}, true
}

func toolResponseRawMessage(msg model.Message) json.RawMessage {
	if len(msg.ContentParts) == 0 && msg.ReasoningContent == "" && len(msg.ToolCalls) == 0 {
		return rawJSONOrJSONString([]byte(msg.Content))
	}
	payload := make(map[string]any)
	if msg.Content != "" {
		payload["content"] = jsonValueOrString([]byte(msg.Content))
	}
	if len(msg.ContentParts) > 0 {
		payload["parts"] = otelPartsFromContentParts(msg.ContentParts)
	}
	if msg.ReasoningContent != "" {
		payload["reasoning"] = msg.ReasoningContent
	}
	if len(msg.ToolCalls) > 0 {
		parts := make([]OTelMessagePart, 0, len(msg.ToolCalls))
		for _, toolCall := range msg.ToolCalls {
			parts = append(parts, otelPartFromToolCall(toolCall))
		}
		payload["tool_calls"] = parts
	}
	if len(payload) == 0 {
		return rawJSONOrJSONString([]byte(""))
	}
	bts, err := json.Marshal(payload)
	if err != nil {
		return rawJSONOrJSONString([]byte(msg.Content))
	}
	return bts
}

func rawJSONOrJSONString(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if json.Valid(trimmed) {
		return append(json.RawMessage(nil), trimmed...)
	}
	bts, err := json.Marshal(string(raw))
	if err != nil {
		return nil
	}
	return bts
}

func jsonValueOrString(raw []byte) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err == nil {
		return v
	}
	return string(raw)
}

func normalizedOTelMessageRole(role model.Role, fallback model.Role) model.Role {
	if role.IsValid() {
		return role
	}
	return fallback
}

func isZeroOTelTelemetryMessage(msg model.Message) bool {
	return msg.Role == "" &&
		msg.Content == "" &&
		len(msg.ContentParts) == 0 &&
		msg.ToolID == "" &&
		msg.ToolName == "" &&
		len(msg.ToolCalls) == 0 &&
		msg.ReasoningContent == ""
}

func fileMetadata(file *model.File) (string, string) {
	if file == nil {
		return otelModalityFile, ""
	}
	mimeType := normalizeMIMEType(file.MimeType)
	if mimeType == "" {
		mimeType = mimeTypeFromName(file.Name)
	}
	return modalityFromMIMEType(mimeType), mimeType
}

func modalityFromMIMEType(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return otelModalityImage
	case strings.HasPrefix(mimeType, "audio/"):
		return otelModalityAudio
	case strings.HasPrefix(mimeType, "video/"):
		return otelModalityVideo
	default:
		return otelModalityFile
	}
}

func imageMIMEType(image *model.Image) string {
	if image == nil {
		return ""
	}
	if mimeType := normalizeFormatAsMIME(image.Format, "image"); mimeType != "" {
		return mimeType
	}
	return mimeTypeFromURL(image.URL)
}

func normalizeFormatAsMIME(format, category string) string {
	normalized := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(format, ".")))
	if normalized == "" {
		return ""
	}
	if strings.Contains(normalized, "/") {
		return normalized
	}
	switch category {
	case "image":
		switch normalized {
		case "jpg", "jpeg":
			return "image/jpeg"
		case "png":
			return "image/png"
		case "gif":
			return "image/gif"
		case "webp":
			return "image/webp"
		case "bmp":
			return "image/bmp"
		case "tiff", "tif":
			return "image/tiff"
		case "svg":
			return "image/svg+xml"
		default:
			return "image/" + normalized
		}
	case "audio":
		switch normalized {
		case "mp3":
			return "audio/mpeg"
		case "wav":
			return "audio/wav"
		case "m4a":
			return "audio/mp4"
		default:
			return "audio/" + normalized
		}
	default:
		return ""
	}
}

func mimeTypeFromURL(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return mimeTypeFromName(rawURL)
	}
	return mimeTypeFromName(path.Base(parsed.Path))
}

func mimeTypeFromName(name string) string {
	ext := strings.ToLower(path.Ext(strings.TrimSpace(name)))
	if ext == "" {
		return ""
	}
	return normalizeMIMEType(mime.TypeByExtension(ext))
}

func normalizeMIMEType(mimeType string) string {
	return strings.TrimSpace(strings.ToLower(mimeType))
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
