//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package imageinput

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionroute"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionnoop "trpc.group/trpc-go/trpc-agent-go/session/noop"
)

func TestIsImageURLFailure(t *testing.T) {
	require.True(t, IsImageURLFailure(
		errors.New("400 failed to fetch image url"),
		nil,
	))

	code := "invalid_image_url"
	param := "messages[0].content[1].image_url.url"
	require.True(t, IsImageURLFailure(nil, &model.ResponseError{
		Message: "invalid request",
		Code:    &code,
		Param:   &param,
	}))

	require.False(t, IsImageURLFailure(
		errors.New("context length exceeded"),
		nil,
	))

	require.True(t, IsImageURLFailure(
		errors.New("下载图片异常: not-a-url"),
		nil,
	))

	invalidBase64 := "invalid_base64"
	require.True(t, IsImageURLFailure(nil, &model.ResponseError{
		Message: "Invalid base64 image_url.",
		Code:    &invalidBase64,
	}))

	require.False(t, IsImageURLFailure(
		errors.New("invalid image format"),
		nil,
	))
}

func TestIsImageURLFailureForRequestUsesImageURLContext(t *testing.T) {
	msg := model.NewUserMessage("current")
	msg.AddImageURL("not-a-url", "auto")
	req := &model.Request{Messages: []model.Message{msg}}

	require.True(t, IsImageURLFailureForRequest(
		errors.New("The URL must be either a HTTP, data or file URL."),
		nil,
		req,
	))

	textOnly := &model.Request{Messages: []model.Message{
		model.NewUserMessage("open the URL"),
	}}
	require.False(t, IsImageURLFailureForRequest(
		errors.New("The URL must be either a HTTP, data or file URL."),
		nil,
		textOnly,
	))

	require.True(t, IsImageURLFailureForRequest(
		errors.New("多模态请求中的URL不符合安全要求，请将URL存放到cos或使用base64编码"),
		nil,
		req,
	))

	require.False(t, IsImageURLFailureForRequest(
		errors.New("invalid image format"),
		nil,
		req,
	))
}

func TestMarkUnavailableImageURLsFromRequestPrefersMentionedURL(t *testing.T) {
	const (
		currentURL = "https://example.invalid/current.png"
		otherURL   = "https://example.invalid/other.png"
	)
	currentMsg := model.NewUserMessage("current")
	currentMsg.AddImageURL(currentURL, "auto")
	otherMsg := model.NewUserMessage("other")
	otherMsg.AddImageURL(otherURL, "auto")

	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("other-inv", otherMsg),
			*imageMessageEvent("inv", currentMsg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(currentMsg),
	)
	inv.InvocationID = "inv"
	inv.RunOptions.RequestID = "req"
	req := &model.Request{Messages: []model.Message{otherMsg, currentMsg}}

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		req,
		errors.New("failed to fetch image url "+otherURL),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	unavailable := UnavailableImageURLSet(sess)
	require.NotContains(t, unavailable, currentURL)
	require.Contains(t, unavailable, otherURL)
}

func TestMarkUnavailableImageURLsFromRequestMatchesWholeURLToken(t *testing.T) {
	const (
		shortURL = "https://example.invalid/img.png"
		longURL  = "https://example.invalid/img.png?bad=1"
	)
	shortMsg := model.NewUserMessage("short")
	shortMsg.AddImageURL(shortURL, "auto")
	longMsg := model.NewUserMessage("long")
	longMsg.AddImageURL(longURL, "auto")

	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("short-inv", shortMsg),
			*imageMessageEvent("long-inv", longMsg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(longMsg),
	)
	req := &model.Request{Messages: []model.Message{shortMsg, longMsg}}

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		req,
		errors.New("failed to fetch image url "+longURL),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	unavailable := UnavailableImageURLSet(sess)
	require.NotContains(t, unavailable, shortURL)
	require.Contains(t, unavailable, longURL)
}

func TestMarkUnavailableImageURLsFromRequestFallsBackToCurrentMessage(t *testing.T) {
	const (
		currentURL = "https://example.invalid/current.png"
		otherURL   = "https://example.invalid/other.png"
	)
	currentMsg := model.NewUserMessage("current")
	currentMsg.AddImageURL(currentURL, "auto")
	otherMsg := model.NewUserMessage("other")
	otherMsg.AddImageURL(otherURL, "auto")

	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("other-inv", otherMsg),
			*imageMessageEvent("current-inv", currentMsg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(currentMsg),
	)
	inv.InvocationID = "current-inv"
	req := &model.Request{Messages: []model.Message{otherMsg, currentMsg}}

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		req,
		errors.New("failed to fetch image"),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	unavailable := UnavailableImageURLSet(sess)
	require.Contains(t, unavailable, currentURL)
	require.NotContains(t, unavailable, otherURL)
}

func TestMarkUnavailableImageURLsFromRequestFallsBackToAllRequestURLsWithoutCurrentMessage(
	t *testing.T,
) {
	const (
		firstURL  = "https://example.invalid/first.png"
		secondURL = "https://example.invalid/second.png"
	)
	firstMsg := model.NewUserMessage("first")
	firstMsg.AddImageURL(firstURL, "auto")
	secondMsg := model.NewUserMessage("second")
	secondMsg.AddImageURL(secondURL, "auto")

	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("first-inv", firstMsg),
			*imageMessageEvent("second-inv", secondMsg),
		},
	}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	req := &model.Request{Messages: []model.Message{firstMsg, secondMsg}}

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		req,
		errors.New("failed to fetch image"),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	unavailable := UnavailableImageURLSet(sess)
	require.Contains(t, unavailable, firstURL)
	require.Contains(t, unavailable, secondURL)
}

func TestMarkUnavailableImageURLsFromRequestDoesNotSetLocalStateOnPersistFailure(
	t *testing.T,
) {
	const imageURL = "https://example.invalid/current.png"
	persistErr := errors.New("persist failed")
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("current-inv", msg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSessionService(&failingUpdateSessionService{
			Service: sessionnoop.NewService(),
			err:     persistErr,
		}),
	)
	inv.InvocationID = "current-inv"

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("failed to fetch image"),
		nil,
	)

	require.ErrorIs(t, err, persistErr)
	require.Zero(t, count)
	_, ok := sess.GetState(UnavailableImageURLsStateKey)
	require.False(t, ok)
}

func TestMarkUnavailableImageURLsFromRequestUsesPersistedSessionEvent(
	t *testing.T,
) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	liveSess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	persistedSess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("current-inv", msg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(liveSess),
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSessionService(&persistedSessionService{
			Service: sessionnoop.NewService(),
			sess:    persistedSess,
		}),
	)
	inv.InvocationID = "current-inv"

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("Unable to download image from "+imageURL),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Empty(t, UnavailableImageURLSet(liveSess))
	require.Contains(t, UnavailableImageURLSet(persistedSess), imageURL)
}

func TestMarkUnavailableImageURLsFromRequestUsesRoutedSession(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	rootSess := &session.Session{ID: "root", AppName: "app", UserID: "user"}
	routedSess := &session.Session{
		ID:      "root/team/member",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("current-inv", msg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(rootSess),
		agent.WithInvocationMessage(msg),
	)
	inv.InvocationID = "current-inv"
	sessionroute.AttachEventRouter(inv, routedSessionRouter{sess: routedSess})

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("Unable to download image from "+imageURL),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Empty(t, UnavailableImageURLSet(rootSess))
	require.Contains(t, UnavailableImageURLSet(routedSess), imageURL)
}

func TestMarkUnavailableImageURLsFromRequestDoesNotFallbackToRootWhenRouted(
	t *testing.T,
) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	rootSess := &session.Session{
		ID:      "root",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("current-inv", msg),
		},
	}
	routedSess := &session.Session{
		ID:      "root/team/member",
		AppName: "app",
		UserID:  "user",
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(rootSess),
		agent.WithInvocationMessage(msg),
	)
	inv.InvocationID = "current-inv"
	sessionroute.AttachEventRouter(inv, routedSessionRouter{sess: routedSess})

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("Unable to download image from "+imageURL),
		nil,
	)

	require.NoError(t, err)
	require.Zero(t, count)
	require.Empty(t, UnavailableImageURLSet(rootSess))
	require.Empty(t, UnavailableImageURLSet(routedSess))
}

func TestMarkUnavailableImageURLsFromRequestUsesPersistedRoutedSessionEvent(
	t *testing.T,
) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	rootSess := &session.Session{ID: "root", AppName: "app", UserID: "user"}
	routedSess := &session.Session{
		ID:      "root/team/member",
		AppName: "app",
		UserID:  "user",
	}
	persistedRoutedSess := &session.Session{
		ID:      routedSess.ID,
		AppName: routedSess.AppName,
		UserID:  routedSess.UserID,
		Events: []event.Event{
			*imageMessageEvent("current-inv", msg),
		},
	}
	sessionSvc := &persistedSessionService{
		Service: sessionnoop.NewService(),
		sess:    persistedRoutedSess,
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(rootSess),
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSessionService(sessionSvc),
	)
	inv.InvocationID = "current-inv"
	sessionroute.AttachEventRouter(inv, routedSessionRouter{sess: routedSess})

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("Unable to download image from "+imageURL),
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, session.Key{
		AppName:   routedSess.AppName,
		UserID:    routedSess.UserID,
		SessionID: routedSess.ID,
	}, sessionSvc.key)
	require.Empty(t, UnavailableImageURLSet(rootSess))
	require.Empty(t, UnavailableImageURLSet(routedSess))
	require.Contains(t, UnavailableImageURLSet(persistedRoutedSess), imageURL)
}

func TestProjectUnavailableImageURLs(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	evt := imageMessageEvent("current-inv", msg)
	sess := &session.Session{Events: []event.Event{*evt}}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
	)
	inv.InvocationID = "current-inv"

	_, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("failed to fetch image"),
		nil,
	)
	require.NoError(t, err)

	projected := ProjectUnavailableImageURLs(sess, *evt, 0, msg, "[missing image]")
	require.Len(t, projected.ContentParts, 1)
	require.Equal(t, model.ContentTypeText, projected.ContentParts[0].Type)
	require.NotNil(t, projected.ContentParts[0].Text)
	require.Equal(t, "[missing image]", *projected.ContentParts[0].Text)

	require.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
}

func TestProjectUnavailableImageURLsScopesToEventPart(t *testing.T) {
	const imageURL = "https://example.invalid/shared.png"
	first := model.NewUserMessage("first")
	first.AddImageURL(imageURL, "auto")
	second := model.NewUserMessage("second")
	second.AddImageURL(imageURL, "auto")
	firstEvent := imageMessageEvent("first-inv", first)
	secondEvent := imageMessageEvent("second-inv", second)
	sess := &session.Session{Events: []event.Event{*firstEvent, *secondEvent}}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(first),
	)
	inv.InvocationID = "first-inv"

	_, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{first}},
		errors.New("failed to fetch image"),
		nil,
	)
	require.NoError(t, err)

	projectedFirst := ProjectUnavailableImageURLs(
		sess,
		*firstEvent,
		0,
		first,
		"[missing image]",
	)
	require.Equal(t, model.ContentTypeText, projectedFirst.ContentParts[0].Type)

	projectedSecond := ProjectUnavailableImageURLs(
		sess,
		*secondEvent,
		0,
		second,
		"[missing image]",
	)
	require.Equal(t, model.ContentTypeImage, projectedSecond.ContentParts[0].Type)
	require.Equal(t, imageURL, projectedSecond.ContentParts[0].Image.URL)
}

func TestProjectUnavailableImageURLsIgnoresLegacyURLOnlyState(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	evt := imageMessageEvent("current-inv", msg)
	raw, err := json.Marshal(unavailableImageURLState{
		Version: 1,
		URLs: []UnavailableImageURLRecord{
			{URL: imageURL},
		},
	})
	require.NoError(t, err)
	sess := &session.Session{Events: []event.Event{*evt}}
	sess.SetState(UnavailableImageURLsStateKey, raw)

	projected := ProjectUnavailableImageURLs(sess, *evt, 0, msg, "[missing image]")

	require.Len(t, projected.ContentParts, 1)
	require.Equal(t, model.ContentTypeImage, projected.ContentParts[0].Type)
	require.Equal(t, imageURL, projected.ContentParts[0].Image.URL)
}

func TestMarkUnavailableImageURLsFromRequestUsesResponseParam(t *testing.T) {
	const (
		firstURL  = "https://example.invalid/first.png"
		secondURL = "https://example.invalid/second.png"
	)
	first := model.NewUserMessage("first")
	first.AddImageURL(firstURL, "auto")
	second := model.NewUserMessage("second")
	second.AddImageURL(secondURL, "auto")
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("first-inv", first),
			*imageMessageEvent("second-inv", second),
		},
	}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	param := "messages[1].content[0].image_url.url"

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{first, second}},
		errors.New("invalid request"),
		&model.ResponseError{Message: "invalid image url", Param: &param},
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	unavailable := UnavailableImageURLSet(sess)
	require.NotContains(t, unavailable, firstURL)
	require.Contains(t, unavailable, secondURL)
}

func TestMarkUnavailableImageURLsFromRequestSkipsAmbiguousDuplicateURL(
	t *testing.T,
) {
	const imageURL = "https://example.invalid/shared.png"
	first := model.NewUserMessage("first")
	first.AddImageURL(imageURL, "auto")
	second := model.NewUserMessage("second")
	second.AddImageURL(imageURL, "auto")
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			*imageMessageEvent("first-inv", first),
			*imageMessageEvent("second-inv", second),
		},
	}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{first, second}},
		errors.New("failed to fetch image url "+imageURL),
		nil,
	)

	require.NoError(t, err)
	require.Zero(t, count)
	require.Empty(t, UnavailableImageURLSet(sess))
}

func imageMessageEvent(invocationID string, msg model.Message) *event.Event {
	return event.NewResponseEvent(
		invocationID,
		"user",
		&model.Response{
			Choices: []model.Choice{{Message: msg}},
		},
	)
}

type failingUpdateSessionService struct {
	*sessionnoop.Service
	err error
}

func (s *failingUpdateSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	return s.err
}

type persistedSessionService struct {
	*sessionnoop.Service
	sess *session.Session
	key  session.Key
}

func (s *persistedSessionService) GetSession(
	_ context.Context,
	key session.Key,
	_ ...session.Option,
) (*session.Session, error) {
	s.key = key
	return s.sess, nil
}

type routedSessionRouter struct {
	sess *session.Session
}

func (r routedSessionRouter) RouteEvent(
	*agent.Invocation,
	*event.Event,
) (*session.Session, bool) {
	return r.sess, r.sess != nil
}
