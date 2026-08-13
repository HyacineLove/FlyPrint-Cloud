import React, { useCallback, useEffect, useState } from 'react';
import { Button, Card, Descriptions, Drawer, Form, Input, Modal, Popconfirm, Space, Switch, Table, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { EditOutlined, EyeOutlined, PlusOutlined, ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { apiService } from '../../services/api';
import type { ApiResponse } from '../../services/api';
import { EntityCell, FullIdentifier } from '../DisplayValue';

interface SitePortal {
  code: string; display_name: string; entry_url: string; claim_base_url: string;
  enabled: boolean; oauth_client_id?: string; oauth_client_enabled?: boolean; edge_node_count?: number;
}

async function request(path: string, init?: RequestInit) {
  const body = await apiService.request<ApiResponse<any>>(path, init);
  if (body.code !== 200 && body.code !== 201) throw new Error(body.message || '请求失败');
  return body.data;
}

const SitePortals: React.FC = () => {
  const [portals, setPortals] = useState<SitePortal[]>([]);
  const [loading, setLoading] = useState(true);
  const [formOpen, setFormOpen] = useState(false);
  const [editingPortal, setEditingPortal] = useState<SitePortal | null>(null);
  const [credential, setCredential] = useState<{ client_id: string; client_secret: string } | null>(null);
  const [detailPortal, setDetailPortal] = useState<SitePortal | null>(null);
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    setLoading(true);
    try { setPortals((await request('/admin/site-portals')) || []); }
    catch (error) { message.error(error instanceof Error ? error.message : 'Site Portal 加载失败'); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const save = async (values: Record<string, string>) => {
    try {
      if (editingPortal) {
        await request(`/admin/site-portals/${encodeURIComponent(editingPortal.code)}`, { method: 'PUT', body: JSON.stringify({ ...values, code: editingPortal.code }) });
        message.success('Site Portal 配置已保存');
      } else {
        const result = await request('/admin/site-portals', { method: 'POST', body: JSON.stringify(values) });
        setCredential({ client_id: result.client_id, client_secret: result.client_secret });
      }
      setFormOpen(false); setEditingPortal(null); form.resetFields(); await load();
    } catch (error) { message.error(error instanceof Error ? error.message : 'Site Portal 创建失败'); }
  };
  const openCreate = () => { setEditingPortal(null); form.resetFields(); setFormOpen(true); };
  const openEdit = (portal: SitePortal) => {
    setEditingPortal(portal);
    form.setFieldsValue({ code: portal.code, display_name: portal.display_name, entry_url: portal.entry_url, claim_base_url: portal.claim_base_url });
    setFormOpen(true);
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
    { title: '入口', width: 280, render: (_, portal) => <EntityCell primary={portal.display_name} secondary={portal.code} /> },
    { title: '关联 Edge', dataIndex: 'edge_node_count', width: 100, render: (value) => value ?? 0 },
    { title: '启用', width: 90, render: (_, portal) => <Switch checked={portal.enabled} onChange={(checked) => void toggle(portal, checked)} /> },
    { title: '操作', width: 340, render: (_, portal) => <Space size="small"><Button type="link" icon={<EyeOutlined />} onClick={() => setDetailPortal(portal)}>详情</Button><Button type="link" icon={<EditOutlined />} onClick={() => openEdit(portal)}>编辑</Button><Button type="link" icon={<SyncOutlined />} onClick={() => void rotate(portal)}>轮换凭证</Button><Popconfirm title="确定删除此 Site Portal？" description="已有关联身份或 Edge 入口配置时无法删除。" onConfirm={() => void remove(portal)} okText="删除" cancelText="取消"><Button type="link" danger>删除</Button></Popconfirm></Space> },
  ];

  return <Card title="Site Portal 管理" extra={<Space><Button icon={<ReloadOutlined />} onClick={() => void load()}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新增 Site Portal</Button></Space>}>
    <Table rowKey="code" loading={loading} dataSource={portals} columns={columns} pagination={false} scroll={{ x: 820 }} />
    <Drawer title="Site Portal 详情" open={!!detailPortal} onClose={() => setDetailPortal(null)} width={520}>
      {detailPortal ? <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label="显示名称">{detailPortal.display_name}</Descriptions.Item>
        <Descriptions.Item label="编码">{detailPortal.code}</Descriptions.Item>
        <Descriptions.Item label="状态">{detailPortal.enabled ? '启用' : '停用'}</Descriptions.Item>
        <Descriptions.Item label="关联 Edge">{detailPortal.edge_node_count ?? 0}</Descriptions.Item>
        <Descriptions.Item label="入口地址">{detailPortal.entry_url || '-'}</Descriptions.Item>
        <Descriptions.Item label="Site Portal Claim API 地址（非 PRP）">{detailPortal.claim_base_url || '-'}</Descriptions.Item>
        <Descriptions.Item label="OAuth Client ID"><FullIdentifier value={detailPortal.oauth_client_id} /></Descriptions.Item>
        <Descriptions.Item label="OAuth Client 状态">{detailPortal.oauth_client_enabled === false ? '停用' : '启用'}</Descriptions.Item>
      </Descriptions> : null}
    </Drawer>
    <Modal open={formOpen} title={editingPortal ? '编辑 Site Portal' : '新增 Site Portal'} okText={editingPortal ? '保存' : '创建并生成凭证'} cancelText="取消" onCancel={() => { setFormOpen(false); setEditingPortal(null); form.resetFields(); }} onOk={() => form.submit()} destroyOnClose>
      <Form form={form} layout="vertical" onFinish={save}>
        <Form.Item name="code" label="编码" rules={[{ required: true }]}><Input placeholder="official" disabled={!!editingPortal} /></Form.Item>
        <Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="entry_url" label="入口地址" rules={[{ required: true, type: 'url' }]}><Input placeholder="http://portal.example/entry" /></Form.Item>
        <Form.Item name="claim_base_url" label="Site Portal Claim API 地址（非 PRP）" rules={[{ required: true, type: 'url' }]}><Input placeholder="http://portal.example" /></Form.Item>
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
