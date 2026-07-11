package scenario

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// MemoryWrite describes one deterministic memory write.
type MemoryWrite struct {
	Content      string
	Topics       []string
	Kind         memory.Kind
	EventTime    *time.Time
	Participants []string
	Location     string
}

// MemoryCase describes writes followed by read and search verification.
type MemoryCase struct {
	Name        string
	Writes      []MemoryWrite
	SearchQuery string
}

var memoryTaskTime = time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

// Case05_Memory covers preferences, facts, task experience, and history.
// var Case05_Memory = &MemoryCase{
// 	Name: "case05_memory",
// 	Writes: []MemoryWrite{
// 		{
// 			Content: "用户偏好简洁中文回答",
// 			Topics:  []string{"language", "preference"},
// 			Kind:    memory.KindFact,
// 		},
// 		{
// 			Content: "用户正在开发 trpc-agent-go",
// 			Topics:  []string{"project", "fact"},
// 			Kind:    memory.KindFact,
// 		},
// 		{
// 			Content:      "Windows SQLite 测试需要 MinGW GCC",
// 			Topics:       []string{"sqlite", "experience"},
// 			Kind:         memory.KindEpisode,
// 			EventTime:    &memoryTaskTime,
// 			Participants: []string{"Liam"},
// 			Location:     "Windows",
// 		},
// 		{
// 			Content: "已完成 Session replay 的事件、状态和 Track 测试",
// 			Topics:  []string{"summary", "history"},
// 			Kind:    memory.KindFact,
// 		},
// 	},
// 	SearchQuery: "SQLite GCC",
// }
