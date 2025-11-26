#!/usr/bin/env python3
"""
ADK A2A Agent Server Example

This example demonstrates how to create an A2A agent server using ADK Python,
which can be called by trpc-agent-go's a2aagent client.

"""

import os
from datetime import datetime
from typing import Annotated
from a2a.server.agent_execution import request_context_builder
from a2a.types import AgentCapabilities
from google.adk import Agent
from google.adk.models.lite_llm import LiteLlm
from google.adk.tools import FunctionTool
from google.adk.a2a.converters.request_converter import (
    convert_a2a_request_to_agent_run_request,
)
from google.adk.a2a.executor.a2a_agent_executor import (
    A2aAgentExecutor,
    A2aAgentExecutorConfig,
)


def calculator(
    operation: Annotated[str, "The operation to perform: add, subtract, multiply, divide"],
    a: Annotated[float, "First number"],
    b: Annotated[float, "Second number"]
) -> str:
    """Perform basic mathematical calculations."""
    try:
        if operation == "add":
            result = a + b
        elif operation == "subtract":
            result = a - b
        elif operation == "multiply":
            result = a * b
        elif operation == "divide":
            if b == 0:
                return "Error: Division by zero"
            result = a / b
        else:
            return f"Error: Unknown operation '{operation}'"

        return f"{a} {operation} {b} = {result}"
    except Exception as e:
        return f"Error: {str(e)}"


def current_time(
    timezone: Annotated[str, "Timezone name (e.g., 'UTC', 'America/New_York'). Use 'local' for local time."] = "local"
) -> str:
    """Get the current time and date for a specific timezone."""
    try:
        now = datetime.now()
        if timezone == "local" or timezone == "":
            time_str = now.strftime("%Y-%m-%d %H:%M:%S")
            return f"Current local time: {time_str}"

        return (
            f"Current time (timezone support limited): {now.strftime('%Y-%m-%d %H:%M:%S')} "
            f"(Note: This is local time, timezone '{timezone}' conversion not implemented)"
        )
    except Exception as e:
        return f"Error: {str(e)}"


def create_agent() -> Agent:
    """Create an agent with streaming support and tools."""

    # Get custom model name if specified
    custom_model = os.getenv("MODEL_NAME")
    model_name = custom_model or "gpt-4o-mini"

    print(f"Using model: {model_name}")

    # Check for OpenAI API key
    if not os.getenv("OPENAI_API_KEY"):
        print("⚠️  Warning: OPENAI_API_KEY not set")
        print("Please set it with: export OPENAI_API_KEY='your-api-key'")
        print()

    # Check for custom API URL
    base_url = os.getenv("OPENAI_BASE_URL") or os.getenv("OPENAI_API_URL")
    if base_url:
        print(f"Using custom API URL: {base_url}")
        os.environ["OPENAI_API_BASE"] = base_url

    # Create LiteLLM model with streaming enabled
    litellm_model_name = f"openai/{model_name}" if not model_name.startswith("openai/") else model_name
    model = LiteLlm(
        model=litellm_model_name,
        stream=True  # Enable streaming
    )

    # Create tools
    calculator_tool = FunctionTool(calculator)
    time_tool = FunctionTool(current_time)

    # Create agent with streaming and tools
    agent = Agent(
        name="adk_simple_agent",
        description="A helpful assistant with calculator and time tools. Use tools when appropriate.",
        model=model,
        tools=[calculator_tool, time_tool],
    )

    print(f"✅ Agent created with {len(agent.tools)} tools (streaming enabled)")
    
    return agent


def logging_request_converter(request, part_converter):
    """Wrap the default converter so we can log user and session IDs."""
    agent_request = convert_a2a_request_to_agent_run_request(
        request,
        part_converter=part_converter,
    )
    user_id = agent_request.user_id or "unknown"
    session_id = agent_request.session_id or "unknown"
    print(f"📋 Session Info - User: {user_id}, Session: {session_id}")
    return agent_request


# Create the agent
agent = create_agent()

# Build A2A app
from google.adk.runners import Runner
from google.adk.artifacts.in_memory_artifact_service import InMemoryArtifactService
from google.adk.sessions.in_memory_session_service import InMemorySessionService
from google.adk.memory.in_memory_memory_service import InMemoryMemoryService
from google.adk.auth.credential_service.in_memory_credential_service import InMemoryCredentialService
from google.adk.a2a.utils.agent_card_builder import AgentCardBuilder
from a2a.server.apps import A2AStarletteApplication
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.tasks import InMemoryTaskStore
from starlette.applications import Starlette


async def create_runner() -> Runner:
    """Create a runner for the agent."""
    return Runner(
        app_name=agent.name or "adk_agent",
        agent=agent,
        artifact_service=InMemoryArtifactService(),
        session_service=InMemorySessionService(),
        memory_service=InMemoryMemoryService(),
        credential_service=InMemoryCredentialService(),
    )


# Create A2A components
task_store = InMemoryTaskStore()
executor_config = A2aAgentExecutorConfig(
    request_converter=logging_request_converter,
)
agent_executor = A2aAgentExecutor(
    runner=create_runner,
    config=executor_config,
)
request_handler = DefaultRequestHandler(
    agent_executor=agent_executor,
    task_store=task_store
)

# Build agent card
rpc_url = "http://localhost:8081/"
card_builder = AgentCardBuilder(agent=agent, rpc_url=rpc_url, capabilities=AgentCapabilities(streaming=True))

# Create Starlette app
a2a_app = Starlette()


# Setup A2A routes during startup
async def setup_a2a():
    """Setup A2A routes."""
    agent_card = await card_builder.build()
    
    # Create A2A app
    a2a_starlette_app = A2AStarletteApplication(
        agent_card=agent_card,
        http_handler=request_handler,
    )
    
    # Add A2A routes to main app
    a2a_starlette_app.add_routes_to_app(a2a_app)


a2a_app.add_event_handler("startup", setup_a2a)


# Main entry point for direct execution
if __name__ == "__main__":
    import uvicorn

    print("=" * 60)
    print("ADK A2A Agent Server (Streaming Mode)")
    print("=" * 60)
    print(f"Server URL: http://localhost:8081")
    print(f"Agent Card: http://localhost:8081/.well-known/agent-card.json")
    print("=" * 60)
    print(f"Features:")
    print(f"  • Streaming: Enabled")
    print(f"  • Tools: calculator, current_time")
    print("=" * 60)
    print("\n🚀 Server ready for trpc-agent-go connections")
    print("Press Ctrl+C to stop\n")

    # Run the server
    uvicorn.run(a2a_app, host="0.0.0.0", port=8081)

