package telegram

// Update is a Telegram update.
//
// Telegram docs: core.telegram.org/bots/api#update
type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message is a Telegram message.
//
// Telegram docs: core.telegram.org/bots/api#message
type Message struct {
	MessageID int    `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      *Chat  `json:"chat,omitempty"`
	Text      string `json:"text,omitempty"`
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

const (
	chatTypePrivate    = "private"
	chatTypeGroup      = "group"
	chatTypeSuperGroup = "supergroup"
)

// IsGroupChat reports whether the chat type represents a group chat.
func IsGroupChat(chatType string) bool {
	return chatType == chatTypeGroup || chatType == chatTypeSuperGroup
}
