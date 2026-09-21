//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRetainTypedUserMessage(t *testing.T) {
	hello, world, annot := "hello", "world", "annotation"
	image, file := imagePart(), filePart()
	helloWorldInv := invState(userMsg("", textPart(hello), textPart(world), file))
	interleavedInv := invState(userMsg("", textPart(hello), image, textPart(world)))
	merged := userMsg("first", textPart(annot), textPart(hello), textPart(world), file)

	tests := []struct {
		name      string
		state     State
		last      model.Message
		userInput string
		wantOK    bool
		want      model.Message
	}{
		{
			name:  "empty user input",
			state: helloWorldInv,
			last:  userMsg("", textPart(hello)),
		},
		{
			name:      "last without content parts",
			state:     helloWorldInv,
			last:      model.NewUserMessage(hello),
			userInput: hello + "\n" + world,
		},
		{
			name: "content plus parts invocation is ineligible",
			state: invState(model.Message{
				Role:         model.RoleUser,
				Content:      hello,
				ContentParts: []model.ContentPart{textPart(hello), image},
			}),
			last:      userMsg("", textPart(hello), image),
			userInput: hello,
		},
		{
			name:      "unchanged baseline keeps merged content and extra parts",
			state:     helloWorldInv,
			last:      merged,
			userInput: hello + "\n" + world,
			wantOK:    true,
			want:      merged,
		},
		{
			name:      "rewrite later window after leading text keeps that part",
			state:     helloWorldInv,
			last:      userMsg("", textPart(annot), textPart(hello), textPart(world), file),
			userInput: hello,
			wantOK:    true,
			want:      userMsg("", textPart(annot), textPart(hello), file),
		},
		{
			name:      "rewrite interleaved text-image-text keeps image and content",
			state:     interleavedInv,
			last:      userMsg("first", textPart(hello), image, textPart(world)),
			userInput: hello,
			wantOK:    true,
			want:      userMsg("first", textPart(hello), image),
		},
		{
			name:      "unmatched media fail-closed keeps last",
			state:     helloWorldInv,
			last:      userMsg("", textPart(hello), image),
			userInput: "rewritten",
			wantOK:    true,
			want:      userMsg("", textPart(hello), image),
		},
		{
			name:      "unmatched text-only falls through",
			state:     helloWorldInv,
			last:      userMsg("", textPart(hello)),
			userInput: "rewritten",
		},
		{
			name:      "empty invocation baseline keeps media last",
			state:     invState(userMsg("", image)),
			last:      userMsg("", textPart(hello), image),
			userInput: hello,
			wantOK:    true,
			want:      userMsg("", textPart(hello), image),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := retainTypedUserMessage(tt.state, tt.last, tt.userInput)
			require.Equal(t, tt.wantOK, ok)
			require.True(t, model.MessagesEqual(tt.want, got))
		})
	}
}

func TestRewriteContentPartsUserText(t *testing.T) {
	hello, world, empty := "hello", "world", ""
	image := imagePart()
	tests := []struct {
		name      string
		msg       model.Message
		original  string
		userInput string
		wantOK    bool
		want      model.Message
	}{
		{name: "empty original", msg: userMsg("", textPart(hello), image), userInput: hello},
		{
			name:      "user input equals original",
			msg:       userMsg("", textPart(hello), textPart(world)),
			original:  hello + "\n" + world,
			userInput: hello + "\n" + world,
		},
		{name: "empty parts", msg: model.NewUserMessage(hello), original: hello, userInput: world},
		{
			name: "skips empty and nil text parts",
			msg: userMsg("",
				model.ContentPart{Type: model.ContentTypeText},
				model.ContentPart{Type: model.ContentTypeText, Text: &empty},
				image,
			),
			original:  hello,
			userInput: world,
		},
		{
			name:      "single text window keeps image",
			msg:       userMsg("", textPart(hello), image),
			original:  hello,
			userInput: world,
			wantOK:    true,
			want:      userMsg("", textPart(world), image),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rewriteContentPartsUserText(tt.msg, tt.original, tt.userInput)
			require.Equal(t, tt.wantOK, ok)
			require.True(t, model.MessagesEqual(tt.want, got))
		})
	}
}

func TestInvocationContentPartsHelpers(t *testing.T) {
	hello, world := "hello", "world"
	image := imagePart()
	tests := []struct {
		name      string
		state     State
		partsOnly bool
		text      string
	}{
		{name: "nil state"},
		{name: "empty state", state: State{}},
		{name: "wrong-type exec context", state: State{StateKeyExecContext: "x"}},
		{name: "nil invocation", state: State{StateKeyExecContext: &ExecutionContext{}}},
		{
			name:      "parts only",
			state:     invState(userMsg("", textPart(hello), image, textPart(world))),
			partsOnly: true,
			text:      hello + "\n" + world,
		},
		{
			name: "content wins",
			state: invState(model.Message{
				Content:      hello,
				ContentParts: []model.ContentPart{textPart(world), image},
			}),
			text: hello,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.partsOnly, invocationIsContentPartsOnly(tt.state))
			require.Equal(t, tt.text, invocationTextContent(tt.state))
		})
	}
	require.False(t, hasNonTextContentPart(userMsg("", textPart(hello))))
	require.True(t, hasNonTextContentPart(userMsg("", image)))
	require.False(t, nonEmptyTextPart(model.ContentPart{Type: model.ContentTypeText}))
	require.False(t, nonEmptyTextPart(model.ContentPart{Type: model.ContentTypeText, Text: ptr("")}))
	require.False(t, nonEmptyTextPart(image))
	require.True(t, nonEmptyTextPart(textPart(hello)))
}

func invState(msg model.Message) State {
	return State{StateKeyExecContext: &ExecutionContext{
		Invocation: &agent.Invocation{Message: msg},
	}}
}

func userMsg(content string, parts ...model.ContentPart) model.Message {
	return model.Message{Role: model.RoleUser, Content: content, ContentParts: parts}
}

func textPart(s string) model.ContentPart {
	return model.ContentPart{Type: model.ContentTypeText, Text: &s}
}

func imagePart() model.ContentPart {
	return model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: &model.Image{URL: "https://example.com/a.png"},
	}
}

func filePart() model.ContentPart {
	return model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{Name: "notes.txt", Data: []byte("notes")},
	}
}

func ptr(s string) *string { return &s }
