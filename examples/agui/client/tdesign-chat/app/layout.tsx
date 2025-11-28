//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

import type { Metadata } from 'next';
import 'tdesign-react/es/style/index.css';
import '@tdesign-react/chat/es/style/index.js';
import './globals.css';

export const metadata: Metadata = {
  title: 'AG-UI TDesign Chat Demo',
  description: 'TDesign Chat front-end that streams AG-UI events from a Go agent server.',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
