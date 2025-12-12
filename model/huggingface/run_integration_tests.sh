#!/bin/bash

# HuggingFace 集成测试运行脚本
# 使用方式：
#   export HUGGINGFACE_API_KEY=your_api_key
#   ./run_integration_tests.sh

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}  HuggingFace Integration Tests Runner${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# 检查 API Key
if [ -z "$HUGGINGFACE_API_KEY" ]; then
    echo -e "${RED}❌ Error: HUGGINGFACE_API_KEY is not set${NC}"
    echo ""
    echo "Please set your HuggingFace API key:"
    echo -e "  ${YELLOW}export HUGGINGFACE_API_KEY=your_api_key${NC}"
    echo ""
    echo "Get your API key from: https://huggingface.co/settings/tokens"
    exit 1
fi

# 显示配置信息
echo -e "${GREEN}✓${NC} API Key found: ${HUGGINGFACE_API_KEY:0:10}..."
echo ""

# 显示测试模型
if [ -n "$HUGGINGFACE_TEST_MODEL" ]; then
    echo -e "${GREEN}✓${NC} Using custom model: ${YELLOW}$HUGGINGFACE_TEST_MODEL${NC}"
else
    echo -e "${GREEN}✓${NC} Using default model: ${YELLOW}microsoft/DialoGPT-small${NC}"
    echo -e "  ${BLUE}(Set HUGGINGFACE_TEST_MODEL to use a different model)${NC}"
fi
echo ""

# 询问运行哪些测试
echo "Select tests to run:"
echo "  1) All integration tests"
echo "  2) Non-streaming test only"
echo "  3) Streaming test only"
echo "  4) Callbacks test only"
echo "  5) Run all tests (including unit tests)"
echo ""
read -p "Enter your choice (1-5) [default: 1]: " choice
choice=${choice:-1}

echo ""
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}  Running Tests...${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# 设置环境变量
export RUN_INTEGRATION_TESTS=true

# 根据选择运行测试
case $choice in
    1)
        echo -e "${YELLOW}Running all integration tests...${NC}"
        go test -v -timeout 5m -run TestIntegration ./model/huggingface/...
        ;;
    2)
        echo -e "${YELLOW}Running non-streaming test...${NC}"
        go test -v -timeout 5m -run TestIntegration_RealAPI_NonStreaming ./model/huggingface/...
        ;;
    3)
        echo -e "${YELLOW}Running streaming test...${NC}"
        go test -v -timeout 5m -run TestIntegration_RealAPI_Streaming ./model/huggingface/...
        ;;
    4)
        echo -e "${YELLOW}Running callbacks test...${NC}"
        go test -v -timeout 5m -run TestIntegration_RealAPI_WithCallbacks ./model/huggingface/...
        ;;
    5)
        echo -e "${YELLOW}Running all tests (unit + integration)...${NC}"
        go test -v -timeout 5m ./model/huggingface/...
        ;;
    *)
        echo -e "${RED}Invalid choice. Exiting.${NC}"
        exit 1
        ;;
esac

# 检查测试结果
if [ $? -eq 0 ]; then
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}✅ All tests passed successfully!${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
else
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${RED}❌ Some tests failed. Please check the output above.${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    exit 1
fi
