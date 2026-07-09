package normalize

// 归一化： 将不同后端拿出的东西 处理转化/映射成 在同一评价标准下

import (
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// 同一的格式
type Event struct {
	Index   int
	Role    string
	Content string
}

type SnapShot struct {
	SessionId string
	Events    []Event
}

//从某一session 转化得到 snapshot

func FromSession(sess *session.Session) *SnapShot {

	if sess == nil {
		return &SnapShot{}
	}

	snapshot := &SnapShot{
		SessionId: sess.ID,
		Events:    make([]Event, 0, len(sess.Events)),
	}

	for i, evt := range sess.Events {
		role, content := RoleAndContent(evt)

		snapshot.Events = append(snapshot.Events, Event{
			Index:   i,
			Role:    role,
			Content: content,
		})

	}
	return snapshot
}

func RoleAndContent(evt event.Event) (string, string) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return "", ""
	}
	msg := evt.Response.Choices[0].Message
	return string(msg.Role), msg.Content
}
