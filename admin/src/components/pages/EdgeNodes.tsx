import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Input, Modal, Select, Space, Switch, Table, Tag, Tooltip, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, FileTextOutlined, PlusOutlined, PrinterOutlined, TeamOutlined } from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { buildApiUrl, buildAuthUrl } from '../../config';
import { DateTimeValue } from '../DisplayValue';
import { RelationStack } from '../RelationLinks';

interface EdgeNode {
  id: string; name: string; alias?: string; location?: string; connection_status: string;
  health_status: string; health_message?: string; enabled: boolean; last_heartbeat?: string;
  version?: string; registration_state: string; login_source?: string; ops_contact_count?: number; printer_count?: number; job_count?: number;
}

interface IntegrationProvider {
  code: string;
  display_name: string;
  enabled: boolean;
}

async function request(path: string, init?: RequestInit) {
  const me = await fetch(buildAuthUrl('me'));
  const token = (await me.json())?.data?.access_token;
  const response = await fetch(buildApiUrl(path), {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(init?.headers || {}) },
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return (await response.json())?.data;
}

const statusTag = (status: string) => <Tag color={status === 'online' ? 'success' : status === 'unstable' ? 'warning' : 'default'}>{status === 'online' ? '在线' : status === 'unstable' ? '连接不稳定' : '离线'}</Tag>;
const healthTag = (status: string) => <Tag color={status === 'healthy' ? 'success' : status === 'critical' ? 'error' : status === 'degraded' ? 'warning' : 'default'}>{status === 'healthy' ? '健康' : status === 'critical' ? '严重' : status === 'degraded' ? '降级' : '未知'}</Tag>;

const EdgeNodes: React.FC = () => {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const nodeFilter = searchParams.get('node_id') || '';
  const [nodes, setNodes] = useState<EdgeNode[]>([]);
  const [providers, setProviders] = useState<IntegrationProvider[]>([]);
  const [loading, setLoading] = useState(true);
  const [savingLoginSource, setSavingLoginSource] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [alias, setAlias] = useState('');
  const [activation, setActivation] = useState<{ code: string; expiresAt: string } | null>(null);
  const [keyword, setKeyword] = useState('');
  const [connectionFilter, setConnectionFilter] = useState<string | undefined>();
  const [enabledFilter, setEnabledFilter] = useState<string | undefined>();

  const load = useCallback(async () => {
    try {
      const [nodeData, providerData] = await Promise.all([
        request('/admin/edge-nodes?page=1&page_size=100'),
        request('/admin/integration-providers'),
      ]);
      setNodes(nodeData?.items || []);
      setProviders(providerData || []);
    }
    catch { message.error('节点信息加载失败'); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); const timer = window.setInterval(load, 30000); return () => window.clearInterval(timer); }, [load]);
  useEffect(() => {
    if (!editing) return undefined;
    const closeEditor = (event: MouseEvent) => { if (!(event.target as HTMLElement).closest('.inline-name-editor')) setEditing(null); };
    document.addEventListener('mousedown', closeEditor);
    return () => document.removeEventListener('mousedown', closeEditor);
  }, [editing]);

  const visibleNodes = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return nodes.filter((node) => {
      if (nodeFilter && node.id !== nodeFilter) return false;
      if (connectionFilter && node.connection_status !== connectionFilter) return false;
      if (enabledFilter === 'enabled' && !node.enabled) return false;
      if (enabledFilter === 'disabled' && node.enabled) return false;
      if (!q) return true;
      return [node.id, node.name, node.alias, node.location, node.version]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(q));
    });
  }, [nodes, nodeFilter, keyword, connectionFilter, enabledFilter]);

  const saveAlias = async (node: EdgeNode) => {
    try { await request(`/admin/edge-nodes/${node.id}/alias`, { method: 'PATCH', body: JSON.stringify({ alias: alias.trim() }) }); message.success('名称已保存'); setEditing(null); load(); }
    catch { message.error('名称保存失败'); }
  };
  const createActivation = async () => {
    try { const data = await request('/admin/edge-nodes/activations', { method: 'POST', body: '{}' }); setActivation({ code: data.activation_code, expiresAt: data.expires_at }); load(); }
    catch { message.error('创建待激活终端失败'); }
  };
  const toggle = async (node: EdgeNode, enabled: boolean) => {
    try { await request(`/admin/edge-nodes/${node.id}/enabled`, { method: 'PATCH', body: JSON.stringify({ enabled }) }); load(); }
    catch { message.error('状态更新失败'); }
  };
  const saveLoginSource = async (node: EdgeNode, loginSource: string) => {
    setSavingLoginSource(node.id);
    try {
      const data = await request(`/admin/edge-nodes/${node.id}/login-source`, {
        method: 'PATCH',
        body: JSON.stringify({ login_source: loginSource }),
      });
      setNodes(current => current.map(item => item.id === node.id ? { ...item, login_source: data?.login_source || loginSource } : item));
      message.success('登录源已保存');
    } catch { message.error('登录源保存失败'); }
    finally { setSavingLoginSource(null); }
  };
  const remove = (node: EdgeNode) => Modal.confirm({
    title: '删除节点？', content: `删除后该节点的专属凭据将失效，节点需要重新激活。\n${node.id}`,
    okText: '删除', okType: 'danger', cancelText: '取消',
    onOk: async () => { try { await request(`/admin/edge-nodes/${node.id}`, { method: 'DELETE' }); message.success('节点已删除'); load(); } catch { message.error('删除失败'); } },
  });

  const columns: ColumnsType<EdgeNode> = [
    { title: '节点 ID', dataIndex: 'id', width: 280, sorter: (a, b) => a.id.localeCompare(b.id), render: (id: string) => <span style={{ wordBreak: 'break-all' }}>{id}</span> },
    {
      title: '节点名称',
      width: 220,
      sorter: (a, b) => (a.alias || a.name || '').localeCompare(b.alias || b.name || ''),
      render: (_, node) => editing === node.id
        ? <Space.Compact className="inline-name-editor"><Input autoFocus value={alias} onChange={event => setAlias(event.target.value)} onPressEnter={() => saveAlias(node)} placeholder="留空以清除别名" /><Button type="primary" onClick={() => saveAlias(node)}>保存</Button></Space.Compact>
        : <span onClick={() => { setEditing(node.id); setAlias(node.alias || node.name || ''); }} style={{ cursor: 'pointer' }}><div>{node.alias || node.name || '待激活终端'}</div>{node.alias && <div style={{ color: '#8c8c8c', fontSize: 12 }}>（{node.name || '待上报'}）</div>}</span>,
    },
    { title: '节点位置', dataIndex: 'location', sorter: (a, b) => (a.location || '').localeCompare(b.location || ''), render: value => value || '-' },
    {
      title: '节点状态',
      dataIndex: 'connection_status',
      width: 110,
      filters: [{ text: '在线', value: 'online' }, { text: '连接不稳定', value: 'unstable' }, { text: '离线', value: 'offline' }],
      onFilter: (value, record) => record.connection_status === value,
      render: statusTag,
    },
    { title: '节点健康状态', width: 120, render: (_, node) => <Tooltip title={node.health_message}>{healthTag(node.health_status)}</Tooltip> },
    {
      title: '关联',
      width: 110,
      render: (_, node) => (
        <RelationStack
          items={[
            { key: 'ops', title: '运维人员', icon: <TeamOutlined />, value: node.ops_contact_count ?? 0, to: `/ops-contacts?node_id=${encodeURIComponent(node.id)}` },
            { key: 'printers', title: '打印机', icon: <PrinterOutlined />, value: node.printer_count ?? 0, to: `/printers?edge_node_id=${encodeURIComponent(node.id)}` },
            { key: 'jobs', title: '打印任务', icon: <FileTextOutlined />, value: node.job_count ?? 0, to: `/print-jobs?edge_node_id=${encodeURIComponent(node.id)}` },
          ]}
        />
      ),
    },
    { title: '节点最后心跳', dataIndex: 'last_heartbeat', width: 150, sorter: (a, b) => String(a.last_heartbeat || '').localeCompare(String(b.last_heartbeat || '')), render: value => <DateTimeValue value={value} /> },
    { title: '节点版本', dataIndex: 'version', width: 90, sorter: (a, b) => (a.version || '').localeCompare(b.version || ''), render: value => value || '-' },
    {
      title: '登录源',
      width: 180,
      render: (_, node) => (
        <Select
          data-testid={`node-login-source-${node.id}`}
          aria-label={`节点 ${node.alias || node.name || node.id} 登录源`}
          value={node.login_source || 'official'}
          loading={savingLoginSource === node.id}
          style={{ minWidth: 150 }}
          options={[
            { value: 'official', label: '官方入口' },
            ...providers.map(provider => ({
              value: provider.code,
              label: provider.display_name,
              disabled: !provider.enabled,
            })),
          ]}
          onChange={value => saveLoginSource(node, value)}
        />
      ),
    },
    { title: '节点启用状态', width: 105, render: (_, node) => <Switch checked={node.enabled} disabled={node.registration_state === 'pending_activation'} onChange={value => toggle(node, value)} /> },
    { title: '', width: 70, render: (_, node) => <Button danger type="primary" icon={<DeleteOutlined />} onClick={() => remove(node)} /> },
  ];

  return <div>
    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16, gap: 12, flexWrap: 'wrap' }}>
      <Space wrap>
        <Input.Search allowClear placeholder="搜索 ID / 名称 / 位置" style={{ width: 240 }} value={keyword} onChange={(e) => setKeyword(e.target.value)} />
        <Select allowClear placeholder="连接状态" style={{ width: 140 }} value={connectionFilter} onChange={setConnectionFilter}
          options={[{ value: 'online', label: '在线' }, { value: 'unstable', label: '连接不稳定' }, { value: 'offline', label: '离线' }]} />
        <Select allowClear placeholder="启用状态" style={{ width: 120 }} value={enabledFilter} onChange={setEnabledFilter}
          options={[{ value: 'enabled', label: '已启用' }, { value: 'disabled', label: '已停用' }]} />
        {nodeFilter ? (
          <>
            <span>已按节点筛选</span>
            <Button onClick={() => navigate('/edge-nodes')}>清除筛选</Button>
          </>
        ) : null}
      </Space>
      <Button type="primary" icon={<PlusOutlined />} onClick={createActivation}>创建待激活终端</Button>
    </div>
    <Card><Table rowKey="id" loading={loading} dataSource={visibleNodes} columns={columns} scroll={{ x: 1560 }} pagination={{ pageSize: 20, showSizeChanger: false }} /></Card>
    <Modal open={!!activation} footer={<Button type="primary" onClick={() => setActivation(null)}>我已保存</Button>} closable={false} title="一次性激活码">
      <Typography.Paragraph>请在 Edge 的初始激活界面填写 Cloud URL 和以下激活码。激活码仅显示一次，10 分钟后失效。</Typography.Paragraph>
      <Typography.Title level={3} copyable={{ text: activation?.code }}>{activation?.code}</Typography.Title>
      <Space align="start"><Typography.Text type="secondary">失效时间：</Typography.Text><DateTimeValue value={activation?.expiresAt} /></Space>
    </Modal>
  </div>;
};

export default EdgeNodes;
