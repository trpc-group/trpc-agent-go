//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestAllToolCreators(t *testing.T) {
	// Verify that AllToolCreators contains all expected tools.
	expectedTools := []string{
		memory.AddToolName,
		memory.UpdateToolName,
		memory.SearchToolName,
		memory.LoadToolName,
		memory.DeleteToolName,
		memory.ClearToolName,
	}

	for _, toolName := range expectedTools {
		creator, exists := AllToolCreators[toolName]
		assert.True(t, exists, "Tool %s should exist in AllToolCreators", toolName)
		assert.NotNil(t, creator, "Tool creator for %s should not be nil", toolName)

		// Verify that creators can actually create tools.
		tool := creator()
		assert.NotNil(t, tool, "Tool creator for %s should return a non-nil tool", toolName)

		// Verify that the tool has a valid declaration.
		decl := tool.Declaration()
		assert.NotNil(t, decl, "Tool declaration for %s should not be nil", toolName)
		assert.Equal(t, toolName, decl.Name, "Tool declaration name should match %s", toolName)
		assert.NotEmpty(t, decl.Description, "Tool description for %s should not be empty", toolName)
	}

	// Verify no extra tools are in the map.
	assert.Len(t, AllToolCreators, len(expectedTools), "AllToolCreators should contain exactly the expected tools")
}

func TestDefaultEnabledTools(t *testing.T) {
	// Verify that DefaultEnabledTools contains expected tools.
	expectedTools := []string{
		memory.AddToolName,
		memory.UpdateToolName,
		memory.SearchToolName,
		memory.LoadToolName,
	}

	for _, toolName := range expectedTools {
		creator, exists := DefaultEnabledTools[toolName]
		assert.True(t, exists, "Tool %s should exist in DefaultEnabledTools", toolName)
		assert.NotNil(t, creator, "Tool creator for %s should not be nil", toolName)
	}

	// Verify that delete and clear tools are not included.
	assert.NotContains(t, DefaultEnabledTools, memory.DeleteToolName)
	assert.NotContains(t, DefaultEnabledTools, memory.ClearToolName)

	// Verify no extra tools are in the map.
	assert.Len(t, DefaultEnabledTools, len(expectedTools), "DefaultEnabledTools should contain exactly the expected tools")
}

func TestIsValidToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"valid add tool", memory.AddToolName, true},
		{"valid update tool", memory.UpdateToolName, true},
		{"valid delete tool", memory.DeleteToolName, true},
		{"valid clear tool", memory.ClearToolName, true},
		{"valid search tool", memory.SearchToolName, true},
		{"valid load tool", memory.LoadToolName, true},
		{"invalid tool", "invalid_tool", false},
		{"empty tool name", "", false},
		{"case sensitive", "ADD_MEMORY", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidToolName(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSearchTokens(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected []string
	}{
		{"empty query", "", nil},
		{"whitespace only", "   ", nil},
		{"single character", "a", []string{}},
		{"short word", "hi", []string{"hi"}},
		{"english words", "hello world", []string{"hello", "world"}},
		{"english with stopwords", "the quick brown fox", []string{"quick", "brown", "fox"}},
		{"english with punctuation", "hello, world!", []string{"hello", "world"}},
		{"english with numbers", "test123 abc456", []string{"test123", "abc456"}},
		{"mixed case", "Hello World", []string{"hello", "world"}},
		{"chinese single character", "中", []string{"中"}},
		{"chinese bigrams", "中文测试", []string{"中文", "文测", "测试"}},
		{"chinese with punctuation", "中文，测试！", []string{"中文", "文测", "测试"}},
		{"chinese with spaces", "中文 测试", []string{"中文", "文测", "测试"}},
		{"mixed chinese and english", "hello中文world", []string{"he", "el", "ll", "lo", "o中", "中文", "文w", "wo", "or", "rl", "ld"}},
		{"only punctuation", "!@#$%", []string{}},
		{"only stopwords", "the and or", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSearchTokens(tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildSearchTokens_EdgeCases(t *testing.T) {
	t.Run("very long query", func(t *testing.T) {
		longQuery := strings.Repeat("hello world ", 1000)
		result := BuildSearchTokens(longQuery)
		require.NotNil(t, result)
		assert.Contains(t, result, "hello")
		assert.Contains(t, result, "world")
	})

	t.Run("unicode edge cases", func(t *testing.T) {
		// Test various Unicode characters.
		result := BuildSearchTokens("🚀hello🌟world")
		assert.Contains(t, result, "hello")
		assert.Contains(t, result, "world")
	})

	t.Run("only CJK punctuation", func(t *testing.T) {
		result := BuildSearchTokens("，。！？")
		assert.Empty(t, result)
	})

	t.Run("mixed CJK and punctuation", func(t *testing.T) {
		result := BuildSearchTokens("中文，测试！")
		expected := []string{"中文", "文测", "测试"}
		assert.Equal(t, expected, result)
	})
}

func TestBuildSearchTokens_Performance(t *testing.T) {
	// Test performance to ensure it's not too slow.
	query := "hello world this is a test query with multiple words"

	// Run multiple times to ensure performance stability.
	for i := 0; i < 1000; i++ {
		result := BuildSearchTokens(query)
		require.NotNil(t, result)
		assert.Contains(t, result, "hello")
		assert.Contains(t, result, "world")
	}
}

func TestMatchMemoryEntry(t *testing.T) {
	now := time.Now()
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Hello world, this is a test memory",
			Topics: []string{"test", "example"},
		},
		CreatedAt: now,
	}

	tests := []struct {
		name     string
		entry    *memory.Entry
		query    string
		expected bool
	}{
		{
			name:     "exact content match",
			entry:    entry,
			query:    "hello world",
			expected: true,
		},
		{
			name:     "partial content match",
			entry:    entry,
			query:    "test memory",
			expected: true,
		},
		{
			name:     "topic match",
			entry:    entry,
			query:    "example",
			expected: true,
		},
		{
			name:     "case insensitive match",
			entry:    entry,
			query:    "HELLO WORLD",
			expected: true,
		},
		{
			name:     "chinese content match",
			entry:    &memory.Entry{Memory: &memory.Memory{Memory: "这是一个中文测试", Topics: []string{"测试"}}},
			query:    "中文测试",
			expected: true,
		},
		{
			name:     "chinese topic match",
			entry:    &memory.Entry{Memory: &memory.Memory{Memory: "test content", Topics: []string{"中文测试"}}},
			query:    "中文",
			expected: true,
		},
		{
			name:     "no match",
			entry:    entry,
			query:    "nonexistent",
			expected: false,
		},
		{
			name:     "empty query",
			entry:    entry,
			query:    "",
			expected: false,
		},
		{
			name:     "whitespace query",
			entry:    entry,
			query:    "   ",
			expected: false,
		},
		{
			name:     "nil entry",
			entry:    nil,
			query:    "test",
			expected: false,
		},
		{
			name:     "nil memory",
			entry:    &memory.Entry{Memory: nil},
			query:    "test",
			expected: false,
		},
		{
			name:     "stopword only query",
			entry:    entry,
			query:    "the and or",
			expected: false,
		},
		{
			name:     "punctuation only query",
			entry:    entry,
			query:    "!@#$%",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchMemoryEntry(tt.entry, tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchMemoryEntry_EdgeCases(t *testing.T) {
	t.Run("very long content", func(t *testing.T) {
		longContent := strings.Repeat("hello world ", 1000)
		entry := &memory.Entry{
			Memory: &memory.Memory{
				Memory: longContent,
				Topics: []string{"test"},
			},
		}
		result := MatchMemoryEntry(entry, "hello")
		assert.True(t, result)
	})

	t.Run("very long query", func(t *testing.T) {
		entry := &memory.Entry{
			Memory: &memory.Memory{
				Memory: "test content",
				Topics: []string{"example"},
			},
		}
		longQuery := strings.Repeat("hello world ", 1000)
		result := MatchMemoryEntry(entry, longQuery)
		assert.False(t, result)
	})

	t.Run("unicode characters", func(t *testing.T) {
		entry := &memory.Entry{
			Memory: &memory.Memory{
				Memory: "🚀hello🌟world",
				Topics: []string{"emoji"},
			},
		}
		result := MatchMemoryEntry(entry, "hello")
		assert.True(t, result)
	})

	t.Run("mixed languages", func(t *testing.T) {
		entry := &memory.Entry{
			Memory: &memory.Memory{
				Memory: "hello 世界 world",
				Topics: []string{"多语言", "multilingual"},
			},
		}
		assert.True(t, MatchMemoryEntry(entry, "hello"))
		assert.True(t, MatchMemoryEntry(entry, "世界"))
		assert.True(t, MatchMemoryEntry(entry, "world"))
		assert.True(t, MatchMemoryEntry(entry, "多语言"))
		assert.True(t, MatchMemoryEntry(entry, "multilingual"))
	})
}

func TestDedupStrings_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "no duplicates",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with empty strings",
			input:    []string{"a", "", "b", "", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "all empty strings",
			input:    []string{"", "", ""},
			expected: []string{},
		},
		{
			name:     "all same strings",
			input:    []string{"a", "a", "a"},
			expected: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dedupStrings(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsCJK_Coverage(t *testing.T) {
	tests := []struct {
		name     string
		r        rune
		expected bool
	}{
		{"chinese character", '中', true},
		{"english letter", 'a', false},
		{"number", '1', false},
		{"space", ' ', false},
		{"punctuation", ',', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCJK(tt.r)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsPunct_Coverage(t *testing.T) {
	tests := []struct {
		name     string
		r        rune
		expected bool
	}{
		{"punctuation comma", ',', true},
		{"punctuation period", '.', true},
		{"symbol", '$', true},
		{"letter", 'a', false},
		{"number", '1', false},
		{"space", ' ', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPunct(tt.r)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsStopword_Coverage(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		expected bool
	}{
		{"stopword a", "a", true},
		{"stopword the", "the", true},
		{"stopword and", "and", true},
		{"stopword or", "or", true},
		{"stopword of", "of", true},
		{"stopword in", "in", true},
		{"stopword on", "on", true},
		{"stopword to", "to", true},
		{"stopword for", "for", true},
		{"stopword with", "with", true},
		{"stopword is", "is", true},
		{"stopword are", "are", true},
		{"stopword am", "am", true},
		{"stopword be", "be", true},
		{"stopword an", "an", true},
		{"not stopword", "hello", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isStopword(tt.s)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchMemoryEntry_TokensWithTopics(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "This is a simple test",
			Topics: []string{"simple", "example"},
		},
	}

	// Test matching topic via token.
	result := MatchMemoryEntry(entry, "example")
	assert.True(t, result)

	// Test matching content via token.
	result = MatchMemoryEntry(entry, "simple")
	assert.True(t, result)

	// Test non-matching token.
	result = MatchMemoryEntry(entry, "complex")
	assert.False(t, result)
}

func TestMatchMemoryEntry_FallbackNoTokens(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Test content",
			Topics: []string{"topic"},
		},
	}

	// Query that produces no tokens (only punctuation).
	result := MatchMemoryEntry(entry, "!@#$%")
	assert.False(t, result)
}

func TestBuildSearchTokens_Duplicates(t *testing.T) {
	// Test deduplication in bigrams.
	result := BuildSearchTokens("中中中中")
	assert.NotNil(t, result)
	// Should have deduplicated "中中" bigram.
	assert.Len(t, result, 1)
	assert.Equal(t, "中中", result[0])
}

func TestMatchMemoryEntry_EmptyTokensWithTopics(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Test content",
			Topics: []string{"topic1", "topic2"},
		},
	}

	// Query that produces empty tokens after filtering
	result := MatchMemoryEntry(entry, "   ")
	assert.False(t, result)
}

func TestMatchMemoryEntry_TokenMatchInTopics(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Some content",
			Topics: []string{"important", "keyword"},
		},
	}

	// Query that matches a topic
	result := MatchMemoryEntry(entry, "keyword")
	assert.True(t, result)
}

func TestMatchMemoryEntry_NoTokensButTopicMatch(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Some content",
			Topics: []string{"special!topic"},
		},
	}

	// Query with only punctuation that produces no tokens, but matches topic in fallback
	result := MatchMemoryEntry(entry, "special!")
	assert.True(t, result)
}

func TestMatchMemoryEntry_EmptyTokenInList(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Test content with keyword",
			Topics: []string{},
		},
	}

	// This should match the content
	result := MatchMemoryEntry(entry, "keyword")
	assert.True(t, result)
}

func TestBuildSearchTokens_MixedContent(t *testing.T) {
	// Test with mixed CJK and English
	result := BuildSearchTokens("hello世界test")
	assert.NotNil(t, result)
	// Should have both English words and CJK bigrams
	assert.NotEmpty(t, result)
}

func TestBuildSearchTokens_OnlyPunctuation(t *testing.T) {
	// Test with only punctuation
	result := BuildSearchTokens("!@#$%^&*()")
	// Should return empty or minimal tokens
	assert.NotNil(t, result)
}

func TestBuildSearchTokens_StopwordsOnly(t *testing.T) {
	// Test with only stopwords
	result := BuildSearchTokens("the a an")
	// Should filter out stopwords
	assert.NotNil(t, result)
	// Stopwords should be filtered
	for _, token := range result {
		assert.NotEqual(t, "the", token)
		assert.NotEqual(t, "a", token)
		assert.NotEqual(t, "an", token)
	}
}

func TestMatchMemoryEntry_NilMemory(t *testing.T) {
	entry := &memory.Entry{
		Memory: nil,
	}

	result := MatchMemoryEntry(entry, "test")
	assert.False(t, result)
}

func TestMatchMemoryEntry_WhitespaceQuery(t *testing.T) {
	entry := &memory.Entry{
		Memory: &memory.Memory{
			Memory: "Test content",
			Topics: []string{"topic"},
		},
	}

	result := MatchMemoryEntry(entry, "   \t\n  ")
	assert.False(t, result)
}
