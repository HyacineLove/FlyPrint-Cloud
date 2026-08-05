import React, { useState } from 'react';
import { Form, Input, Button, Card, message, Typography } from 'antd';
import { Link, useSearchParams } from 'react-router-dom';
import { buildAppPath, buildAuthUrl } from '../../config';
import { apiService } from '../../services/api';

const { Title, Text } = Typography;

const Register: React.FC = () => {
  const [loading, setLoading] = useState(false);
  const [searchParams] = useSearchParams();
  const returnTo = searchParams.get('return_to');

  const onFinish = async (values: { email: string; password: string }) => {
    setLoading(true);
    try {
      const response = await fetch(buildAuthUrl('register'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(values),
      });
      const result = await response.json();
      if (!response.ok || !result.access_token) {
        message.error(result.message || '注册失败');
        return;
      }
      apiService.setToken(result.access_token);
      const expiresDate = new Date(Date.now() + (result.expires_in || 3600) * 1000);
      document.cookie = `access_token=${result.access_token}; path=/; expires=${expiresDate.toUTCString()}`;
      window.location.href = returnTo && returnTo.startsWith('/') ? returnTo : buildAppPath('/');
    } catch {
      message.error('网络错误，请稍后重试');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fp-auth-shell">
      <Card className="fp-auth-card">
        <Title level={3}>注册官方账号</Title>
        <Text type="secondary">注册后可在扫码终端上传并确认打印</Text>
        <Form layout="vertical" size="large" onFinish={onFinish} style={{ marginTop: 24 }}>
          <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入有效的邮箱' }]}>
            <Input type="email" autoComplete="email" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true, min: 8, message: '密码至少需要 8 个字符' }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading} block>注册并登录</Button>
          </Form.Item>
        </Form>
        <div style={{ textAlign: 'center' }}>
          <Link to={`/login${returnTo ? `?return_to=${encodeURIComponent(returnTo)}` : ''}`}>已有账号，返回登录</Link>
        </div>
      </Card>
    </div>
  );
};

export default Register;
