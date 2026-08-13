import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Form, Input, Modal, Popconfirm, Select, Space, Switch, Table, message } from 'antd';
import { EditOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { apiService } from '../../services/api';
import type { ApiResponse } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';

interface OpsContact {
  id: string;
  name: string;
  phone: string;
  enabled: boolean;
  node_ids?: string[];
}

interface EdgeNodeOption {
  id: string;
  name: string;
  alias?: string;
}

interface ContactForm {
  name: string;
  phone: string;
  enabled: boolean;
  node_ids: string[];
}

const OpsContacts: React.FC = () => {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const nodeFilter = searchParams.get('node_id') || '';
  const [contacts, setContacts] = useState<OpsContact[]>([]);
  const [nodes, setNodes] = useState<EdgeNodeOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<OpsContact | undefined>();
  const [formVisible, setFormVisible] = useState(false);
  const [form] = Form.useForm<ContactForm>();

  const nodeLabel = useMemo(() => {
    const map = Object.fromEntries(nodes.map((node) => [node.id, node.alias || node.name || node.id]));
    return (id: string) => map[id] || id;
  }, [nodes]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const query = new URLSearchParams({ page: '1', page_size: '100' });
      if (nodeFilter) {
        query.set('node_id', nodeFilter);
      }
      const [contactsResponse, nodesResponse] = await Promise.all([
        apiService.get<{ items: OpsContact[] }>(`/admin/ops-contacts?${query}`),
        apiService.get<{ items: EdgeNodeOption[] }>('/admin/edge-nodes?page=1&page_size=100'),
      ]);
      setContacts(contactsResponse.data?.items || []);
      setNodes(nodesResponse.data?.items || []);
    } catch (error) {
      message.error(mapApiError(error, '加载运维人员失败'));
    } finally {
      setLoading(false);
    }
  }, [nodeFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  const openCreate = () => {
    setEditing(undefined);
    form.setFieldsValue({ name: '', phone: '', enabled: true, node_ids: nodeFilter ? [nodeFilter] : [] });
    setFormVisible(true);
  };

  const openEdit = (contact: OpsContact) => {
    setEditing(contact);
    form.setFieldsValue({
      name: contact.name,
      phone: contact.phone,
      enabled: contact.enabled,
      node_ids: contact.node_ids || [],
    });
    setFormVisible(true);
  };

  const save = async (values: ContactForm) => {
    const payload = {
      name: values.name.trim(),
      phone: values.phone.trim(),
      enabled: values.enabled,
      node_ids: values.node_ids || [],
    };
    try {
      const result = await apiService.request<ApiResponse<any>>(
        editing ? `/admin/ops-contacts/${editing.id}` : '/admin/ops-contacts',
        { method: editing ? 'PUT' : 'POST', body: JSON.stringify(payload) },
      );
      if (result.code !== 200 && result.code !== 201) throw new Error(result.message || '保存失败');
      message.success(editing ? '运维人员已更新' : '运维人员已创建');
      setFormVisible(false);
      await load();
    } catch (error) {
      message.error(mapApiError(error, '保存运维人员失败'));
    }
  };

  const toggleEnabled = async (contact: OpsContact, enabled: boolean) => {
    try {
      const result = await apiService.request<ApiResponse<any>>(`/admin/ops-contacts/${contact.id}/enabled`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled }),
      });
      if (result.code !== 200) throw new Error(result.message || '更新失败');
      await load();
    } catch (error) {
      message.error(mapApiError(error, '更新启用状态失败'));
    }
  };

  const remove = async (contact: OpsContact) => {
    try {
      const result = await apiService.request<ApiResponse<any>>(`/admin/ops-contacts/${contact.id}`, {
        method: 'DELETE',
      });
      if (result.code !== 200) throw new Error(result.message || '删除失败');
      message.success('运维人员已删除');
      await load();
    } catch (error) {
      message.error(mapApiError(error, '删除失败'));
    }
  };

  const columns: ColumnsType<OpsContact> = [
    { title: '姓名', dataIndex: 'name', width: 140 },
    { title: '电话', dataIndex: 'phone', width: 160 },
    {
      title: '绑定节点',
      render: (_, contact) => (
        <Space wrap size={[4, 4]}>
          {(contact.node_ids || []).length === 0
            ? '-'
            : (contact.node_ids || []).map((id) => (
              <Link key={id} to={`/edge-nodes?node_id=${encodeURIComponent(id)}`}>{nodeLabel(id)}</Link>
            ))}
        </Space>
      ),
    },
    {
      title: '启用',
      width: 90,
      render: (_, contact) => <Switch checked={contact.enabled} onChange={(value) => toggleEnabled(contact, value)} />,
    },
    {
      title: '',
      width: 120,
      render: (_, contact) => (
        <Space>
          <Button icon={<EditOutlined />} onClick={() => openEdit(contact)} />
          <Popconfirm title="删除该运维人员？" onConfirm={() => remove(contact)} okText="删除" cancelText="取消">
            <Button danger>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16, gap: 12, flexWrap: 'wrap' }}>
        <Space>
          {nodeFilter ? (
            <>
              <span>已按节点筛选：{nodeLabel(nodeFilter)}</span>
              <Button onClick={() => navigate('/ops-contacts')}>清除筛选</Button>
            </>
          ) : (
            <span>运维人员（展示用联系人，非登录账号）</span>
          )}
        </Space>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建运维人员</Button>
      </div>
      <Table rowKey="id" loading={loading} dataSource={contacts} columns={columns} pagination={{ pageSize: 20, showSizeChanger: false }} />
      <Modal
        open={formVisible}
        title={editing ? '编辑运维人员' : '新建运维人员'}
        onCancel={() => setFormVisible(false)}
        onOk={() => form.submit()}
        destroyOnClose
        okText="保存"
        cancelText="取消"
      >
        <Form form={form} layout="vertical" onFinish={save} initialValues={{ enabled: true, node_ids: [] }}>
          <Form.Item name="name" label="姓名" rules={[{ required: true, message: '请输入姓名' }]}>
            <Input maxLength={100} />
          </Form.Item>
          <Form.Item name="phone" label="电话" rules={[{ required: true, message: '请输入电话' }]}>
            <Input maxLength={40} />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="node_ids" label="绑定节点">
            <Select
              mode="multiple"
              allowClear
              optionFilterProp="label"
              options={nodes.map((node) => ({
                value: node.id,
                label: `${node.alias || node.name || '未命名'} (${node.id.slice(0, 8)}…)`,
              }))}
            />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default OpsContacts;
