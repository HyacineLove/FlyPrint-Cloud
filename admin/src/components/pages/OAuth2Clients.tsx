import React, { useEffect, useState } from 'react';
import { Button, Form, Input, message, Modal, Popconfirm, Space, Table, Tag } from 'antd';
import { CopyOutlined, DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { apiService } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';

interface OAuth2Client {
  id: string;
  client_id: string;
  client_type: string;
  allowed_scopes: string;
  description: string;
  enabled: boolean;
}

interface ClientForm {
  client_id: string;
  client_type: string;
  description?: string;
  scopes: string;
}

const OAuth2Clients: React.FC = () => {
  const [clients, setClients] = useState<OAuth2Client[]>([]);
  const [loading, setLoading] = useState(true);
  const [modalVisible, setModalVisible] = useState(false);
  const [editingClient, setEditingClient] = useState<OAuth2Client | null>(null);
  const [copyingID, setCopyingID] = useState<string | null>(null);
  const [form] = Form.useForm<ClientForm>();

  const loadClients = async () => {
    setLoading(true);
    try {
      const result = await apiService.get<{ items?: OAuth2Client[] }>('/admin/oauth2-clients');
      if (result.code !== 200) throw new Error(result.message || '获取客户端列表失败');
      setClients(result.data?.items || []);
    } catch (error) {
      message.error(mapApiError(error, '获取客户端列表失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    const initialize = async () => {
      try {
        if (await apiService.getToken()) {
          await loadClients();
          return;
        }
      } catch (error) {
        console.error('初始化 OAuth2 客户端页面失败', error);
      }
      setLoading(false);
    };
    void initialize();
  }, []);

  const submitClient = async (values: ClientForm) => {
    if (!(await apiService.getToken())) return;
    const editing = Boolean(editingClient);
    const url = editing
      ? `/admin/oauth2-clients/${editingClient!.id}`
      : '/admin/oauth2-clients';
    const body = editing
      ? { allowed_scopes: values.scopes, description: values.description || '', enabled: editingClient!.enabled }
      : {
          client_id: values.client_id,
          client_type: values.client_type,
          allowed_scopes: values.scopes,
          description: values.description || '',
        };
    try {
      const result = await apiService.request<any>(url, {
        method: editing ? 'PUT' : 'POST',
        body: JSON.stringify(body),
      });
      if (result.code !== 200 && result.code !== 201) {
        throw new Error(result.message || (editing ? '更新失败' : '创建失败'));
      }
      message.success(editing ? '更新成功' : '创建成功，可使用“复制密钥”获取密钥');
      setModalVisible(false);
      setEditingClient(null);
      form.resetFields();
      await loadClients();
    } catch (error) {
      message.error(mapApiError(error, '操作失败'));
    }
  };

  const copySecret = async (client: OAuth2Client) => {
    if (copyingID) return;
    setCopyingID(client.id);
    try {
      const result = await apiService.request<any>(`/admin/oauth2-clients/${client.id}/secret/copy`, {
        method: 'POST',
        cache: 'no-store',
      });
      if (result.code !== 200 || !result.data?.client_secret) {
        throw new Error(result.message || '复制密钥失败');
      }
      await navigator.clipboard.writeText(result.data.client_secret);
      message.success('密钥已复制到剪贴板');
    } catch (error) {
      message.error(mapApiError(error, '复制密钥失败'));
    } finally {
      setCopyingID(null);
    }
  };

  const resetSecret = async (client: OAuth2Client) => {
    try {
      const result = await apiService.request<any>(`/admin/oauth2-clients/${client.id}/secret`, {
        method: 'PUT',
      });
      if (result.code !== 200) throw new Error(result.message || '重置失败');
      message.success('密钥已更新，可使用“复制密钥”获取新密钥');
    } catch (error) {
      message.error(mapApiError(error, '重置失败'));
    }
  };

  const deleteClient = async (client: OAuth2Client) => {
    try {
      const result = await apiService.request<any>(`/admin/oauth2-clients/${client.id}`, {
        method: 'DELETE',
      });
      if (result.code !== 200) throw new Error(result.message || '删除失败');
      message.success('删除成功');
      await loadClients();
    } catch (error) {
      message.error(mapApiError(error, '删除失败'));
    }
  };

  const openCreate = () => {
    setEditingClient(null);
    form.setFieldsValue({ client_id: '', client_type: 'edge_node', description: '', scopes: 'edge:register edge:heartbeat edge:printer' });
    setModalVisible(true);
  };

  const openEdit = (client: OAuth2Client) => {
    setEditingClient(client);
    form.setFieldsValue({
      client_id: client.client_id,
      client_type: client.client_type,
      description: client.description,
      scopes: client.allowed_scopes,
    });
    setModalVisible(true);
  };

  const columns: ColumnsType<OAuth2Client> = [
    { title: 'Client ID', dataIndex: 'client_id', width: 190, render: (value) => <code>{value}</code> },
    { title: '类型', dataIndex: 'client_type', width: 110 },
    { title: '描述', dataIndex: 'description', ellipsis: true },
    {
      title: 'Scopes', dataIndex: 'allowed_scopes', width: 240,
      render: (value: string) => <>{(value || '').split(' ').filter(Boolean).map((scope) => <Tag key={scope} color="blue">{scope}</Tag>)}</>,
    },
    { title: '状态', dataIndex: 'enabled', width: 80, render: (enabled) => <Tag color={enabled ? 'green' : 'red'}>{enabled ? '启用' : '禁用'}</Tag> },
    {
      title: '操作', key: 'action', width: 300, fixed: 'right',
      render: (_, client) => <Space size="small">
        <Button type="link" size="small" icon={<CopyOutlined />} loading={copyingID === client.id} onClick={() => void copySecret(client)}>复制密钥</Button>
        <Button type="link" size="small" icon={<EditOutlined />} onClick={() => openEdit(client)}>编辑</Button>
        <Popconfirm title="重置密钥" description="旧密钥将立即失效。重置后可使用“复制密钥”获取新密钥。" onConfirm={() => void resetSecret(client)} okText="确定" cancelText="取消">
          <Button type="link" size="small" icon={<ReloadOutlined />}>重置密钥</Button>
        </Popconfirm>
        <Popconfirm title="删除客户端" description="删除后无法恢复。" onConfirm={() => void deleteClient(client)} okText="确定" cancelText="取消">
          <Button type="link" size="small" danger icon={<DeleteOutlined />}>删除</Button>
        </Popconfirm>
      </Space>,
    },
  ];

  return <div>
    <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
      <h2 style={{ margin: 0 }}>OAuth2 客户端管理</h2>
      <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>创建客户端</Button>
    </div>
    <Table columns={columns} dataSource={clients} rowKey="id" loading={loading} pagination={{ pageSize: 10 }} scroll={{ x: 1000 }} />
    <Modal title={editingClient ? '编辑客户端' : '创建客户端'} open={modalVisible} onCancel={() => setModalVisible(false)} footer={null} destroyOnClose>
      <Form form={form} layout="vertical" onFinish={submitClient}>
        <Form.Item name="client_id" label="Client ID" rules={[{ required: true, message: '请输入 Client ID' }, { pattern: /^[a-z0-9-]+$/, message: '只能包含小写字母、数字和横线' }]}>
          <Input disabled={Boolean(editingClient)} />
        </Form.Item>
        <Form.Item name="client_type" label="客户端类型" rules={[{ required: true }]}><Input disabled={Boolean(editingClient)} /></Form.Item>
        <Form.Item name="description" label="描述"><Input.TextArea rows={2} /></Form.Item>
        <Form.Item name="scopes" label="Scopes" extra="多个 scope 使用空格分隔" rules={[{ required: true, message: '请输入 Scopes' }]}><Input /></Form.Item>
        <Space><Button type="primary" htmlType="submit">{editingClient ? '保存' : '创建'}</Button><Button onClick={() => setModalVisible(false)}>取消</Button></Space>
      </Form>
    </Modal>
  </div>;
};

export default OAuth2Clients;
