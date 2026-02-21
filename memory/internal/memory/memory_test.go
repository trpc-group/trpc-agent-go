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
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

func TestGenerateMemoryID(t *testing.T) {
	const testAppName = "test-app"
	const testUserID = "user-1"

	tests := []struct {
		name     string
		memory   *memory.Memory
		expected string
	}{
		{
			name: "simple memory without topics",
			memory: &memory.Memory{
				Memory: "User likes coffee",
				Topics: nil,
			},
			expected: "", // Will verify it's not empty and consistent.
		},
		{
			name: "memory with empty topics",
			memory: &memory.Memory{
				Memory: "User works in tech",
				Topics: []string{},
			},
			expected: "", // Will verify it's not empty and consistent.
		},
		{
			name: "memory with topics",
			memory: &memory.Memory{
				Memory: "User prefers dark mode",
				Topics: []string{"preferences", "ui"},
			},
			expected: "", // Will verify it's not empty and consistent.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := GenerateMemoryID(tt.memory, testAppName, testUserID)
			assert.NotEmpty(t, id)
			// Verify consistency: same input should produce same output.
			id2 := GenerateMemoryID(tt.memory, testAppName, testUserID)
			assert.Equal(t, id, id2)
			// Verify length: SHA256 produces 64 hex characters.
			assert.Len(t, id, 64)
		})
	}

	t.Run("different memories produce different IDs", func(t *testing.T) {
		mem1 := &memory.Memory{Memory: "User likes coffee"}
		mem2 := &memory.Memory{Memory: "User likes tea"}
		id1 := GenerateMemoryID(mem1, testAppName, testUserID)
		id2 := GenerateMemoryID(mem2, testAppName, testUserID)
		assert.NotEqual(t, id1, id2)
	})

	t.Run("same content different topics produce different IDs", func(t *testing.T) {
		mem1 := &memory.Memory{Memory: "User likes coffee", Topics: []string{"food"}}
		mem2 := &memory.Memory{Memory: "User likes coffee", Topics: []string{"drink"}}
		id1 := GenerateMemoryID(mem1, testAppName, testUserID)
		id2 := GenerateMemoryID(mem2, testAppName, testUserID)
		assert.NotEqual(t, id1, id2)
	})

	t.Run("topics order does not affect ID", func(t *testing.T) {
		mem1 := &memory.Memory{Memory: "User likes coffee", Topics: []string{"a", "b"}}
		mem2 := &memory.Memory{Memory: "User likes coffee", Topics: []string{"b", "a"}}
		id1 := GenerateMemoryID(mem1, testAppName, testUserID)
		id2 := GenerateMemoryID(mem2, testAppName, testUserID)
		// Same order after sorting produces same IDs.
		assert.Equal(t, id1, id2)
	})

	t.Run("different users produce different IDs", func(t *testing.T) {
		mem := &memory.Memory{Memory: "User likes coffee"}
		id1 := GenerateMemoryID(mem, "app1", "user1")
		id2 := GenerateMemoryID(mem, "app1", "user2")
		id3 := GenerateMemoryID(mem, "app2", "user1")
		assert.NotEqual(t, id1, id2)
		assert.NotEqual(t, id1, id3)
		assert.NotEqual(t, id2, id3)
	})
}

func TestApplyAutoModeDefaults(t *testing.T) {
	t.Run("nil enabledTools", func(t *testing.T) {
		userExplicitlySet := make(map[string]bool)
		ApplyAutoModeDefaults(nil, userExplicitlySet)
		// Should not panic
	})

	t.Run("empty maps", func(t *testing.T) {
		enabledTools := make(map[string]struct{})
		userExplicitlySet := make(map[string]bool)

		ApplyAutoModeDefaults(enabledTools, userExplicitlySet)

		// Should set auto mode defaults.
		_, hasAdd := enabledTools[memory.AddToolName]
		_, hasUpdate := enabledTools[memory.UpdateToolName]
		_, hasSearch := enabledTools[memory.SearchToolName]
		_, hasClear := enabledTools[memory.ClearToolName]
		_, hasLoad := enabledTools[memory.LoadToolName]
		assert.True(t, hasAdd)
		assert.True(t, hasUpdate)
		assert.True(t, hasSearch)
		assert.False(t, hasClear)
		assert.False(t, hasLoad)
	})

	t.Run("user explicitly set takes precedence", func(t *testing.T) {
		enabledTools := map[string]struct{}{
			memory.SearchToolName: {},
			memory.LoadToolName:   {},
		}
		userExplicitlySet := map[string]bool{
			memory.SearchToolName: true,
			memory.LoadToolName:   true,
		}

		ApplyAutoModeDefaults(enabledTools, userExplicitlySet)

		// User settings should be preserved.
		_, hasSearch := enabledTools[memory.SearchToolName]
		_, hasLoad := enabledTools[memory.LoadToolName]
		_, hasAdd := enabledTools[memory.AddToolName]
		_, hasUpdate := enabledTools[memory.UpdateToolName]
		_, hasClear := enabledTools[memory.ClearToolName]
		assert.True(t, hasSearch)
		assert.True(t, hasLoad)
		assert.True(t, hasAdd)
		assert.True(t, hasUpdate)
		assert.False(t, hasClear)
	})
}

func TestBuildToolsList(t *testing.T) {
	// Mock tool creators
	toolCreators := map[string]memory.ToolCreator{
		memory.AddToolName: func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		},
		memory.SearchToolName: func() tool.Tool {
			return &mockTool{name: memory.SearchToolName}
		},
	}

	t.Run("agentic mode", func(t *testing.T) {
		enabledTools := map[string]struct{}{
			memory.AddToolName: {},
		}
		cachedTools := make(map[string]tool.Tool)

		tools := BuildToolsList(nil, toolCreators, enabledTools, cachedTools)

		// Should only include enabled tools in agentic mode.
		assert.Len(t, tools, 1)
		assert.Equal(t, memory.AddToolName, tools[0].(*mockTool).name)
	})

	t.Run("auto mode", func(t *testing.T) {
		// Mock extractor for auto mode.
		ext := &mockExtractorForMemoryTest{}
		enabledTools := map[string]struct{}{
			memory.SearchToolName: {},
		}
		cachedTools := make(map[string]tool.Tool)

		// Add Load tool creator for this test.
		toolCreators[memory.LoadToolName] = func() tool.Tool {
			return &mockTool{name: memory.LoadToolName}
		}

		tools := BuildToolsList(ext, toolCreators, enabledTools, cachedTools)

		// In auto mode, only Search should be exposed.
		assert.Len(t, tools, 1)
		assert.Equal(t, memory.SearchToolName, tools[0].(*mockTool).name)
	})

	t.Run("caching", func(t *testing.T) {
		cachedTools := make(map[string]tool.Tool)
		enabledTools := map[string]struct{}{
			memory.AddToolName: {},
		}

		// First call.
		tools1 := BuildToolsList(nil, toolCreators, enabledTools, cachedTools)
		assert.Len(t, tools1, 1)

		// Second call should reuse cached tool.
		tools2 := BuildToolsList(nil, toolCreators, enabledTools, cachedTools)
		assert.Len(t, tools2, 1)
		assert.Same(t, tools1[0], tools2[0])
	})

	t.Run("stable ordering", func(t *testing.T) {
		// Add more tools to test ordering.
		toolCreators[memory.UpdateToolName] = func() tool.Tool {
			return &mockTool{name: memory.UpdateToolName}
		}
		enabledTools := map[string]struct{}{
			memory.UpdateToolName: {},
			memory.AddToolName:    {},
		}
		cachedTools := make(map[string]tool.Tool)

		tools := BuildToolsList(nil, toolCreators, enabledTools, cachedTools)

		// Should be sorted alphabetically
		assert.Len(t, tools, 2)
		assert.Equal(t, memory.AddToolName, tools[0].(*mockTool).name)
		assert.Equal(t, memory.UpdateToolName, tools[1].(*mockTool).name)
	})
}

// mockTool for testing
type mockTool struct {
	name string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: "Mock tool",
	}
}

// mockExtractorForMemoryTest for testing (different from auto_test.go's mockExtractor)
type mockExtractorForMemoryTest struct{}

func (m *mockExtractorForMemoryTest) Extract(ctx context.Context, messages []model.Message, existing []*memory.Entry) ([]*extractor.Operation, error) {
	return nil, nil
}

func (m *mockExtractorForMemoryTest) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
}

func (m *mockExtractorForMemoryTest) SetPrompt(prompt string) {}

func (m *mockExtractorForMemoryTest) SetModel(model model.Model) {}

func (m *mockExtractorForMemoryTest) Metadata() map[string]any {
	return nil
}

func TestShouldIncludeTool(t *testing.T) {
	t.Run("agentic mode", func(t *testing.T) {
		enabledTools := map[string]struct{}{
			memory.AddToolName: {},
		}

		assert.True(t, shouldIncludeTool(memory.AddToolName, nil, enabledTools))
		assert.False(t, shouldIncludeTool(memory.SearchToolName, nil, enabledTools))
	})

	t.Run("auto mode", func(t *testing.T) {
		ext := &mockExtractorForMemoryTest{}
		enabledTools := map[string]struct{}{
			memory.SearchToolName: {},
			memory.AddToolName:    {},
		}

		// Search should be included (exposed in auto mode).
		assert.True(t, shouldIncludeTool(memory.SearchToolName, ext, enabledTools))
		// Add should not be included (not exposed in auto mode).
		assert.False(t, shouldIncludeTool(memory.AddToolName, ext, enabledTools))
	})
}

func TestShouldIncludeAutoMemoryTool(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		enabledTools map[string]struct{}
		expected     bool
	}{
		{"search enabled", memory.SearchToolName, map[string]struct{}{memory.SearchToolName: {}}, true},
		{"search disabled", memory.SearchToolName, map[string]struct{}{}, false},
		{"load enabled", memory.LoadToolName, map[string]struct{}{memory.LoadToolName: {}}, true},
		{"load disabled", memory.LoadToolName, map[string]struct{}{}, false},
		{"non-exposed tool", memory.AddToolName, map[string]struct{}{memory.AddToolName: {}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldIncludeAutoMemoryTool(tt.toolName, tt.enabledTools)
			assert.Equal(t, tt.expected, result)
		})
	}
}
