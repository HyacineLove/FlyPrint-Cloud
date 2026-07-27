import React, { useCallback, useEffect, useState } from 'react';
import { Button, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag, message } from 'antd';
import { EditOutlined, KeyOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { buildApiUrl } from '../../config';
import { apiService } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';

interface ManagedUser {
  id: string;
  email: string;
  role: 'admin' | 'operator' | 'viewer' | string;
  status: 'active' | 'inactive' | string;
  last_login?: string;
  created_at?: string;
}

interface UserFormValues {
  email: string;
  password?: string;
  role: string;
  status?: string;
}

const roleOptions = [
  { value: 'admin', label: '管理员' },
  { value: 'operator', label: '运维人员' },
  { value: 'viewer', label: '普通用户' },
];

const formatDate = (value?: string) => (value ? new Date(value).toLocaleString() : '-');

const Users: React.FC = () => {
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [token, setToken] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<ManagedUser>();
  const [formVisible, setFormVisible] = useState(false);
  const [passwordUser, setPasswordUser] = useState<ManagedUser>();
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [form] = Form.useForm<UserFormValues>();
  const [passwordForm] = Form.useForm<{ new_password: string }>();

  const load = useCallback(async (accessToken: string) => {
    setLoading(true);
    try {
      const response = await fetch(buildApiUrl('/admin/users?page=1&page_size=100'), {
        headers: { Authorization: `Bearer ${accessToken}` },
      });
      const result = await response.json();
      if (!response.ok || result.code !== 200) {
        throw new Error(result.message || '加载用户失败');
      }
      setUsers(result.data?.items || []);
    } catch (error) {
      message.error(mapApiError(error, '加载用户失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void (async () => {
      const accessToken = await apiService.getToken();
      if (accessToken) {
        setToken(accessToken);
        await load(accessToken);
      } else {
        setLoading(false);
      }
    })();
  }, [load]);

  const openCreate = () => {
    setEditing(undefined);
    form.setFieldsValue({ email: '', password: '', role: 'viewer' });
    setFormVisible(true);
  };

  const openEdit = (user: ManagedUser) => {
    setEditing(user);
    form.setFieldsValue({ email: user.email, role: user.role, status: user.status });
    setFormVisible(true);
  };

  const save = async (values: UserFormValues) => {
    if (!token) return;
    const payload = editing
      ? { email: values.email.trim(), role: values.role, status: values.status }
      : { email: values.email.trim(), password: values.password, role: values.role };
    try {
      const response = await fetch(buildApiUrl(editing ? `/admin/users/${editing.id}` : '/admin/users'), {
        method: editing ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(payload),
      });
      const result = await response.json();
      if (!response.ok || (result.code !== 200 && result.code !== 201)) {
        throw new Error(result.message || '保存用户失败');
      }
      message.success(editing ? '用户已更新' : '用户已创建');
      setFormVisible(false);
      await load(token);
    } catch (error) {
      message.error(mapApiError(error, '保存用户失败'));
    }
  };

  const remove = async (user: ManagedUser) => {
    if (!token) return;
    try {
      const response = await fetch(buildApiUrl(`/admin/users/${user.id}`), {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      });
      const result = await response.json();
      if (!response.ok || result.code !== 200) {
        throw new Error(result.message || '删除用户失败');
      }
      message.success('用户已停用');
      await load(token);
    } catch (error) {
      message.error(mapApiError(error, '删除用户失败'));
    }
  };

  const changePassword = async (values: { new_password: string }) => {
    if (!token || !passwordUser) return;
    try {
      const response = await fetch(buildApiUrl(`/admin/users/${passwordUser.id}/password`), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify(values),
      });
      const result = await response.json();
      if (!response.ok || result.code !== 200) {
        throw new Error(result.message || '修改密码失败');
      }
      message.success('密码已修改');
      setPasswordVisible(false);
      passwordForm.resetFields();
    } catch (error) {
      message.error(mapApiError(error, '修改密码失败'));
    }
  };

  const columns: ColumnsType<ManagedUser> = [
    { title: '邮箱（登录名）', dataIndex: 'email', width: 260 },
    { title: '角色', dataIndex: 'role', render: (role) => roleOptions.find((item) => item.value === role)?.label || role },
    { title: '状态', dataIndex: 'status', render: (status) => <Tag color={status === 'active' ? 'green' : 'default'}>{status === 'active' ? '启用' : '停用'}</Tag> },
    { title: '最后登录', dataIndex: 'last_login', render: formatDate },
    {
      title: '操作',
      width: 220,
      render: (_, user) => (
        <Space>
          <Button icon={<EditOutlined />} onClick={() => openEdit(user)}>编辑</Button>
          <Button icon={<KeyOutlined />} onClick={() => { setPasswordUser(user); passwordForm.resetFields(); setPasswordVisible(true); }}>改密码</Button>
          <Popconfirm title="停用此用户？" onConfirm={() => remove(user)} okText="停用" cancelText="取消">
            <Button danger>停用</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div>
          <h2 style={{ marginBottom: 4 }}>用户管理</h2>
          <span style={{ color: '#666' }}>邮箱是唯一登录标识；用户名字段仅作为旧表兼容字段保留。</span>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建用户</Button>
      </div>
      <Table rowKey="id" loading={loading} dataSource={users} columns={columns} pagination={{ pageSize: 20, showSizeChanger: false }} scroll={{ x: 1000 }} />

      <Modal open={formVisible} title={editing ? '编辑用户' : '新建用户'} onCancel={() => setFormVisible(false)} onOk={() => form.submit()} destroyOnClose okText="保存" cancelText="取消">
        <Form form={form} layout="vertical" onFinish={save}>
          <Form.Item name="email" label="邮箱（登录名）" rules={[{ required: true, type: 'email', message: '请输入有效的邮箱' }]}>
            <Input type="email" autoComplete="email" />
          </Form.Item>
          {!editing ? (
            <Form.Item name="password" label="初始密码" rules={[{ required: true, min: 8, message: '密码至少需要 8 个字符' }]}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
          ) : null}
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select options={roleOptions} />
          </Form.Item>
          {editing ? (
            <Form.Item name="status" label="状态" rules={[{ required: true }]}>
              <Select options={[{ value: 'active', label: '启用' }, { value: 'inactive', label: '停用' }]} />
            </Form.Item>
          ) : null}
        </Form>
      </Modal>

      <Modal open={passwordVisible} title={`修改密码：${passwordUser?.email || ''}`} onCancel={() => setPasswordVisible(false)} onOk={() => passwordForm.submit()} destroyOnClose okText="保存" cancelText="取消">
        <Form form={passwordForm} layout="vertical" onFinish={changePassword}>
          <Form.Item name="new_password" label="新密码" rules={[{ required: true, min: 8, message: '密码至少需要 8 个字符' }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default Users;
