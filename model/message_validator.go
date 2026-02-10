//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

const nonEmptyContentPlaceholder = " "

type roleGroupKind uint8

const (
	roleGroupUnknown roleGroupKind = iota
	roleGroupUserTool
	roleGroupAssistant
)

// HasPayload reports whether the message has non-empty Content or ContentParts.
func HasPayload(msg Message) bool {
	return msg.Content != "" || len(msg.ContentParts) > 0
}

func roleGroupOf(role Role) roleGroupKind {
	switch role {
	case RoleUser, RoleTool:
		return roleGroupUserTool
	case RoleAssistant:
		return roleGroupAssistant
	default:
		return roleGroupUnknown
	}
}

// validateAndFixMessageSequence validates and fixes a message sequence to
// ensure it complies with strict chat API requirements.
//
// Requirements enforced for non-system messages:
// - Roles must be one of system/user/assistant/tool.
// - Messages must alternate between user/tool group and assistant group.
// - The last message must be user or tool.
// - Content must not be empty.
//
// The function operates at the round level: a round starts with a user message
// group and includes everything up to (but excluding) the next user message
// group. A round is either fully kept or fully removed.
func validateAndFixMessageSequence(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}

	messages = removeInvalidRoleMessages(messages)
	if len(messages) == 0 {
		return messages
	}

	messages = ensureNonEmptyContent(messages)
	if len(messages) == 0 {
		return messages
	}

	prefix, rounds := splitIntoUserAnchoredRounds(messages)
	rounds = filterValidRounds(rounds)

	result := make([]Message, 0, len(prefix)+len(messages))
	result = append(result, prefix...)
	for _, r := range rounds {
		result = append(result, r...)
	}

	result = ensureLastMessageIsUserOrTool(result)
	if len(result) == 0 {
		return result
	}

	return result
}

func removeInvalidRoleMessages(messages []Message) []Message {
	result := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role.IsValid() {
			result = append(result, msg)
		}
	}
	return result
}

func ensureNonEmptyContent(messages []Message) []Message {
	result := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Content != "" {
			result = append(result, msg)
			continue
		}

		hasPayload := len(msg.ContentParts) > 0 ||
			len(msg.ToolCalls) > 0 ||
			msg.ReasoningContent != ""
		if !hasPayload {
			continue
		}

		msg.Content = nonEmptyContentPlaceholder
		result = append(result, msg)
	}
	return result
}

func splitIntoUserAnchoredRounds(messages []Message) ([]Message, [][]Message) {
	prefix := make([]Message, 0)
	rounds := make([][]Message, 0)

	var (
		currRound       []Message
		inRound         bool
		lastNonSystem   Role
		hasNonSystemAny bool
	)

	flushRound := func() {
		if len(currRound) == 0 {
			return
		}
		rounds = append(rounds, currRound)
		currRound = nil
	}

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if inRound {
				currRound = append(currRound, msg)
			} else {
				prefix = append(prefix, msg)
			}
			continue
		}

		hasNonSystemAny = true
		if msg.Role == RoleUser && lastNonSystem != RoleUser {
			if inRound {
				flushRound()
			}
			inRound = true
			currRound = make([]Message, 0, 8)
		}

		if !inRound {
			lastNonSystem = msg.Role
			continue
		}

		currRound = append(currRound, msg)
		lastNonSystem = msg.Role
	}

	if hasNonSystemAny {
		flushRound()
	}

	return prefix, rounds
}

func filterValidRounds(rounds [][]Message) [][]Message {
	result := make([][]Message, 0, len(rounds))
	for _, r := range rounds {
		if len(r) == 0 {
			continue
		}
		if !isRoundValid(r) {
			continue
		}
		result = append(result, r)
	}
	return result
}

func isRoundValid(round []Message) bool {
	firstNonSystemRole := Role("")
	for _, msg := range round {
		if msg.Role == RoleSystem {
			continue
		}
		firstNonSystemRole = msg.Role
		break
	}

	if firstNonSystemRole != RoleUser {
		return false
	}

	var (
		prevGroup roleGroupKind
		hasPrev   bool
	)
	for _, msg := range round {
		if msg.Role == RoleSystem {
			continue
		}
		group := roleGroupOf(msg.Role)
		if group == roleGroupUnknown {
			return false
		}
		if !hasPrev {
			if group != roleGroupUserTool {
				return false
			}
			prevGroup = group
			hasPrev = true
			continue
		}
		if group == prevGroup {
			continue
		}
		prevGroup = group
	}

	return true
}

func ensureLastMessageIsUserOrTool(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}

	// Remove trailing system messages. This ensures the last message is a
	// strict user/tool message when possible.
	end := len(messages)
	for end > 0 && messages[end-1].Role == RoleSystem {
		end--
	}
	messages = messages[:end]
	if len(messages) == 0 {
		return nil
	}

	if messages[len(messages)-1].Role == RoleUser ||
		messages[len(messages)-1].Role == RoleTool {
		return messages
	}

	// Remove trailing assistant group.
	i := len(messages) - 1
	for i >= 0 && messages[i].Role == RoleAssistant {
		i--
	}
	messages = messages[:i+1]

	if len(messages) == 0 {
		return nil
	}
	if messages[len(messages)-1].Role == RoleUser ||
		messages[len(messages)-1].Role == RoleTool {
		return messages
	}
	return nil
}
