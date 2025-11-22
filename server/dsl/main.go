// Package main implements the DSL platform HTTP server.
//
// This server provides REST APIs for:
// - Component/Model/Tool registry queries
// - Workflow CRUD operations
// - DSL validation and compilation
// - Workflow execution (streaming and non-streaming)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Register builtin components
)

var (
	port = flag.Int("port", 8090, "HTTP server port")
	host = flag.String("host", "0.0.0.0", "HTTP server host")
)

func main() {
	flag.Parse()

	// Create server
	server := NewServer()

	// Setup HTTP routes
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// Create HTTP server
	addr := fmt.Sprintf("%s:%d", *host, *port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      corsMiddleware(loggingMiddleware(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("🚀 DSL Platform Server starting on %s", addr)
		log.Printf("📖 API documentation: http://%s/api/docs", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("✅ Server stopped")
}

// Server holds the DSL platform server state.
type Server struct {
	componentRegistry *registry.Registry
	modelRegistry     *registry.ModelRegistry
	toolRegistry      *registry.ToolRegistry
	toolSetRegistry   *registry.ToolSetRegistry
	compiler          *dsl.Compiler

	// TODO: Add workflow storage (database/in-memory)
	// workflowStore WorkflowStore

	// TODO: Add execution manager
	// executionManager *ExecutionManager
}

// NewServer creates a new DSL platform server.
func NewServer() *Server {
	// Use default registries (with built-in components/tools/toolsets auto-registered)
	componentRegistry := registry.DefaultRegistry
	modelRegistry := registry.NewModelRegistry()
	toolRegistry := registry.DefaultToolRegistry       // Use DefaultToolRegistry with built-in tools
	toolSetRegistry := registry.DefaultToolSetRegistry // Use DefaultToolSetRegistry with built-in toolsets

	// Create compiler
	compiler := dsl.NewCompiler(componentRegistry).
		WithModelRegistry(modelRegistry).
		WithToolRegistry(toolRegistry).
		WithToolSetRegistry(toolSetRegistry)

	return &Server{
		componentRegistry: componentRegistry,
		modelRegistry:     modelRegistry,
		toolRegistry:      toolRegistry,
		toolSetRegistry:   toolSetRegistry,
		compiler:          compiler,
	}
}

// RegisterRoutes registers all HTTP routes.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	log.Println("📝 Registering routes...")

	// API documentation
	mux.HandleFunc("/api/docs", s.handleAPIDocs)

	// Component registry
	mux.HandleFunc("/api/v1/components", methodHandler("GET", s.handleListComponents))
	// Single component metadata by name. We use a trailing slash pattern so this
	// works consistently even on older Go versions without path variables.
	mux.HandleFunc("/api/v1/components/", methodHandler("GET", s.handleGetComponent))

	// Model registry
	mux.HandleFunc("/api/v1/models", methodHandler("GET", s.handleListModels))

	// Tool registry
	mux.HandleFunc("/api/v1/tools", methodHandler("GET", s.handleListTools))

	// ToolSet registry
	mux.HandleFunc("/api/v1/toolsets", methodHandler("GET", s.handleListToolSets))

	// Workflow CRUD
	mux.HandleFunc("/api/v1/workflows", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			s.handleListWorkflows(w, r)
		case "POST":
			s.handleCreateWorkflow(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/v1/workflows/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			s.handleGetWorkflow(w, r)
		case "PUT":
			s.handleUpdateWorkflow(w, r)
		case "DELETE":
			s.handleDeleteWorkflow(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Validation and compilation
	mux.HandleFunc("/api/v1/workflows/validate", methodHandler("POST", s.handleValidateWorkflow))
	mux.HandleFunc("/api/v1/workflows/{id}/compile", methodHandler("POST", s.handleCompileWorkflow))
	mux.HandleFunc("/api/v1/workflows/schema", methodHandler("POST", s.handleWorkflowSchema))
	// Per-node variable view for editors (for variable pickers / templating)
	mux.HandleFunc("/api/v1/workflows/vars", methodHandler("POST", s.handleWorkflowVars))

	// Execution
	mux.HandleFunc("/api/v1/workflows/{id}/execute", methodHandler("POST", s.handleExecuteWorkflow))
	mux.HandleFunc("/api/v1/workflows/{id}/execute/stream", methodHandler("POST", s.handleExecuteWorkflowStream))

	// Health check
	mux.HandleFunc("/health", methodHandler("GET", s.handleHealth))

	log.Println("✅ Routes registered successfully")
}

// methodHandler wraps a handler to only respond to a specific HTTP method.
func methodHandler(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	}
}

// Middleware: CORS
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Middleware: Logging
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("→ %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("← %s %s (%v)", r.Method, r.URL.Path, time.Since(start))
	})
}

// handleHealth handles health check requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// handleAPIDocs serves the OpenAPI documentation.
func (s *Server) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	// TODO: Serve openapi.json or Swagger UI
	http.ServeFile(w, r, "server/dsl/openapi.json")
}

// respondJSON writes a JSON response.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding JSON response: %v", err)
	}
}

// respondError writes an error response.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{
		"error": message,
	})
}
