package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestDefaultWebOptions(t *testing.T) {
	opts := DefaultWebOptions()

	// Verify default values
	if opts.CookieName != "tRPC_Session_ID" {
		t.Errorf("Expected CookieName to be 'tRPC_Session_ID', got %s", opts.CookieName)
	}
	if opts.CookiePath != "/" {
		t.Errorf("Expected CookiePath to be '/', got %s", opts.CookiePath)
	}
	if opts.CookieMaxAge != 86400*30 {
		t.Errorf("Expected CookieMaxAge to be %d, got %d", 86400*30, opts.CookieMaxAge)
	}
	if !opts.CookieHTTPOnly {
		t.Errorf("Expected CookieHTTPOnly to be true")
	}
	if opts.HeaderName != "X-Session-ID" {
		t.Errorf("Expected HeaderName to be 'X-Session-ID', got %s", opts.HeaderName)
	}
}

func TestMiddleware_Cookie(t *testing.T) {
	// Create a memory manager for testing
	manager := NewMemoryManager()
	middleware := Middleware(manager)

	// Create a test handler that accesses session
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := GetSession(r.Context())
		if !ok {
			t.Error("Expected session in context, not found")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Add a message to mark the session as modified
		session.SetMetadata("_modified", true)

		// Write session ID in response
		w.Write([]byte(session.ID()))
	})

	// Create a request without existing session
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()

	// Call the middleware with the test handler
	middleware(testHandler).ServeHTTP(w, req)

	// Check the response
	resp := w.Result()
	defer resp.Body.Close()

	// Verify that we get a session cookie
	cookies := resp.Cookies()
	var sessionID string
	for _, cookie := range cookies {
		if cookie.Name == "tRPC_Session_ID" {
			sessionID = cookie.Value
			break
		}
	}

	if sessionID == "" {
		t.Error("Expected session cookie, not found")
	}
}

func TestMiddleware_ExistingCookie(t *testing.T) {
	// Create a memory manager for testing
	manager := NewMemoryManager()

	// Create a session first
	ctx := context.Background()
	session, err := manager.Create(ctx)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	existingSessionID := session.ID()

	middleware := Middleware(manager)

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := GetSession(r.Context())
		if !ok {
			t.Error("Expected session in context, not found")
			return
		}

		if session.ID() != existingSessionID {
			t.Errorf("Expected session ID %s, got %s", existingSessionID, session.ID())
		}

		w.Write([]byte(session.ID()))
	})

	// Create a request with existing session cookie
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	req.AddCookie(&http.Cookie{
		Name:  "tRPC_Session_ID",
		Value: existingSessionID,
	})

	w := httptest.NewRecorder()

	// Call the middleware with the test handler
	middleware(testHandler).ServeHTTP(w, req)

	// Check the response
	resp := w.Result()
	defer resp.Body.Close()

	// The handler should use the existing session
	cookies := resp.Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "tRPC_Session_ID" && cookie.Value == existingSessionID {
			found = true
			break
		}
	}

	// We shouldn't have a new cookie since we're using the existing session
	if found {
		t.Error("Expected no new session cookie for existing session")
	}
}

func TestMiddleware_Header(t *testing.T) {
	// Create a memory manager for testing
	manager := NewMemoryManager()

	// Custom options to use headers instead of cookies
	opts := WebOptions{
		HeaderName: "X-Session-ID",
		UseHeaders: true,
	}

	middleware := Middleware(manager, opts)

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := GetSession(r.Context())
		if !ok {
			t.Error("Expected session in context, not found")
			return
		}

		w.Write([]byte(session.ID()))
	})

	// Create a request without existing session
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()

	// Call the middleware with the test handler
	middleware(testHandler).ServeHTTP(w, req)

	// Check the response
	resp := w.Result()
	defer resp.Body.Close()

	// Verify that we get a session header
	sessionID := resp.Header.Get("X-Session-ID")
	if sessionID == "" {
		t.Error("Expected session header, not found")
	}
}

func TestMiddleware_ExistingHeader(t *testing.T) {
	// Create a memory manager for testing
	manager := NewMemoryManager()

	// Create a session first
	ctx := context.Background()
	session, err := manager.Create(ctx)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	existingSessionID := session.ID()

	// Custom options to use headers instead of cookies
	opts := WebOptions{
		HeaderName: "X-Session-ID",
		UseHeaders: true,
	}

	middleware := Middleware(manager, opts)

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := GetSession(r.Context())
		if !ok {
			t.Error("Expected session in context, not found")
			return
		}

		if session.ID() != existingSessionID {
			t.Errorf("Expected session ID %s, got %s", existingSessionID, session.ID())
		}

		w.Write([]byte(session.ID()))
	})

	// Create a request with existing session header
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	req.Header.Set("X-Session-ID", existingSessionID)

	w := httptest.NewRecorder()

	// Call the middleware with the test handler
	middleware(testHandler).ServeHTTP(w, req)
}

func TestMiddleware_SessionModification(t *testing.T) {
	// Create test FileStore
	tmpDir := t.TempDir()
	fileStore, err := NewFileStore(WithDirectory(tmpDir))
	if err != nil {
		t.Fatalf("Failed to create file store: %v", err)
	}

	// Create manager with the file store
	manager, err := NewPersistentManager(fileStore)
	if err != nil {
		t.Fatalf("Failed to create persistent manager: %v", err)
	}

	middleware := Middleware(manager)

	// Create a test handler that modifies the session
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := GetSession(r.Context())
		if !ok {
			t.Error("Expected session in context, not found")
			return
		}

		// Modify session by setting metadata
		session.SetMetadata("test_key", "test_value")

		w.Write([]byte(session.ID()))
	})

	// Create a request
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()

	// Call the middleware with the test handler
	middleware(testHandler).ServeHTTP(w, req)

	// Get the session ID from the response cookie
	resp := w.Result()
	defer resp.Body.Close()

	var sessionID string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "tRPC_Session_ID" {
			sessionID = cookie.Value
			break
		}
	}

	if sessionID == "" {
		t.Fatal("Session ID not found in response")
	}

	// Verify the session was saved with the metadata
	session, err := manager.Get(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	value, ok := session.GetMetadata("test_key")
	if !ok {
		t.Error("Session metadata not saved")
	}
	if value != "test_value" {
		t.Errorf("Expected metadata value 'test_value', got '%v'", value)
	}
}

func TestGetSession(t *testing.T) {
	// Create a context with session
	ctx := context.Background()
	sessionMock := memory.NewBaseSession("test-id", nil)
	ctx = context.WithValue(ctx, sessionKey, sessionMock)

	// Get session from context
	session, ok := GetSession(ctx)
	if !ok {
		t.Error("Expected to get session from context")
	}
	if session.ID() != "test-id" {
		t.Errorf("Expected session ID 'test-id', got '%s'", session.ID())
	}

	// Test with context that doesn't have session
	_, ok = GetSession(context.Background())
	if ok {
		t.Error("Expected not to get session from empty context")
	}
}

func TestGetManager(t *testing.T) {
	// Create a context with manager
	ctx := context.Background()
	managerMock := NewMemoryManager()
	ctx = context.WithValue(ctx, managerKey, managerMock)

	// Get manager from context
	manager, ok := GetManager(ctx)
	if !ok {
		t.Error("Expected to get manager from context")
	}
	if manager != managerMock {
		t.Error("Retrieved manager does not match the one stored in context")
	}

	// Test with context that doesn't have manager
	_, ok = GetManager(context.Background())
	if ok {
		t.Error("Expected not to get manager from empty context")
	}
}

func TestMustGetSession(t *testing.T) {
	// Create a context with session
	ctx := context.Background()
	sessionMock := memory.NewBaseSession("test-id", nil)
	ctx = context.WithValue(ctx, sessionKey, sessionMock)

	// Test successful retrieval
	session := MustGetSession(ctx)
	if session.ID() != "test-id" {
		t.Errorf("Expected session ID 'test-id', got '%s'", session.ID())
	}

	// Test panic with context that doesn't have session
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected MustGetSession to panic with empty context")
		}
	}()
	MustGetSession(context.Background())
}

func TestMustGetManager(t *testing.T) {
	// Create a context with manager
	ctx := context.Background()
	managerMock := NewMemoryManager()
	ctx = context.WithValue(ctx, managerKey, managerMock)

	// Test successful retrieval
	manager := MustGetManager(ctx)
	if manager != managerMock {
		t.Error("Retrieved manager does not match the one stored in context")
	}

	// Test panic with context that doesn't have manager
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected MustGetManager to panic with empty context")
		}
	}()
	MustGetManager(context.Background())
}

// Mock implementation of StoreProvider that tracks save calls
type webMockStore struct {
	savedSessions map[string]memory.Session
	saveCount     int
}

func newWebMockStore() *webMockStore {
	return &webMockStore{
		savedSessions: make(map[string]memory.Session),
	}
}

func (s *webMockStore) Save(ctx context.Context, session memory.Session) error {
	s.savedSessions[session.ID()] = session
	s.saveCount++
	return nil
}

func (s *webMockStore) Load(ctx context.Context, id string) (memory.Session, error) {
	session, exists := s.savedSessions[id]
	if !exists {
		return nil, ErrSessionNotFound
	}
	return session, nil
}

func (s *webMockStore) Delete(ctx context.Context, id string) error {
	delete(s.savedSessions, id)
	return nil
}

func (s *webMockStore) ListIDs(ctx context.Context) ([]string, error) {
	var ids []string
	for id := range s.savedSessions {
		ids = append(ids, id)
	}
	return ids, nil
}

// Test that Middleware correctly saves modified sessions
func TestMiddleware_SaveModifiedSession(t *testing.T) {
	// Create mock store and manager
	store := newWebMockStore()
	manager := &webMockManager{
		store:    store,
		sessions: make(map[string]memory.Session),
	}

	middleware := Middleware(manager)

	// Handler that modifies the session
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := GetSession(r.Context())
		// Simulate activity that would update LastUpdated
		time.Sleep(10 * time.Millisecond)
		session.SetMetadata("test_key", "modified_value")
	})

	// Create a request
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()

	// Call the middleware
	middleware(testHandler).ServeHTTP(w, req)

	// Verify the session was saved
	if store.saveCount == 0 {
		t.Error("Expected the modified session to be saved")
	}
}

// Mock implementation of Manager
type webMockManager struct {
	store    *webMockStore
	sessions map[string]memory.Session
}

func (m *webMockManager) Get(ctx context.Context, id string) (memory.Session, error) {
	if id == "" {
		return m.Create(ctx)
	}

	if session, exists := m.sessions[id]; exists {
		return session, nil
	}

	session := memory.NewBaseSession(id, nil)
	m.sessions[id] = session
	return session, nil
}

func (m *webMockManager) Create(ctx context.Context, options ...Option) (memory.Session, error) {
	id := "test-session-" + time.Now().Format("150405")
	session := memory.NewBaseSession(id, nil)
	m.sessions[id] = session
	return session, nil
}

func (m *webMockManager) Delete(ctx context.Context, id string) error {
	delete(m.sessions, id)
	return nil
}

func (m *webMockManager) ListIDs(ctx context.Context) ([]string, error) {
	var ids []string
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids, nil
}

// Implement StoreProvider interface
func (m *webMockManager) Save(ctx context.Context, session memory.Session) error {
	return m.store.Save(ctx, session)
}

func (m *webMockManager) Load(ctx context.Context, id string) (memory.Session, error) {
	return m.store.Load(ctx, id)
}

// Implement CleanExpired method to fulfill the Manager interface
func (m *webMockManager) CleanExpired(ctx context.Context) (int, error) {
	// Since this is a mock, just return 0 expired sessions
	return 0, nil
}
