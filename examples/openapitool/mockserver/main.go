//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main gives an example of mock server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()

	// Load OpenAPI specification
	loader := &openapi3.Loader{Context: ctx, IsExternalRefsAllowed: true}
	doc, err := loader.LoadFromFile("../petstore3.yaml")
	if err != nil {
		log.Fatalf("Failed to load OpenAPI spec: %v", err)
	}

	// Validate the specification
	if err := doc.Validate(ctx); err != nil {
		log.Fatalf("OpenAPI spec validation failed: %v", err)
	}

	// Create HTTP server
	handler := &MockServerHandler{doc: doc}
	handler.setupRoutes()

	log.Println("Starting OpenAPI Mock Server on :8080")
	log.Println("Available endpoints:")
	for _, path := range doc.Paths.InMatchingOrder() {
		pathItem := doc.Paths.Find(path)
		if pathItem != nil {
			if pathItem.Get != nil {
				log.Printf("  GET    %s", path)
			}
			if pathItem.Post != nil {
				log.Printf("  POST   %s", path)
			}
			if pathItem.Put != nil {
				log.Printf("  PUT    %s", path)
			}
			if pathItem.Delete != nil {
				log.Printf("  DELETE %s", path)
			}
		}
	}

	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

type MockServerHandler struct {
	doc *openapi3.T
	mux *http.ServeMux
}

// responseCapturer captures the response body for logging
type responseCapturer struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (rc *responseCapturer) Write(b []byte) (int, error) {
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

func (rc *responseCapturer) WriteHeader(statusCode int) {
	rc.statusCode = statusCode
	rc.ResponseWriter.WriteHeader(statusCode)
}

func (h *MockServerHandler) setupRoutes() {
	h.mux = http.NewServeMux()

	const pathPrefix = "/api/v3"
	// Setup routes for all paths in the OpenAPI spec
	for path, pathItem := range h.doc.Paths.Map() {
		absPath := pathPrefix + path
		if pathItem.Get != nil {
			h.mux.HandleFunc("GET "+absPath, h.createHandler(absPath, pathItem.Get))
		}
		if pathItem.Post != nil {
			h.mux.HandleFunc("POST "+absPath, h.createHandler(absPath, pathItem.Post))
		}
		if pathItem.Put != nil {
			h.mux.HandleFunc("PUT "+absPath, h.createHandler(absPath, pathItem.Put))
		}
		if pathItem.Delete != nil {
			h.mux.HandleFunc("DELETE "+absPath, h.createHandler(absPath, pathItem.Delete))
		}
	}

	// Add a health check endpoint
	h.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})
}

func (h *MockServerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Create response capturer to log response body
	rc := &responseCapturer{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	// Handle the request
	h.mux.ServeHTTP(rc, r)

	// Log response body if it exists
	if rc.body.Len() > 0 {
		log.Printf("Response Status: %d", rc.statusCode)
		log.Printf("Response Body: %s", rc.body.String())
	}
}

func (h *MockServerHandler) createHandler(path string, operation *openapi3.Operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Handling %s %s", r.Method, r.URL.Path)

		// Read and log request body
		if r.Body != nil {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("Error reading request body: %v", err)
			} else {
				log.Printf("Request Body: %s", string(bodyBytes))
				// Restore the body so it can be read again by other handlers
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}

		// Set content type based on Accept header or default to JSON
		contentType := "application/json"
		if accept := r.Header.Get("Accept"); accept != "" {
			if strings.Contains(accept, "application/xml") {
				contentType = "application/xml"
			} else if strings.Contains(accept, "application/json") {
				contentType = "application/json"
			}
		}
		w.Header().Set("Content-Type", contentType)

		// Generate mock response based on the operation's responses
		responses := operation.Responses
		if responses == nil {
			h.sendError(w, "No responses defined", http.StatusInternalServerError)
			return
		}

		// Try to get a 200 response first, then fallback to other success codes
		var response *openapi3.Response
		for _, code := range []string{"200", "201", "204"} {
			if resp, exists := responses.Map()[code]; exists && resp.Value != nil {
				response = resp.Value
				break
			}
		}

		if response == nil {
			// If no success response found, use the first available response
			for _, resp := range responses.Map() {
				if resp.Value != nil {
					response = resp.Value
					break
				}
			}
		}

		if response == nil {
			h.sendError(w, "No valid response found", http.StatusInternalServerError)
			return
		}

		// Generate mock data based on response schema
		mockData := h.generateMockData(response, path, operation.OperationID)

		// Set appropriate status code
		statusCode := http.StatusOK

		w.WriteHeader(statusCode)

		if contentType == "application/xml" {
			// Simple XML response (for demonstration)
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<response>
  <status>success</status>
  <message>Mock response for %s</message>
  <data>%v</data>
</response>`, operation.OperationID, mockData)
		} else {
			// JSON response
			json.NewEncoder(w).Encode(mockData)
		}
	}
}

func (h *MockServerHandler) generateMockData(response *openapi3.Response, path, operationID string) any {
	// Simple mock data generation based on operation ID and path
	switch operationID {
	case "addPet":
		return map[string]any{
			"id":   123,
			"name": "Mock Pet",
			"category": map[string]any{
				"id":   1,
				"name": "Dogs",
			},
			"photoUrls": []string{"http://example.com/photo1.jpg"},
			"tags": []map[string]any{
				{"id": 1, "name": "friendly"},
			},
			"status": "available",
		}
	case "getPetById":
		return map[string]any{
			"id":   123,
			"name": "Mock Pet",
			"category": map[string]any{
				"id":   1,
				"name": "Dogs",
			},
			"photoUrls": []string{"http://example.com/photo1.jpg"},
			"tags": []map[string]any{
				{"id": 1, "name": "friendly"},
			},
			"status": "available",
		}
	case "findPetsByStatus":
		return []map[string]any{
			{
				"id":     1,
				"name":   "Pet 1",
				"status": "available",
			},
			{
				"id":     2,
				"name":   "Pet 2",
				"status": "pending",
			},
		}
	case "getInventory":
		return map[string]int{
			"available": 5,
			"pending":   2,
			"sold":      3,
		}
	case "getOrderById":
		return map[string]any{
			"id":       456,
			"petId":    123,
			"quantity": 1,
			"shipDate": "2023-01-01T12:00:00Z",
			"status":   "approved",
			"complete": false,
		}
	case "getUserByName":
		return map[string]any{
			"id":         789,
			"username":   "mockuser",
			"firstName":  "Mock",
			"lastName":   "User",
			"email":      "mock@example.com",
			"userStatus": 1,
		}
	default:
		// Generic response for other operations
		return map[string]any{
			"id":      uuid.New().String(),
			"message": fmt.Sprintf("Mock response for %s", operationID),
			"path":    path,
			"success": true,
		}
	}
}

func (h *MockServerHandler) sendError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]any{
		"error":   message,
		"code":    statusCode,
		"success": false,
	})
}
