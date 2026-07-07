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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
	)

	require.NoError(t, err)
	require.Equal(t, 1, count)
	unavailable := UnavailableImageURLSet(sess)
	require.NotContains(t, unavailable, currentURL)
	require.Contains(t, unavailable, otherURL)
}

func TestMarkUnavailableImageURLsFromRequestFallsBackToAllRequestURLs(t *testing.T) {
	const (
		currentURL = "https://example.invalid/current.png"
		otherURL   = "https://example.invalid/other.png"
	)
	currentMsg := model.NewUserMessage("current")
	currentMsg.AddImageURL(currentURL, "auto")
	otherMsg := model.NewUserMessage("other")
	otherMsg.AddImageURL(otherURL, "auto")

	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(currentMsg),
	)
	req := &model.Request{Messages: []model.Message{otherMsg, currentMsg}}

	count, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		req,
		errors.New("failed to fetch image"),
	)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	unavailable := UnavailableImageURLSet(sess)
	require.Contains(t, unavailable, currentURL)
	require.Contains(t, unavailable, otherURL)
}

func TestProjectUnavailableImageURLs(t *testing.T) {
	const imageURL = "https://example.invalid/current.png"
	msg := model.NewUserMessage("current")
	msg.AddImageURL(imageURL, "auto")
	sess := &session.Session{}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
	)

	_, err := MarkUnavailableImageURLsFromRequest(
		context.Background(),
		inv,
		&model.Request{Messages: []model.Message{msg}},
		errors.New("failed to fetch image"),
	)
	require.NoError(t, err)

	projected := ProjectUnavailableImageURLs(sess, msg, "[missing image]")
	require.Len(t, projected.ContentParts, 1)
	require.Equal(t, model.ContentTypeText, projected.ContentParts[0].Type)
	require.NotNil(t, projected.ContentParts[0].Text)
	require.Equal(t, "[missing image]", *projected.ContentParts[0].Text)

	require.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
}
