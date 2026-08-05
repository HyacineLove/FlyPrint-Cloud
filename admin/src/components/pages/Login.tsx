import React, { useEffect, useState } from 'react';
import { Form, Input, Button, Card, message, Typography, Spin } from 'antd';
import { MailOutlined, LockOutlined } from '@ant-design/icons';
import { Link, useSearchParams } from 'react-router-dom';
import { buildAuthUrl, buildAppPath } from '../../config';
import { apiService } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';

const { Title, Text } = Typography;

interface LoginForm {
  email: string;
  password: string;
}

const Login: React.FC = () => {
  const [searchParams] = useSearchParams();
  const [loading, setLoading] = useState(false);
  const [checkingMode, setCheckingMode] = useState(true);

  useEffect(() => {
    fetch(buildAuthUrl('mode'))
      .then((response) => response.json())
      .then((data) => {
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
      // OAuth2 的标准 username 字段在 builtin 模式下承载邮箱登录名。
      formData.append('username', values.email.trim());
      formData.append('password', values.password);

      const response = await fetch(buildAuthUrl('token'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: formData.toString(),
      });
      const result = await response.json();

      if (response.ok && result.access_token) {
        apiService.setToken(result.access_token);
        const expiresDate = new Date(Date.now() + (result.expires_in || 3600) * 1000);
        document.cookie = `access_token=${result.access_token}; path=/; expires=${expiresDate.toUTCString()}`;
        message.success('登录成功');
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

  if (checkingMode) {
    return <div className="fp-auth-shell"><Spin size="large" /></div>;
  }

  const registerPath = `/register${searchParams.get('return_to') ? `?return_to=${encodeURIComponent(searchParams.get('return_to') || '')}` : ''}`;

  return (
    <div className="fp-auth-shell">
      <Card className="fp-auth-card">
        <div className="fp-auth-heading">
          <Title level={3} style={{ marginBottom: 8, color: '#0b1f3a' }}>飞印服务管理中心</Title>
          <Text type="secondary">Cloud 管理端登录</Text>
        </div>
        <Form name="login" onFinish={onFinish} size="large" autoComplete="off">
          <Form.Item name="email" rules={[{ required: true, type: 'email', message: '请输入有效的邮箱' }]}>
            <Input prefix={<MailOutlined />} type="email" autoComplete="email" placeholder="邮箱" />
          </Form.Item>
          <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password prefix={<LockOutlined />} autoComplete="current-password" placeholder="密码" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading} block>登录</Button>
          </Form.Item>
          <div style={{ textAlign: 'center' }}><Link to={registerPath}>注册官方账号</Link></div>
        </Form>
      </Card>
    </div>
  );
};

export default Login;
