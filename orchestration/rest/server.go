// Package rest provides a RESTful API for the agent framework.
package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
)

// Server provides REST API endpoints for the agent framework.
type Server struct {
	runner runner.Runner
	router *http.ServeMux
}

// NewServer creates a new API server with the given runner.
func NewServer(r runner.Runner) *Server {
	server := &Server{
		runner: r,
		router: http.NewServeMux(),
	}

	// Register handlers
	server.router.HandleFunc("GET /sessions", server.ListSessions)
	server.router.HandleFunc("POST /sessions", server.CreateSession)
	server.router.HandleFunc("GET /sessions/{id}", server.GetSession)
	server.router.HandleFunc("DELETE /sessions/{id}", server.DeleteSession)
	server.router.HandleFunc("POST /sessions/{id}/run", server.RunWithSession)
	server.router.HandleFunc("POST /sessions/{id}/run_stream", server.RunStreamWithSession)

	return server
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return s.router
}

// ListSessions lists all sessions.
func (s *Server) ListSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessionIDs, err := s.runner.ListSessions(ctx)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to list sessions", err)
		return
	}

	response := map[string]interface{}{
		"sessions": sessionIDs,
	}

	respondWithJSON(w, http.StatusOK, response)
}

// CreateSession creates a new session.
func (s *Server) CreateSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessionID, err := s.runner.CreateSession(ctx)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create session", err)
		return
	}

	response := map[string]interface{}{
		"id": sessionID,
	}

	respondWithJSON(w, http.StatusCreated, response)
}

// GetSession gets information about a session.
func (s *Server) GetSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	session, err := s.runner.GetSession(ctx, sessionID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Session %s not found", sessionID), err)
		return
	}

	// Get messages from the session
	messages, err := session.GetMessages(ctx)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get session messages", err)
		return
	}

	// Format response
	response := map[string]interface{}{
		"id":         sessionID,
		"messages":   messages,
		"updated_at": session.LastUpdated(),
	}

	respondWithJSON(w, http.StatusOK, response)
}

// DeleteSession deletes a session.
func (s *Server) DeleteSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	err := s.runner.DeleteSession(ctx, sessionID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, fmt.Sprintf("Session %s not found", sessionID), err)
		return
	}

	// Return simple success response
	response := map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Session %s deleted", sessionID),
	}

	respondWithJSON(w, http.StatusOK, response)
}

// RunRequest represents a request to run an agent.
type RunRequest struct {
	Message string `json:"message"`
}

// RunWithSession executes the agent in a session.
func (s *Server) RunWithSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	// Parse request body
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Create user message
	input := message.NewUserMessage(req.Message)

	// Execute the agent in the session
	response, err := s.runner.RunWithSession(ctx, sessionID, *input)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to run agent", err)
		return
	}

	// Return the agent's response
	result := map[string]interface{}{
		"message": response,
	}

	respondWithJSON(w, http.StatusOK, result)
}

// RunStreamWithSession executes the agent in a session with streaming.
func (s *Server) RunStreamWithSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	// Parse request body
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Create user message
	input := message.NewUserMessage(req.Message)

	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Get the flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondWithError(w, http.StatusInternalServerError, "Streaming not supported", nil)
		return
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Create a done channel for client disconnection
	done := make(chan struct{})

	// Handle client disconnection
	go func() {
		<-r.Context().Done()
		close(done)
	}()

	// Execute the agent in the session with streaming
	eventCh, err := s.runner.RunAsyncWithSession(ctx, sessionID, *input)
	if err != nil {
		sseEvent := map[string]interface{}{
			"type": "error",
			"data": map[string]string{
				"message": fmt.Sprintf("Failed to run agent: %v", err),
			},
		}
		sendSSE(w, flusher, sseEvent)
		return
	}

	// Stream events to the client
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				// Channel closed, done streaming
				sendSSE(w, flusher, map[string]interface{}{
					"type": "done",
				})
				return
			}

			// Convert event to SSE format
			data := formatEventForSSE(evt)
			sendSSE(w, flusher, data)

		case <-done:
			// Client disconnected
			return

		case <-ctx.Done():
			// Context timeout
			sendSSE(w, flusher, map[string]interface{}{
				"type": "error",
				"data": map[string]string{
					"message": "Request timed out",
				},
			})
			return
		}
	}
}

// formatEventForSSE formats an event for SSE transmission.
func formatEventForSSE(evt *event.Event) map[string]interface{} {
	result := map[string]interface{}{
		"id":         evt.ID,
		"type":       string(evt.Type),
		"created_at": evt.CreatedAt,
	}

	// Add data based on event type
	switch evt.Type {
	case event.TypeStreamChunk:
		content, _ := evt.GetMetadata("content")
		sequence, _ := evt.GetMetadata("sequence")
		result["data"] = map[string]interface{}{
			"content":  content,
			"sequence": sequence,
		}

	case event.TypeStreamToolCall:
		result["data"] = evt.Data

	case event.TypeStreamToolResult:
		result["data"] = evt.Data

	case event.TypeStreamEnd:
		completeText, _ := evt.GetMetadata("complete_text")
		if completeText == nil {
			if data, ok := evt.Data.(map[string]interface{}); ok {
				completeText = data["complete_text"]
			}
		}
		result["data"] = map[string]interface{}{
			"complete_text": completeText,
		}

	case event.TypeError:
		errMsg, _ := evt.GetMetadata("error")
		errCode, _ := evt.GetMetadata("error_code")
		result["data"] = map[string]interface{}{
			"error":      errMsg,
			"error_code": errCode,
		}

	default:
		result["data"] = evt.Data
	}

	// Add metadata
	if len(evt.Metadata) > 0 {
		result["metadata"] = evt.Metadata
	}

	return result
}

// sendSSE sends a Server-Sent Event.
func sendSSE(w http.ResponseWriter, flusher http.Flusher, data map[string]interface{}) {
	// Convert data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		// Log error, but continue
		fmt.Printf("Error marshaling SSE data: %v\n", err)
		return
	}

	// Format as SSE
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()
}

// respondWithJSON sends a JSON response.
func respondWithJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Log the error but don't try to change the response
		fmt.Printf("Error encoding JSON response: %v\n", err)
	}
}

// respondWithError sends an error response.
func respondWithError(w http.ResponseWriter, status int, message string, err error) {
	response := map[string]string{
		"error": message,
	}

	if err != nil {
		response["details"] = err.Error()
	}

	respondWithJSON(w, status, response)
}
