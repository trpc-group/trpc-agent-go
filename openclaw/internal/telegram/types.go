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

// Update is a Telegram update.
//
// Telegram docs: core.telegram.org/bots/api#update
type Update struct {
	UpdateID      int              `json:"update_id"`
	Message       *Message         `json:"message,omitempty"`
	CallbackQuery *CallbackQuery   `json:"callback_query,omitempty"`
	MyChatMember  *ChatMemberEvent `json:"my_chat_member,omitempty"`
}

// Message is a Telegram message.
//
// Telegram docs: core.telegram.org/bots/api#message
type Message struct {
	MessageID       int      `json:"message_id"`
	MessageThreadID int      `json:"message_thread_id,omitempty"`
	From            *User    `json:"from,omitempty"`
	Chat            *Chat    `json:"chat,omitempty"`
	Text            string   `json:"text,omitempty"`
	Caption         string   `json:"caption,omitempty"`
	ReplyToMessage  *Message `json:"reply_to_message,omitempty"`

	Photo     []PhotoSize `json:"photo,omitempty"`
	Document  *Document   `json:"document,omitempty"`
	Audio     *Audio      `json:"audio,omitempty"`
	Voice     *Voice      `json:"voice,omitempty"`
	Video     *Video      `json:"video,omitempty"`
	Animation *Animation  `json:"animation,omitempty"`
	VideoNote *VideoNote  `json:"video_note,omitempty"`
}

// CallbackQuery is an inline keyboard callback interaction.
//
// Telegram docs: core.telegram.org/bots/api#callbackquery
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from,omitempty"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// InlineKeyboardMarkup is one inline keyboard payload.
//
// Telegram docs: core.telegram.org/bots/api#inlinekeyboardmarkup
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// InlineKeyboardButton is one inline keyboard button.
//
// Telegram docs: core.telegram.org/bots/api#inlinekeyboardbutton
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

// PhotoSize is one size variant of a photo.
//
// Telegram docs: core.telegram.org/bots/api#photosize
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// Document is one general file (sent as document).
//
// Telegram docs: core.telegram.org/bots/api#document
type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// Audio is one audio file.
//
// Telegram docs: core.telegram.org/bots/api#audio
type Audio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// Voice is one voice note.
//
// Telegram docs: core.telegram.org/bots/api#voice
type Voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// Video is one video file.
//
// Telegram docs: core.telegram.org/bots/api#video
type Video struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// Animation is one animated file. Telegram often uses this shape for
// short silent clips or GIF-like uploads.
type Animation struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// VideoNote is one circular video note.
type VideoNote struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// File is a Telegram file descriptor returned by getFile.
//
// Telegram docs: core.telegram.org/bots/api#file
type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

// BotCommand is one bot command entry for Telegram command menus.
//
// Telegram docs: core.telegram.org/bots/api#botcommand
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// User is a Telegram user.
//
// Telegram docs: core.telegram.org/bots/api#user
type User struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot,omitempty"`
	Username string `json:"username,omitempty"`
}

// Chat is a Telegram chat.
//
// Telegram docs: core.telegram.org/bots/api#chat
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// ChatMemberEvent is a my_chat_member update payload.
//
// Telegram docs: core.telegram.org/bots/api#chatmemberupdated
type ChatMemberEvent struct {
	Chat          *Chat       `json:"chat,omitempty"`
	From          *User       `json:"from,omitempty"`
	Date          int64       `json:"date,omitempty"`
	OldChatMember *ChatMember `json:"old_chat_member,omitempty"`
	NewChatMember *ChatMember `json:"new_chat_member,omitempty"`
}

// ChatMember is a minimal chat member descriptor.
//
// Telegram docs: core.telegram.org/bots/api#chatmember
type ChatMember struct {
	Status string `json:"status,omitempty"`
	User   *User  `json:"user,omitempty"`
}

const (
	chatTypePrivate    = "private"
	chatTypeGroup      = "group"
	chatTypeSuperGroup = "supergroup"
)

// IsGroupChat reports whether the chat type represents a group chat.
func IsGroupChat(chatType string) bool {
	return chatType == chatTypeGroup || chatType == chatTypeSuperGroup
}
