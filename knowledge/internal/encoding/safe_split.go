//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package encoding

import (
	"strings"
	"unicode/utf8"
)

// SafeSplit splits text at a specific character position while respecting UTF-8 boundaries.
// This ensures that no UTF-8 characters are broken in the middle.
func SafeSplit(text string, pos int) (string, string) {
	if pos <= 0 {
		return "", text
	}

	charCount := RuneCount(text)
	if pos >= charCount {
		return text, ""
	}

	// Convert character position to byte position.
	bytePos := charToBytePos(text, pos)
	if bytePos == -1 {
		return "", text
	}

	return text[:bytePos], text[bytePos:]
}

// SafeSplitBySize splits text into chunks of specified character size while respecting UTF-8 boundaries.
// The size parameter represents the number of characters (runes), not bytes.
func SafeSplitBySize(text string, size int) []string {
	if size <= 0 {
		return []string{text}
	}

	if text == "" {
		return []string{""}
	}

	var chunks []string
	remainingText := text

	for len(remainingText) > 0 {
		// Convert character size to byte position.
		bytePos := charToBytePos(remainingText, size)
		if bytePos == -1 {
			// If we can't find the position, take the remaining text.
			chunks = append(chunks, remainingText)
			break
		}

		// Find the safe split point for this chunk.
		splitPoint := findSafeSplitPoint(remainingText, bytePos)

		// Extract the chunk.
		chunk := remainingText[:splitPoint]
		chunks = append(chunks, chunk)

		// Update remaining text.
		remainingText = remainingText[splitPoint:]
	}

	return chunks
}

// SafeSplitBySeparator splits text by separator while ensuring UTF-8 safety.
// This is similar to strings.Split but guarantees UTF-8 boundary integrity.
func SafeSplitBySeparator(text, separator string) []string {
	if separator == "" {
		// Split by individual characters (runes).
		return splitByRunes(text)
	}

	// Use standard strings.Split for non-empty separators as they're already safe.
	return strings.Split(text, separator)
}

// SafeSubstring extracts a substring from text while respecting UTF-8 boundaries.
// start and end are character positions (not byte positions).
func SafeSubstring(text string, start, end int) string {
	if start < 0 || end < start {
		return ""
	}

	charCount := RuneCount(text)
	if start >= charCount {
		return ""
	}

	// Convert character positions to byte positions.
	startByte := charToBytePos(text, start)
	if startByte == -1 {
		return ""
	}

	endByte := charToBytePos(text, end)
	if endByte == -1 {
		endByte = len(text)
	}

	// Ensure we don't exceed text boundaries.
	if endByte > len(text) {
		endByte = len(text)
	}

	// Find safe boundaries.
	startByte = findSafeSplitPoint(text, startByte)
	endByte = findSafeSplitPoint(text, endByte)

	return text[startByte:endByte]
}

// SafeOverlap extracts the last n characters from text while respecting UTF-8 boundaries.
// This is useful for creating overlapping chunks.
func SafeOverlap(text string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= utf8.RuneCountInString(text) {
		return text
	}

	// Find the starting position for overlap.
	charCount := utf8.RuneCountInString(text)
	startChar := charCount - n

	// Convert to byte position and find safe boundary.
	startByte := charToBytePos(text, startChar)
	if startByte == -1 {
		return ""
	}

	startByte = findSafeSplitPoint(text, startByte)
	return text[startByte:]
}

// findSafeSplitPoint finds a safe point to split text without breaking UTF-8 characters.
func findSafeSplitPoint(text string, targetPos int) int {
	if targetPos <= 0 {
		return 0
	}
	if targetPos >= len(text) {
		return len(text)
	}

	// Start from the target position and work backwards to find a safe boundary.
	for i := targetPos; i > 0; i-- {
		if isValidUTF8Boundary(text, i) {
			return i
		}
	}

	// If no safe boundary found backwards, try to find the next safe boundary.
	for i := targetPos; i < len(text); i++ {
		if isValidUTF8Boundary(text, i) {
			return i
		}
	}

	// Last resort: return the entire text.
	return len(text)
}

// isValidUTF8Boundary checks if a given position is a valid UTF-8 boundary.
func isValidUTF8Boundary(text string, pos int) bool {
	if pos <= 0 || pos >= len(text) {
		return true
	}

	// Check if the byte at this position is the start of a UTF-8 sequence.
	// UTF-8 start bytes have the pattern: 0xxxxxxx, 110xxxxx, 1110xxxx, or 11110xxx.
	b := text[pos]
	return (b & 0xC0) != 0x80
}

// splitByRunes splits text into individual runes (characters) safely.
func splitByRunes(text string) []string {
	var splits []string
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError {
			// Handle invalid UTF-8 sequences gracefully.
			splits = append(splits, text)
			break
		}
		splits = append(splits, string(r))
		text = text[size:]
	}
	return splits
}

// charToBytePos converts a character position to a byte position.
func charToBytePos(text string, charPos int) int {
	if charPos < 0 {
		return -1
	}

	bytePos := 0
	for i := 0; i < charPos && bytePos < len(text); i++ {
		_, size := utf8.DecodeRuneInString(text[bytePos:])
		if size == 0 {
			return -1
		}
		bytePos += size
	}

	return bytePos
}

// ValidateUTF8 checks if a string is valid UTF-8 and returns a cleaned version.
// If the string contains invalid UTF-8 sequences, it attempts to clean them.
func ValidateUTF8(text string) string {
	if utf8.ValidString(text) {
		return text
	}

	// Clean invalid UTF-8 sequences.
	var cleaned strings.Builder
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError {
			// Skip invalid sequences.
			text = text[1:]
			continue
		}
		cleaned.WriteRune(r)
		text = text[size:]
	}

	return cleaned.String()
}

// RuneCount returns the number of characters (runes) in the text.
// This is more accurate than len() for multi-byte characters.
func RuneCount(text string) int {
	return utf8.RuneCountInString(text)
}

// IsValidUTF8 checks if a string contains only valid UTF-8 sequences.
func IsValidUTF8(text string) bool {
	return utf8.ValidString(text)
}
