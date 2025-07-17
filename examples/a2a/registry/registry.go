package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	agentURLMap = make(map[string]string)
	agentMap    = make(map[string]*a2aAgent)
	httpClient  = &http.Client{
		Timeout: 120 * time.Second,
	}
	agentCardURL    = "%s/.well-known/agent.json"
	agentReqtimeout = 120 * time.Second
)

type agentCallReq struct {
	Message string `json:"message"`
}

// GenerateToolList generates tool list from all registered agents
func GenerateToolList() ([]tool.Tool, error) {
	toolList := make([]*function.FunctionTool[agentCallReq, protocol.MessageResult], 0, len(agentURLMap))
	log.Infof("generate tool list from %d agents", len(agentURLMap))
	for name, url := range agentURLMap {
		log.Infof("fetch agent card for %s: %s", name, url)
		card := fetchAgentCard(name, url)
		if card.Name != "" {
			agent, err := buildA2AAgent(name, url, card)
			if err != nil {
				log.Errorf("Failed to build A2A agent for %s: %v\n", name, err)
				continue
			}
			log.Infof("found agent name: %+v, description: %+v", card.Name, card.Description)
			toolList = append(toolList, agent.tool)
		}
	}
	return convertToolList(toolList), nil
}

// RegisterAgentService registers agent by name and url
func RegisterAgentService(name string, url string) {
	agentURLMap[name] = url
}

type a2aAgent struct {
	name     string
	url      string
	toolName string
	tool     *function.FunctionTool[agentCallReq, protocol.MessageResult]
	client   *client.A2AClient
}

func fetchAgentCard(name string, url string) server.AgentCard {
	agentCardURL := fmt.Sprintf(agentCardURL, url)
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "GET", agentCardURL, nil)
	if err != nil {
		log.Errorf("Failed to create request for agent %s: %v\n", name, err)
		return server.AgentCard{}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Errorf("Failed to fetch agent card for %s: %v\n", name, err)
		return server.AgentCard{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Errorf("Failed to fetch agent card for %s: status %d\n", name, resp.StatusCode)
		return server.AgentCard{}
	}

	var card server.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		log.Errorf("Failed to decode agent card for %s: %v\n", name, err)
		return server.AgentCard{}
	}
	return card
}

func buildA2AAgent(name string, url string, card server.AgentCard) (*a2aAgent, error) {
	agent := &a2aAgent{name: name, url: url}
	client, err := client.NewA2AClient(url, client.WithTimeout(agentReqtimeout))
	if err != nil {
		log.Errorf("Failed to create A2A client for %s: %v\n", name, err)
		return nil, err
	}
	agent.client = client
	toolName, tool := agent.createSendMessageTool(name, card)
	agent.toolName = toolName
	agent.tool = tool
	return agent, nil
}

func (a *a2aAgent) createSendMessageTool(
	agentName string,
	card server.AgentCard,
) (string, *function.FunctionTool[agentCallReq, protocol.MessageResult]) {
	description := fmt.Sprintf(
		"Send non-streaming message to %s agent: %s", agentName, card.Description)
	toolName := fmt.Sprintf("non_streaming_%s", agentName)

	return toolName, function.NewFunctionTool(
		func(params agentCallReq) protocol.MessageResult {
			message := protocol.Message{
				Role:  protocol.MessageRoleAgent,
				Parts: []protocol.Part{protocol.NewTextPart(params.Message)},
			}
			sendMessageParams := protocol.SendMessageParams{
				Message: message,
			}
			result, err := a.sendMessageToAgent(sendMessageParams)
			if err != nil {
				log.Errorf("Error sending message to %s: %v\n", agentName, err)
				return protocol.MessageResult{}
			}
			return result
		},
		function.WithName(toolName),
		function.WithDescription(description),
	)
}

func (a *a2aAgent) sendMessageToAgent(params protocol.SendMessageParams) (protocol.MessageResult, error) {
	result, err := a.client.SendMessage(context.Background(), params)
	if err != nil {
		return protocol.MessageResult{}, err
	}
	return *result, nil
}

func convertToolList(toolList []*function.FunctionTool[agentCallReq, protocol.MessageResult]) []tool.Tool {
	tools := make([]tool.Tool, 0, len(toolList))
	for _, tool := range toolList {
		tools = append(tools, tool)
	}
	return tools
}
