//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

// Package encoding provides encoding detection and conversion utilities for knowledge management.
package encoding

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// Encoding represents the detected text encoding.
type Encoding string

// Encoding constants.
const (
	EncodingUTF8     Encoding = "UTF-8"
	EncodingGBK      Encoding = "GBK"
	EncodingGB18030  Encoding = "GB18030"
	EncodingBig5     Encoding = "Big5"
	EncodingShiftJIS Encoding = "Shift_JIS"
	EncodingEUCJP    Encoding = "EUC-JP"
	EncodingEUCKR    Encoding = "EUC-KR"
	EncodingISO8859  Encoding = "ISO-8859"
	EncodingWindows  Encoding = "Windows-1252"
	EncodingUnknown  Encoding = "Unknown"
)

// Info contains information about detected text encoding.
type Info struct {
	Encoding    Encoding
	Confidence  float64
	IsValid     bool
	Description string
}

// DetectEncoding automatically detects the encoding of the given text.
// Returns encoding information with confidence level.
func DetectEncoding(text string) Info {
	if text == "" {
		return Info{
			Encoding:    EncodingUTF8,
			Confidence:  1.0,
			IsValid:     true,
			Description: "Empty text, defaulting to UTF-8",
		}
	}

	// First, check if it's valid UTF-8.
	if utf8.ValidString(text) {
		// Additional UTF-8 validation: check for common UTF-8 patterns.
		confidence := calculateUTF8Confidence(text)
		return Info{
			Encoding:    EncodingUTF8,
			Confidence:  confidence,
			IsValid:     true,
			Description: "Valid UTF-8 text",
		}
	}

	// Try to detect other encodings.
	detected := detectNonUTF8Encoding(text)
	return detected
}

// SmartProcessText intelligently processes text based on detected encoding.
// If UTF-8 is detected, it applies UTF-8 safe processing.
// If other encoding is detected, it converts to UTF-8.
// Returns the processed text and encoding information.
func SmartProcessText(text string) (string, Info) {
	info := DetectEncoding(text)

	switch info.Encoding {
	case EncodingUTF8:
		if info.IsValid {
			// Already UTF-8, just clean and validate.
			cleaned := ValidateUTF8(text)
			return cleaned, info
		}
		// Invalid UTF-8, try to convert from detected encoding.
		fallthrough

	default:
		// Try to convert from detected encoding to UTF-8.
		converted, err := convertToUTF8(text, info.Encoding)
		if err != nil {
			// Conversion failed, return original with error info.
			info.IsValid = false
			info.Description = "Failed to convert to UTF-8: " + err.Error()
			return text, info
		}

		// Successfully converted.
		info.Encoding = EncodingUTF8
		info.IsValid = true
		info.Confidence = 0.9 // Slightly lower confidence due to conversion
		info.Description = "Converted from " + string(info.Encoding) + " to UTF-8"
		return converted, info
	}
}

// IsUTF8Safe checks if the text can be safely processed as UTF-8.
// Returns true if the text is valid UTF-8 with high confidence.
func IsUTF8Safe(text string) bool {
	info := DetectEncoding(text)
	return info.Encoding == EncodingUTF8 && info.IsValid && info.Confidence > 0.8
}

// calculateUTF8Confidence calculates confidence level for UTF-8 detection.
func calculateUTF8Confidence(text string) float64 {
	if !utf8.ValidString(text) {
		return 0.0
	}

	confidence := 0.85 // Base confidence for valid UTF-8.

	// Check for common UTF-8 patterns.
	runes := []rune(text)

	// Bonus for having multi-byte characters (typical in UTF-8).
	hasMultiByte := false
	for _, r := range runes {
		if r > 127 {
			hasMultiByte = true
			break
		}
	}
	if hasMultiByte {
		confidence += 0.1
	}

	// Bonus for having CJK characters (strong UTF-8 indicator).
	hasCJK := false
	for _, r := range runes {
		if (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
			(r >= 0x3040 && r <= 0x309F) || // Hiragana
			(r >= 0x30A0 && r <= 0x30FF) || // Katakana
			(r >= 0xAC00 && r <= 0xD7AF) { // Hangul Syllables
			hasCJK = true
			break
		}
	}
	if hasCJK {
		confidence += 0.05
	}

	// Ensure confidence doesn't exceed 1.0.
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// detectNonUTF8Encoding attempts to detect non-UTF-8 encodings.
func detectNonUTF8Encoding(text string) Info {
	// Try common encodings based on byte patterns.
	bytes := []byte(text)

	// Check for GBK/GB18030 patterns (common in Chinese text).
	if isLikelyGBK(bytes) {
		return Info{
			Encoding:    EncodingGBK,
			Confidence:  0.7,
			IsValid:     false,
			Description: "Likely GBK/GB18030 encoding based on byte patterns",
		}
	}

	// Check for Big5 patterns (Traditional Chinese).
	if isLikelyBig5(bytes) {
		return Info{
			Encoding:    EncodingBig5,
			Confidence:  0.7,
			IsValid:     false,
			Description: "Likely Big5 encoding based on byte patterns",
		}
	}

	// Check for Shift_JIS patterns (Japanese).
	if isLikelyShiftJIS(bytes) {
		return Info{
			Encoding:    EncodingShiftJIS,
			Confidence:  0.7,
			IsValid:     false,
			Description: "Likely Shift_JIS encoding based on byte patterns",
		}
	}

	// Check for EUC-KR patterns (Korean).
	if isLikelyEUCKR(bytes) {
		return Info{
			Encoding:    EncodingEUCKR,
			Confidence:  0.7,
			IsValid:     false,
			Description: "Likely EUC-KR encoding based on byte patterns",
		}
	}

	// Default to unknown encoding.
	return Info{
		Encoding:    EncodingUnknown,
		Confidence:  0.3,
		IsValid:     false,
		Description: "Unknown encoding, unable to detect",
	}
}

// isLikelyGBK checks if byte pattern suggests GBK encoding.
func isLikelyGBK(bytes []byte) bool {
	// Need at least 2 bytes to form a valid GBK character.
	if len(bytes) < 2 {
		return false
	}

	// Count valid GBK patterns.
	validPatterns := 0
	totalPatterns := 0

	for i := 0; i < len(bytes)-1; i++ {
		if bytes[i] >= 0x81 && bytes[i] <= 0xFE {
			totalPatterns++
			next := bytes[i+1]
			if (next >= 0x40 && next <= 0x7E) || (next >= 0x80 && next <= 0xFE) {
				validPatterns++
			}
		}
	}

	// Require at least 2 valid patterns and high success rate.
	return totalPatterns >= 2 && validPatterns >= 2 &&
		float64(validPatterns)/float64(totalPatterns) > 0.8
}

// isLikelyBig5 checks if byte pattern suggests Big5 encoding.
func isLikelyBig5(bytes []byte) bool {
	// Need at least 2 bytes to form a valid Big5 character.
	if len(bytes) < 2 {
		return false
	}

	// Count valid Big5 patterns.
	validPatterns := 0
	totalPatterns := 0

	for i := 0; i < len(bytes)-1; i++ {
		if bytes[i] >= 0xA1 && bytes[i] <= 0xFE {
			totalPatterns++
			next := bytes[i+1]
			if (next >= 0x40 && next <= 0x7E) || (next >= 0xA1 && next <= 0xFE) {
				validPatterns++
			}
		}
	}

	// Require at least 2 valid patterns and high success rate.
	return totalPatterns >= 2 && validPatterns >= 2 &&
		float64(validPatterns)/float64(totalPatterns) > 0.8
}

// isLikelyShiftJIS checks if byte pattern suggests Shift_JIS encoding.
func isLikelyShiftJIS(bytes []byte) bool {
	for i := 0; i < len(bytes); i++ {
		if (bytes[i] >= 0x81 && bytes[i] <= 0x9F) || (bytes[i] >= 0xE0 && bytes[i] <= 0xEF) {
			if i+1 < len(bytes) {
				next := bytes[i+1]
				if (next >= 0x40 && next <= 0x7E) || (next >= 0x80 && next <= 0xFC) {
					return true
				}
			}
		}
	}
	return false
}

// isLikelyEUCKR checks if byte pattern suggests EUC-KR encoding.
func isLikelyEUCKR(bytes []byte) bool {
	for i := 0; i < len(bytes); i++ {
		if bytes[i] >= 0xA1 && bytes[i] <= 0xFE {
			if i+1 < len(bytes) {
				next := bytes[i+1]
				if next >= 0xA1 && next <= 0xFE {
					return true
				}
			}
		}
	}
	return false
}

// convertToUTF8 converts text from the specified encoding to UTF-8.
func convertToUTF8(text string, fromEncoding Encoding) (string, error) {
	var enc encoding.Encoding

	switch fromEncoding {
	case EncodingGBK, EncodingGB18030:
		enc = simplifiedchinese.GBK
	case EncodingBig5:
		enc = traditionalchinese.Big5
	case EncodingShiftJIS:
		enc = japanese.ShiftJIS
	case EncodingEUCJP:
		enc = japanese.EUCJP
	case EncodingEUCKR:
		enc = korean.EUCKR
	case EncodingISO8859:
		enc = charmap.ISO8859_1
	case EncodingWindows:
		enc = charmap.Windows1252
	default:
		return text, fmt.Errorf("unsupported encoding: %s", fromEncoding)
	}

	// Convert to UTF-8.
	reader := transform.NewReader(bytes.NewReader([]byte(text)), enc.NewDecoder())
	converted, err := io.ReadAll(reader)
	if err != nil {
		return text, fmt.Errorf("failed to convert from %s: %w", fromEncoding, err)
	}

	return string(converted), nil
}
