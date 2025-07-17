#!/bin/bash

# A2A 快速启动脚本
# 启动两个代理服务器和交互式客户端

set -e  # 遇到错误时退出

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印带颜色的消息
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

# 检查环境变量
check_env() {
    print_info "检查环境配置..."
    
    if [ -z "$OPENAI_API_KEY" ]; then
        print_warning "OPENAI_API_KEY 未设置"
        echo "请设置 OpenAI API Key："
        echo "export OPENAI_API_KEY=\"your-api-key-here\""
        echo ""
        echo "示例配置："
        echo "export OPENAI_API_KEY=\"sk-xxx\""
        echo "export OPENAI_BASE_URL=\"https://api.openai.com/v1\""
        echo "export OPENAI_MODEL=\"gpt-4o-mini\""
        echo ""
        echo "或使用其他兼容服务："
        echo "export OPENAI_API_KEY=\"your-key\""
        echo "export OPENAI_BASE_URL=\"https://api.deepseek.com/v1\""
        echo "export OPENAI_MODEL=\"deepseek-chat\""
        exit 1
    fi
    
    print_success "环境变量检查通过"
    echo "  OPENAI_API_KEY: ${OPENAI_API_KEY:0:8}..."
    echo "  OPENAI_BASE_URL: ${OPENAI_BASE_URL:-https://api.openai.com/v1}"
    echo "  OPENAI_MODEL: ${OPENAI_MODEL:-gpt-4o-mini}"
    echo ""
}

# 检查端口占用
check_ports() {
    print_info "检查端口占用..."
    
    if lsof -i :8082 >/dev/null 2>&1; then
        print_warning "端口 8082 已被占用"
        echo "正在尝试关闭占用端口的进程..."
        pkill -f "codecc_agent" || true
        sleep 2
    fi
    
    if lsof -i :8081 >/dev/null 2>&1; then
        print_warning "端口 8081 已被占用"
        echo "正在尝试关闭占用端口的进程..."
        pkill -f "entrance_agent" || true
        sleep 2
    fi
    
    print_success "端口检查完成"
}

# 编译所有组件
build_all() {
    print_info "编译所有组件..."
    
    # 编译入口代理
    print_info "编译入口代理..."
    cd agents/entrance
    go build -o entrance_agent .
    cd ../..
    
    # 编译代码检查代理
    print_info "编译代码检查代理..."
    cd agents/codecheck
    go build -o codecc_agent .
    cd ../..
    
    # 编译客户端
    print_info "编译客户端..."
    cd client
    go build -o client .
    cd ..
    
    print_success "所有组件编译完成"
}

# 启动代理
start_agents() {
    print_info "启动代理服务器..."
    
    # 创建日志目录
    mkdir -p logs
    
    # 获取模型名称
    MODEL_NAME=${OPENAI_MODEL:-gpt-4o-mini}
    
    # 启动代码检查代理 (先启动)
    print_info "启动代码检查代理 (端口 8082)..."
    cd agents/codecheck
    nohup ./codecc_agent -model="$MODEL_NAME" > ../../logs/codecc_agent.log 2>&1 &
    CODECC_PID=$!
    cd ../..
    echo $CODECC_PID > logs/codecc_agent.pid
    sleep 2
    
    # 启动入口代理 (后启动)
    print_info "启动入口代理 (端口 8081)..."
    cd agents/entrance
    nohup ./entrance_agent -model="$MODEL_NAME" > ../../logs/entrance_agent.log 2>&1 &
    ENTRANCE_PID=$!
    cd ../..
    echo $ENTRANCE_PID > logs/entrance_agent.pid
    sleep 2
    
    print_success "代理服务器启动完成"
}

# 检查代理健康状态
check_agents() {
    print_info "检查代理健康状态..."
    
    # 检查代码检查代理 (先检查)
    if curl -s http://localhost:8082/.well-known/agent.json >/dev/null; then
        print_success "代码检查代理 (8082) 运行正常"
    else
        print_error "代码检查代理 (8082) 启动失败"
        show_logs
        exit 1
    fi
    
    # 检查入口代理 (后检查)
    if curl -s http://localhost:8081/.well-known/agent.json >/dev/null; then
        print_success "入口代理 (8081) 运行正常"
    else
        print_error "入口代理 (8081) 启动失败"
        show_logs
        exit 1
    fi
    
    echo ""
    print_success "所有代理运行正常！"
}

# 显示日志
show_logs() {
    echo ""
    print_info "查看最近的日志："
    
    if [ -f logs/codecc_agent.log ]; then
        echo "=== 代码检查代理日志 ==="
        tail -10 logs/codecc_agent.log
        echo ""
    fi
    
    if [ -f logs/entrance_agent.log ]; then
        echo "=== 入口代理日志 ==="
        tail -10 logs/entrance_agent.log
        echo ""
    fi
}

# 显示代理信息
show_agent_info() {
    echo ""
    print_info "代理信息："
    echo "┌───────────────────────────────────────────────────────────────┐"
    echo "│                     A2A 代理服务                               │"
    echo "├───────────────────────────────────────────────────────────────┤"
    echo "│ 🚪 入口代理     │ http://localhost:8081                         │"
    echo "│ 🔍 代码检查代理  │ http://localhost:8082                         │"
    echo "├───────────────────────────────────────────────────────────────┤"
    echo "│ 📊 Agent Cards |                                              │"
    echo "│   入口代理      │ http://localhost:8081/.well-known/agent.json │"
    echo "│   代码检查代理  │ http://localhost:8082/.well-known/agent.json  │"
    echo "└───────────────────────────────────────────────────────────────┘"
    echo ""
}

# 启动客户端菜单
client_menu() {
    echo ""
    print_info "选择要连接的代理："
    echo "1) 入口代理 (http://localhost:8081)"
    echo "2) 代码检查代理 (http://localhost:8082)"
    echo "3) 自定义 URL"
    echo "4) 退出"
    echo ""
    read -p "请选择 [1-4]: " choice
    
    case $choice in
        1)
            print_info "连接到入口代理..."
            cd client
            ./client -url http://localhost:8081
            cd ..
            ;;
        2)
            print_info "连接到代码检查代理..."
            cd client
            ./client -url http://localhost:8082
            cd ..
            ;;
        3)
            read -p "请输入代理 URL: " custom_url
            print_info "连接到 $custom_url..."
            cd client
            ./client -url "$custom_url"
            cd ..
            ;;
        4)
            print_info "退出客户端菜单"
            return
            ;;
        *)
            print_error "无效选择"
            client_menu
            ;;
    esac
}

# 停止所有代理
stop_agents() {
    print_info "停止所有代理..."
    
    # 先停止入口代理 (启动顺序的逆序)
    if [ -f logs/entrance_agent.pid ]; then
        ENTRANCE_PID=$(cat logs/entrance_agent.pid)
        kill $ENTRANCE_PID 2>/dev/null || true
        rm -f logs/entrance_agent.pid
    fi
    
    # 再停止代码检查代理
    if [ -f logs/codecc_agent.pid ]; then
        CODECC_PID=$(cat logs/codecc_agent.pid)
        kill $CODECC_PID 2>/dev/null || true
        rm -f logs/codecc_agent.pid
    fi
    
    # 强制杀死进程
    pkill -f "entrance_agent" || true
    pkill -f "codecc_agent" || true
    
    print_success "所有代理已停止"
}

# 清理函数
cleanup() {
    echo ""
    print_info "正在清理..."
    stop_agents
    exit 0
}

# 设置信号处理
trap cleanup SIGINT SIGTERM

# 显示帮助
show_help() {
    echo "A2A 快速启动脚本"
    echo ""
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  -h, --help     显示帮助信息"
    echo "  -b, --build    仅编译，不启动"
    echo "  -s, --stop     停止所有代理"
    echo "  -l, --logs     显示日志"
    echo "  -c, --client   仅启动客户端"
    echo ""
    echo "环境变量:"
    echo "  OPENAI_API_KEY    OpenAI API 密钥 (必需)"
    echo "  OPENAI_BASE_URL   API 基础 URL (可选)"
    echo "  OPENAI_MODEL      使用的模型 (可选)"
    echo ""
    echo "示例:"
    echo "  $0                # 完整启动流程"
    echo "  $0 --build        # 仅编译"
    echo "  $0 --stop         # 停止代理"
    echo "  $0 --client       # 仅启动客户端"
}

# 主函数
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
                print_error "客户端未编译，请先运行: $0 --build"
                exit 1
            fi
            client_menu
            exit 0
            ;;
        "")
            # 默认完整流程
            ;;
        *)
            print_error "未知选项: $1"
            show_help
            exit 1
            ;;
    esac
    
    # 完整启动流程
    echo "🚀 A2A 快速启动脚本"
    echo "=================="
    
    check_env
    check_ports
    build_all
    start_agents
    check_agents
    show_agent_info
    
    print_success "所有组件启动完成！"
    echo ""
    print_info "可用命令："
    echo "  查看日志: tail -f logs/codecc_agent.log"
    echo "  查看日志: tail -f logs/entrance_agent.log"
    echo "  停止代理: $0 --stop"
    echo "  启动客户端: $0 --client"
    echo ""
    
    # 询问是否启动客户端
    read -p "是否现在启动客户端? [y/N]: " start_client
    if [[ $start_client =~ ^[Yy] ]]; then
        client_menu
    else
        print_info "代理服务器已在后台运行"
        print_info "使用 '$0 --client' 来启动客户端"
        print_info "使用 '$0 --stop' 来停止所有代理"
    fi
}

# 检查是否在正确的目录
if [ ! -d "agents" ] || [ ! -d "client" ]; then
    print_error "请在 examples/a2a 目录下运行此脚本"
    exit 1
fi

# 运行主函数
main "$@" 