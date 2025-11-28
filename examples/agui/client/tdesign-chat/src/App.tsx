import React, { useRef, useState, useMemo, useCallback } from 'react';
import { LoadingIcon, CheckCircleFilledIcon, CloseCircleFilledIcon } from 'tdesign-icons-react';
import {
  ChatList,
  ChatSender,
  ChatMessage,
  ChatActionBar,
  isAIMessage,
  ToolCallRenderer,
  useChat,
  useAgentToolcall,
} from '@tdesign-react/chat';
import type {
  TdChatMessageConfig,
  TdChatSenderParams,
  ChatMessagesData,
  ChatRequestParams,
  AIMessageContent,
  ToolcallComponentProps,
  TdChatActionsName,
} from '@tdesign-react/chat';

// 默认提示语
const DEFAULT_PROMPT = 'Calculate 2*(10+11), first explain the idea, then calculate, and finally give the conclusion.';

// ==================== 类型定义 ====================

interface CalculatorArgs {
  operation: string;
  a: number;
  b: number;
}

interface CalculatorResult {
  result: number;
}

// ==================== 工具组件 ====================

const CalculatorTool: React.FC<ToolcallComponentProps<CalculatorArgs, CalculatorResult>> = ({
  status,
  args,
  result,
  error,
}) => {
  const operatorMap: Record<string, string> = {
    add: '+',
    subtract: '-',
    multiply: '×',
    divide: '÷',
    power: '^',
    '+': '+',
    '-': '-',
    '*': '×',
    '/': '÷',
    '^': '^',
  };

  const getOperatorSymbol = () => {
    if (!args?.operation) return '';
    return operatorMap[args.operation] || args.operation;
  };

  const getStatusBadge = () => {
    if (status === 'executing') {
      return (
        <span className="status-badge status-badge--executing">
          <LoadingIcon />
          计算中...
        </span>
      );
    }
    if (status === 'complete') {
      return (
        <span className="status-badge status-badge--complete">
          <CheckCircleFilledIcon />
          计算完成
        </span>
      );
    }
    if (status === 'error') {
      return (
        <span className="status-badge status-badge--error">
          <CloseCircleFilledIcon />
          计算失败
        </span>
      );
    }
    return null;
  };

  const cardClassName = `tool-card ${status === 'executing' ? 'tool-card--executing' : ''} ${
    status === 'error' ? 'tool-card--error' : ''
  }`;

  return (
    <div className={cardClassName}>
      <div className="tool-card__header">
        <span className="tool-card__icon">🧮</span>
        <span>计算器工具</span>
        {getStatusBadge()}
      </div>

      {args && (
        <div className="tool-card__body">
          <div className="tool-card__args">
            <div style={{ fontSize: '14px', marginBottom: '4px' }}>
              <strong>计算表达式：</strong>
            </div>
            <div style={{ fontSize: '18px', fontWeight: 600, color: '#0052d9' }}>
              {args.a} {getOperatorSymbol()} {args.b}
            </div>
          </div>
        </div>
      )}

      {status === 'complete' && result && (
        <div className="tool-card__result">
          <div className="tool-card__result-label">计算结果</div>
          <div className="tool-card__result-value">= {result.result}</div>
        </div>
      )}

      {status === 'error' && error && (
        <div className="tool-card__error">❌ 错误: {error.message || '计算失败'}</div>
      )}
    </div>
  );
};

// ==================== 主组件 ====================

export default function App() {
  const listRef = useRef<any>(null);
  const [inputValue, setInputValue] = useState<string>(DEFAULT_PROMPT);
  // 保持会话ID，用于多轮对话
  const [threadId] = useState(() => `session-${Date.now()}`);

  // 注册计算器工具
  useAgentToolcall([
    {
      name: 'calculator',
      description: '计算器工具，支持加减乘除和幂运算',
      parameters: [
        { name: 'operation', type: 'string', required: true },
        { name: 'a', type: 'number', required: true },
        { name: 'b', type: 'number', required: true },
      ],
      component: CalculatorTool,
    },
  ]);

  // 聊天配置
  const { chatEngine, messages, status } = useChat({
    defaultMessages: [],
    chatServiceConfig: {
      endpoint: 'http://127.0.0.1:8080/agui',
      protocol: 'agui',
      stream: true,
      onRequest: (params: ChatRequestParams) => {
        // 生成唯一的 run ID
        const runId = `run-${Date.now()}`;
        
        return {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            threadId: threadId,  // 会话ID（保持不变）
            runId: runId,        // 运行ID（每次请求唯一）
            messages: [
              {
                role: 'user',
                content: params.prompt,
              }
            ],
          }),
        };
      },
      onError: (err: Error | Response) => {
        console.error('AG-UI 服务错误:', err);
      },
    },
  });

  const senderLoading = useMemo(() => status === 'pending' || status === 'streaming', [status]);

  // 消息配置
  const messageProps: TdChatMessageConfig = {
    user: {
      variant: 'base',
      placement: 'right',
      avatar: 'https://tdesign.gtimg.com/site/avatar.jpg',
    },
    assistant: {
      placement: 'left',
      avatar: 'https://tdesign.gtimg.com/site/chat-avatar.png',
    },
  };

  // 操作栏配置
  const getActionBar = (isLast: boolean): TdChatActionsName[] => {
    const actions: TdChatActionsName[] = ['good', 'bad', 'copy'];
    if (isLast) actions.unshift('replay');
    return actions;
  };

  // 操作处理
  const handleAction = useCallback(
    (name: string) => {
      if (name === 'replay') {
        chatEngine.regenerateAIMessage();
      }
    },
    [chatEngine]
  );

  // 渲染消息内容
  const renderMessageContent = useCallback((item: AIMessageContent, index: number, isLast: boolean) => {
    // 工具调用渲染
    if (item.type === 'toolcall' || item.type.startsWith('toolcall-')) {
      return (
        <div slot={`${item.type}-${index}`} key={`toolcall-${index}`}>
          <ToolCallRenderer toolCall={item.data} />
        </div>
      );
    }
    return null;
  }, []);

  // 渲染消息体
  const renderMsgContents = useCallback(
    (message: ChatMessagesData, isLast: boolean) => (
      <>
        {message.content?.map((item, index) => renderMessageContent(item as AIMessageContent, index, isLast))}
        {isAIMessage(message) && message.status === 'complete' && (
          <ChatActionBar slot="actionbar" actionBar={getActionBar(isLast)} handleAction={handleAction} />
        )}
      </>
    ),
    [renderMessageContent, handleAction]
  );

  // 发送消息
  const handleSend = async (e: CustomEvent<TdChatSenderParams>) => {
    const { value } = e.detail;
    await chatEngine.sendUserMessage({ prompt: value });
    setInputValue('');
    setTimeout(() => {
      listRef.current?.scrollList({ to: 'bottom' });
    }, 100);
  };

  return (
    <div className="agui-chat">
      <div className="agui-chat__container">
        <header className="agui-chat__header">
          <h1 className="agui-chat__title">AG-UI + TDesign Chat Demo</h1>
          <p className="agui-chat__subtitle">与 Go AG-UI 服务进行对话 | 支持工具调用 | 支持流式响应</p>
        </header>

        <div className="agui-chat__content">
          <ChatList ref={listRef}>
            {messages.map((message, idx) => (
              <ChatMessage key={message.id} {...messageProps[message.role]} message={message}>
                {renderMsgContents(message, idx === messages.length - 1)}
              </ChatMessage>
            ))}
          </ChatList>

          <ChatSender
            value={inputValue}
            placeholder={DEFAULT_PROMPT}
            loading={senderLoading}
            onChange={(e: CustomEvent) => setInputValue(e.detail)}
            onSend={handleSend as any}
            onStop={() => chatEngine.abortChat()}
          />
        </div>
      </div>
    </div>
  );
}
