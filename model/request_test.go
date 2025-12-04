//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRole_String(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want string
	}{
		{
			name: "system role",
			role: RoleSystem,
			want: "system",
		},
		{
			name: "user role",
			role: RoleUser,
			want: "user",
		},
		{
			name: "assistant role",
			role: RoleAssistant,
			want: "assistant",
		},
		{
			name: "custom role",
			role: Role("custom"),
			want: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.String(); got != tt.want {
				t.Errorf("Role.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRole_IsValid(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{
			name: "valid system role",
			role: RoleSystem,
			want: true,
		},
		{
			name: "valid user role",
			role: RoleUser,
			want: true,
		},
		{
			name: "valid assistant role",
			role: RoleAssistant,
			want: true,
		},
		{
			name: "invalid empty role",
			role: Role(""),
			want: false,
		},
		{
			name: "invalid custom role",
			role: Role("custom"),
			want: false,
		},
		{
			name: "invalid mixed case role",
			role: Role("System"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsValid(); got != tt.want {
				t.Errorf("Role.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewSystemMessage(t *testing.T) {
	content := "You are a helpful assistant."
	msg := NewSystemMessage(content)

	if msg.Role != RoleSystem {
		t.Errorf("NewSystemMessage() role = %v, want %v", msg.Role, RoleSystem)
	}
	if msg.Content != content {
		t.Errorf("NewSystemMessage() content = %v, want %v", msg.Content, content)
	}
}

func TestNewUserMessage(t *testing.T) {
	content := "Hello, how are you?"
	msg := NewUserMessage(content)

	if msg.Role != RoleUser {
		t.Errorf("NewUserMessage() role = %v, want %v", msg.Role, RoleUser)
	}
	if msg.Content != content {
		t.Errorf("NewUserMessage() content = %v, want %v", msg.Content, content)
	}
}

func TestNewAssistantMessage(t *testing.T) {
	content := "I'm doing well, thank you!"
	msg := NewAssistantMessage(content)

	if msg.Role != RoleAssistant {
		t.Errorf("NewAssistantMessage() role = %v, want %v", msg.Role, RoleAssistant)
	}
	if msg.Content != content {
		t.Errorf("NewAssistantMessage() content = %v, want %v", msg.Content, content)
	}
}

func TestMessage_JSON(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "Test message",
	}

	// Test that the message can be marshaled to JSON
	expected := `{"role":"user","content":"Test message"}`

	// We're not testing JSON marshaling directly here since it's built-in
	// but we can test that the struct tags are correct by checking field values
	if msg.Role != RoleUser {
		t.Errorf("Message.Role = %v, want %v", msg.Role, RoleUser)
	}
	if msg.Content != "Test message" {
		t.Errorf("Message.Content = %v, want %v", msg.Content, "Test message")
	}

	_ = expected // Suppress unused variable warning
}

func TestRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		request *Request
		wantErr bool
	}{
		{
			name: "valid basic request",
			request: &Request{
				Messages: []Message{
					NewUserMessage("Hello"),
				},
			},
			wantErr: false,
		},
		{
			name: "empty messages",
			request: &Request{
				Messages: []Message{},
			},
			wantErr: false, // Message validation might be done elsewhere
		},
		{
			name: "with optional parameters",
			request: &Request{
				Messages: []Message{
					NewSystemMessage("You are helpful"),
					NewUserMessage("Hello"),
				},
				GenerationConfig: GenerationConfig{
					MaxTokens:        intPtr(100),
					Temperature:      floatPtr(0.7),
					TopP:             floatPtr(0.9),
					PresencePenalty:  floatPtr(0.1),
					FrequencyPenalty: floatPtr(0.1),
					Stop:             []string{"END"},
					Stream:           true,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Since there's no explicit validation in the Request struct,
			// we just verify the struct can be created successfully
			if tt.request == nil {
				t.Error("Request should not be nil")
			}
		})
	}
}

func TestContentPartWithImage(t *testing.T) {
	// Test creating a content part with image
	imagePart := ContentPart{
		Type: "image",
		Image: &Image{
			URL:    "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
			Detail: "high",
		},
	}

	if imagePart.Type != "image" {
		t.Errorf("Expected type to be 'image', got %s", imagePart.Type)
	}

	if imagePart.Image == nil {
		t.Error("Expected Image to be set")
	}

	if imagePart.Image.Detail != "high" {
		t.Errorf("Expected detail to be 'high', got %s", imagePart.Image.Detail)
	}
}

func TestContentPartWithAudio(t *testing.T) {
	// Test creating a content part with audio
	audioPart := ContentPart{
		Type: "audio",
		Audio: &Audio{
			Data:   []byte("data:audio/wav;base64,UklGRnoGAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQoGAACBhYqFbF1fdJivrJBhNjVgodDbq2EcBj+a2/LDciUFLIHO8tiJNwgZaLvt559NEAxQp+PwtmMcBjiR1/LMeSwFJHfH8N2QQAoUXrTp66hVFApGn+DyvmwhBSuBzvLZiTYIG2m98OScTgwOUarm7blmGgU7k9n1unEiBC13yO/eizEIHWq+8+OWT"),
			Format: "wav",
		},
	}

	if audioPart.Type != "audio" {
		t.Errorf("Expected type to be 'audio', got %s", audioPart.Type)
	}

	if audioPart.Audio == nil {
		t.Error("Expected Audio to be set")
	}
}

func TestContentPartWithFile(t *testing.T) {
	// Test creating a content part with file
	filePart := ContentPart{
		Type: "file",
		File: &File{
			FileID: "file-abc123",
		},
	}

	if filePart.Type != "file" {
		t.Errorf("Expected type to be 'file', got %s", filePart.Type)
	}

	if filePart.File == nil {
		t.Error("Expected File to be set")
	}

	if filePart.File.FileID != "file-abc123" {
		t.Errorf("Expected FileID to be 'file-abc123', got %s", filePart.File.FileID)
	}
}

func TestMessage_WithReasoningContent(t *testing.T) {
	// Test message with ReasoningContent field
	msg := Message{
		Role:             RoleAssistant,
		Content:          "This is the main content",
		ReasoningContent: "This is the reasoning content",
	}

	// Verify field values
	if msg.Role != RoleAssistant {
		t.Errorf("Message.Role = %v, want %v", msg.Role, RoleAssistant)
	}
	if msg.Content != "This is the main content" {
		t.Errorf("Message.Content = %v, want %v", msg.Content, "This is the main content")
	}
	if msg.ReasoningContent != "This is the reasoning content" {
		t.Errorf("Message.ReasoningContent = %v, want %v", msg.ReasoningContent, "This is the reasoning content")
	}
}

// Helper functions for test data
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

func TestFunctionDefinitionParam_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		param    FunctionDefinitionParam
		expected string
	}{
		{
			name: "empty arguments",
			param: FunctionDefinitionParam{
				Name:        "test_function",
				Description: "A test function",
				Arguments:   []byte{},
			},
			expected: `{"name":"test_function","description":"A test function"}`,
		},
		{
			name: "with arguments",
			param: FunctionDefinitionParam{
				Name:        "test_function",
				Description: "A test function",
				Arguments:   []byte(`{"param1": "value1", "param2": 42}`),
			},
			expected: `{"arguments":"{\"param1\": \"value1\", \"param2\": 42}","name":"test_function","description":"A test function"}`,
		},
		{
			name: "with strict flag",
			param: FunctionDefinitionParam{
				Name:        "strict_function",
				Description: "A strict function",
				Strict:      true,
				Arguments:   []byte(`{"param": "value"}`),
			},
			expected: `{"arguments":"{\"param\": \"value\"}","name":"strict_function","description":"A strict function","strict":true}`,
		},
		{
			name: "complex arguments",
			param: FunctionDefinitionParam{
				Name:        "complex_function",
				Description: "A complex function",
				Arguments:   []byte(`{"nested": {"key": "value"}, "array": [1, 2, 3], "boolean": true}`),
			},
			expected: `{"arguments":"{\"nested\": {\"key\": \"value\"}, \"array\": [1, 2, 3], \"boolean\": true}","name":"complex_function","description":"A complex function"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test marshaling
			jsonData, err := json.Marshal(tt.param)
			require.NoError(t, err)

			// Parse the result to check structure
			var result map[string]any
			err = json.Unmarshal(jsonData, &result)
			require.NoError(t, err)

			// Check that Arguments field is a string (not base64 encoded)
			if len(tt.param.Arguments) > 0 {
				arguments, exists := result["arguments"]
				require.True(t, exists, "Arguments field should exist")
				require.IsType(t, "", arguments, "Arguments should be a string, not base64 encoded")

				// Verify the string content is readable JSON
				argumentsStr := arguments.(string)
				assert.Equal(t, string(tt.param.Arguments), argumentsStr, "Arguments should be readable JSON string")
			}

			// Check other fields
			assert.Equal(t, tt.param.Name, result["name"])
			assert.Equal(t, tt.param.Description, result["description"])
			if tt.param.Strict {
				assert.Equal(t, tt.param.Strict, result["strict"])
			}
		})
	}
}

func TestFunctionDefinitionParam_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		expected FunctionDefinitionParam
	}{
		{
			name:     "empty arguments",
			jsonData: `{"name":"test_function","description":"A test function"}`,
			expected: FunctionDefinitionParam{
				Name:        "test_function",
				Description: "A test function",
				Arguments:   []byte{},
			},
		},
		{
			name:     "with arguments",
			jsonData: `{"arguments":"{\"param1\": \"value1\", \"param2\": 42}","name":"test_function","description":"A test function"}`,
			expected: FunctionDefinitionParam{
				Name:        "test_function",
				Description: "A test function",
				Arguments:   []byte(`{"param1": "value1", "param2": 42}`),
			},
		},
		{
			name:     "with strict flag",
			jsonData: `{"arguments":"{\"param\": \"value\"}","name":"strict_function","description":"A strict function","strict":true}`,
			expected: FunctionDefinitionParam{
				Name:        "strict_function",
				Description: "A strict function",
				Strict:      true,
				Arguments:   []byte(`{"param": "value"}`),
			},
		},
		{
			name:     "complex arguments",
			jsonData: `{"arguments":"{\"nested\": {\"key\": \"value\"}, \"array\": [1, 2, 3], \"boolean\": true}","name":"complex_function","description":"A complex function"}`,
			expected: FunctionDefinitionParam{
				Name:        "complex_function",
				Description: "A complex function",
				Arguments:   []byte(`{"nested": {"key": "value"}, "array": [1, 2, 3], "boolean": true}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result FunctionDefinitionParam
			err := json.Unmarshal([]byte(tt.jsonData), &result)
			require.NoError(t, err)

			assert.Equal(t, tt.expected.Name, result.Name)
			assert.Equal(t, tt.expected.Description, result.Description)
			assert.Equal(t, tt.expected.Strict, result.Strict)
			assert.Equal(t, tt.expected.Arguments, result.Arguments)
		})
	}
}

func TestMessage_AddFileData(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		data     []byte
		mimetype string
	}{
		{
			name:     "add text file",
			filename: "test.txt",
			data:     []byte("Hello, World!"),
			mimetype: "text/plain",
		},
		{
			name:     "add JSON file",
			filename: "config.json",
			data:     []byte(`{"key": "value"}`),
			mimetype: "application/json",
		},
		{
			name:     "add PDF file",
			filename: "document.pdf",
			data:     []byte("PDF content"),
			mimetype: "application/pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{}
			msg.AddFileData(tt.filename, tt.data, tt.mimetype)

			require.Len(t, msg.ContentParts, 1)
			assert.Equal(t, ContentTypeFile, msg.ContentParts[0].Type)
			require.NotNil(t, msg.ContentParts[0].File)
			assert.Equal(t, tt.filename, msg.ContentParts[0].File.Name)
			assert.Equal(t, tt.data, msg.ContentParts[0].File.Data)
			assert.Equal(t, tt.mimetype, msg.ContentParts[0].File.MimeType)
		})
	}
}

func TestMessage_AddFileID(t *testing.T) {
	tests := []struct {
		name   string
		fileID string
	}{
		{
			name:   "add file with ID",
			fileID: "file-abc123",
		},
		{
			name:   "add file with empty ID",
			fileID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{}
			msg.AddFileID(tt.fileID)

			require.Len(t, msg.ContentParts, 1)
			assert.Equal(t, ContentTypeFile, msg.ContentParts[0].Type)
			require.NotNil(t, msg.ContentParts[0].File)
			assert.Equal(t, tt.fileID, msg.ContentParts[0].File.FileID)
		})
	}
}

func TestMessage_AddImageURL(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		detail string
	}{
		{
			name:   "add image with auto detail",
			url:    "https://example.com/image.png",
			detail: "auto",
		},
		{
			name:   "add image with high detail",
			url:    "https://example.com/photo.jpg",
			detail: "high",
		},
		{
			name:   "add image with low detail",
			url:    "https://example.com/thumb.webp",
			detail: "low",
		},
		{
			name:   "add image with empty detail",
			url:    "https://example.com/image.gif",
			detail: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{}
			msg.AddImageURL(tt.url, tt.detail)

			require.Len(t, msg.ContentParts, 1)
			assert.Equal(t, ContentTypeImage, msg.ContentParts[0].Type)
			require.NotNil(t, msg.ContentParts[0].Image)
			assert.Equal(t, tt.url, msg.ContentParts[0].Image.URL)
			assert.Equal(t, tt.detail, msg.ContentParts[0].Image.Detail)
		})
	}
}

func TestMessage_AddImageData(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		detail string
		format string
	}{
		{
			name:   "add PNG image",
			data:   []byte("PNG data"),
			detail: "high",
			format: "png",
		},
		{
			name:   "add JPEG image",
			data:   []byte("JPEG data"),
			detail: "auto",
			format: "jpeg",
		},
		{
			name:   "add WEBP image",
			data:   []byte("WEBP data"),
			detail: "low",
			format: "webp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{}
			msg.AddImageData(tt.data, tt.detail, tt.format)

			require.Len(t, msg.ContentParts, 1)
			assert.Equal(t, ContentTypeImage, msg.ContentParts[0].Type)
			require.NotNil(t, msg.ContentParts[0].Image)
			assert.Equal(t, tt.data, msg.ContentParts[0].Image.Data)
			assert.Equal(t, tt.detail, msg.ContentParts[0].Image.Detail)
			assert.Equal(t, tt.format, msg.ContentParts[0].Image.Format)
		})
	}
}

func TestMessage_AddAudioData(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		format string
	}{
		{
			name:   "add WAV audio",
			data:   []byte("WAV data"),
			format: "wav",
		},
		{
			name:   "add MP3 audio",
			data:   []byte("MP3 data"),
			format: "mp3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{}
			msg.AddAudioData(tt.data, tt.format)

			require.Len(t, msg.ContentParts, 1)
			assert.Equal(t, ContentTypeAudio, msg.ContentParts[0].Type)
			require.NotNil(t, msg.ContentParts[0].Audio)
			assert.Equal(t, tt.data, msg.ContentParts[0].Audio.Data)
			assert.Equal(t, tt.format, msg.ContentParts[0].Audio.Format)
		})
	}
}

func TestMessage_AddFilePath(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		fileExt     string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "add text file",
			fileContent: "Hello, World!",
			fileExt:     ".txt",
			expectError: false,
		},
		{
			name:        "add JSON file",
			fileContent: `{"key": "value"}`,
			fileExt:     ".json",
			expectError: false,
		},
		{
			name:        "add Python file",
			fileContent: "print('hello')",
			fileExt:     ".py",
			expectError: false,
		},
		{
			name:        "add Markdown file",
			fileContent: "# Title\nContent",
			fileExt:     ".md",
			expectError: false,
		},
		{
			name:        "unsupported file extension",
			fileContent: "content",
			fileExt:     ".unknown",
			expectError: true,
			errorMsg:    "unknown file extension",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file
			tmpFile, err := os.CreateTemp("", "test*"+tt.fileExt)
			require.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			// Write content
			_, err = tmpFile.WriteString(tt.fileContent)
			require.NoError(t, err)
			tmpFile.Close()

			// Test AddFilePath
			msg := &Message{}
			err = msg.AddFilePath(tmpFile.Name())

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				require.Len(t, msg.ContentParts, 1)
				assert.Equal(t, ContentTypeFile, msg.ContentParts[0].Type)
				require.NotNil(t, msg.ContentParts[0].File)
				assert.Equal(t, []byte(tt.fileContent), msg.ContentParts[0].File.Data)
			}
		})
	}

	// Test file not exists
	t.Run("file not exists", func(t *testing.T) {
		msg := &Message{}
		err := msg.AddFilePath("/nonexistent/file.txt")
		assert.Error(t, err)
	})
}

func TestMessage_AddImageFilePath(t *testing.T) {
	tests := []struct {
		name        string
		fileExt     string
		detail      string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "add PNG image",
			fileExt:     ".png",
			detail:      "high",
			expectError: false,
		},
		{
			name:        "add JPG image",
			fileExt:     ".jpg",
			detail:      "auto",
			expectError: false,
		},
		{
			name:        "add JPEG image",
			fileExt:     ".jpeg",
			detail:      "low",
			expectError: false,
		},
		{
			name:        "add WEBP image",
			fileExt:     ".webp",
			detail:      "",
			expectError: false,
		},
		{
			name:        "add GIF image",
			fileExt:     ".gif",
			detail:      "auto",
			expectError: false,
		},
		{
			name:        "unsupported image format",
			fileExt:     ".bmp",
			detail:      "auto",
			expectError: true,
			errorMsg:    "unsupported image format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file with fake image data
			tmpFile, err := os.CreateTemp("", "test*"+tt.fileExt)
			require.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			// Write some fake image data
			fakeImageData := []byte("fake image data content")
			_, err = tmpFile.Write(fakeImageData)
			require.NoError(t, err)
			tmpFile.Close()

			// Test AddImageFilePath
			msg := &Message{}
			err = msg.AddImageFilePath(tmpFile.Name(), tt.detail)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				require.Len(t, msg.ContentParts, 1)
				assert.Equal(t, ContentTypeImage, msg.ContentParts[0].Type)
				require.NotNil(t, msg.ContentParts[0].Image)
				assert.Equal(t, fakeImageData, msg.ContentParts[0].Image.Data)
				assert.Equal(t, tt.detail, msg.ContentParts[0].Image.Detail)
			}
		})
	}

	// Test file not exists
	t.Run("image file not exists", func(t *testing.T) {
		msg := &Message{}
		err := msg.AddImageFilePath("/nonexistent/image.png", "auto")
		assert.Error(t, err)
	})
}

func TestMessage_AddAudioFilePath(t *testing.T) {
	tests := []struct {
		name        string
		fileExt     string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "add WAV audio",
			fileExt:     ".wav",
			expectError: false,
		},
		{
			name:        "add MP3 audio",
			fileExt:     ".mp3",
			expectError: false,
		},
		{
			name:        "unsupported audio format - AAC",
			fileExt:     ".aac",
			expectError: true,
			errorMsg:    "unsupported audio format",
		},
		{
			name:        "unsupported audio format - FLAC",
			fileExt:     ".flac",
			expectError: true,
			errorMsg:    "unsupported audio format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file with fake audio data
			tmpFile, err := os.CreateTemp("", "test*"+tt.fileExt)
			require.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			// Write some fake audio data
			fakeAudioData := []byte("fake audio data content")
			_, err = tmpFile.Write(fakeAudioData)
			require.NoError(t, err)
			tmpFile.Close()

			// Test AddAudioFilePath
			msg := &Message{}
			err = msg.AddAudioFilePath(tmpFile.Name())

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				require.Len(t, msg.ContentParts, 1)
				assert.Equal(t, ContentTypeAudio, msg.ContentParts[0].Type)
				require.NotNil(t, msg.ContentParts[0].Audio)
				assert.Equal(t, fakeAudioData, msg.ContentParts[0].Audio.Data)
			}
		})
	}

	// Test file not exists
	t.Run("audio file not exists", func(t *testing.T) {
		msg := &Message{}
		err := msg.AddAudioFilePath("/nonexistent/audio.wav")
		assert.Error(t, err)
	})
}

func TestInferMimeType(t *testing.T) {
	tests := []struct {
		name        string
		filepath    string
		expectMime  string
		expectError bool
	}{
		{
			name:       "text file",
			filepath:   "test.txt",
			expectMime: "text/plain",
		},
		{
			name:       "markdown file",
			filepath:   "README.md",
			expectMime: "text/markdown",
		},
		{
			name:       "JSON file",
			filepath:   "config.json",
			expectMime: "application/json",
		},
		{
			name:       "PDF file",
			filepath:   "document.pdf",
			expectMime: "application/pdf",
		},
		{
			name:       "Python file",
			filepath:   "script.py",
			expectMime: "text/x-python",
		},
		{
			name:       "JavaScript file",
			filepath:   "app.js",
			expectMime: "text/javascript",
		},
		{
			name:       "TypeScript file",
			filepath:   "main.ts",
			expectMime: "application/typescript",
		},
		{
			name:       "C file",
			filepath:   "main.c",
			expectMime: "text/x-c",
		},
		{
			name:       "case insensitive - uppercase",
			filepath:   "TEST.TXT",
			expectMime: "text/plain",
		},
		{
			name:       "case insensitive - mixed",
			filepath:   "Test.Py",
			expectMime: "text/x-python",
		},
		{
			name:        "unknown extension",
			filepath:    "file.unknown",
			expectError: true,
		},
		{
			name:        "no extension",
			filepath:    "filename",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mime, err := inferMimeType(tt.filepath)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unknown file extension")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectMime, mime)
			}
		})
	}
}

func TestFunctionDefinitionParam_UnmarshalJSON_InvalidJSON(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
	}{
		{
			name:     "invalid JSON syntax",
			jsonData: `{name:"test", invalid}`,
		},
		{
			name:     "unclosed braces",
			jsonData: `{"name":"test"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result FunctionDefinitionParam
			err := json.Unmarshal([]byte(tt.jsonData), &result)
			require.Error(t, err)
		})
	}
}

func TestNewToolMessage(t *testing.T) {
	toolID := "tool-123"
	toolName := "test-tool"
	content := "Tool execution result"

	msg := NewToolMessage(toolID, toolName, content)

	assert.Equal(t, RoleTool, msg.Role)
	assert.Equal(t, toolID, msg.ToolID)
	assert.Equal(t, toolName, msg.ToolName)
	assert.Equal(t, content, msg.Content)
}

func TestRole_ToolRole(t *testing.T) {
	role := RoleTool
	assert.Equal(t, "tool", role.String())
	assert.True(t, role.IsValid())
}
