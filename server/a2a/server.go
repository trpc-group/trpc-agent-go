package a2a

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var userIDHeader = "X-User-ID"

// Server is the a2a server with multi agents.
type Server struct {
	a2aEndpoints map[string]*a2aEndpoint
	opts         *options
	handler      http.Handler
	httpServer   *http.Server
}

// a2aEndpoint is the a2a server endpoint.
type a2aEndpoint struct {
	server *a2a.A2AServer
	agent  agent.Agent
}

// New creates a new a2a server.
func New(opts ...Option) (*Server, error) {
	s := &Server{
		opts: defaultOptions,
	}
	for _, opt := range opts {
		opt(s.opts)
	}

	if s.opts.sessionService == nil {
		s.opts.sessionService = inmemory.NewSessionService()
	}

	if len(s.opts.agents) == 0 {
		return nil, errors.New("agents are required")
	}

	if err := s.initA2AServer(); err != nil {
		return nil, fmt.Errorf("failed to init a2a server: %w", err)
	}

	// combine all a2a endpoints in one http handler
	handler := http.NewServeMux()
	for path, endpoint := range s.a2aEndpoints {
		prefix := fmt.Sprintf("/a2a/%s/", path)
		router := s.getAgentRouter(endpoint)
		handler.Handle(prefix, router)
		log.Infof("Registered agent '%s' at path: %s", endpoint.agent.Info().Name, prefix)
	}
	s.handler = handler

	return s, nil
}

// Start starts the a2a server.
func (s *Server) Start() error {
	if s.handler == nil {
		return errors.New("Server not initialized")
	}
	s.httpServer = &http.Server{
		Addr:    s.opts.host,
		Handler: s.handler,
	}
	log.Infof("Starting a2a server at %s", s.opts.host)
	return s.httpServer.ListenAndServe()
}

// Stop stops the a2a server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return errors.New("http server is nil")
	}
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to stop a2a server: %w", err)
	}
	log.Infof("Stopped a2a server at %s", s.opts.host)
	return nil
}

// Host returns the host of the a2a server.
func (s *Server) Host() string {
	return s.opts.host
}

// Handler returns the http handler for the server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) getAgentRouter(endpoint *a2aEndpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint.server.Handler().ServeHTTP(w, r)
	})
}

func (s *Server) initA2AServer() error {
	if s.opts == nil || len(s.opts.agents) == 0 {
		return errors.New("agents are required")
	}

	a2aEndpoints := make(map[string]*a2aEndpoint)
	for path, agent := range s.opts.agents {
		server, err := s.buildA2AServer(path, agent, s.opts.sessionService)
		if err != nil {
			return fmt.Errorf("buildA2AServer failed: %w", err)
		}
		a2aEndpoints[path] = &a2aEndpoint{
			agent:  agent,
			server: server,
		}
	}
	s.a2aEndpoints = a2aEndpoints
	return nil
}

func (s *Server) buildAgentCard(path string, agent agent.Agent) a2a.AgentCard {
	url := fmt.Sprintf("http://%s/a2a/%s/", s.opts.host, path)
	if s.opts.agentCards != nil {
		if agentCard, ok := s.opts.agentCards[path]; ok {
			agentCard.URL = url
			return agentCard
		}
	}
	desc := agent.Info().Description
	name := agent.Info().Name
	return a2a.AgentCard{
		Name:        name,
		Description: desc,
		URL:         url,
		Capabilities: a2a.AgentCapabilities{
			Streaming: boolPtr(false),
		},
		Skills: []a2a.AgentSkill{
			{
				Name:        name,
				Description: &desc,
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
				Tags:        []string{"default"},
			},
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
}

func (s *Server) buildProcessor(path string, agent agent.Agent, sessionService session.Service) *messageProcessor {
	agentName := agent.Info().Name
	runner := runner.NewRunner(agentName, agent, runner.WithSessionService(sessionService))
	return &messageProcessor{
		runner: runner,
	}
}

func (s *Server) buildA2AServer(path string, agent agent.Agent, sessionService session.Service) (*a2a.A2AServer, error) {
	agentCard := s.buildAgentCard(path, agent)

	var processor taskmanager.MessageProcessor
	if s.opts.processorBuilder != nil {
		processor = s.opts.processorBuilder(path, agent, sessionService)
	} else {
		processor = s.buildProcessor(path, agent, sessionService)
	}

	// Create a task manager that wraps the session service
	taskManager, err := taskmanager.NewMemoryTaskManager(processor, taskmanager.WithMaxHistoryLength(1))
	if err != nil {
		return nil, err
	}

	authProvider := &userAuthProvider{}
	a2aServer, err := a2a.NewA2AServer(agentCard, taskManager, a2a.WithAuthProvider(authProvider))
	if err != nil {
		return nil, err
	}

	return a2aServer, nil
}

// userAuthProvider is a simple auth provider that always returns nil.
type userAuthProvider struct{}

// Authenticate implements auth.AuthProvider.
func (p *userAuthProvider) Authenticate(r *http.Request) (*auth.User, error) {
	userID := r.Header.Get(userIDHeader)
	if userID == "" {
		userID = uuid.New().String()
	}
	return &auth.User{
		ID: userID,
	}, nil
}

// messageProcessor is the message processor for the a2a server.
type messageProcessor struct {
	runner runner.Runner
}

// ProcessMessage is the main entry point for processing messages.
func (m *messageProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	user, ok := ctx.Value(auth.AuthUserKey).(*auth.User)
	if !ok {
		return nil, errors.New("userID is required")
	}
	if message.ContextID == nil {
		return nil, errors.New("context id not exists")
	}

	userID := user.ID
	ctxID := *message.ContextID
	if options.Streaming {
		return m.processStreamingMessage(ctx, userID, ctxID, message, options, handler)
	}
	return m.processMessage(ctx, userID, ctxID, message, options, handler)
}

func (m *messageProcessor) processStreamingMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	// not supported yet
	return m.processMessage(ctx, userID, ctxID, message, options, handler)
}

func (m *messageProcessor) processMessage(
	ctx context.Context,
	userID string,
	ctxID string,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	text := extractTextFromMessage(message)
	if text == "" {
		message := protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{
			protocol.NewTextPart("input is empty!"),
		})
		return &taskmanager.MessageProcessingResult{
			Result: &message,
		}, nil
	}

	agentMsg := model.NewUserMessage(text)
	agentMsgChan, err := m.runner.Run(ctx, userID, ctxID, agentMsg, agent.RunOptions{})
	if err != nil {
		log.Errorf("failed to run agent: %v", err)
		return nil, err
	}

	content, err := processAgentResponse(agentMsgChan)
	if err != nil {
		log.Errorf("failed to process agent streaming response: %v", err)
		return nil, err
	}

	result := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{protocol.NewTextPart(content)})
	return &taskmanager.MessageProcessingResult{
		Result: &result,
	}, nil
}

// ExtractTextFromMessage extracts the text content from a message.
func extractTextFromMessage(message protocol.Message) string {
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			return textPart.Text
		}
	}
	return ""
}

// ProcessStreamingResponse handles the streaming response with tool call visualization.
func processAgentResponse(eventChan <-chan *event.Event) (string, error) {
	var (
		fullContent string
	)

	for event := range eventChan {
		if event.Error != nil {
			log.Errorf("streaming process error: %v", event.Error)
			continue
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				log.Debugf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					log.Debugf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Delta.Content != "" {
				fullContent += choice.Delta.Content
			}
		}
		if event.Done {
			break
		}
	}
	return fullContent, nil
}

func boolPtr(b bool) *bool {
	return &b
}
