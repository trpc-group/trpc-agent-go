//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import "fmt"

// Error represents a JSON repair error with position information.
type Error struct {
	Message  string // Message is the error message.
	Position int    // Position is the position of the error in the input string.
}

// Error returns the error message and position.
func (e *Error) Error() string {
	return fmt.Sprintf("%s at position %d", e.Message, e.Position)
}
