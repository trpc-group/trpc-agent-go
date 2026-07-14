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
	"fmt"
	"regexp"
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
	param := "messages[1].content[1].image_url.url"

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

func TestMarkUnavailableImageURLsFromRequestMapsProviderContentIndex(
	t *testing.T,
) {
	const (
		firstURL  = "https://example.invalid/first.png"
		secondURL = "https://example.invalid/second.png"
	)
	tests := []struct {
		name       string
		param      string
		wantCount  int
		wantFirst  bool
		wantSecond bool
	}{
		{
			name:      "content text is not an image",
			param:     "messages[0].content[0].image_url.url",
			wantCount: 0,
		},
		{
			name:      "first image follows message content",
			param:     "messages[0].content[1].image_url.url",
			wantCount: 1,
			wantFirst: true,
		},
		{
			name:       "second image follows message content",
			param:      "messages[0].content[2].image_url.url",
			wantCount:  1,
			wantSecond: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := model.NewUserMessage("describe these images")
			msg.AddImageURL(firstURL, "auto")
			msg.AddImageURL(secondURL, "auto")
			sess := &session.Session{
				ID:      "sess",
				AppName: "app",
				UserID:  "user",
				Events: []event.Event{
					*imageMessageEvent("inv", msg),
				},
			}
			inv := agent.NewInvocation(agent.WithInvocationSession(sess))

			count, err := MarkUnavailableImageURLsFromRequest(
				context.Background(),
				inv,
				&model.Request{Messages: []model.Message{msg}},
				errors.New("invalid request"),
				&model.ResponseError{
					Message: "invalid image url",
					Param:   &tt.param,
				},
			)

			require.NoError(t, err)
			require.Equal(t, tt.wantCount, count)
			unavailable := UnavailableImageURLSet(sess)
			if tt.wantFirst {
				require.Contains(t, unavailable, firstURL)
			} else {
				require.NotContains(t, unavailable, firstURL)
			}
			if tt.wantSecond {
				require.Contains(t, unavailable, secondURL)
			} else {
				require.NotContains(t, unavailable, secondURL)
			}
		})
	}
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

func TestMarkUnavailableImageURLsFromRequestHandlesEdgeInputs(t *testing.T) {
	msg := model.NewUserMessage("current")
	msg.AddImageURL("https://example.invalid/current.png", "auto")
	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		nil,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("failed to fetch image"),
		nil,
	)
	require.NoError(t, err)
	require.Zero(t, count)

	inv := agent.NewInvocation()
	count, err = MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("failed to fetch image"),
		nil,
	)
	require.NoError(t, err)
	require.Zero(t, count)

	inv = agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "sess"}),
	)
	count, err = MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		nil,
		errors.New("failed to fetch image"),
		nil,
	)
	require.NoError(t, err)
	require.Zero(t, count)

	require.True(t, IsImageURLFailureForRequest(
		errors.New("failed to fetch image url"),
		nil,
		nil,
	))
}

func TestMarkUnavailableImageURLsFromRequestPropagatesPersistedSessionError(
	t *testing.T,
) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	getErr := errors.New("get failed")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		}),
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSessionService(&failingGetSessionService{
			Service: sessionnoop.NewService(),
			err:     getErr,
		}),
	)

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("Unable to download image from "+imageURL),
		nil,
	)

	require.ErrorIs(t, err, getErr)
	require.Zero(t, count)
}

func TestWriteUnavailableImageURLMarksUpdatesSkipsAndTrims(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	raw, err := json.Marshal(unavailableImageURLState{
		Version: unavailableImageURLStateVersion,
		URLs: []UnavailableImageURLRecord{
			{URL: "https://example.invalid/legacy.png"},
			{EventID: "evt", PartIndex: 0, URL: imageURL},
		},
	})
	require.NoError(t, err)
	sess.SetState(UnavailableImageURLsStateKey, raw)
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	inv.InvocationID = "inv"
	inv.RunOptions.RequestID = "req"

	count, err := writeUnavailableImageURLMarks(
		context.Background(),
		inv,
		sess,
		[]imageURLMark{
			{URL: "https://example.invalid/invalid.png"},
			{EventID: "evt", PartIndex: 0, URL: imageURL},
		},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	state := readUnavailableImageURLState(sess)
	require.Len(t, state.URLs, 2)
	require.Equal(t, "req", state.URLs[1].RequestID)
	require.Empty(t, state.URLs[1].Error)

	var marks []imageURLMark
	for i := 0; i < maxUnavailableImageURLRecords+5; i++ {
		marks = append(marks, imageURLMark{
			EventID:   fmt.Sprintf("evt-%d", i),
			PartIndex: i,
			URL:       fmt.Sprintf("https://example.invalid/%d.png", i),
		})
	}
	count, err = writeUnavailableImageURLMarks(
		context.Background(),
		inv,
		sess,
		marks,
		errors.New("failed"),
	)
	require.NoError(t, err)
	require.Equal(t, maxUnavailableImageURLRecords+5, count)
	state = readUnavailableImageURLState(sess)
	require.Len(t, state.URLs, maxUnavailableImageURLRecords)
}

func TestWriteUnavailableImageURLMarksNoValidMarks(t *testing.T) {
	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))

	count, err := writeUnavailableImageURLMarks(
		context.Background(),
		inv,
		sess,
		[]imageURLMark{{URL: "https://example.invalid/invalid.png"}},
		errors.New("failed"),
	)

	require.NoError(t, err)
	require.Zero(t, count)
}

func TestProjectUnavailableImageURLsSkipsNonMatchingParts(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	matchingMsg := model.NewUserMessage("current")
	matchingMsg.AddImageURL(imageURL, "auto")
	evt := imageMessageEvent("current-inv", matchingMsg)
	raw, err := json.Marshal(unavailableImageURLState{
		Version: unavailableImageURLStateVersion,
		URLs: []UnavailableImageURLRecord{
			{EventID: evt.ID, PartIndex: 4, URL: imageURL},
		},
	})
	require.NoError(t, err)
	sess := &session.Session{}
	sess.SetState(UnavailableImageURLsStateKey, raw)
	text := "hello"
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage},
			{Type: model.ContentTypeImage, Image: &model.Image{}},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.invalid/other.png"}},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: imageURL}},
		},
	}

	projected := ProjectUnavailableImageURLs(sess, *evt, 0, msg, "")

	require.Equal(t, model.ContentTypeText, projected.ContentParts[4].Type)
	require.NotNil(t, projected.ContentParts[4].Text)
	require.Equal(t, DefaultUnavailableImageURLPlaceholder, *projected.ContentParts[4].Text)
}

func TestUnavailableImageURLStateHelpersSkipEmptyAndLegacyRecords(t *testing.T) {
	sess := &session.Session{}
	require.Nil(t, unavailableImageURLLocationSet(sess))
	raw, err := json.Marshal(unavailableImageURLState{
		Version: unavailableImageURLStateVersion,
		URLs: []UnavailableImageURLRecord{
			{URL: ""},
			{URL: "https://example.invalid/legacy.png"},
		},
	})
	require.NoError(t, err)
	sess.SetState(UnavailableImageURLsStateKey, raw)

	require.Equal(t, map[string]struct{}{
		"https://example.invalid/legacy.png": {},
	}, UnavailableImageURLSet(sess))
	require.Empty(t, unavailableImageURLLocationSet(sess))
}

func TestImageURLMarkSelectionEdgeCases(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	sess := &session.Session{
		Events: []event.Event{
			*imageMessageEvent("inv", msg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
	)
	inv.InvocationID = "inv"

	require.Nil(t, imageURLMarksToMarkInSession(
		nil,
		sess,
		&model.Request{Messages: []model.Message{msg}},
		nil,
		nil,
	))
	require.Nil(t, imageURLMarksToMarkInSession(
		inv,
		sess,
		&model.Request{Messages: []model.Message{model.NewUserMessage("text")}},
		nil,
		nil,
	))

	require.Nil(t, imageURLMarksFromResponseParam(sess, nil, nil, nil))
	badParam := "messages[10].content[0].image_url.url"
	require.Nil(t, imageURLMarksFromResponseParam(
		sess,
		&model.Request{Messages: []model.Message{msg}},
		nil,
		&model.ResponseError{Param: &badParam},
	))
	badParam = "messages[0].content[10].image_url.url"
	require.Nil(t, imageURLMarksFromResponseParam(
		sess,
		&model.Request{Messages: []model.Message{msg}},
		nil,
		&model.ResponseError{Param: &badParam},
	))
	badParam = "messages[0].content[0].image_url.url"
	require.Nil(t, imageURLMarksFromResponseParam(
		sess,
		&model.Request{Messages: []model.Message{model.NewUserMessage("text")}},
		nil,
		&model.ResponseError{Param: &badParam},
	))
	emptyImage := model.NewUserMessage("empty")
	emptyImage.ContentParts = []model.ContentPart{
		{Type: model.ContentTypeImage, Image: &model.Image{}},
	}
	require.Nil(t, imageURLMarksFromResponseParam(
		sess,
		&model.Request{Messages: []model.Message{emptyImage}},
		nil,
		&model.ResponseError{Param: &badParam},
	))

	other := model.NewUserMessage("other")
	other.AddImageURL(imageURL, "auto")
	require.Nil(t, imageURLMarksFromResponseParam(
		sess,
		&model.Request{Messages: []model.Message{other}},
		nil,
		&model.ResponseError{Param: &badParam},
	))
}

func TestImageURLRefSelectionHelpers(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	sess := &session.Session{
		Events: []event.Event{
			*imageMessageEvent("inv", msg),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
	)
	inv.InvocationID = "other-inv"

	require.Nil(t, currentInvocationImageURLMarks(nil, sess, []string{imageURL}))
	require.Empty(t, currentInvocationImageURLMarks(
		inv,
		sess,
		[]string{"https://example.invalid/missing.png"},
	))
	require.Empty(t, currentInvocationImageURLMarks(inv, sess, []string{imageURL}))
	inv.InvocationID = "inv"
	inv.Message = model.NewUserMessage("different")
	require.Empty(t, currentInvocationImageURLMarks(inv, sess, []string{imageURL}))

	require.Nil(t, uniqueSessionImageURLMarks(nil, &model.Request{}, []string{imageURL}))
	require.Empty(t, uniqueSessionImageURLMarks(
		sess,
		&model.Request{Messages: []model.Message{msg}},
		[]string{"https://example.invalid/missing.png"},
	))
	require.Empty(t, uniqueSessionImageURLMarks(
		sess,
		&model.Request{Messages: []model.Message{model.NewUserMessage("different")}},
		[]string{imageURL},
	))
	ambiguousSess := &session.Session{
		Events: []event.Event{
			*imageMessageEvent("inv-1", msg),
			*imageMessageEvent("inv-2", msg),
		},
	}
	require.Empty(t, uniqueSessionImageURLMarks(
		ambiguousSess,
		&model.Request{Messages: []model.Message{msg}},
		[]string{imageURL},
	))
}

func TestSessionImageURLRefsSkipsInvalidEvents(t *testing.T) {
	require.Nil(t, sessionImageURLRefs(nil))
	text := "hello"
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage},
			{Type: model.ContentTypeImage, Image: &model.Image{}},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.invalid/current.png"}},
		},
	}
	valid := imageMessageEvent("inv", msg)
	sess := &session.Session{
		Events: []event.Event{
			{},
			{ID: "no-response"},
			*valid,
		},
	}

	refs := sessionImageURLRefs(sess)

	require.Len(t, refs, 1)
	require.Equal(t, "https://example.invalid/current.png", refs[0].URL)
}

func TestSmallImageURLHelpers(t *testing.T) {
	require.False(t, requestContainsMessage(nil, model.NewUserMessage("x")))
	code := "invalid_image_url"
	param := "messages[0].content[0].image_url.url"
	nested := &model.ResponseError{
		Message: "invalid image url",
		Code:    &code,
		Param:   &param,
	}
	require.Same(t, nested, responseError(nested, nil))
	require.Contains(t, imageFailureText(nested, nil), "invalid image url")
	require.Empty(t, errorString(nil))

	require.Equal(t, []string{"a", "b"}, dedupeOrdered([]string{"", "a", "a", "b"}))
	require.Equal(t, map[string]struct{}{"a": {}, "b": {}}, urlSet([]string{"", "a", "b"}))
	require.Nil(t, dedupeImageURLMarks(nil))
	require.Equal(t, []imageURLMark{{
		EventID:   "evt",
		PartIndex: 0,
		URL:       "u",
	}}, dedupeImageURLMarks([]imageURLMark{
		{URL: "invalid"},
		{EventID: "evt", PartIndex: 0, URL: "u"},
		{EventID: "evt", PartIndex: 0, URL: "u"},
	}))

	require.False(t, containsURLToken("", "u"))
	require.False(t, containsURLToken("prefix", "missing"))
	require.False(t, containsURLToken("xhttps://example.invalid/a.pngsuffix", "https://example.invalid/a.png"))
	require.True(t, containsURLToken("(https://example.invalid/a.png)", "https://example.invalid/a.png"))
	require.False(t, containsWordSignal("", "link"))
	require.False(t, containsWordSignal("blink", "link"))
	require.True(t, containsWordSignal("use link now", "link"))
	require.True(t, isURLTokenBoundaryBefore("https://example.invalid/a.png", 0))
	require.True(t, isURLTokenBoundaryAfter("https://example.invalid/a.png", len("https://example.invalid/a.png")))
	require.True(t, isWordBoundaryBefore("link", 0))
	require.True(t, isWordBoundaryAfter("link", len("link")))
}

func TestParseAndExtractImageURLEdgeCases(t *testing.T) {
	require.Equal(t, []string(nil), requestImageURLs(nil))
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText},
			{Type: model.ContentTypeImage, Image: &model.Image{}},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.invalid/a.png"}},
		},
	}
	require.Equal(t, []string{"https://example.invalid/a.png"}, messageImageURLs(msg))
	require.Nil(t, mentionedURLs([]string{"https://example.invalid/a.png"}, ""))

	_, _, ok := parseImageURLParam("")
	require.False(t, ok)
	messageIndex, partIndex, ok := parseImageURLParam("messages.2.content.3.image_url.url")
	require.True(t, ok)
	require.Equal(t, 2, messageIndex)
	require.Equal(t, 3, partIndex)
	_, _, ok = parseImageURLParamWithPattern(regexp.MustCompile(`x(\w+)y(\w+)`), "xay1")
	require.False(t, ok)
	_, _, ok = parseImageURLParamWithPattern(regexp.MustCompile(`x(\d+)y(\w+)`), "x1ya")
	require.False(t, ok)
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

type failingGetSessionService struct {
	*sessionnoop.Service
	err error
}

func (s *failingGetSessionService) GetSession(
	_ context.Context,
	_ session.Key,
	_ ...session.Option,
) (*session.Session, error) {
	return nil, s.err
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
