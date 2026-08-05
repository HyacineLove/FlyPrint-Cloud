import React, { useCallback, useEffect, useState } from 'react';
import { Button, Card, Form, Input, Modal, Popconfirm, Space, Switch, Table, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { PlusOutlined, ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { buildApiUrl, buildAuthUrl } from '../../config';

interface SitePortal {
  code: string; display_name: string; entry_url: string; claim_base_url: string;
  enabled: boolean; oauth_client_id?: string; oauth_client_enabled?: boolean;
}

async function request(path: string, init?: RequestInit) {
  const me = await fetch(buildAuthUrl('me'));
  const token = (await me.json())?.data?.access_token;
  const response = await fetch(buildApiUrl(path), {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(init?.headers || {}) },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.message || body.error || `HTTP ${response.status}`);
  return body.data;
}

const SitePortals: React.FC = () => {
  const [portals, setPortals] = useState<SitePortal[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [credential, setCredential] = useState<{ client_id: string; client_secret: string } | null>(null);
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    setLoading(true);
    try { setPortals((await request('/admin/site-portals')) || []); }
    catch (error) { message.error(error instanceof Error ? error.message : 'Site Portal 加载失败'); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const create = async (values: Record<string, string>) => {
    try {
      const result = await request('/admin/site-portals', { method: 'POST', body: JSON.stringify(values) });
      setCreateOpen(false); form.resetFields(); setCredential({ client_id: result.client_id, client_secret: result.client_secret }); await load();
    } catch (error) { message.error(error instanceof Error ? error.message : 'Site Portal 创建失败'); }
  };
  const toggle = async (portal: SitePortal, enabled: boolean) => {
    try { await request(`/admin/site-portals/${encodeURIComponent(portal.code)}/enabled`, { method: 'PATCH', body: JSON.stringify({ enabled }) }); await load(); }
    catch (error) { message.error(error instanceof Error ? error.message : '状态更新失败'); }
  };
  const rotate = async (portal: SitePortal) => {
    try { const result = await request(`/admin/site-portals/${encodeURIComponent(portal.code)}/rotate-secret`, { method: 'POST', body: '{}' }); setCredential(result); }
    catch (error) { message.error(error instanceof Error ? error.message : '凭证轮换失败'); }
  };
  const remove = async (portal: SitePortal) => {
    try { await request(`/admin/site-portals/${encodeURIComponent(portal.code)}`, { method: 'DELETE' }); await load(); }
    catch (error) { message.error(error instanceof Error ? error.message : '删除失败'); }
  };

  const columns: ColumnsType<SitePortal> = [
    { title: '编码', dataIndex: 'code', width: 150 },
    { title: '显示名称', dataIndex: 'display_name', width: 180 },
    { title: '入口地址', dataIndex: 'entry_url', ellipsis: true },
    { title: 'OAuth Client ID', dataIndex: 'oauth_client_id', width: 200, render: (value) => value || '-' },
    { title: '启用', width: 90, render: (_, portal) => <Switch checked={portal.enabled} onChange={(checked) => void toggle(portal, checked)} /> },
    { title: '操作', width: 220, render: (_, portal) => <Space size="small"><Button type="link" icon={<SyncOutlined />} onClick={() => void rotate(portal)}>轮换凭证</Button><Popconfirm title="确定删除此 Site Portal？" description="已有关联身份或 Edge 默认配置时无法删除。" onConfirm={() => void remove(portal)} okText="删除" cancelText="取消"><Button type="link" danger>删除</Button></Popconfirm></Space> },
  ];

  return <Card title="Site Portal 管理" extra={<Space><Button icon={<ReloadOutlined />} onClick={() => void load()}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>新增 Site Portal</Button></Space>}>
    <Table rowKey="code" loading={loading} dataSource={portals} columns={columns} pagination={false} />
    <Modal open={createOpen} title="新增 Site Portal" okText="创建并生成凭证" cancelText="取消" onCancel={() => setCreateOpen(false)} onOk={() => form.submit()} destroyOnClose>
      <Form form={form} layout="vertical" onFinish={create}>
        <Form.Item name="code" label="编码" rules={[{ required: true }]}><Input placeholder="official" /></Form.Item>
        <Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="entry_url" label="入口地址" rules={[{ required: true, type: 'url' }]}><Input placeholder="http://portal.example/entry" /></Form.Item>
        <Form.Item name="claim_base_url" label="Claim 地址" rules={[{ required: true, type: 'url' }]}><Input placeholder="http://portal.example" /></Form.Item>
      </Form>
    </Modal>
    <Modal open={!!credential} title="请立即保存 Site Portal 凭证" footer={<Button type="primary" onClick={() => setCredential(null)}>我已保存</Button>} onCancel={() => setCredential(null)}>
      <Typography.Paragraph>Secret 仅在创建或轮换时显示一次。</Typography.Paragraph>
      <Typography.Paragraph copyable={{ text: credential?.client_id }}>{credential?.client_id}</Typography.Paragraph>
      <Typography.Paragraph copyable={{ text: credential?.client_secret }}>{credential?.client_secret}</Typography.Paragraph>
    </Modal>
  </Card>;
};

export default SitePortals;
