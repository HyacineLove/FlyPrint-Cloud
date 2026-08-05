import React from 'react';
import ReactDOM from 'react-dom/client';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import App from './App';
import './theme.css';

const root = ReactDOM.createRoot(
  document.getElementById('root') as HTMLElement
);
root.render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#1268e8',
          colorInfo: '#1268e8',
          colorBgLayout: '#f4f7fb',
          colorText: '#172033',
          colorTextSecondary: '#697386',
          colorBorder: '#e1e9f4',
          borderRadius: 12,
          fontFamily: 'Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "Microsoft YaHei", sans-serif',
        },
        components: {
          Layout: { headerBg: '#ffffff', siderBg: '#0b1f3a' },
          Menu: { darkItemBg: '#0b1f3a', darkItemSelectedBg: '#1268e8' },
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>
);
