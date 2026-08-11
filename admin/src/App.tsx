import React, { useState, useEffect } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom';
import { Layout, Menu, Avatar, Dropdown, Space, Typography } from 'antd';
import { 
  DashboardOutlined, 
  PrinterOutlined, 
  CloudServerOutlined, 
  FileTextOutlined,
  LogoutOutlined,
  ControlOutlined,
  TeamOutlined,
  UserOutlined,
  ApartmentOutlined,
  DownOutlined,
} from '@ant-design/icons';
import type { MenuProps } from 'antd';

// 导入页面组件
import Dashboard from './components/pages/Dashboard';
import EdgeNodes from './components/pages/EdgeNodes';
import Printers from './components/pages/Printers';
import PrintJobs from './components/pages/PrintJobs';
import PublicUpload from './components/pages/PublicUpload';
import Login from './components/pages/Login';
import BusinessSettings from './components/pages/BusinessSettings';
import SitePortals from './components/pages/SitePortals';
import OpsContacts from './components/pages/OpsContacts';
import Users from './components/pages/Users';

// 导入错误边界和工具
import ErrorBoundary from './components/ErrorBoundary';
import Loading from './components/Loading';
import { buildAuthUrl, buildAppPath, APP_BASENAME } from './config';

const { Header, Sider, Content } = Layout;
const { Text, Title } = Typography;

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

  const pageTitles: Record<string, string> = {
    '/': '总仪表盘',
    '/edge-nodes': 'Edge 节点',
    '/ops-contacts': '运维人员',
    '/users': '用户管理',
    '/printers': '打印机',
    '/print-jobs': '打印任务',
    '/business-settings': '业务配置',
    '/site-portals': 'Site Portal',
  };
  const currentPageTitle = pageTitles[location.pathname] || '管理中心';

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
      type: 'group',
      label: '运营',
      children: [
        { key: '/', icon: <DashboardOutlined />, label: '总仪表盘' },
        { key: '/edge-nodes', icon: <CloudServerOutlined />, label: 'Edge 节点' },
        { key: '/printers', icon: <PrinterOutlined />, label: '打印机' },
        { key: '/print-jobs', icon: <FileTextOutlined />, label: '打印任务' },
      ],
    },
    {
      type: 'group',
      label: '账号与入口',
      children: [
        { key: '/site-portals', icon: <ApartmentOutlined />, label: 'Site Portal' },
        { key: '/users', icon: <UserOutlined />, label: '用户管理' },
        { key: '/ops-contacts', icon: <TeamOutlined />, label: '运维人员' },
      ],
    },
    {
      type: 'group',
      label: '系统',
      children: [
        { key: '/business-settings', icon: <ControlOutlined />, label: '业务配置' },
      ],
    },
  ];

  // 用户下拉菜单（个人资料/系统设置仍为占位，不提供入口以免误导）
  const userMenuItems: MenuProps['items'] = [
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: '退出当前账号',
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
    <Layout className="fp-admin-layout" style={{ minHeight: '100vh' }}>
      <Sider
        className="fp-admin-sider"
        collapsible
        breakpoint="lg"
        width={224}
        collapsedWidth={72}
        collapsed={collapsed}
        onCollapse={setCollapsed}
        onBreakpoint={broken => setCollapsed(broken)}
        style={{
          position: 'fixed',
          height: '100vh',
          left: 0,
          top: 0,
          bottom: 0,
        }}
      >
        <div className="fp-admin-brand">
          <span className="fp-admin-brand-mark">FP</span>
          {!collapsed && <span className="fp-admin-brand-copy"><strong>飞印 Cloud</strong><small>打印运营控制台</small></span>}
        </div>
        <Menu
          theme="dark"
          selectedKeys={[location.pathname]}
          mode="inline"
          items={menuItems}
          onClick={handleMenuClick}
        />
      </Sider>

      <Layout className="fp-admin-main" style={{ marginLeft: collapsed ? 72 : 224, transition: 'margin-left 0.2s' }}>
        <Header className="fp-admin-header" style={{
          padding: '0 28px',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          position: 'sticky',
          top: 0,
          zIndex: 1,
        }}>
          <div className="fp-header-context">
            <Text className="fp-header-kicker">Cloud 管理中心</Text>
            <Title level={5} className="fp-header-title">{currentPageTitle}</Title>
          </div>
          <Dropdown menu={{ items: userMenuItems }} placement="bottomRight">
            <Space className="fp-user-menu" size={10}>
              <Avatar className="fp-user-avatar">
                {user?.username?.charAt(0).toUpperCase()}
              </Avatar>
              <span className="fp-user-meta">
                <strong>{user?.username}</strong>
                <small>管理员</small>
              </span>
              <DownOutlined className="fp-user-chevron" />
            </Space>
          </Dropdown>
        </Header>

        <Content className="fp-admin-content">
          <div className="fp-admin-surface">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/edge-nodes" element={<EdgeNodes />} />
              <Route path="/ops-contacts" element={<OpsContacts />} />
              <Route path="/users" element={<Users />} />
              <Route path="/printers" element={<Printers />} />
              <Route path="/print-jobs" element={<PrintJobs />} />
              <Route path="/business-settings" element={<BusinessSettings />} />
              <Route path="/site-portals" element={<SitePortals />} />
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
          
          {/* 其他路由都进入管理后台应用 (需要登录) */}
          <Route path="/*" element={<AdminApp />} />
        </Routes>
      </Router>
    </ErrorBoundary>
  );
};

export default App;
