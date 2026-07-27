import React, { useState, useEffect } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom';
import { Layout, Menu, Avatar, Dropdown, Space } from 'antd';
import { 
  DashboardOutlined, 
  PrinterOutlined, 
  CloudServerOutlined, 
  FileTextOutlined,
  LogoutOutlined,
  ControlOutlined,
  ApiOutlined,
  TeamOutlined,
  UserOutlined
} from '@ant-design/icons';
import type { MenuProps } from 'antd';

// 导入页面组件
import Dashboard from './components/pages/Dashboard';
import EdgeNodes from './components/pages/EdgeNodes';
import Printers from './components/pages/Printers';
import PrintJobs from './components/pages/PrintJobs';
import PublicUpload from './components/pages/PublicUpload';
import Login from './components/pages/Login';
import Register from './components/pages/Register';
import BusinessSettings from './components/pages/BusinessSettings';
import IntegrationProviders from './components/pages/IntegrationProviders';
import OpsContacts from './components/pages/OpsContacts';
import Users from './components/pages/Users';

// 导入错误边界和工具
import ErrorBoundary from './components/ErrorBoundary';
import Loading from './components/Loading';
import { buildAuthUrl, buildAppPath, APP_BASENAME } from './config';

const { Header, Sider, Content } = Layout;

interface User {
  id: string;
  username: string;
  email: string;
  role: string;
  status: string;
}

// 管理后台主应用组件 (Admin App)
const AdminApp: React.FC = () => {
  const navigate = useNavigate();
  const location = useLocation();
  
  const [collapsed, setCollapsed] = useState(false);
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  // 获取当前用户信息
  useEffect(() => {
    const getCurrentUser = async () => {
      try {
        const response = await fetch(buildAuthUrl('me'));
        
        // 检查 HTTP 状态码，如果是 401 未授权，直接跳转登录
        if (response.status === 401 || !response.ok) {
          window.location.href = buildAppPath('/login');
          return;
        }
        
        const result = await response.json();
        
        if (result.code === 200 && result.data) {
          setUser({
            id: result.data.user_id || '1',
            username: result.data.preferred_username || result.data.username || 'n/a',
            email: result.data.email || 'admin@example.com',
            role: 'admin',
            status: 'active'
          });
        } else {
          // 如果获取用户信息失败，重定向到登录页面
          window.location.href = buildAppPath('/login');
        }
      } catch (error) {
        console.error('获取用户信息失败:', error);
        // 任何错误都重定向到登录页面（无需延迟）
        window.location.href = buildAppPath('/login');
      } finally {
        setLoading(false);
      }
    };

    getCurrentUser();
  }, []);

  // 处理登出
  const handleLogout = async () => {
    try {
      await fetch(buildAuthUrl('logout'), { method: 'POST' });
    } catch (error) {
      console.error('登出失败:', error);
    } finally {
      window.location.href = buildAppPath('/login');
    }
  };

  // 菜单项配置
  const menuItems: MenuProps['items'] = [
    {
      key: '/',
      icon: <DashboardOutlined />,
      label: '总仪表盘',
    },
    {
      key: '/edge-nodes',
      icon: <CloudServerOutlined />,
      label: '节点信息',
    },
    {
      key: '/ops-contacts',
      icon: <TeamOutlined />,
      label: '运维人员',
    },
    {
      key: '/users',
      icon: <UserOutlined />,
      label: '用户管理',
    },
    {
      key: '/printers',
      icon: <PrinterOutlined />,
      label: '打印机信息',
    },
    {
      key: '/print-jobs',
      icon: <FileTextOutlined />,
      label: '打印任务',
    },
    {
      key: '/business-settings',
      icon: <ControlOutlined />,
      label: '业务配置',
    },
    {
      key: '/integration-providers',
      icon: <ApiOutlined />,
      label: '三方接入',
    },
  ];

  // 用户下拉菜单（个人资料/系统设置仍为占位，不提供入口以免误导）
  const userMenuItems: MenuProps['items'] = [
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: '退出登录',
      onClick: handleLogout,
    },
  ];

  const handleMenuClick = (e: any) => {
    navigate(e.key);
  };

  if (loading) {
    return <Loading fullscreen tip="加载用户信息..." />;
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        style={{
          position: 'fixed',
          height: '100vh',
          left: 0,
          top: 0,
          bottom: 0,
        }}
      >
        <div style={{
          height: 48,
          margin: 16,
          background: 'rgba(255, 255, 255, 0.3)',
          borderRadius: 6,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          color: 'white',
          fontWeight: 'bold',
          whiteSpace: 'nowrap',
          fontSize: 16,
        }}>
          {collapsed ? 'FP' : '飞印服务管理中心'}
        </div>
        <Menu
          theme="dark"
          selectedKeys={[location.pathname]}
          mode="inline"
          items={menuItems}
          onClick={handleMenuClick}
        />
      </Sider>

      <Layout style={{ marginLeft: collapsed ? 80 : 200, transition: 'margin-left 0.2s' }}>
        <Header style={{
          background: '#fff',
          padding: '0 24px',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          boxShadow: '0 1px 4px rgba(0,21,41,.08)',
          position: 'sticky',
          top: 0,
          zIndex: 1,
        }}>
          <div />
          <Space>
            <span>欢迎, {user?.username}</span>
            <Dropdown menu={{ items: userMenuItems }} placement="bottomRight">
              <Avatar style={{ cursor: 'pointer' }}>
                {user?.username?.charAt(0).toUpperCase()}
              </Avatar>
            </Dropdown>
          </Space>
        </Header>

        <Content style={{
          margin: '24px',
          minHeight: 'calc(100vh - 112px)',
        }}>
          <div style={{
            background: '#fff',
            padding: 24,
            borderRadius: 8,
            boxShadow: '0 1px 3px rgba(0, 0, 0, 0.1)',
            minHeight: 'calc(100vh - 160px)',
          }}>
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/edge-nodes" element={<EdgeNodes />} />
              <Route path="/ops-contacts" element={<OpsContacts />} />
              <Route path="/users" element={<Users />} />
              <Route path="/printers" element={<Printers />} />
              <Route path="/print-jobs" element={<PrintJobs />} />
              <Route path="/business-settings" element={<BusinessSettings />} />
              <Route path="/integration-providers" element={<IntegrationProviders />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </div>
        </Content>
      </Layout>
    </Layout>
  );
};

// App 根组件
const App: React.FC = () => {
  return (
    <ErrorBoundary>
      <Router basename={APP_BASENAME || undefined}>
        <Routes>
          {/* 独立的文件上传页面，不需要 Admin 登录 */}
          <Route path="/upload" element={<PublicUpload />} />
          
          {/* 登录页面 (builtin OAuth2 模式) */}
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
          
          {/* 其他路由都进入管理后台应用 (需要登录) */}
          <Route path="/*" element={<AdminApp />} />
        </Routes>
      </Router>
    </ErrorBoundary>
  );
};

export default App;
