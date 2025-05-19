#!/bin/bash

# Script to run the A2A runner using runner mode directly

set -e

# Define a usage function
usage() {
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  --async              Run in asynchronous mode"
    echo "  --model=NAME         Specify model name (default: gemini-2.0-flash)"
    echo "  --provider=PROVIDER  Specify model provider (default: gemini)"
    echo "  --message=MESSAGE    Message to process (default: predefined example)"
    echo "  --timeout=SECONDS    Runner timeout in seconds (default: 30)"
    echo "  --server             Run as an A2A server for client interaction"
    echo "  --client             Act as an A2A client (requires a running server)"
    echo "  --stream             Use streaming when in client mode"
    echo "  --address=ADDRESS    Server address:port (default: localhost:8081)"
    echo "  --debug              Enable debug logging"
    echo "  --help               Show this help message"
    exit 1
}

# Default values
ASYNC_FLAG=""
MODEL_NAME="gemini-2.0-flash"
MODEL_PROVIDER="gemini"
MESSAGE="Hello! Please calculate 25 * 3, translate 'hello' to Spanish, and convert 10 kilometers to miles."
TIMEOUT=30
DEBUG_FLAG=""
SERVER_FLAG=""
CLIENT_FLAG=""
STREAM_FLAG=""
SERVER_ADDRESS="localhost:8081"

# Process arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --async)
            ASYNC_FLAG="--runner-async"
            shift
            ;;
        --model=*)
            MODEL_NAME="${1#*=}"
            shift
            ;;
        --provider=*)
            MODEL_PROVIDER="${1#*=}"
            shift
            ;;
        --message=*)
            MESSAGE="${1#*=}"
            shift
            ;;
        --timeout=*)
            TIMEOUT="${1#*=}"
            shift
            ;;
        --server)
            SERVER_FLAG="--runner"
            MESSAGE=""  # Clear message to force server mode
            shift
            ;;
        --client)
            CLIENT_FLAG="--server=false"
            shift
            ;;
        --stream)
            STREAM_FLAG="--stream"
            shift
            ;;
        --address=*)
            SERVER_ADDRESS="${1#*=}"
            shift
            ;;
        --debug)
            DEBUG_FLAG="--debug"
            shift
            ;;
        --help)
            usage
            ;;
        *)
            echo "Unknown option: $1"
            usage
            ;;
    esac
done

# Check if the binary has been built
if [ ! -f ./a2a_example ]; then
    echo "Building a2a_example..."
    go build -o a2a_example
fi

# Determine running mode
if [ -n "$CLIENT_FLAG" ]; then
    # Client mode - connect to an A2A server
    CMD="./a2a_example $CLIENT_FLAG $STREAM_FLAG --address=$SERVER_ADDRESS --message=\"$MESSAGE\""
    echo "Running in A2A client mode:"
    echo "  Connecting to: $SERVER_ADDRESS"
    echo "  Streaming:    ${STREAM_FLAG:+enabled}"
    echo "  Message:      $MESSAGE"
else
    # Runner mode - either direct or server
    CMD="./a2a_example $SERVER_FLAG $ASYNC_FLAG --model-provider=$MODEL_PROVIDER --model-name=$MODEL_NAME --runner-timeout=$TIMEOUT $DEBUG_FLAG"
    
    # Add message only if not in server mode
    if [ -z "$SERVER_FLAG" ]; then
        CMD="$CMD --message=\"$MESSAGE\""
    fi
    
    echo "Running runner example with:"
    echo "  Model:     $MODEL_PROVIDER/$MODEL_NAME"
    
    if [ -n "$SERVER_FLAG" ]; then
        echo "  Mode:      A2A server (for client interaction)"
        echo "  Address:   $SERVER_ADDRESS"
        echo "  Message:   N/A (waiting for client requests)"
    else
        echo "  Async:     ${ASYNC_FLAG:+true}"
        echo "  Timeout:   $TIMEOUT seconds"
        echo "  Message:   $MESSAGE"
    fi
fi

echo ""
echo "Command: $CMD"
echo "==============================================="

# Execute the command (need to eval for proper quoting)
eval $CMD 