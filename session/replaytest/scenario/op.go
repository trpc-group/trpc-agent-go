// 定义所有的 测试支持的 操作类型

package scenario

// 定义 case 基本结构
type Case struct {
	Name string // case 名称
	Ops  []Op   // case 操作
}

// 新类型
type OpKind string

type Op struct {
	Kind    OpKind            // 具体操作类型
	Role    string            // user / assistant
	Content string            // 信息内容
	State   map[string]string // 状态

	ToolID   string // 工具ID
	ToolName string
	ToolArgs string

	FilterKey string
	Force     bool

	TrackName    string
	TrackPayload string
}

// 枚举操作类型
const (
	OpCreateSession      OpKind = "create_session"
	OpAppendEvent        OpKind = "append_event"
	OpUpdateState        OpKind = "update_state"
	OpWriteInMemory      OpKind = "write_in_memory"
	OpUpdateSummary      OpKind = "update_summary"
	OpAppendToolCall     OpKind = "append_tool_call"
	OpAppendToolResponse OpKind = "append_tool_response"
	OpCreateSummary      OpKind = "create_summary"
	OpAppendTrack        OpKind = "append_track"
)
