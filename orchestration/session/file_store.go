package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// FileStoreOptions configures the FileStore.
type FileStoreOptions struct {
	// Directory is the path where session files will be stored.
	Directory string

	// FileMode sets the permissions for created files.
	FileMode os.FileMode
}

// FileStoreOption is a function that configures a FileStore.
type FileStoreOption func(*FileStoreOptions)

// WithDirectory sets the directory for session storage.
func WithDirectory(dir string) FileStoreOption {
	return func(o *FileStoreOptions) {
		o.Directory = dir
	}
}

// WithFileMode sets the file permissions for session files.
func WithFileMode(mode os.FileMode) FileStoreOption {
	return func(o *FileStoreOptions) {
		o.FileMode = mode
	}
}

// sessionData represents the serializable data for a session.
type sessionData struct {
	ID          string                 `json:"id"`
	Messages    []*message.Message     `json:"messages"`
	Metadata    map[string]interface{} `json:"metadata"`
	LastUpdated time.Time              `json:"last_updated"`
}

// FileStore is a file-based implementation of StoreProvider.
type FileStore struct {
	options FileStoreOptions
	mutex   sync.RWMutex
}

// NewFileStore creates a new FileStore with the given options.
func NewFileStore(options ...FileStoreOption) (*FileStore, error) {
	// Default options
	opts := FileStoreOptions{
		Directory: os.TempDir(),
		FileMode:  0644,
	}

	// Apply provided options
	for _, option := range options {
		option(&opts)
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(opts.Directory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	return &FileStore{
		options: opts,
	}, nil
}

// Save persists a session to a file.
func (f *FileStore) Save(ctx context.Context, session memory.Session) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	// Get session data
	messages, err := session.GetMessages(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}

	// Create serializable session data
	data := &sessionData{
		ID:          session.ID(),
		Messages:    messages,
		LastUpdated: session.LastUpdated(),
		Metadata:    make(map[string]interface{}),
	}

	// Extract metadata into the map
	metadataKeys := []string{"expiration"}
	for _, key := range metadataKeys {
		if value, exists := session.GetMetadata(key); exists {
			data.Metadata[key] = value
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}

	// Write to file
	filename := filepath.Join(f.options.Directory, fmt.Sprintf("session_%s.json", session.ID()))
	if err := os.WriteFile(filename, jsonData, f.options.FileMode); err != nil {
		return fmt.Errorf("%w: %v", ErrStorageFailed, err)
	}

	return nil
}

// Load retrieves a session from a file.
func (f *FileStore) Load(ctx context.Context, id string) (memory.Session, error) {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	// Read file
	filename := filepath.Join(f.options.Directory, fmt.Sprintf("session_%s.json", id))
	jsonData, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return nil, ErrSessionNotFound
	} else if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRetrievalFailed, err)
	}

	// Unmarshal JSON
	var data sessionData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRetrievalFailed, err)
	}

	// Create a new session
	session := memory.NewBaseSession(data.ID, nil)

	// Restore messages
	for _, msg := range data.Messages {
		if err := session.AddMessage(ctx, msg); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRetrievalFailed, err)
		}
	}

	// Restore metadata
	for key, value := range data.Metadata {
		session.SetMetadata(key, value)
	}

	return session, nil
}

// Delete removes a session file.
func (f *FileStore) Delete(ctx context.Context, id string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	filename := filepath.Join(f.options.Directory, fmt.Sprintf("session_%s.json", id))
	err := os.Remove(filename)
	if os.IsNotExist(err) {
		// Not an error if the file doesn't exist
		return nil
	}
	return err
}

// ListIDs returns all session IDs from the file store.
func (f *FileStore) ListIDs(ctx context.Context) ([]string, error) {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	files, err := os.ReadDir(f.options.Directory)
	if err != nil {
		return nil, err
	}

	prefix := "session_"
	suffix := ".json"
	ids := make([]string, 0, len(files))

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		if len(name) > len(prefix)+len(suffix) &&
			name[:len(prefix)] == prefix &&
			name[len(name)-len(suffix):] == suffix {
			id := name[len(prefix) : len(name)-len(suffix)]
			ids = append(ids, id)
		}
	}

	return ids, nil
}
