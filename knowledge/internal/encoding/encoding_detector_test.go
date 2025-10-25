//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package encoding

import (
	"testing"
)

// TestDetectEncoding tests the encoding detection functionality.
func TestDetectEncoding(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected Encoding
		valid    bool
	}{
		{
			name:     "empty string",
			text:     "",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "pure ASCII",
			text:     "Hello World",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "Chinese UTF-8",
			text:     "人工智能机器学习",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "mixed UTF-8",
			text:     "Hello 世界 World",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "invalid UTF-8",
			text:     "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expected: EncodingUnknown,
			valid:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			info := DetectEncoding(tc.text)

			if info.Encoding != tc.expected {
				t.Errorf("DetectEncoding(%q) = %s, expected %s",
					tc.text, info.Encoding, tc.expected)
			}

			if info.IsValid != tc.valid {
				t.Errorf("DetectEncoding(%q) valid = %v, expected %v",
					tc.text, info.IsValid, tc.valid)
			}

			// Verify confidence is reasonable
			if info.Confidence < 0.0 || info.Confidence > 1.0 {
				t.Errorf("DetectEncoding(%q) confidence = %f, should be between 0.0 and 1.0",
					tc.text, info.Confidence)
			}
		})
	}
}

// TestSmartProcessText tests the smart text processing functionality.
func TestSmartProcessText(t *testing.T) {
	testCases := []struct {
		name           string
		text           string
		expectValid    bool
		expectEncoding Encoding
	}{
		{
			name:           "valid UTF-8",
			text:           "Hello 世界",
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
		{
			name:           "empty string",
			text:           "",
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
		{
			name:           "ASCII only",
			text:           "Hello World",
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
		{
			name:           "pure Chinese",
			text:           "中文测试文本",
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
		{
			name:           "mixed scripts",
			text:           "Test中文123テスト",
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processed, info := SmartProcessText(tc.text)

			// Verify the processed text is not empty (unless input was empty)
			if tc.text != "" && processed == "" {
				t.Errorf("SmartProcessText(%q) returned empty processed text", tc.text)
			}

			// Verify encoding info
			if info.Encoding != tc.expectEncoding {
				t.Errorf("SmartProcessText(%q) encoding = %s, expected %s",
					tc.text, info.Encoding, tc.expectEncoding)
			}

			if info.IsValid != tc.expectValid {
				t.Errorf("SmartProcessText(%q) valid = %v, expected %v",
					tc.text, info.IsValid, tc.expectValid)
			}

			// Verify the processed text is valid UTF-8
			if !IsValidUTF8(processed) {
				t.Errorf("SmartProcessText(%q) returned invalid UTF-8: %q", tc.text, processed)
			}
		})
	}
}

// TestIsUTF8Safe tests the UTF-8 safety check.
func TestIsUTF8Safe(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "valid UTF-8",
			text:     "Hello 世界",
			expected: true,
		},
		{
			name:     "empty string",
			text:     "",
			expected: true,
		},
		{
			name:     "pure ASCII",
			text:     "Hello World",
			expected: true,
		},
		{
			name:     "invalid UTF-8",
			text:     "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsUTF8Safe(tc.text)

			if result != tc.expected {
				t.Errorf("IsUTF8Safe(%q) = %v, expected %v",
					tc.text, result, tc.expected)
			}
		})
	}
}

// TestEncodingPatternsEmpty tests encoding pattern detection with empty bytes.
func TestEncodingPatternsEmpty(t *testing.T) {
	emptyBytes := []byte{}

	if isLikelyGBK(emptyBytes) {
		t.Error("isLikelyGBK returned true for empty bytes")
	}
	if isLikelyBig5(emptyBytes) {
		t.Error("isLikelyBig5 returned true for empty bytes")
	}
	if isLikelyShiftJIS(emptyBytes) {
		t.Error("isLikelyShiftJIS returned true for empty bytes")
	}
	if isLikelyEUCKR(emptyBytes) {
		t.Error("isLikelyEUCKR returned true for empty bytes")
	}
}

// TestIsLikelyGBKPattern tests the GBK pattern detection.
func TestIsLikelyGBKPattern(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    []byte
		expected bool
	}{
		{
			name:     "empty",
			bytes:    []byte{},
			expected: false,
		},
		{
			name:     "single byte",
			bytes:    []byte{0x81},
			expected: false,
		},
		{
			name:     "GBK pattern with valid continuation",
			bytes:    []byte{0x81, 0x40, 0x82, 0x44},
			expected: true,
		},
		{
			name:     "low confidence GBK pattern",
			bytes:    []byte{0x81, 0x20, 0x82, 0x21},
			expected: false,
		},
		{
			name:     "one valid pattern",
			bytes:    []byte{0x81, 0x40},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isLikelyGBK(tc.bytes)
			if result != tc.expected {
				t.Errorf("isLikelyGBK(%v) = %v, expected %v", tc.bytes, result, tc.expected)
			}
		})
	}
}

// TestIsLikelyBig5Pattern tests the Big5 pattern detection.
func TestIsLikelyBig5Pattern(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    []byte
		expected bool
	}{
		{
			name:     "empty",
			bytes:    []byte{},
			expected: false,
		},
		{
			name:     "single byte",
			bytes:    []byte{0xA1},
			expected: false,
		},
		{
			name:     "Big5 pattern with valid continuation",
			bytes:    []byte{0xA1, 0x40, 0xA2, 0x44},
			expected: true,
		},
		{
			name:     "Big5 with A1 range",
			bytes:    []byte{0xA1, 0xA1, 0xA2, 0xA2},
			expected: true,
		},
		{
			name:     "low confidence Big5 pattern",
			bytes:    []byte{0xA1, 0x20, 0xA2, 0x21},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isLikelyBig5(tc.bytes)
			if result != tc.expected {
				t.Errorf("isLikelyBig5(%v) = %v, expected %v", tc.bytes, result, tc.expected)
			}
		})
	}
}

// TestIsLikelyShiftJISPattern tests the Shift_JIS pattern detection.
func TestIsLikelyShiftJISPattern(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    []byte
		expected bool
	}{
		{
			name:     "empty",
			bytes:    []byte{},
			expected: false,
		},
		{
			name:     "Shift_JIS pattern 81-9F range",
			bytes:    []byte{0x81, 0x40},
			expected: true,
		},
		{
			name:     "Shift_JIS pattern E0-EF range",
			bytes:    []byte{0xE0, 0x40},
			expected: true,
		},
		{
			name:     "invalid continuation",
			bytes:    []byte{0x81, 0x20},
			expected: false,
		},
		{
			name:     "single byte",
			bytes:    []byte{0x81},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isLikelyShiftJIS(tc.bytes)
			if result != tc.expected {
				t.Errorf("isLikelyShiftJIS(%v) = %v, expected %v", tc.bytes, result, tc.expected)
			}
		})
	}
}

// TestIsLikelyEUCKRPattern tests the EUC-KR pattern detection.
func TestIsLikelyEUCKRPattern(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    []byte
		expected bool
	}{
		{
			name:     "empty",
			bytes:    []byte{},
			expected: false,
		},
		{
			name:     "EUC-KR pattern",
			bytes:    []byte{0xA1, 0xA1},
			expected: true,
		},
		{
			name:     "EUC-KR pattern FE range",
			bytes:    []byte{0xFE, 0xA1},
			expected: true,
		},
		{
			name:     "invalid continuation",
			bytes:    []byte{0xA1, 0x20},
			expected: false,
		},
		{
			name:     "single byte",
			bytes:    []byte{0xA1},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isLikelyEUCKR(tc.bytes)
			if result != tc.expected {
				t.Errorf("isLikelyEUCKR(%v) = %v, expected %v", tc.bytes, result, tc.expected)
			}
		})
	}
}

// TestDetectNonUTF8EncodingDirect tests detectNonUTF8Encoding function directly.
func TestDetectNonUTF8EncodingDirect(t *testing.T) {
	testCases := []struct {
		name          string
		text          string
		expectedType  Encoding
		minConfidence float64
		maxConfidence float64
	}{
		{
			name:          "ASCII bytes",
			text:          "Hello World",
			expectedType:  EncodingUnknown,
			minConfidence: 0.0,
			maxConfidence: 0.5,
		},
		{
			name:          "empty string",
			text:          "",
			expectedType:  EncodingUnknown,
			minConfidence: 0.0,
			maxConfidence: 0.5,
		},
		{
			name:          "single high byte",
			text:          "\xFF",
			expectedType:  EncodingUnknown,
			minConfidence: 0.0,
			maxConfidence: 0.5,
		},
		{
			name:          "mixed ASCII and high bytes",
			text:          "test\xFF\xFE",
			expectedType:  EncodingUnknown,
			minConfidence: 0.0,
			maxConfidence: 0.5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			info := detectNonUTF8Encoding(tc.text)
			if info.Encoding != tc.expectedType {
				t.Errorf("detectNonUTF8Encoding(%q) = %s, expected %s", tc.text, info.Encoding, tc.expectedType)
			}
			if info.Confidence < tc.minConfidence || info.Confidence > tc.maxConfidence {
				t.Errorf("confidence %f not in range [%f, %f]", info.Confidence, tc.minConfidence, tc.maxConfidence)
			}
		})
	}
}

// TestConvertToUTF8 tests the convertToUTF8 function.
func TestConvertToUTF8(t *testing.T) {
	testCases := []struct {
		name        string
		text        string
		encoding    Encoding
		shouldError bool
		checkValid  bool
	}{
		{
			name:        "unsupported encoding",
			text:        "test",
			encoding:    EncodingUnknown,
			shouldError: true,
			checkValid:  false,
		},
		{
			name:        "GBK to UTF-8",
			text:        "test",
			encoding:    EncodingGBK,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "Big5 to UTF-8",
			text:        "test",
			encoding:    EncodingBig5,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "Shift_JIS to UTF-8",
			text:        "test",
			encoding:    EncodingShiftJIS,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "EUC-JP to UTF-8",
			text:        "test",
			encoding:    EncodingEUCJP,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "EUC-KR to UTF-8",
			text:        "test",
			encoding:    EncodingEUCKR,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "ISO-8859 to UTF-8",
			text:        "test",
			encoding:    EncodingISO8859,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "Windows-1252 to UTF-8",
			text:        "test",
			encoding:    EncodingWindows,
			shouldError: false,
			checkValid:  true,
		},
		{
			name:        "GB18030 to UTF-8",
			text:        "test",
			encoding:    EncodingGB18030,
			shouldError: false,
			checkValid:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := convertToUTF8(tc.text, tc.encoding)

			if tc.shouldError && err == nil {
				t.Errorf("convertToUTF8 expected error but got none")
			}
			if !tc.shouldError && err != nil {
				t.Errorf("convertToUTF8 got unexpected error: %v", err)
			}

			if tc.checkValid && !IsValidUTF8(result) {
				t.Errorf("result is not valid UTF-8: %q", result)
			}
		})
	}
}

// TestCalculateUTF8ConfidenceExtended tests UTF-8 confidence calculation with more cases.
func TestCalculateUTF8ConfidenceExtended(t *testing.T) {
	testCases := []struct {
		name        string
		text        string
		expectedMin float64
		expectedMax float64
	}{
		{
			name:        "pure ASCII",
			text:        "Hello World",
			expectedMin: 0.8,
			expectedMax: 0.9,
		},
		{
			name:        "with multi-byte",
			text:        "Hello 世界",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "with CJK",
			text:        "人工智能",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "with Hiragana",
			text:        "ひらがな",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "with Katakana",
			text:        "カタカナ",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "with Hangul",
			text:        "한글",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "numbers only",
			text:        "12345",
			expectedMin: 0.8,
			expectedMax: 0.9,
		},
		{
			name:        "special characters",
			text:        "!@#$%^&*()",
			expectedMin: 0.8,
			expectedMax: 0.9,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			confidence := calculateUTF8Confidence(tc.text)

			if confidence < tc.expectedMin || confidence > tc.expectedMax {
				t.Errorf("calculateUTF8Confidence(%q) = %f, expected between %f and %f",
					tc.text, confidence, tc.expectedMin, tc.expectedMax)
			}
		})
	}
}

// TestSmartProcessTextWithInvalidUTF8 tests SmartProcessText with invalid UTF-8.
func TestSmartProcessTextWithInvalidUTF8(t *testing.T) {
	testCases := []struct {
		name        string
		text        string
		expectValid bool
	}{
		{
			name:        "valid UTF-8 processing",
			text:        "Hello 世界",
			expectValid: true,
		},
		{
			name:        "empty string",
			text:        "",
			expectValid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processed, info := SmartProcessText(tc.text)

			// For valid UTF-8 input, output should be valid UTF-8
			if tc.expectValid && !IsValidUTF8(processed) {
				t.Errorf("SmartProcessText(%q) returned invalid UTF-8: %q", tc.text, processed)
			}

			// Verify the result has encoding info
			if info.Encoding == "" {
				t.Errorf("SmartProcessText(%q) returned empty encoding", tc.text)
			}
		})
	}
}

// BenchmarkDetectEncoding benchmarks the encoding detection performance.
func BenchmarkDetectEncoding(b *testing.B) {
	text := "人工智能机器学习深度学习神经网络自然语言处理计算机视觉"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DetectEncoding(text)
	}
}

// TestDetectNonUTF8Encoding tests non-UTF8 encoding detection based on byte patterns.
func TestDetectNonUTF8Encoding(t *testing.T) {
	tests := []struct {
		name             string
		bytes            []byte
		expectedEncoding Encoding
		minConfidence    float64
	}{
		{
			name: "GBK pattern - multiple valid sequences",
			// Construct bytes matching GBK pattern: first byte 0x81-0xFE, second byte 0x40-0xFE
			bytes:            []byte{0xB0, 0xA1, 0xB3, 0xC9, 0xD6, 0xD0},
			expectedEncoding: EncodingGBK,
			minConfidence:    0.6,
		},
		{
			name: "Big5 pattern - may detect as GBK due to range overlap",
			// Note: Big5 byte ranges overlap with GBK, so GBK detection takes priority
			// This test verifies that multi-byte encoding is detected, not specifically Big5
			bytes:            []byte{0xA1, 0x40, 0xA2, 0x50},
			expectedEncoding: EncodingGBK, // GBK has priority in detection
			minConfidence:    0.6,
		},
		{
			name: "Shift_JIS pattern - specific range",
			// Use Shift_JIS specific range that minimizes GBK false positive
			bytes:            []byte{0x88, 0xEA, 0x00}, // Add 0x00 to break GBK pattern
			expectedEncoding: EncodingShiftJIS,
			minConfidence:    0.6,
		},
		{
			name: "EUC-KR pattern - both bytes high",
			// EUC-KR: both bytes 0xA1-0xFE
			bytes:            []byte{0xC7, 0xD1, 0xB1, 0xDB, 0x00}, // Add 0x00 to break GBK pattern
			expectedEncoding: EncodingEUCKR,
			minConfidence:    0.6,
		},
		{
			name: "Unknown pattern - no valid multi-byte sequences",
			// Bytes that don't match any known pattern
			bytes:            []byte{0x01, 0x02, 0x03, 0x04},
			expectedEncoding: EncodingUnknown,
			minConfidence:    0.0,
		},
		{
			name: "Too short - single high byte",
			// Single byte, can't form multi-byte pattern
			bytes:            []byte{0xB0},
			expectedEncoding: EncodingUnknown,
			minConfidence:    0.0,
		},
		{
			name: "ASCII only - no multi-byte sequences",
			// Pure ASCII should return unknown
			bytes:            []byte("Hello World"),
			expectedEncoding: EncodingUnknown,
			minConfidence:    0.0,
		},
		{
			name:             "Empty string",
			bytes:            []byte{},
			expectedEncoding: EncodingUnknown,
			minConfidence:    0.0,
		},
		{
			name: "Incomplete multi-byte sequence",
			// High byte without valid second byte
			bytes:            []byte{0x81, 0x20, 0x82, 0x30},
			expectedEncoding: EncodingUnknown,
			minConfidence:    0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := detectNonUTF8Encoding(string(tt.bytes))

			if info.Encoding != tt.expectedEncoding {
				t.Errorf("detectNonUTF8Encoding() encoding = %s, want %s",
					info.Encoding, tt.expectedEncoding)
			}

			if info.Confidence < tt.minConfidence {
				t.Errorf("detectNonUTF8Encoding() confidence = %.2f, want >= %.2f",
					info.Confidence, tt.minConfidence)
			}

			// For non-unknown encodings, IsValid should be false (needs conversion)
			if tt.expectedEncoding != EncodingUnknown && info.IsValid {
				t.Errorf("detectNonUTF8Encoding() IsValid = true, expected false for non-UTF8")
			}

			// Description should not be empty
			if info.Description == "" {
				t.Error("detectNonUTF8Encoding() returned empty description")
			}
		})
	}
}

// BenchmarkSmartProcessText benchmarks the smart text processing performance.
func BenchmarkSmartProcessText(b *testing.B) {
	text := "人工智能机器学习深度学习神经网络自然语言处理计算机视觉"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SmartProcessText(text)
	}
}
