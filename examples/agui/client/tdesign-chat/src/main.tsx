import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import 'tdesign-react/es/style/index.css';
import '@tdesign-react/chat/es/style/index.js';
import './index.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
