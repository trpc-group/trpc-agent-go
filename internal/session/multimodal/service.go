//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package multimodal provides internal session multimodal content governance.
package multimodal

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	artifactScheme = "artifact://"
	defaultMime    = "application/octet-stream"
)

// Config controls internal session multimodal governance.
type Config struct {
	Enabled bool
}

// Wrap wraps a session service with multimodal externalization and hydration.
func Wrap(inner session.Service, artifactService artifact.Service, cfg Config) session.Service {
	if inner == nil || !cfg.Enabled {
		return inner
	}
	base := &Service{
		Service:         inner,
		artifactService: artifactService,
		cfg:             cfg,
	}
	return wrapOptionalInterfaces(base, inner)
}

// Service decorates a session service with multimodal governance.
type Service struct {
	session.Service
	artifactService artifact.Service
	cfg             Config
}

type searchableService struct {
	*Service
	session.SearchableService
}

func (s *searchableService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.Service.searchEvents(ctx, s.SearchableService, req)
}

type windowService struct {
	*Service
	session.WindowService
}

func (s *windowService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.Service.getEventWindow(ctx, s.WindowService, req)
}

type trackService struct {
	*Service
	session.TrackService
}

type searchableWindowService struct {
	*Service
	session.SearchableService
	session.WindowService
}

func (s *searchableWindowService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.Service.searchEvents(ctx, s.SearchableService, req)
}

func (s *searchableWindowService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.Service.getEventWindow(ctx, s.WindowService, req)
}

type searchableTrackService struct {
	*Service
	session.SearchableService
	session.TrackService
}

func (s *searchableTrackService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.Service.searchEvents(ctx, s.SearchableService, req)
}

type windowTrackService struct {
	*Service
	session.WindowService
	session.TrackService
}

func (s *windowTrackService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.Service.getEventWindow(ctx, s.WindowService, req)
}

type searchableWindowTrackService struct {
	*Service
	session.SearchableService
	session.WindowService
	session.TrackService
}

func (s *searchableWindowTrackService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.Service.searchEvents(ctx, s.SearchableService, req)
}

func (s *searchableWindowTrackService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.Service.getEventWindow(ctx, s.WindowService, req)
}

func wrapOptionalInterfaces(base *Service, inner session.Service) session.Service {
	searchable, hasSearch := inner.(session.SearchableService)
	window, hasWindow := inner.(session.WindowService)
	track, hasTrack := inner.(session.TrackService)
	switch {
	case hasSearch && hasWindow && hasTrack:
		return &searchableWindowTrackService{
			Service:           base,
			SearchableService: searchable,
			WindowService:     window,
			TrackService:      track,
		}
	case hasSearch && hasWindow:
		return &searchableWindowService{
			Service:           base,
			SearchableService: searchable,
			WindowService:     window,
		}
	case hasSearch && hasTrack:
		return &searchableTrackService{
			Service:           base,
			SearchableService: searchable,
			TrackService:      track,
		}
	case hasWindow && hasTrack:
		return &windowTrackService{
			Service:       base,
			WindowService: window,
			TrackService:  track,
		}
	case hasSearch:
		return &searchableService{
			Service:           base,
			SearchableService: searchable,
		}
	case hasWindow:
		return &windowService{
			Service:       base,
			WindowService: window,
		}
	case hasTrack:
		return &trackService{
			Service:      base,
			TrackService: track,
		}
	default:
		return base
	}
}

// CreateSession returns a caller-owned runtime session view.
func (s *Service) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.CreateSession(ctx, key, state, options...)
	if err != nil || sess == nil {
		return sess, err
	}
	return sess.Clone(), nil
}

// AppendEvent externalizes standard multimodal content before persistence.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if !s.cfg.Enabled || evt == nil {
		return s.Service.AppendEvent(ctx, sess, evt, options...)
	}
	if sess == nil {
		return session.ErrNilSession
	}
	info := artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	persisted, saved, changed, err := externalizeEvent(
		ctx,
		evt,
		info,
		s.artifactService,
	)
	if err != nil {
		bestEffortDelete(ctx, s.artifactService, info, saved)
		return err
	}
	if !changed {
		return s.Service.AppendEvent(ctx, sess, evt, options...)
	}
	persistedSess := sess.Clone()
	if err := s.Service.AppendEvent(ctx, persistedSess, persisted, options...); err != nil {
		bestEffortDelete(ctx, s.artifactService, info, saved)
		return err
	}
	sess.UpdateUserSession(evt, options...)
	return nil
}

// GetSession hydrates internal multimodal references before returning a session.
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.GetSession(ctx, key, options...)
	if err != nil || sess == nil || !s.cfg.Enabled {
		return sess, err
	}
	info := artifact.SessionInfo{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
	}
	return hydrateSession(ctx, sess, info, s.artifactService)
}

// ListSessions hydrates full session results. Metadata-only list calls skip
// hydration because they intentionally omit event payloads.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	sessions, err := s.Service.ListSessions(ctx, userKey, options...)
	if err != nil || !s.cfg.Enabled || listSessionOnlyMeta(options) {
		return sessions, err
	}
	for i, sess := range sessions {
		if sess == nil {
			continue
		}
		info := artifact.SessionInfo{
			AppName:   sess.AppName,
			UserID:    sess.UserID,
			SessionID: sess.ID,
		}
		hydrated, err := hydrateSession(ctx, sess, info, s.artifactService)
		if err != nil {
			return nil, err
		}
		sessions[i] = hydrated
	}
	return sessions, nil
}

func (s *Service) searchEvents(
	ctx context.Context,
	searchable session.SearchableService,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	results, err := searchable.SearchEvents(ctx, req)
	if err != nil || !s.cfg.Enabled {
		return results, err
	}
	for i := range results {
		info := artifact.SessionInfo{
			AppName:   results[i].SessionKey.AppName,
			UserID:    results[i].SessionKey.UserID,
			SessionID: results[i].SessionKey.SessionID,
		}
		evt, changed, err := hydrateEvent(ctx, &results[i].Event, info, s.artifactService)
		if err != nil {
			return nil, err
		}
		if changed {
			results[i].Event = *evt
		}
	}
	return results, nil
}

func (s *Service) getEventWindow(
	ctx context.Context,
	window session.WindowService,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	result, err := window.GetEventWindow(ctx, req)
	if err != nil || result == nil || !s.cfg.Enabled {
		return result, err
	}
	for i := range result.Entries {
		info := artifact.SessionInfo{
			AppName:   result.SessionKey.AppName,
			UserID:    result.SessionKey.UserID,
			SessionID: result.SessionKey.SessionID,
		}
		evt, changed, err := hydrateEvent(ctx, &result.Entries[i].Event, info, s.artifactService)
		if err != nil {
			return nil, err
		}
		if changed {
			result.Entries[i].Event = *evt
		}
	}
	return result, nil
}

type savedArtifact struct {
	name string
}

type externalizeTarget struct {
	data         []byte
	mimeType     string
	originalName string
	fromDataURL  bool
	apply        func(*model.ContentPart, *model.ContentRef)
}

func externalizeEvent(
	ctx context.Context,
	evt *event.Event,
	info artifact.SessionInfo,
	svc artifact.Service,
) (*event.Event, []savedArtifact, bool, error) {
	if evt == nil || evt.Response == nil {
		return evt, nil, false, nil
	}
	if !shouldPersistEvent(evt) {
		return evt, nil, false, nil
	}
	var (
		cloned  *event.Event
		saved   []savedArtifact
		changed bool
	)
	for choiceIndex := range evt.Response.Choices {
		msg := evt.Response.Choices[choiceIndex].Message
		if hasExternalizableContent(msg) {
			if svc == nil {
				return nil, saved, false, errors.New(
					"session multimodal externalization: artifact service is nil",
				)
			}
			if cloned == nil {
				cloned = cloneEventForMutation(evt)
			}
			targetMsg := &cloned.Response.Choices[choiceIndex].Message
			artifacts, err := externalizeMessage(
				ctx,
				targetMsg,
				info,
				svc,
				evt.ID,
				evt.RequestID,
				choiceIndex,
			)
			if err != nil {
				return nil, append(saved, artifacts...), true, err
			}
			saved = append(saved, artifacts...)
			changed = true
		}
		delta := evt.Response.Choices[choiceIndex].Delta
		if hasExternalizableContent(delta) {
			if svc == nil {
				return nil, saved, false, errors.New(
					"session multimodal externalization: artifact service is nil",
				)
			}
			if cloned == nil {
				cloned = cloneEventForMutation(evt)
			}
			targetMsg := &cloned.Response.Choices[choiceIndex].Delta
			artifacts, err := externalizeMessage(
				ctx,
				targetMsg,
				info,
				svc,
				evt.ID,
				evt.RequestID,
				choiceIndex,
			)
			if err != nil {
				return nil, append(saved, artifacts...), true, err
			}
			saved = append(saved, artifacts...)
			changed = true
		}
	}
	if !changed {
		return evt, nil, false, nil
	}
	return cloned, saved, true, nil
}

func externalizeMessage(
	ctx context.Context,
	msg *model.Message,
	info artifact.SessionInfo,
	svc artifact.Service,
	eventKey string,
	requestID string,
	messageIndex int,
) ([]savedArtifact, error) {
	saved := make([]savedArtifact, 0)
	for partIndex := range msg.ContentParts {
		target, ok, err := externalizeTargetForPart(&msg.ContentParts[partIndex])
		if err != nil {
			return saved, err
		}
		if !ok {
			continue
		}
		filename := artifactName(target.data, target.mimeType, target.originalName)
		version, err := svc.SaveArtifact(ctx, info, filename, &artifact.Artifact{
			Data:     cloneBytes(target.data),
			MimeType: target.mimeType,
			Name:     target.originalName,
		})
		if err != nil {
			return saved, fmt.Errorf("session multimodal externalization: save artifact %s: %w", filename, err)
		}
		saved = append(saved, savedArtifact{name: filename})
		ref := &model.ContentRef{
			ArtifactRef:     fmt.Sprintf("%s%s@%d", artifactScheme, filename, version),
			ArtifactName:    filename,
			ArtifactVersion: version,
			MimeType:        target.mimeType,
			SizeBytes:       int64(len(target.data)),
			SHA256:          sha256Hex(target.data),
			OriginalName:    target.originalName,
			FromDataURL:     target.fromDataURL,
			EventKey:        eventKey,
			MessageIndex:    messageIndex,
			PartIndex:       partIndex,
			RequestID:       requestID,
		}
		target.apply(&msg.ContentParts[partIndex], ref)
	}
	return saved, nil
}

func externalizeTargetForPart(part *model.ContentPart) (externalizeTarget, bool, error) {
	if part == nil || part.ContentRef != nil {
		return externalizeTarget{}, false, nil
	}
	switch part.Type {
	case model.ContentTypeImage:
		if part.Image == nil {
			return externalizeTarget{}, false, nil
		}
		if len(part.Image.Data) > 0 {
			data := cloneBytes(part.Image.Data)
			mimeType := imageMimeType(part.Image.Format)
			return externalizeTarget{
				data:     data,
				mimeType: mimeType,
				apply: func(p *model.ContentPart, ref *model.ContentRef) {
					p.Image.Data = nil
					p.ContentRef = ref
				},
			}, true, nil
		}
		if data, mimeType, ok, err := parseDataURL(part.Image.URL); ok || err != nil {
			if err != nil {
				return externalizeTarget{}, false, err
			}
			return externalizeTarget{
				data:        data,
				mimeType:    chooseNonEmpty(mimeType, imageMimeType(part.Image.Format)),
				fromDataURL: true,
				apply: func(p *model.ContentPart, ref *model.ContentRef) {
					p.Image.URL = ""
					p.ContentRef = ref
				},
			}, true, nil
		}
	case model.ContentTypeAudio:
		if part.Audio == nil || len(part.Audio.Data) == 0 {
			return externalizeTarget{}, false, nil
		}
		data := cloneBytes(part.Audio.Data)
		return externalizeTarget{
			data:     data,
			mimeType: audioMimeType(part.Audio.Format),
			apply: func(p *model.ContentPart, ref *model.ContentRef) {
				p.Audio.Data = nil
				p.ContentRef = ref
			},
		}, true, nil
	case model.ContentTypeFile:
		if part.File == nil {
			return externalizeTarget{}, false, nil
		}
		if len(part.File.Data) > 0 {
			data := cloneBytes(part.File.Data)
			mimeType := chooseNonEmpty(part.File.MimeType, mimeFromName(part.File.Name))
			return externalizeTarget{
				data:         data,
				mimeType:     normalizeMime(mimeType),
				originalName: part.File.Name,
				apply: func(p *model.ContentPart, ref *model.ContentRef) {
					p.File.Data = nil
					p.ContentRef = ref
				},
			}, true, nil
		}
		if data, mimeType, ok, err := parseDataURL(part.File.URL); ok || err != nil {
			if err != nil {
				return externalizeTarget{}, false, err
			}
			return externalizeTarget{
				data:         data,
				mimeType:     chooseNonEmpty(mimeType, part.File.MimeType, mimeFromName(part.File.Name)),
				originalName: part.File.Name,
				fromDataURL:  true,
				apply: func(p *model.ContentPart, ref *model.ContentRef) {
					p.File.URL = ""
					p.ContentRef = ref
				},
			}, true, nil
		}
	}
	return externalizeTarget{}, false, nil
}

func hydrateSession(
	ctx context.Context,
	sess *session.Session,
	info artifact.SessionInfo,
	svc artifact.Service,
) (*session.Session, error) {
	if sess == nil {
		return nil, nil
	}
	needsHydrate := false
	for i := range sess.Events {
		if eventNeedsHydrate(&sess.Events[i]) {
			needsHydrate = true
			break
		}
	}
	if !needsHydrate {
		return sess, nil
	}
	if svc == nil {
		return nil, errors.New("session multimodal hydrate: artifact service is nil")
	}
	hydrated := sess.Clone()
	for i := range hydrated.Events {
		evt, changed, err := hydrateEvent(ctx, &hydrated.Events[i], info, svc)
		if err != nil {
			return nil, err
		}
		if changed {
			hydrated.Events[i] = *evt
		}
	}
	return hydrated, nil
}

func hydrateEvent(
	ctx context.Context,
	evt *event.Event,
	info artifact.SessionInfo,
	svc artifact.Service,
) (*event.Event, bool, error) {
	if evt == nil || evt.Response == nil || !eventNeedsHydrate(evt) {
		return evt, false, nil
	}
	cloned := cloneEventForMutation(evt)
	for choiceIndex := range cloned.Response.Choices {
		if err := hydrateMessage(ctx, &cloned.Response.Choices[choiceIndex].Message, info, svc); err != nil {
			return nil, false, err
		}
		if err := hydrateMessage(ctx, &cloned.Response.Choices[choiceIndex].Delta, info, svc); err != nil {
			return nil, false, err
		}
	}
	return cloned, true, nil
}

func hydrateMessage(
	ctx context.Context,
	msg *model.Message,
	info artifact.SessionInfo,
	svc artifact.Service,
) error {
	for partIndex := range msg.ContentParts {
		part := &msg.ContentParts[partIndex]
		if part.ContentRef == nil {
			continue
		}
		ref := part.ContentRef
		name, version, err := artifactNameVersion(ref)
		if err != nil {
			return err
		}
		art, err := svc.LoadArtifact(ctx, info, name, &version)
		if err != nil {
			return fmt.Errorf("session multimodal hydrate: load artifact %s@%d: %w", name, version, err)
		}
		if art == nil {
			return fmt.Errorf("session multimodal hydrate: artifact not found: %s@%d", name, version)
		}
		data := cloneBytes(art.Data)
		switch part.Type {
		case model.ContentTypeImage:
			if part.Image == nil {
				part.Image = &model.Image{}
			}
			part.Image.Data = data
			part.Image.Format = chooseNonEmpty(part.Image.Format, imageFormat(ref.MimeType, art.MimeType))
		case model.ContentTypeAudio:
			if part.Audio == nil {
				part.Audio = &model.Audio{}
			}
			part.Audio.Data = data
			part.Audio.Format = chooseNonEmpty(part.Audio.Format, audioFormat(ref.MimeType, art.MimeType))
		case model.ContentTypeFile:
			if part.File == nil {
				part.File = &model.File{}
			}
			part.File.Data = data
			part.File.Name = chooseNonEmpty(part.File.Name, ref.OriginalName, art.Name)
			part.File.MimeType = chooseNonEmpty(part.File.MimeType, ref.MimeType, art.MimeType)
		}
	}
	return nil
}

func hasExternalizableContent(msg model.Message) bool {
	for _, part := range msg.ContentParts {
		if part.ContentRef != nil {
			continue
		}
		switch part.Type {
		case model.ContentTypeImage:
			if part.Image != nil &&
				(len(part.Image.Data) > 0 || isDataURL(part.Image.URL)) {
				return true
			}
		case model.ContentTypeAudio:
			if part.Audio != nil && len(part.Audio.Data) > 0 {
				return true
			}
		case model.ContentTypeFile:
			if part.File != nil &&
				(len(part.File.Data) > 0 || isDataURL(part.File.URL)) {
				return true
			}
		}
	}
	return false
}

func shouldPersistEvent(evt *event.Event) bool {
	return evt != nil &&
		evt.Response != nil &&
		!evt.IsPartial &&
		evt.Response.IsValidContent()
}

func listSessionOnlyMeta(options []session.Option) bool {
	opt := &session.Options{}
	for _, option := range options {
		if option != nil {
			option(opt)
		}
	}
	return opt.ListSessionOnlyMeta
}

func isDataURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "data:")
}

func eventNeedsHydrate(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	for _, choice := range evt.Response.Choices {
		if messageNeedsHydrate(choice.Message) || messageNeedsHydrate(choice.Delta) {
			return true
		}
	}
	return false
}

func messageNeedsHydrate(msg model.Message) bool {
	for _, part := range msg.ContentParts {
		if part.ContentRef == nil {
			continue
		}
		switch part.Type {
		case model.ContentTypeImage:
			if part.Image == nil || len(part.Image.Data) == 0 {
				return true
			}
		case model.ContentTypeAudio:
			if part.Audio == nil || len(part.Audio.Data) == 0 {
				return true
			}
		case model.ContentTypeFile:
			if part.File == nil || len(part.File.Data) == 0 {
				return true
			}
		}
	}
	return false
}

func cloneEventForMutation(evt *event.Event) *event.Event {
	// Do not use event.Clone here: it intentionally generates a new event ID
	// and updates version fields. Governance clones must preserve event identity
	// because persisted and runtime views describe the same logical event.
	clone := *evt
	clone.Response = cloneResponseForMutation(evt.Response)
	clone.LongRunningToolIDs = cloneStringSet(evt.LongRunningToolIDs)
	clone.StateDelta = cloneStateDelta(evt.StateDelta)
	clone.Extensions = cloneExtensions(evt.Extensions)
	if evt.Actions != nil {
		clone.Actions = &event.EventActions{
			SkipSummarization: evt.Actions.SkipSummarization,
		}
	}
	return &clone
}

func cloneResponseForMutation(rsp *model.Response) *model.Response {
	if rsp == nil {
		return nil
	}
	clone := rsp.Clone()
	clone.Choices = make([]model.Choice, len(rsp.Choices))
	for i, choice := range rsp.Choices {
		clone.Choices[i] = choice
		clone.Choices[i].Message = cloneMessage(choice.Message)
		clone.Choices[i].Delta = cloneMessage(choice.Delta)
	}
	return clone
}

func cloneMessage(msg model.Message) model.Message {
	clone := msg
	if msg.ContentParts != nil {
		clone.ContentParts = make([]model.ContentPart, len(msg.ContentParts))
		for i, part := range msg.ContentParts {
			clone.ContentParts[i] = cloneContentPart(part)
		}
	}
	if msg.ToolCalls != nil {
		clone.ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
	}
	return clone
}

func cloneContentPart(part model.ContentPart) model.ContentPart {
	clone := part
	if part.Text != nil {
		text := *part.Text
		clone.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		image.Data = cloneBytes(part.Image.Data)
		clone.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = cloneBytes(part.Audio.Data)
		clone.Audio = &audio
	}
	if part.File != nil {
		file := *part.File
		file.Data = cloneBytes(part.File.Data)
		clone.File = &file
	}
	if part.ContentRef != nil {
		ref := *part.ContentRef
		clone.ContentRef = &ref
	}
	return clone
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStateDelta(in map[string][]byte) map[string][]byte {
	if in == nil {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = cloneBytes(v)
	}
	return out
}

func cloneExtensions(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = json.RawMessage(cloneBytes(v))
	}
	return out
}

func parseDataURL(raw string) ([]byte, string, bool, error) {
	if !isDataURL(raw) {
		return nil, "", false, nil
	}
	payload := strings.TrimSpace(raw)[len("data:"):]
	comma := strings.Index(payload, ",")
	if comma < 0 {
		return nil, "", true, fmt.Errorf("session multimodal externalization: invalid data URL")
	}
	meta := payload[:comma]
	body := payload[comma+1:]
	parts := strings.Split(meta, ";")
	mimeType := defaultMime
	if parts[0] != "" {
		mimeType = parts[0]
	}
	if hasDataURLFlag(parts[1:], "base64") {
		data, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil, "", true, fmt.Errorf("session multimodal externalization: decode data URL: %w", err)
		}
		return data, normalizeMime(mimeType), true, nil
	}
	data, err := url.PathUnescape(body)
	if err != nil {
		return nil, "", true, fmt.Errorf("session multimodal externalization: decode data URL: %w", err)
	}
	return []byte(data), normalizeMime(mimeType), true, nil
}

func hasDataURLFlag(parts []string, flag string) bool {
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), flag) {
			return true
		}
	}
	return false
}

func artifactName(data []byte, mimeType, originalName string) string {
	sum := sha256.Sum256(data)
	hashPrefix := hex.EncodeToString(sum[:])[:16]
	return fmt.Sprintf(
		"sessionpart_%d_%s_%s%s",
		time.Now().UnixMilli(),
		hashPrefix,
		uuid.NewString(),
		artifactExt(mimeType, originalName),
	)
}

func artifactExt(mimeType, originalName string) string {
	ext := ""
	if mimeType != "" {
		if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
			ext = exts[0]
		}
	}
	if ext == "" && originalName != "" {
		ext = filepath.Ext(originalName)
	}
	if ext == "" {
		ext = ".bin"
	}
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if strings.ContainsAny(ext, `/\`) {
		return ".bin"
	}
	return ext
}

func artifactNameVersion(ref *model.ContentRef) (string, int, error) {
	if ref == nil {
		return "", 0, errors.New("session multimodal hydrate: content ref is nil")
	}
	if ref.ArtifactName != "" {
		return ref.ArtifactName, ref.ArtifactVersion, nil
	}
	raw := strings.TrimSpace(ref.ArtifactRef)
	if !strings.HasPrefix(raw, artifactScheme) {
		return "", 0, fmt.Errorf("session multimodal hydrate: invalid artifact ref: %s", raw)
	}
	rest := strings.TrimPrefix(raw, artifactScheme)
	idx := strings.LastIndex(rest, "@")
	if idx <= 0 || idx == len(rest)-1 {
		return "", 0, fmt.Errorf("session multimodal hydrate: artifact ref must pin version: %s", raw)
	}
	version, err := strconv.Atoi(rest[idx+1:])
	if err != nil {
		return "", 0, fmt.Errorf("session multimodal hydrate: invalid artifact version: %s", raw)
	}
	return rest[:idx], version, nil
}

func bestEffortDelete(
	ctx context.Context,
	svc artifact.Service,
	info artifact.SessionInfo,
	saved []savedArtifact,
) {
	if svc == nil {
		return
	}
	seen := make(map[string]struct{}, len(saved))
	for _, item := range saved {
		if item.name == "" {
			continue
		}
		if _, ok := seen[item.name]; ok {
			continue
		}
		seen[item.name] = struct{}{}
		_ = svc.DeleteArtifact(ctx, info, item.name)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeMime(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return defaultMime
	}
	return strings.TrimSpace(mimeType)
}

func imageMimeType(format string) string {
	format = strings.TrimSpace(format)
	if format == "" {
		return defaultMime
	}
	if strings.Contains(format, "/") {
		return format
	}
	if format == "jpg" {
		format = "jpeg"
	}
	return "image/" + format
}

func audioMimeType(format string) string {
	format = strings.TrimSpace(format)
	if format == "" {
		return defaultMime
	}
	if strings.Contains(format, "/") {
		return format
	}
	return "audio/" + format
}

func imageFormat(values ...string) string {
	mimeType := chooseNonEmpty(values...)
	if strings.HasPrefix(mimeType, "image/") {
		format := strings.TrimPrefix(mimeType, "image/")
		if format == "jpeg" {
			return "jpg"
		}
		return format
	}
	return ""
}

func audioFormat(values ...string) string {
	mimeType := chooseNonEmpty(values...)
	if strings.HasPrefix(mimeType, "audio/") {
		return strings.TrimPrefix(mimeType, "audio/")
	}
	return ""
}

func mimeFromName(name string) string {
	if ext := filepath.Ext(name); ext != "" {
		return mime.TypeByExtension(ext)
	}
	return ""
}
