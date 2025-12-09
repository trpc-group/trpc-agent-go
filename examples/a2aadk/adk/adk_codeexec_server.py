#!/usr/bin/env python3
"""
ADK A2A Agent Server with Code Execution

This example demonstrates an ADK A2A agent with code execution capabilities,
which can be called by trpc-agent-go's a2aagent client.
"""

import os
from datetime import datetime
from typing import Annotated
from a2a.server.agent_execution import request_context_builder
from a2a.types import AgentCapabilities
from google.adk import Agent
from google.adk.models.lite_llm import LiteLlm
from google.adk.a2a.converters.request_converter import (
    convert_a2a_request_to_agent_run_request,
)
from google.adk.a2a.executor.a2a_agent_executor import (
    A2aAgentExecutor,
    A2aAgentExecutorConfig,
)
from google.adk.code_executors import UnsafeLocalCodeExecutor
import logging

# Enable debug logging for code execution
logging.basicConfig(level=logging.INFO)
logging.getLogger('google_adk').setLevel(logging.INFO)


def create_agent() -> Agent:
    """Create an agent with code execution support."""

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
        api_base=base_url if base_url else None,
        stream=True
    )

    # Create code execution tool
    code_executor = UnsafeLocalCodeExecutor()
    
    # Create agent with code execution and tools
    agent = Agent(
        name="adk_codeexec_agent",
        description="A helpful assistant with Python code execution and time tool. Can analyze data and execute Python code.",
        instruction="""You are a helpful assistant with Python code execution capabilities.

IMPORTANT: When writing Python code, you MUST use print() to output results. 
The code executor only captures stdout, so any result you want to see must be printed.

IMPORTANT: After code execution results are shown, provide a final text answer 
to the user. Do NOT generate more code unless the user asks for it.

Example - WRONG (no output):
```python
result = [1, 2, 3]
result  # This won't show anything
```

Example - CORRECT:
```python
result = [1, 2, 3]
print(result)  # This will show the output
```
""",
        model=model,
        code_executor=code_executor,
    )

    print(f"✅ Agent created with code execution enabled and {len(agent.tools)} tools")
    
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

# Custom event converter with logging
from google.adk.a2a.converters.event_converter import convert_event_to_a2a_events
from google.adk.a2a.converters.part_converter import convert_genai_part_to_a2a_part

def logging_event_converter(event, invocation_context, task_id=None, context_id=None, part_converter=None):
    """Wrap the default event converter to log events."""
    print(f"\n📤 ADK Event:")
    print(f"   Author: {event.author}")
    print(f"   Invocation ID: {event.invocation_id}")
    if event.content and event.content.parts:
        for i, part in enumerate(event.content.parts):
            if part.text:
                text_preview = part.text[:80].replace('\n', '\\n') if len(part.text) > 80 else part.text.replace('\n', '\\n')
                print(f"   Part[{i}]: TextPart: {text_preview}...")
            elif part.executable_code:
                print(f"   Part[{i}]: ✅ ExecutableCode (lang={part.executable_code.language})")
                code_preview = part.executable_code.code[:100].replace('\n', '\\n')
                print(f"            Code: {code_preview}...")
            elif part.code_execution_result:
                print(f"   Part[{i}]: ✅ CodeExecutionResult (outcome={part.code_execution_result.outcome})")
                output_preview = (part.code_execution_result.output[:100].replace('\n', '\\n') 
                                  if part.code_execution_result.output else 'None')
                print(f"            Output: {output_preview}...")
            elif part.function_call:
                print(f"   Part[{i}]: FunctionCall (name={part.function_call.name})")
            elif part.function_response:
                print(f"   Part[{i}]: FunctionResponse (name={part.function_response.name})")
            else:
                print(f"   Part[{i}]: Unknown part type: {type(part)}")
    else:
        print(f"   Content: None or empty")
    
    # Call original converter with correct part_converter
    if part_converter is None:
        part_converter = convert_genai_part_to_a2a_part
    
    result = list(convert_event_to_a2a_events(event, invocation_context, task_id, context_id, part_converter))
    print(f"   -> Converted to {len(result)} A2A events")
    return result

executor_config = A2aAgentExecutorConfig(
    request_converter=logging_request_converter,
    event_converter=logging_event_converter,
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
rpc_url = "http://localhost:8082/"
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
    print("ADK A2A Agent Server with Code Execution")
    print("=" * 60)
    print(f"Server URL: http://localhost:8082")
    print(f"Agent Card: http://localhost:8082/.well-known/agent-card.json")
    print("=" * 60)
    print(f"Features:")
    print(f"  • Streaming: Enabled")
    print(f"  • Code Execution: Python")
    print(f"  • Tools: current_time")
    print("=" * 60)
    print("\n🚀 Server ready for trpc-agent-go connections")
    print("Press Ctrl+C to stop\n")

    # Run the server
    uvicorn.run(a2a_app, host="0.0.0.0", port=8082)
