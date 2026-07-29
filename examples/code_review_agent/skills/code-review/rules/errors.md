# Error Handling Rules

## ERR-001: Function Call Result Ignored

- **type**: token
- **severity**: high
- **pattern**: `^\s*_\s*,\s*_\s*:?=\s*\w+(\.\w+)?\(|^\s*\w+\s*,\s*_\s*:?=\s*\w+(\.\w+)?\(|^\s*_\s*:?=\s*\w+(\.\w+)?\(|^\s*_\s*=\s*\w+(\.\w+)?\(`
- **message**: "函数调用的返回值被 _ 丢弃（可能是 error）。应显式检查 error"
- **fix**: "显式检查 error: `val, err := func(); if err != nil { return err }`"

## ERR-002: Bare Error Return Without Wrap

- **type**: token
- **severity**: medium
- **pattern**: `^\s*return\s+err\s*$`
- **message**: "error 直接 return，未使用 fmt.Errorf(\"...: %w\", err) 包装上下文信息"
- **fix**: "使用 fmt.Errorf(\"doing X: %w\", err) 包装错误，保留调用链信息"

## ERR-003: Panic in Non-Init Function

- **type**: token
- **severity**: high
- **pattern**: `^\s*panic\(`
- **message**: "库代码中直接使用 panic，应由调用方处理错误"
- **fix**: "改为 return error，让调用方决定如何处理。仅在不可恢复的初始化中使用 panic"
