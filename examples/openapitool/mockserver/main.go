package main

import (
	"context"
	"encoding/json"
	"fmt"
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

	if err := http.ListenAndServe(":80", handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

type MockServerHandler struct {
	doc *openapi3.T
	mux *http.ServeMux
}

func (h *MockServerHandler) setupRoutes() {
	h.mux = http.NewServeMux()

	// Setup routes for all paths in the OpenAPI spec
	for path, pathItem := range h.doc.Paths.Map() {
		if pathItem.Get != nil {
			h.mux.HandleFunc("GET "+path, h.createHandler(path, pathItem.Get))
		}
		if pathItem.Post != nil {
			h.mux.HandleFunc("POST "+path, h.createHandler(path, pathItem.Post))
		}
		if pathItem.Put != nil {
			h.mux.HandleFunc("PUT "+path, h.createHandler(path, pathItem.Put))
		}
		if pathItem.Delete != nil {
			h.mux.HandleFunc("DELETE "+path, h.createHandler(path, pathItem.Delete))
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

	// Handle the request
	h.mux.ServeHTTP(w, r)
}

func (h *MockServerHandler) createHandler(path string, operation *openapi3.Operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Handling %s %s", r.Method, r.URL.Path)

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
		// if responseCode, err := strconv.Atoi(strings.Split(response.Description, " ")[0]); err == nil {
		// 	statusCode = responseCode
		// }

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

func (h *MockServerHandler) generateMockData(response *openapi3.Response, path, operationID string) interface{} {
	// Simple mock data generation based on operation ID and path
	switch operationID {
	case "getPetById":
		return map[string]interface{}{
			"id":   123,
			"name": "Mock Pet",
			"category": map[string]interface{}{
				"id":   1,
				"name": "Dogs",
			},
			"photoUrls": []string{"http://example.com/photo1.jpg"},
			"tags": []map[string]interface{}{
				{"id": 1, "name": "friendly"},
			},
			"status": "available",
		}
	case "findPetsByStatus":
		return []map[string]interface{}{
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
		return map[string]interface{}{
			"id":       456,
			"petId":    123,
			"quantity": 1,
			"shipDate": "2023-01-01T12:00:00Z",
			"status":   "approved",
			"complete": false,
		}
	case "getUserByName":
		return map[string]interface{}{
			"id":         789,
			"username":   "mockuser",
			"firstName":  "Mock",
			"lastName":   "User",
			"email":      "mock@example.com",
			"userStatus": 1,
		}
	default:
		// Generic response for other operations
		return map[string]interface{}{
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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   message,
		"code":    statusCode,
		"success": false,
	})
}
