import React, { useCallback, useEffect, useState } from 'react';
import { Button, Card, Descriptions, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Space, Switch, Table, Typography, message } from 'antd';
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

interface SitePortalProvider {
  provider_id: string; display_name: string; enabled: boolean; sort_order: number;
  file_base_url: string; sign_secret_ref: string; portal_api_base_url?: string; upload_enabled?: boolean;
}

const SitePortals: React.FC = () => {
  const [portals, setPortals] = useState<SitePortal[]>([]);
  const [loading, setLoading] = useState(true);
  const [formOpen, setFormOpen] = useState(false);
  const [editingPortal, setEditingPortal] = useState<SitePortal | null>(null);
  const [credential, setCredential] = useState<{ client_id: string; client_secret: string } | null>(null);
  const [detailPortal, setDetailPortal] = useState<SitePortal | null>(null);
  const [providers, setProviders] = useState<SitePortalProvider[]>([]);
  const [providerLoading, setProviderLoading] = useState(false);
  const [providerFormOpen, setProviderFormOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<SitePortalProvider | null>(null);
  const [form] = Form.useForm();
  const [providerForm] = Form.useForm();

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

  const loadProviders = async (portal: SitePortal) => {
    setProviderLoading(true);
    try {
      const result = await request(`/admin/site-portals/${encodeURIComponent(portal.code)}/providers`);
      setProviders(result?.providers || []);
    } catch (error) { message.error(error instanceof Error ? error.message : 'Provider 配置加载失败'); }
    finally { setProviderLoading(false); }
  };
  const openDetail = (portal: SitePortal) => { setDetailPortal(portal); void loadProviders(portal); };
  const openCreateProvider = () => { setEditingProvider(null); providerForm.resetFields(); providerForm.setFieldsValue({ enabled: true, upload_enabled: false, sort_order: 0, sign_secret_ref: 'DEFAULT' }); setProviderFormOpen(true); };
  const openEditProvider = (provider: SitePortalProvider) => { setEditingProvider(provider); providerForm.setFieldsValue(provider); setProviderFormOpen(true); };
  const saveProvider = async (values: SitePortalProvider) => {
    if (!detailPortal) return;
    try {
      const path = `/admin/site-portals/${encodeURIComponent(detailPortal.code)}/providers`;
      await request(editingProvider ? `${path}/${encodeURIComponent(editingProvider.provider_id)}` : path, {
        method: editingProvider ? 'PUT' : 'POST', body: JSON.stringify(values),
      });
      message.success(editingProvider ? 'Provider 配置已保存' : 'Provider 已新增');
      setProviderFormOpen(false); setEditingProvider(null); providerForm.resetFields(); await loadProviders(detailPortal);
    } catch (error) { message.error(error instanceof Error ? error.message : 'Provider 配置保存失败'); }
  };
  const toggleProvider = async (provider: SitePortalProvider, enabled: boolean) => {
    if (!detailPortal) return;
    try { await request(`/admin/site-portals/${encodeURIComponent(detailPortal.code)}/providers/${encodeURIComponent(provider.provider_id)}/enabled`, { method: 'PATCH', body: JSON.stringify({ enabled }) }); await loadProviders(detailPortal); }
    catch (error) { message.error(error instanceof Error ? error.message : 'Provider 状态更新失败'); }
  };
  const removeProvider = async (provider: SitePortalProvider) => {
    if (!detailPortal) return;
    try { await request(`/admin/site-portals/${encodeURIComponent(detailPortal.code)}/providers/${encodeURIComponent(provider.provider_id)}`, { method: 'DELETE' }); await loadProviders(detailPortal); }
    catch (error) { message.error(error instanceof Error ? error.message : 'Provider 删除失败'); }
  };

  const columns: ColumnsType<SitePortal> = [
    { title: '入口', width: 280, render: (_, portal) => <EntityCell primary={portal.display_name} secondary={portal.code} /> },
    { title: '关联 Edge', dataIndex: 'edge_node_count', width: 100, render: (value) => value ?? 0 },
    { title: '启用', width: 90, render: (_, portal) => <Switch checked={portal.enabled} onChange={(checked) => void toggle(portal, checked)} /> },
    { title: '操作', width: 340, render: (_, portal) => <Space size="small"><Button type="link" icon={<EyeOutlined />} onClick={() => openDetail(portal)}>详情</Button><Button type="link" icon={<EditOutlined />} onClick={() => openEdit(portal)}>编辑</Button><Button type="link" icon={<SyncOutlined />} onClick={() => void rotate(portal)}>轮换凭证</Button><Popconfirm title="确定删除此 Site Portal？" description="已有关联身份或 Edge 入口配置时无法删除。" onConfirm={() => void remove(portal)} okText="删除" cancelText="取消"><Button type="link" danger>删除</Button></Popconfirm></Space> },
  ];

  return <Card title="Site Portal 管理" extra={<Space><Button icon={<ReloadOutlined />} onClick={() => void load()}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新增 Site Portal</Button></Space>}>
    <Table rowKey="code" loading={loading} dataSource={portals} columns={columns} pagination={false} scroll={{ x: 820 }} />
    <Drawer title="Site Portal 详情" open={!!detailPortal} onClose={() => setDetailPortal(null)} width={520}>
      {detailPortal ? <>
      <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label="显示名称">{detailPortal.display_name}</Descriptions.Item>
        <Descriptions.Item label="编码">{detailPortal.code}</Descriptions.Item>
        <Descriptions.Item label="状态">{detailPortal.enabled ? '启用' : '停用'}</Descriptions.Item>
        <Descriptions.Item label="关联 Edge">{detailPortal.edge_node_count ?? 0}</Descriptions.Item>
        <Descriptions.Item label="入口地址">{detailPortal.entry_url || '-'}</Descriptions.Item>
        <Descriptions.Item label="Site Portal Claim API 地址（非 PRP）">{detailPortal.claim_base_url || '-'}</Descriptions.Item>
        <Descriptions.Item label="OAuth Client ID"><FullIdentifier value={detailPortal.oauth_client_id} /></Descriptions.Item>
        <Descriptions.Item label="OAuth Client 状态">{detailPortal.oauth_client_enabled === false ? '停用' : '启用'}</Descriptions.Item>
      </Descriptions>
      <Card size="small" title="Provider 配置" style={{ marginTop: 16 }} extra={<Button type="primary" size="small" onClick={openCreateProvider}>新增 Provider</Button>}>
        <Table<SitePortalProvider> rowKey="provider_id" size="small" loading={providerLoading} dataSource={providers} pagination={false} scroll={{ x: 760 }} columns={[
          { title: '来源', render: (_, provider) => <EntityCell primary={provider.display_name} secondary={provider.provider_id} /> },
          { title: '排序', dataIndex: 'sort_order', width: 70 },
          { title: '启用', width: 70, render: (_, provider) => <Switch size="small" checked={provider.enabled} onChange={(checked) => void toggleProvider(provider, checked)} /> },
          { title: '操作', width: 150, render: (_, provider) => <Space size="small"><Button type="link" size="small" onClick={() => openEditProvider(provider)}>编辑</Button><Popconfirm title="删除该 Provider？" onConfirm={() => void removeProvider(provider)} okText="删除" cancelText="取消"><Button type="link" size="small" danger>删除</Button></Popconfirm></Space> },
        ]} />
      </Card>
      </> : null}
    </Drawer>
    <Modal open={formOpen} title={editingPortal ? '编辑 Site Portal' : '新增 Site Portal'} okText={editingPortal ? '保存' : '创建并生成凭证'} cancelText="取消" onCancel={() => { setFormOpen(false); setEditingPortal(null); form.resetFields(); }} onOk={() => form.submit()} destroyOnClose>
      <Form form={form} layout="vertical" onFinish={save}>
        <Form.Item name="code" label="编码" rules={[{ required: true }]}><Input placeholder="official" disabled={!!editingPortal} /></Form.Item>
        <Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="entry_url" label="入口地址" rules={[{ required: true, type: 'url' }]}><Input placeholder="http://portal.example/entry" /></Form.Item>
        <Form.Item name="claim_base_url" label="Site Portal Claim API 地址（非 PRP）" rules={[{ required: true, type: 'url' }]}><Input placeholder="http://portal.example" /></Form.Item>
      </Form>
    </Modal>
    <Modal open={providerFormOpen} title={editingProvider ? '编辑 Provider' : '新增 Provider'} okText="保存" cancelText="取消" onCancel={() => { setProviderFormOpen(false); setEditingProvider(null); providerForm.resetFields(); }} onOk={() => providerForm.submit()} destroyOnClose>
      <Form form={providerForm} layout="vertical" onFinish={saveProvider}>
        <Form.Item name="provider_id" label="Provider ID" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9_-]{1,63}$/ }]}><Input disabled={!!editingProvider} placeholder="invoice" /></Form.Item>
        <Form.Item name="display_name" label="Edge 显示名称" rules={[{ required: true, max: 120 }]}><Input placeholder="发票打印" /></Form.Item>
        <Form.Item name="sort_order" label="排序" rules={[{ required: true, type: 'number', min: 0 }]}><InputNumber min={0} precision={0} style={{ width: '100%' }} /></Form.Item>
        <Form.Item name="file_base_url" label="文件服务地址" rules={[{ required: true, type: 'url' }]}><Input placeholder="https://files.example.com" /></Form.Item>
        <Form.Item name="sign_secret_ref" label="本地密钥引用" rules={[{ required: true, pattern: /^[A-Z][A-Z0-9_]{0,31}$/ }]} extra="只填写引用，例如 INVOICE；不会上传密钥原文。"><Input placeholder="INVOICE" /></Form.Item>
        <Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item>
        <Form.Item name="upload_enabled" label="启用 PRP 浏览器上传" valuePropName="checked"><Switch /></Form.Item>
        <Form.Item name="portal_api_base_url" label="Portal API 地址"><Input placeholder="仅上传模式需要" /></Form.Item>
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
