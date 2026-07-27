import React, { useState, useEffect } from 'react';
import { Form, Input, Button, Card, message, Typography, Spin } from 'antd';
import { UserOutlined, LockOutlined } from '@ant-design/icons';
import { Link, useSearchParams } from 'react-router-dom';
import { buildAuthUrl, buildAppPath } from '../../config';
import { apiService } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';

const { Title, Text } = Typography;

const loginShellStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'center',
  alignItems: 'center',
  minHeight: '100vh',
  background: 'linear-gradient(160deg, #0b1f3a 0%, #1268e8 55%, #3d8bfd 100%)',
};

interface LoginForm {
  username: string;
  password: string;
}

const Login: React.FC = () => {
	const [searchParams] = useSearchParams();
  const [loading, setLoading] = useState(false);
  const [checkingMode, setCheckingMode] = useState(true);

  // 检查认证模式，keycloak 模式直接跳转到 OAuth2 登录
  useEffect(() => {
    fetch(buildAuthUrl('mode'))
      .then(r => r.json())
      .then(data => {
        if (data.mode === 'keycloak') {
          window.location.href = buildAuthUrl('login');
        } else {
          setCheckingMode(false);
        }
      })
      .catch(() => setCheckingMode(false));
  }, []);

  const onFinish = async (values: LoginForm) => {
    setLoading(true);
    try {
      const formData = new URLSearchParams();
      formData.append('grant_type', 'password');
      formData.append('username', values.username);
      formData.append('password', values.password);

      const response = await fetch(buildAuthUrl('token'), {
        method: 'POST',
        headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
        },
        body: formData.toString(),
      });

      const result = await response.json();

      if (response.ok && result.access_token) {
		apiService.setToken(result.access_token);
        // Token is set via Set-Cookie header by backend, or we store it
        // For builtin mode, we need to set the cookie manually
        const expiresDate = new Date(Date.now() + (result.expires_in || 3600) * 1000);
        document.cookie = `access_token=${result.access_token}; path=/; expires=${expiresDate.toUTCString()}`;
        
        message.success('登录成功');
        // Redirect to dashboard（子路径部署时带 basename）
        const returnTo = searchParams.get('return_to');
        window.location.href = returnTo && returnTo.startsWith('/') ? returnTo : buildAppPath('/');
      } else {
        message.error(mapApiError(result, '登录失败'));
      }
    } catch (error) {
      console.error('登录错误:', error);
      message.error(mapApiError(error, '网络错误，请稍后重试'));
    } finally {
      setLoading(false);
    }
  };

  // 检查模式中，显示加载状态
  if (checkingMode) {
    return (
      <div style={loginShellStyle}>
        <Spin size="large" />
      </div>
    );
  }

  return (
    <div style={loginShellStyle}>
      <Card
        style={{
          width: 400,
          boxShadow: '0 12px 40px rgba(11, 31, 58, 0.28)',
          borderRadius: 12,
          border: 'none',
        }}
      >
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={3} style={{ marginBottom: 8, color: '#0b1f3a' }}>飞印服务管理中心</Title>
          <Text type="secondary">Cloud 管理端登录</Text>
        </div>

        <Form
          name="login"
          onFinish={onFinish}
          size="large"
          autoComplete="off"
        >
          <Form.Item
            name="username"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input
              prefix={<UserOutlined />}
              placeholder="用户名"
            />
          </Form.Item>

          <Form.Item
            name="password"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="密码"
            />
          </Form.Item>

          <Form.Item>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              block
            >
              登录
            </Button>
          </Form.Item>

			<div style={{ textAlign: 'center' }}>
				<Link to={`/register${searchParams.get('return_to') ? `?return_to=${encodeURIComponent(searchParams.get('return_to') || '')}` : ''}`}>注册官方账号</Link>
			</div>
        </Form>
      </Card>
    </div>
  );
};

export default Login;
