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
