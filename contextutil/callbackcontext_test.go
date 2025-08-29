//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package contextutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// MockArtifactService is a mock implementation of artifact.Service
type MockArtifactService struct {
	mock.Mock
}

func (m *MockArtifactService) SaveArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, artifact *artifact.Artifact) (int, error) {
	args := m.Called(ctx, sessionInfo, filename, artifact)
	return args.Int(0), args.Error(1)
}

func (m *MockArtifactService) LoadArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, version *int) (*artifact.Artifact, error) {
	args := m.Called(ctx, sessionInfo, filename, version)
	return args.Get(0).(*artifact.Artifact), args.Error(1)
}

func (m *MockArtifactService) ListArtifactKeys(ctx context.Context, sessionInfo artifact.SessionInfo) ([]string, error) {
	args := m.Called(ctx, sessionInfo)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockArtifactService) DeleteArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) error {
	args := m.Called(ctx, sessionInfo, filename)
	return args.Error(0)
}

func (m *MockArtifactService) ListVersions(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) ([]int, error) {
	args := m.Called(ctx, sessionInfo, filename)
	return args.Get(0).([]int), args.Error(1)
}

func TestNewCallbackContext(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		expectError bool
		errorMsg    string
	}{
		{
			name:        "context without invocation",
			ctx:         context.Background(),
			expectError: true,
			errorMsg:    "invocation not found in context",
		},
		{
			name:        "context with nil invocation",
			ctx:         agent.NewContextWithInvocation(context.Background(), nil),
			expectError: true,
			errorMsg:    "invocation not found in context",
		},
		{
			name: "context with valid invocation",
			ctx: agent.NewContextWithInvocation(context.Background(), &agent.Invocation{
				AgentName: "test-agent",
			}),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, err := NewCallbackContext(tt.ctx)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, cc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cc)
				assert.Equal(t, tt.ctx, cc.Context())
			}
		})
	}
}

func TestCallbackContext_Context(t *testing.T) {
	ctx := context.Background()
	invocation := &agent.Invocation{AgentName: "test-agent"}
	ctxWithInvocation := agent.NewContextWithInvocation(ctx, invocation)

	cc, err := NewCallbackContext(ctxWithInvocation)
	assert.NoError(t, err)
	assert.Equal(t, ctxWithInvocation, cc.Context())
}

func TestCallbackContext_SaveArtifact(t *testing.T) {
	tests := []struct {
		name          string
		setupContext  func() context.Context
		filename      string
		artifact      *artifact.Artifact
		expectedError string
		mockSetup     func(*MockArtifactService)
		expectedVer   int
	}{
		{
			name: "successful save",
			setupContext: func() context.Context {
				mockService := &MockArtifactService{}
				invocation := &agent.Invocation{
					ArtifactService: mockService,
					Session: &session.Session{
						AppName: "test-app",
						UserID:  "test-user",
						ID:      "test-session",
					},
				}
				return agent.NewContextWithInvocation(context.Background(), invocation)
			},
			filename: "test.txt",
			artifact: &artifact.Artifact{
				Data:     []byte("test data"),
				MimeType: "text/plain",
				Name:     "test.txt",
			},
			mockSetup: func(m *MockArtifactService) {
				m.On("SaveArtifact", mock.Anything, artifact.SessionInfo{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}, "test.txt", mock.Anything).Return(1, nil)
			},
			expectedVer: 1,
		},
		{
			name: "nil artifact service",
			setupContext: func() context.Context {
				invocation := &agent.Invocation{
					ArtifactService: nil,
					Session: &session.Session{
						AppName: "test-app",
						UserID:  "test-user",
						ID:      "test-session",
					},
				}
				return agent.NewContextWithInvocation(context.Background(), invocation)
			},
			filename:      "test.txt",
			artifact:      &artifact.Artifact{},
			expectedError: "artifact service is nil in invocation",
		},
		{
			name: "nil session",
			setupContext: func() context.Context {
				mockService := &MockArtifactService{}
				invocation := &agent.Invocation{
					ArtifactService: mockService,
					Session:         nil,
				}
				return agent.NewContextWithInvocation(context.Background(), invocation)
			},
			filename:      "test.txt",
			artifact:      &artifact.Artifact{},
			expectedError: "invocation exists but no session available",
		},
		{
			name: "missing session fields",
			setupContext: func() context.Context {
				mockService := &MockArtifactService{}
				invocation := &agent.Invocation{
					ArtifactService: mockService,
					Session: &session.Session{
						AppName: "", // Missing AppName
						UserID:  "test-user",
						ID:      "test-session",
					},
				}
				return agent.NewContextWithInvocation(context.Background(), invocation)
			},
			filename:      "test.txt",
			artifact:      &artifact.Artifact{},
			expectedError: "session exists but missing appName or userID or sessionID",
		},
		{
			name: "service error",
			setupContext: func() context.Context {
				mockService := &MockArtifactService{}
				invocation := &agent.Invocation{
					ArtifactService: mockService,
					Session: &session.Session{
						AppName: "test-app",
						UserID:  "test-user",
						ID:      "test-session",
					},
				}
				return agent.NewContextWithInvocation(context.Background(), invocation)
			},
			filename: "test.txt",
			artifact: &artifact.Artifact{},
			mockSetup: func(m *MockArtifactService) {
				m.On("SaveArtifact", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("save failed"))
			},
			expectedError: "save failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupContext()
			cc, err := NewCallbackContext(ctx)
			assert.NoError(t, err)

			// Setup mock if provided
			if tt.mockSetup != nil && cc.invocation.ArtifactService != nil {
				mockService := cc.invocation.ArtifactService.(*MockArtifactService)
				tt.mockSetup(mockService)
			}

			version, err := cc.SaveArtifact(tt.filename, tt.artifact)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedVer, version)
			}

			// Verify mock expectations
			if cc.invocation.ArtifactService != nil {
				mockService := cc.invocation.ArtifactService.(*MockArtifactService)
				mockService.AssertExpectations(t)
			}
		})
	}
}

func TestCallbackContext_LoadArtifact(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		version        *int
		expectedError  string
		mockSetup      func(*MockArtifactService)
		expectedResult *artifact.Artifact
	}{
		{
			name:     "successful load",
			filename: "test.txt",
			version:  nil,
			mockSetup: func(m *MockArtifactService) {
				expectedArtifact := &artifact.Artifact{
					Data:     []byte("test data"),
					MimeType: "text/plain",
					Name:     "test.txt",
				}
				m.On("LoadArtifact", mock.Anything, artifact.SessionInfo{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}, "test.txt", (*int)(nil)).Return(expectedArtifact, nil)
			},
			expectedResult: &artifact.Artifact{
				Data:     []byte("test data"),
				MimeType: "text/plain",
				Name:     "test.txt",
			},
		},
		{
			name:     "load with version",
			filename: "test.txt",
			version:  intPtr(2),
			mockSetup: func(m *MockArtifactService) {
				expectedArtifact := &artifact.Artifact{
					Data:     []byte("version 2 data"),
					MimeType: "text/plain",
					Name:     "test.txt",
				}
				m.On("LoadArtifact", mock.Anything, mock.Anything, "test.txt", intPtr(2)).Return(expectedArtifact, nil)
			},
			expectedResult: &artifact.Artifact{
				Data:     []byte("version 2 data"),
				MimeType: "text/plain",
				Name:     "test.txt",
			},
		},
		{
			name:     "service error",
			filename: "test.txt",
			version:  nil,
			mockSetup: func(m *MockArtifactService) {
				m.On("LoadArtifact", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*artifact.Artifact)(nil), errors.New("load failed"))
			},
			expectedError: "load failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &MockArtifactService{}
			invocation := &agent.Invocation{
				ArtifactService: mockService,
				Session: &session.Session{
					AppName: "test-app",
					UserID:  "test-user",
					ID:      "test-session",
				},
			}
			ctx := agent.NewContextWithInvocation(context.Background(), invocation)
			cc, err := NewCallbackContext(ctx)
			assert.NoError(t, err)

			tt.mockSetup(mockService)

			result, err := cc.LoadArtifact(tt.filename, tt.version)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}

			mockService.AssertExpectations(t)
		})
	}
}

func TestCallbackContext_ListArtifacts(t *testing.T) {
	tests := []struct {
		name           string
		expectedError  string
		mockSetup      func(*MockArtifactService)
		expectedResult []string
	}{
		{
			name: "successful list",
			mockSetup: func(m *MockArtifactService) {
				m.On("ListArtifactKeys", mock.Anything, artifact.SessionInfo{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}).Return([]string{"file1.txt", "file2.jpg"}, nil)
			},
			expectedResult: []string{"file1.txt", "file2.jpg"},
		},
		{
			name: "service error",
			mockSetup: func(m *MockArtifactService) {
				m.On("ListArtifactKeys", mock.Anything, mock.Anything).Return(([]string)(nil), errors.New("list failed"))
			},
			expectedError: "list failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &MockArtifactService{}
			invocation := &agent.Invocation{
				ArtifactService: mockService,
				Session: &session.Session{
					AppName: "test-app",
					UserID:  "test-user",
					ID:      "test-session",
				},
			}
			ctx := agent.NewContextWithInvocation(context.Background(), invocation)
			cc, err := NewCallbackContext(ctx)
			assert.NoError(t, err)

			tt.mockSetup(mockService)

			result, err := cc.ListArtifacts()

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}

			mockService.AssertExpectations(t)
		})
	}
}

func TestCallbackContext_DeleteArtifact(t *testing.T) {
	tests := []struct {
		name          string
		filename      string
		expectedError string
		mockSetup     func(*MockArtifactService)
	}{
		{
			name:     "successful delete",
			filename: "test.txt",
			mockSetup: func(m *MockArtifactService) {
				m.On("DeleteArtifact", mock.Anything, artifact.SessionInfo{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}, "test.txt").Return(nil)
			},
		},
		{
			name:     "service error",
			filename: "test.txt",
			mockSetup: func(m *MockArtifactService) {
				m.On("DeleteArtifact", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("delete failed"))
			},
			expectedError: "delete failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &MockArtifactService{}
			invocation := &agent.Invocation{
				ArtifactService: mockService,
				Session: &session.Session{
					AppName: "test-app",
					UserID:  "test-user",
					ID:      "test-session",
				},
			}
			ctx := agent.NewContextWithInvocation(context.Background(), invocation)
			cc, err := NewCallbackContext(ctx)
			assert.NoError(t, err)

			tt.mockSetup(mockService)

			err = cc.DeleteArtifact(tt.filename)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}

			mockService.AssertExpectations(t)
		})
	}
}

func TestCallbackContext_ListArtifactVersions(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		expectedError  string
		mockSetup      func(*MockArtifactService)
		expectedResult []int
	}{
		{
			name:     "successful list versions",
			filename: "test.txt",
			mockSetup: func(m *MockArtifactService) {
				m.On("ListVersions", mock.Anything, artifact.SessionInfo{
					AppName:   "test-app",
					UserID:    "test-user",
					SessionID: "test-session",
				}, "test.txt").Return([]int{0, 1, 2}, nil)
			},
			expectedResult: []int{0, 1, 2},
		},
		{
			name:     "service error",
			filename: "test.txt",
			mockSetup: func(m *MockArtifactService) {
				m.On("ListVersions", mock.Anything, mock.Anything, mock.Anything).Return(([]int)(nil), errors.New("list versions failed"))
			},
			expectedError: "list versions failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &MockArtifactService{}
			invocation := &agent.Invocation{
				ArtifactService: mockService,
				Session: &session.Session{
					AppName: "test-app",
					UserID:  "test-user",
					ID:      "test-session",
				},
			}
			ctx := agent.NewContextWithInvocation(context.Background(), invocation)
			cc, err := NewCallbackContext(ctx)
			assert.NoError(t, err)

			tt.mockSetup(mockService)

			result, err := cc.ListArtifactVersions(tt.filename)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}

			mockService.AssertExpectations(t)
		})
	}
}

// Helper function to create int pointer
func intPtr(i int) *int {
	return &i
}
