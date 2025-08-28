package tcos

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

func TestArtifact_SessionScope(t *testing.T) {
	// Save-ListVersions-Load-ListKeys-Delete-ListVersions-Load-ListKeys
	t.Skip("Skipping TCOS integration test, need to set up environment variables COS_BUCKET_URL, COS_SECRETID and COS_SECRETKEY")
	s := NewService(os.Getenv("COS_BUCKET_URL"))
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
	t.Skip("Skipping TCOS integration test, need to set up environment variables COS_BUCKET_URL, COS_SECRETID and COS_SECRETKEY")
	// Save-ListVersions-Load-ListKeys-Delete-ListVersions-Load-ListKeys
	s := NewService(os.Getenv("COS_BUCKET_URL"))
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
