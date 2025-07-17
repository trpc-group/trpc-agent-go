#!/bin/bash

# A2A Quick Start Script
# Start two agent servers and interactive client

set -e  # Exit on error

# Color definitions
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Print colored messages
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check environment variables
check_env() {
    print_info "Checking environment configuration..."
    
    if [ -z "$OPENAI_API_KEY" ]; then
        print_warning "OPENAI_API_KEY not set"
        echo "Please set OpenAI API Key:"
        echo "export OPENAI_API_KEY=\"your-api-key-here\""
        echo ""
        echo "Example configuration:"
        echo "export OPENAI_API_KEY=\"sk-xxx\""
        echo "export OPENAI_BASE_URL=\"https://api.openai.com/v1\""
        echo "export OPENAI_MODEL=\"gpt-4o-mini\""
        echo ""
        echo "Or use other compatible services:"
        echo "export OPENAI_API_KEY=\"your-key\""
        echo "export OPENAI_BASE_URL=\"https://api.deepseek.com/v1\""
        echo "export OPENAI_MODEL=\"deepseek-chat\""
        exit 1
    fi
    
    print_success "Environment variables check passed"
    echo "  OPENAI_API_KEY: ${OPENAI_API_KEY:0:8}..."
    echo "  OPENAI_BASE_URL: ${OPENAI_BASE_URL:-https://api.openai.com/v1}"
    echo "  OPENAI_MODEL: ${OPENAI_MODEL:-gpt-4o-mini}"
    echo ""
}

# Check port occupancy
check_ports() {
    print_info "Checking port occupancy..."
    
    if lsof -i :8082 >/dev/null 2>&1; then
        print_warning "Port 8082 is already in use"
        echo "Attempting to close the process using the port..."
        pkill -f "codecc_agent" || true
        sleep 2
    fi
    
    if lsof -i :8081 >/dev/null 2>&1; then
        print_warning "Port 8081 is already in use"
        echo "Attempting to close the process using the port..."
        pkill -f "entrance_agent" || true
        sleep 2
    fi
    
    print_success "Port check completed"
}

# Build all components
build_all() {
    print_info "Building all components..."
    
    # Build entrance agent
    print_info "Building entrance agent..."
    cd agents/entrance
    go build -o entrance_agent .
    cd ../..
    
    # Build code check agent
    print_info "Building code check agent..."
    cd agents/codecheck
    go build -o codecc_agent .
    cd ../..
    
    # Build client
    print_info "Building client..."
    cd client
    go build -o client .
    cd ..
    
    print_success "All components built successfully"
}

# Start agents
start_agents() {
    print_info "Starting agent servers..."
    
    # Create logs directory
    mkdir -p logs
    
    # Get model name
    MODEL_NAME=${OPENAI_MODEL:-deepseek-chat}
    
    # Start code check agent (first)
    print_info "Starting code check agent (port 8082)..."
    cd agents/codecheck
    nohup ./codecc_agent -model="$MODEL_NAME" > ../../logs/codecc_agent.log 2>&1 &
    CODECC_PID=$!
    cd ../..
    echo $CODECC_PID > logs/codecc_agent.pid
    sleep 2
    
    # Start entrance agent (second)
    print_info "Starting entrance agent (port 8081)..."
    cd agents/entrance
    nohup ./entrance_agent -model="$MODEL_NAME" > ../../logs/entrance_agent.log 2>&1 &
    ENTRANCE_PID=$!
    cd ../..
    echo $ENTRANCE_PID > logs/entrance_agent.pid
    sleep 2
    
    print_success "Agent servers started successfully"
}

# Check agent health status
check_agents() {
    print_info "Checking agent health status..."
    
    # Check code check agent (first)
    if curl -s http://localhost:8082/.well-known/agent.json >/dev/null; then
        print_success "Code check agent (8082) running normally"
    else
        print_error "Code check agent (8082) failed to start"
        show_logs
        exit 1
    fi
    
    # Check entrance agent (second)
    if curl -s http://localhost:8081/.well-known/agent.json >/dev/null; then
        print_success "Entrance agent (8081) running normally"
    else
        print_error "Entrance agent (8081) failed to start"
        show_logs
        exit 1
    fi
    
    echo ""
    print_success "All agents running normally!"
}

# Show logs
show_logs() {
    echo ""
    print_info "View recent logs:"
    
    if [ -f logs/codecc_agent.log ]; then
        echo "=== Code Check Agent Logs ==="
        tail -10 logs/codecc_agent.log
        echo ""
    fi
    
    if [ -f logs/entrance_agent.log ]; then
        echo "=== Entrance Agent Logs ==="
        tail -10 logs/entrance_agent.log
        echo ""
    fi
}

# Show agent information
show_agent_info() {
    echo ""
    print_info "Agent Information:"
    echo "┌─────────────────────────────────────────────────────────────────┐"
    echo "│                     A2A Agent Service                           │"
    echo "├─────────────────────────────────────────────────────────────────┤"
    echo "│   Entrance Agent  │ http://localhost:8081                       │"
    echo "│   Code Check Agent│ http://localhost:8082                       │"
    echo "├─────────────────────────────────────────────────────────────────┤"
    echo "│   Agent Cards     |                                             │"
    echo "│   Entrance Agent  │ http://localhost:8081/.well-known/agent.json│"
    echo "│   Code Check Agent│ http://localhost:8082/.well-known/agent.json|"
    echo "└─────────────────────────────────────────────────────────────────┘"
    echo ""
}

# Start client menu
client_menu() {
    echo ""
    print_info "Select an agent to connect to:"
    echo "1) Entrance Agent (http://localhost:8081)"
    echo "2) Code Check Agent (http://localhost:8082)"
    echo "3) Custom URL"
    echo "4) Exit"
    echo ""
    read -p "Please choose [1-4]: " choice
    
    case $choice in
        1)
            print_info "Connecting to Entrance Agent..."
            cd client
            ./client -url http://localhost:8081
            cd ..
            ;;
        2)
            print_info "Connecting to Code Check Agent..."
            cd client
            ./client -url http://localhost:8082
            cd ..
            ;;
        3)
            read -p "Please enter agent URL: " custom_url
            print_info "Connecting to $custom_url..."
            cd client
            ./client -url "$custom_url"
            cd ..
            ;;
        4)
            print_info "Exiting client menu"
            return
            ;;
        *)
            print_error "Invalid choice"
            client_menu
            ;;
    esac
}

# Stop all agents
stop_agents() {
    print_info "Stopping all agents..."
    
    # First stop entrance agent (reverse order of startup)
    if [ -f logs/entrance_agent.pid ]; then
        ENTRANCE_PID=$(cat logs/entrance_agent.pid)
        kill $ENTRANCE_PID 2>/dev/null || true
        rm -f logs/entrance_agent.pid
    fi
    
    # Then stop code check agent
    if [ -f logs/codecc_agent.pid ]; then
        CODECC_PID=$(cat logs/codecc_agent.pid)
        kill $CODECC_PID 2>/dev/null || true
        rm -f logs/codecc_agent.pid
    fi
    
    # Force kill processes
    pkill -f "entrance_agent" || true
    pkill -f "codecc_agent" || true
    
    print_success "All agents stopped"
}

# Cleanup function
cleanup() {
    echo ""
    print_info "Cleaning up..."
    stop_agents
    exit 0
}

# Set signal handling
trap cleanup SIGINT SIGTERM

# Show help
show_help() {
    echo "A2A Quick Start Script"
    echo ""
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  -h, --help     Show help information"
    echo "  -b, --build    Build only, don't start"
    echo "  -s, --stop     Stop all agents"
    echo "  -l, --logs     Show logs"
    echo "  -c, --client   Start client only"
    echo ""
    echo "Environment Variables:"
    echo "  OPENAI_API_KEY    OpenAI API key (required)"
    echo "  OPENAI_BASE_URL   API base URL (optional)"
    echo "  OPENAI_MODEL      Model to use (optional)"
    echo ""
    echo "Examples:"
    echo "  $0                # Complete startup process"
    echo "  $0 --build        # Build only"
    echo "  $0 --stop         # Stop agents"
    echo "  $0 --client       # Start client only"
}

# Main function
main() {
    case "${1:-}" in
        -h|--help)
            show_help
            exit 0
            ;;
        -b|--build)
            check_env
            build_all
            exit 0
            ;;
        -s|--stop)
            stop_agents
            exit 0
            ;;
        -l|--logs)
            show_logs
            exit 0
            ;;
        -c|--client)
            cd client
            if [ ! -f client ]; then
                print_error "Client not built, please run: $0 --build first"
                exit 1
            fi
            client_menu
            exit 0
            ;;
        "")
            # Default complete flow
            ;;
        *)
            print_error "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
    
    # Complete startup process
    echo "🚀 A2A Quick Start Script"
    echo "======================="
    
    check_env
    check_ports
    build_all
    start_agents
    check_agents
    show_agent_info
    
    print_success "All components started successfully!"
    echo ""
    print_info "Available commands:"
    echo "  View logs: tail -f logs/codecc_agent.log"
    echo "  View logs: tail -f logs/entrance_agent.log"
    echo "  Stop agents: $0 --stop"
    echo "  Start client: $0 --client"
    echo ""
    
    # Ask whether to start client
    read -p "Start client now? [y/N]: " start_client
    if [[ $start_client =~ ^[Yy] ]]; then
        client_menu
    else
        print_info "Agent servers are running in the background"
        print_info "Use '$0 --client' to start the client"
        print_info "Use '$0 --stop' to stop all agents"
    fi
}

# Check if in the correct directory
if [ ! -d "agents" ] || [ ! -d "client" ]; then
    print_error "Please run this script in the examples/a2a directory"
    exit 1
fi

# Run main function
main "$@"