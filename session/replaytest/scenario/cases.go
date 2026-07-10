package scenario

//单轮普通对话

var Case01_SingleTurn = &Case{
	Name: "case01_single_turn",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "你好"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "你好，有什么可以帮你？"},
	},
}

var Case02_MultiTurn = &Case{
	Name: "case02_multi_turn",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "你好"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "你好，有什么可以帮你？"},
		{Kind: OpAppendEvent, Role: "user", Content: "帮我写一个 Go 测试"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "可以，先准备输入和期望输出。"},
		{Kind: OpAppendEvent, Role: "user", Content: "今天天气怎么样？"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "武汉刮台风了"},
	},
}

var Case03_UpdateState = &Case{
	Name: "case03_update_state",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpUpdateState, State: map[string]string{"weather": "晴天"}},
		{Kind: OpAppendEvent, Role: "user", Content: "今天天气怎么样？"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "今天天气晴天"},
		{Kind: OpUpdateState, State: map[string]string{"weather": "下雨"}},
		{Kind: OpAppendEvent, Role: "user", Content: "今天天气怎么样？"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "今天天气下雨"},
	},
}

var Case04_ToolCall = &Case{
	Name: "case04_tool_call",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "查一下北京天气"},
		{
			Kind:     OpAppendToolCall,
			ToolID:   "call_weather_1",
			ToolName: "weather_query",
			ToolArgs: `{"city":"北京"}`,
		},
		{
			Kind:     OpAppendToolResponse,
			ToolID:   "call_weather_1",
			ToolName: "weather_query",
			Content:  `{"weather":"晴"}`,
		},
		{Kind: OpAppendEvent, Role: "assistant", Content: "北京今天晴。"},
	},
}
var Case06_Summary = &Case{
	Name: "case06_summary",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "用户喜欢简洁中文回答"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "好的，后续会尽量简洁。"},
		{Kind: OpUpdateSummary, FilterKey: "", Force: true},
	},
}
var Case06_SummaryFilterKey = &Case{
	Name: "case06_summary_filter_key",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "查一下北京天气", FilterKey: "weather"},
		{Kind: OpAppendToolCall, ToolID: "call_weather_2", ToolName: "weather_query", ToolArgs: `{"city":"北京"}`, FilterKey: "weather"},
		{Kind: OpAppendToolResponse, ToolID: "call_weather_2", ToolName: "weather_query", Content: `{"weather":"晴"}`, FilterKey: "weather"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "北京今天晴。", FilterKey: "weather"},
		{Kind: OpAppendEvent, Role: "user", Content: "我喜欢 Go", FilterKey: "profile"},
		{Kind: OpUpdateSummary, FilterKey: "weather", Force: true},
	},
}
