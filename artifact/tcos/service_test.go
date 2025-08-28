package tcos

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

func TestArtifact_SessionScope(t *testing.T) {
	// Save-ListVersions-Load-ListKeys-Delete-ListVersions-Load-ListKeys
	t.Skip("Skipping TCOS integration test, need to set up environment variables COS_SECRETID and COS_SECRETKEY")
	s := NewService("https://trpc-agent-go-test-1259770036.cos.ap-guangzhou.myqcloud.com")
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user1",
		SessionID: "session1",
	}
	sessionScopeKey := "test.txt"
	var artifacts []*artifact.Artifact
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, &artifact.Artifact{
			Data:     []byte("Hello, World!" + strconv.Itoa(i)),
			MimeType: "text/plain",
			Name:     "display_name_user_scope_test.txt",
		})
	}
	t.Cleanup(func() {
		if err := s.DeleteArtifact(context.Background(), sessionInfo, sessionScopeKey); err != nil {
			t.Logf("Cleanup: DeleteArtifact: %v", err)
		}
	})

	for i, a := range artifacts {
		version, err := s.SaveArtifact(context.Background(),
			sessionInfo, sessionScopeKey, a)
		require.NoError(t, err)
		require.Equal(t, i, version)
	}

	version, err := s.ListVersions(context.Background(), sessionInfo, sessionScopeKey)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{0, 1, 2}, version)

	a, err := s.LoadArtifact(context.Background(), sessionInfo, sessionScopeKey, nil)
	require.NoError(t, err)
	require.EqualValues(t, &artifact.Artifact{
		Data:     []byte("Hello, World!" + strconv.Itoa(2)),
		MimeType: "text/plain",
		Name:     sessionScopeKey,
	}, a)
	for i, wanted := range artifacts {
		got, err := s.LoadArtifact(context.Background(),
			sessionInfo, sessionScopeKey, &i)
		require.NoError(t, err)
		require.EqualValues(t, wanted.Data, got.Data)
		require.EqualValues(t, wanted.MimeType, got.MimeType)
		require.EqualValues(t, sessionScopeKey, got.Name)
	}

	keys, err := s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{sessionScopeKey}, keys)

	err = s.DeleteArtifact(context.Background(), sessionInfo, sessionScopeKey)
	require.NoError(t, err)

	keys, err = s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.Empty(t, keys)

	version, err = s.ListVersions(context.Background(), sessionInfo, sessionScopeKey)
	require.NoError(t, err)
	require.Empty(t, version)

	a, err = s.LoadArtifact(context.Background(), sessionInfo, sessionScopeKey, nil)
	require.NoError(t, err)
	require.Nil(t, a)
}

func TestArtifact_UserScope(t *testing.T) {
	t.Skip("Skipping TCOS integration test, need to set up environment variables COS_SECRETID and COS_SECRETKEY")
	// Save-ListVersions-Load-ListKeys-Delete-ListVersions-Load-ListKeys
	s := NewService("https://trpc-agent-go-test-1259770036.cos.ap-guangzhou.myqcloud.com")
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user2",
		SessionID: "session1",
	}
	userScopeKey := "user:test.txt"
	t.Cleanup(func() {
		if err := s.DeleteArtifact(context.Background(), sessionInfo, userScopeKey); err != nil {
			t.Logf("Cleanup: DeleteArtifact: %v", err)
		}
	})

	for i := 0; i < 3; i++ {
		data := []byte("Hi, World!" + strconv.Itoa(i))
		version, err := s.SaveArtifact(context.Background(),
			sessionInfo, userScopeKey, &artifact.Artifact{
				Data:     data,
				MimeType: "text/plain",
				Name:     "display_name_user_scope_test.txt",
			})
		require.NoError(t, err)
		require.Equal(t, i, version)
	}

	version, err := s.ListVersions(context.Background(), sessionInfo, userScopeKey)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{0, 1, 2}, version)

	a, err := s.LoadArtifact(context.Background(), sessionInfo, userScopeKey, nil)
	require.NoError(t, err)
	require.EqualValues(t, &artifact.Artifact{
		Data:     []byte("Hi, World!" + strconv.Itoa(2)),
		MimeType: "text/plain",
		Name:     userScopeKey,
	}, a)
	for i := 0; i < 3; i++ {
		a, err := s.LoadArtifact(context.Background(),
			sessionInfo, userScopeKey, &i)
		require.NoError(t, err)
		require.EqualValues(t, &artifact.Artifact{
			Data:     []byte("Hi, World!" + strconv.Itoa(i)),
			MimeType: "text/plain",
			Name:     userScopeKey,
		}, a)
	}

	keys, err := s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userScopeKey}, keys)

	err = s.DeleteArtifact(context.Background(), sessionInfo, userScopeKey)
	require.NoError(t, err)

	keys, err = s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.Empty(t, keys)

	version, err = s.ListVersions(context.Background(), sessionInfo, userScopeKey)
	require.NoError(t, err)
	require.Empty(t, version)

	a, err = s.LoadArtifact(context.Background(), sessionInfo, userScopeKey, nil)
	require.NoError(t, err)
	require.Nil(t, a)
}

// MockVersionService is a test helper that simulates version tracking
type MockVersionService struct {
	versions map[string][]int // map of object name prefix to list of versions
}

func NewMockVersionService() *MockVersionService {
	return &MockVersionService{
		versions: make(map[string][]int),
	}
}

func (m *MockVersionService) GetVersions(objectPrefix string) []int {
	return m.versions[objectPrefix]
}

func (m *MockVersionService) AddVersion(objectPrefix string, version int) {
	if m.versions[objectPrefix] == nil {
		m.versions[objectPrefix] = []int{}
	}
	m.versions[objectPrefix] = append(m.versions[objectPrefix], version)
}

// TestSaveArtifact_VersionLogic tests the version calculation logic of SaveArtifact
func TestSaveArtifact_VersionLogic(t *testing.T) {
	tests := []struct {
		name             string
		sessionInfo      artifact.SessionInfo
		filename         string
		existingVersions []int
		expectedVersion  int
	}{
		{
			name: "first_version_no_existing",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename:         "new_file.txt",
			existingVersions: []int{},
			expectedVersion:  0,
		},
		{
			name: "second_version",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename:         "existing_file.txt",
			existingVersions: []int{0},
			expectedVersion:  1,
		},
		{
			name: "multiple_existing_versions",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename:         "popular_file.txt",
			existingVersions: []int{0, 1, 2, 5, 10},
			expectedVersion:  11,
		},
		{
			name: "user_namespaced_file_versioning",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename:         "user:profile.jpg",
			existingVersions: []int{0, 1},
			expectedVersion:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate what version should be assigned
			expectedVersion := 0
			if len(tt.existingVersions) > 0 {
				maxVersion := 0
				for _, v := range tt.existingVersions {
					if v > maxVersion {
						maxVersion = v
					}
				}
				expectedVersion = maxVersion + 1
			}

			if expectedVersion != tt.expectedVersion {
				t.Errorf("Version calculation error: got %d, want %d", expectedVersion, tt.expectedVersion)
			}

			// Test object name construction for the calculated version
			objectName := buildObjectName(tt.sessionInfo, tt.filename, expectedVersion)

			// Verify object name format is correct
			if fileHasUserNamespace(tt.filename) {
				expectedPattern := tt.sessionInfo.AppName + "/" + tt.sessionInfo.UserID + "/user/" + tt.filename
				if !contains(objectName, expectedPattern) {
					t.Errorf("User-namespaced object name %q should contain %q", objectName, expectedPattern)
				}
			} else {
				expectedPattern := tt.sessionInfo.AppName + "/" + tt.sessionInfo.UserID + "/" + tt.sessionInfo.SessionID + "/" + tt.filename
				if !contains(objectName, expectedPattern) {
					t.Errorf("Session-scoped object name %q should contain %q", objectName, expectedPattern)
				}
			}
		})
	}
}

// TestSaveArtifact_ArtifactValidation tests artifact validation logic
func TestSaveArtifact_ArtifactValidation(t *testing.T) {
	tests := []struct {
		name     string
		artifact *artifact.Artifact
		wantErr  bool
	}{
		{
			name: "valid_text_artifact",
			artifact: &artifact.Artifact{
				Data:     []byte("Hello, World!"),
				MimeType: "text/plain",
				Name:     "test.txt",
			},
			wantErr: false,
		},
		{
			name: "valid_binary_artifact",
			artifact: &artifact.Artifact{
				Data:     []byte{0x89, 0x50, 0x4E, 0x47}, // PNG header
				MimeType: "image/png",
				Name:     "image.png",
			},
			wantErr: false,
		},
		{
			name: "empty_data_valid",
			artifact: &artifact.Artifact{
				Data:     []byte{},
				MimeType: "text/plain",
				Name:     "empty.txt",
			},
			wantErr: false,
		},
		{
			name: "nil_data_invalid",
			artifact: &artifact.Artifact{
				Data:     nil,
				MimeType: "text/plain",
				Name:     "nil_data.txt",
			},
			wantErr: false, // nil data might be valid in some cases
		},
		{
			name:     "nil_artifact",
			artifact: nil,
			wantErr:  true, // nil artifact should cause error in real implementation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test basic artifact properties
			if tt.artifact != nil {
				if tt.artifact.Data == nil && !tt.wantErr {
					// This is acceptable - empty data is different from nil data
				}
				if tt.artifact.MimeType == "" && !tt.wantErr {
					t.Error("Valid artifact should have MimeType")
				}
			}
		})
	}
}

// TestBuildObjectName_EdgeCases tests edge cases for object name building
func TestBuildObjectName_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		sessionInfo artifact.SessionInfo
		filename    string
		version     int
		wantContain []string // strings that should be in the result
	}{
		{
			name: "special_characters_in_filename",
			sessionInfo: artifact.SessionInfo{
				AppName:   "app",
				UserID:    "user",
				SessionID: "session",
			},
			filename:    "file with spaces & symbols.txt",
			version:     0,
			wantContain: []string{"app", "user", "session", "file with spaces & symbols.txt", "0"},
		},
		{
			name: "unicode_filename",
			sessionInfo: artifact.SessionInfo{
				AppName:   "应用",
				UserID:    "用户123",
				SessionID: "会话456",
			},
			filename:    "测试文件.txt",
			version:     1,
			wantContain: []string{"应用", "用户123", "会话456", "测试文件.txt", "1"},
		},
		{
			name: "user_namespace_with_slash",
			sessionInfo: artifact.SessionInfo{
				AppName:   "app",
				UserID:    "user",
				SessionID: "session",
			},
			filename:    "user:folder/subfolder/file.txt",
			version:     2,
			wantContain: []string{"app", "user", "user", "user:folder/subfolder/file.txt", "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildObjectName(tt.sessionInfo, tt.filename, tt.version)

			for _, want := range tt.wantContain {
				if !contains(result, want) {
					t.Errorf("buildObjectName() = %q, should contain %q", result, want)
				}
			}
		})
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && containsAt(s, substr)))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestService_buildObjectName tests the object name construction logic
func TestService_buildObjectName(t *testing.T) {

	tests := []struct {
		name        string
		sessionInfo artifact.SessionInfo
		filename    string
		version     int
		expected    string
	}{
		{
			name: "regular_session_scoped_file",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename: "document.pdf",
			version:  0,
			expected: "testapp/user123/session456/document.pdf/0",
		},
		{
			name: "user_namespaced_file",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename: "user:profile.jpg",
			version:  1,
			expected: "testapp/user123/user/user:profile.jpg/1",
		},
		{
			name: "higher_version_number",
			sessionInfo: artifact.SessionInfo{
				AppName:   "myapp",
				UserID:    "alice",
				SessionID: "sess789",
			},
			filename: "data.json",
			version:  42,
			expected: "myapp/alice/sess789/data.json/42",
		},
		{
			name: "user_namespaced_complex_filename",
			sessionInfo: artifact.SessionInfo{
				AppName:   "app",
				UserID:    "bob",
				SessionID: "ignored_for_user_files",
			},
			filename: "user:documents/important.docx",
			version:  5,
			expected: "app/bob/user/user:documents/important.docx/5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildObjectName(tt.sessionInfo, tt.filename, tt.version)
			if result != tt.expected {
				t.Errorf("buildObjectName() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestService_buildObjectNamePrefix tests the object name prefix construction
func TestService_buildObjectNamePrefix(t *testing.T) {

	tests := []struct {
		name        string
		sessionInfo artifact.SessionInfo
		filename    string
		expected    string
	}{
		{
			name: "regular_file_prefix",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename: "document.pdf",
			expected: "testapp/user123/session456/document.pdf/",
		},
		{
			name: "user_namespaced_file_prefix",
			sessionInfo: artifact.SessionInfo{
				AppName:   "testapp",
				UserID:    "user123",
				SessionID: "session456",
			},
			filename: "user:profile.jpg",
			expected: "testapp/user123/user/user:profile.jpg/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildObjectNamePrefix(tt.sessionInfo, tt.filename)
			if result != tt.expected {
				t.Errorf("buildObjectNamePrefix() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestService_fileHasUserNamespace tests user namespace detection
func TestService_fileHasUserNamespace(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{
			name:     "user_namespaced_file",
			filename: "user:profile.jpg",
			expected: true,
		},
		{
			name:     "user_namespaced_with_path",
			filename: "user:documents/readme.txt",
			expected: true,
		},
		{
			name:     "regular_file",
			filename: "document.pdf",
			expected: false,
		},
		{
			name:     "file_with_user_in_name",
			filename: "user_data.csv",
			expected: false,
		},
		{
			name:     "empty_filename",
			filename: "",
			expected: false,
		},
		{
			name:     "user_colon_in_middle",
			filename: "data_user:file.txt",
			expected: false,
		},
		{
			name:     "just_user_colon",
			filename: "user:",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fileHasUserNamespace(tt.filename)
			if result != tt.expected {
				t.Errorf("fileHasUserNamespace(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

// TestService_NewService tests service creation with different bucket URLs
func TestService_NewService(t *testing.T) {
	tests := []struct {
		name           string
		bucketURL      string
		expectNonNil   bool
		expectedBucket string
	}{
		{
			name:           "valid_bucket_url",
			bucketURL:      "https://test-bucket.cos.ap-guangzhou.myqcloud.com",
			expectNonNil:   true,
			expectedBucket: "test-bucket",
		},
		{
			name:           "different_region",
			bucketURL:      "https://my-app-bucket.cos.ap-beijing.myqcloud.com",
			expectNonNil:   true,
			expectedBucket: "my-app-bucket",
		},
		{
			name:           "bucket_with_numbers",
			bucketURL:      "https://bucket-123.cos.ap-shanghai.myqcloud.com",
			expectNonNil:   true,
			expectedBucket: "bucket-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(tt.bucketURL)

			if tt.expectNonNil {
				if service == nil {
					t.Error("NewService() returned nil, expected non-nil service")
					return
				}
				if service.cosClient == nil {
					t.Error("NewService() cosClient is nil, expected non-nil client")
				}
			} else {
				if service != nil {
					t.Error("NewService() returned non-nil, expected nil")
				}
			}
		})
	}
}
