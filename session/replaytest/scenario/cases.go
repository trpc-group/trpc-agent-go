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