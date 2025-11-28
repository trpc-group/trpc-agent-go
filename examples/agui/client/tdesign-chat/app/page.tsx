//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

import dynamic from 'next/dynamic';

// 动态导入客户端组件，完全禁用 SSR
const ChatClient = dynamic(() => import('./chat-client'), {
  ssr: false,
  loading: () => (
    <div className="agui-chat">
      <div className="agui-chat__container">
        <header className="agui-chat__header">
          <h1 className="agui-chat__title">AG-UI TDesign Chat Demo</h1>
          <p className="agui-chat__subtitle">正在加载...</p>
        </header>
      </div>
    </div>
  ),
});

export default function Home() {
  return <ChatClient />;
}
