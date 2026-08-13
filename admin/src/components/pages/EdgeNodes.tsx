import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Checkbox, Descriptions, Drawer, Input, Modal, Select, Space, Switch, Table, Tag, Tooltip, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EyeOutlined, FileTextOutlined, PlusOutlined, PrinterOutlined, SettingOutlined, TeamOutlined } from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { apiService } from '../../services/api';
import type { ApiResponse } from '../../services/api';
import { DateTimeValue, EntityCell, FullIdentifier } from '../DisplayValue';
import { RelationStack } from '../RelationLinks';

interface EdgeNode {
  id: string; name: string; alias?: string; location?: string; connection_status: string;
  health_status: string; health_message?: string; enabled: boolean; last_heartbeat?: string;
  version?: string; registration_state: string; ops_contact_count?: number; printer_count?: number; job_count?: number;
}

interface SitePortal {
  code: string;
  display_name: string;
  enabled: boolean;
}

interface PortalConfig {
  edge_node_id: string;
  portals: SitePortal[];
  default_code: string;
}

async function request(path: string, init?: RequestInit) {
  const body = await apiService.request<ApiResponse<any>>(path, init);
  if (body.code !== 200 && body.code !== 201) throw new Error(body.message || '请求失败');
  return body.data;
}

const statusTag = (status: string) => <Tag color={status === 'online' ? 'success' : status === 'unstable' ? 'warning' : 'default'}>{status === 'online' ? '在线' : status === 'unstable' ? '连接不稳定' : '离线'}</Tag>;
const healthTag = (status: string) => <Tag color={status === 'healthy' ? 'success' : status === 'critical' ? 'error' : status === 'degraded' ? 'warning' : 'default'}>{status === 'healthy' ? '健康' : status === 'critical' ? '严重' : status === 'degraded' ? '降级' : '未知'}</Tag>;

const EdgeNodes: React.FC = () => {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const nodeFilter = searchParams.get('node_id') || '';
  const [nodes, setNodes] = useState<EdgeNode[]>([]);
  const [sitePortals, setSitePortals] = useState<SitePortal[]>([]);
  const [portalConfigs, setPortalConfigs] = useState<Record<string, PortalConfig>>({});
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<string | null>(null);
  const [alias, setAlias] = useState('');
  const [activation, setActivation] = useState<{ code: string; expiresAt: string } | null>(null);
  const [portalConfigNode, setPortalConfigNode] = useState<EdgeNode | null>(null);
  const [portalConfigCodes, setPortalConfigCodes] = useState<string[]>([]);
  const [portalConfigDefault, setPortalConfigDefault] = useState('');
  const [portalConfigLoading, setPortalConfigLoading] = useState(false);
  const [portalConfigSaving, setPortalConfigSaving] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [connectionFilter, setConnectionFilter] = useState<string | undefined>();
  const [enabledFilter, setEnabledFilter] = useState<string | undefined>();
  const [detailNode, setDetailNode] = useState<EdgeNode | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const load = useCallback(async () => {
    try {
      const nodeData = await request('/admin/edge-nodes?page=1&page_size=100');
      const loadedNodes: EdgeNode[] = nodeData?.items || [];
      setNodes(loadedNodes);
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
  const openPortalConfig = async (node: EdgeNode) => {
    setPortalConfigNode(node);
    setPortalConfigLoading(true);
    try {
      const [data, portalData] = await Promise.all([
        portalConfigs[node.id] || request(`/admin/edge-nodes/${encodeURIComponent(node.id)}/site-portals`),
        sitePortals.length > 0 ? Promise.resolve(sitePortals) : request('/admin/site-portals'),
      ]);
      setPortalConfigs(current => ({ ...current, [node.id]: data }));
      if (sitePortals.length === 0) setSitePortals(portalData || []);
      const configuredCodes = (data?.portals || []).map((portal: SitePortal) => portal.code);
      setPortalConfigCodes(configuredCodes);
      setPortalConfigDefault(data?.default_code || '');
    } catch { message.error('Site Portal 配置加载失败'); setPortalConfigNode(null); }
    finally { setPortalConfigLoading(false); }
  };
  const openDetail = async (node: EdgeNode) => {
    setDetailNode(node);
    const cachedConfig = portalConfigs[node.id];
    setDetailLoading(!cachedConfig);
    if (cachedConfig) return;
    try {
      const data = await request(`/admin/edge-nodes/${encodeURIComponent(node.id)}/site-portals`);
      setPortalConfigs(current => ({ ...current, [node.id]: data }));
    } catch { message.error('Site Portal 配置加载失败'); }
    finally { setDetailLoading(false); }
  };
  const savePortalConfig = async () => {
    if (!portalConfigNode) return;
    if (portalConfigCodes.length === 0 || !portalConfigDefault) { message.error('至少选择一个 Site Portal，并设置默认入口'); return; }
    setPortalConfigSaving(true);
    try {
      const data = await request(`/admin/edge-nodes/${encodeURIComponent(portalConfigNode.id)}/site-portals`, {
        method: 'PUT',
        body: JSON.stringify({ portal_codes: portalConfigCodes, default_code: portalConfigDefault }),
      });
      setPortalConfigs(current => ({ ...current, [portalConfigNode.id]: data }));
      message.success('Site Portal 配置已保存');
      setPortalConfigNode(null);
    } catch (error) { message.error(error instanceof Error ? error.message : 'Site Portal 配置保存失败'); }
    finally { setPortalConfigSaving(false); }
  };
  const remove = (node: EdgeNode) => Modal.confirm({
    title: '删除节点？', content: `删除后该节点的专属凭据将失效，节点需要重新激活。\n${node.id}`,
    okText: '删除', okType: 'danger', cancelText: '取消',
    onOk: async () => { try { await request(`/admin/edge-nodes/${node.id}`, { method: 'DELETE' }); message.success('节点已删除'); load(); } catch { message.error('删除失败'); } },
  });

  const columns: ColumnsType<EdgeNode> = [
    {
      title: '节点',
      width: 300,
      sorter: (a, b) => (a.alias || a.name || '').localeCompare(b.alias || b.name || ''),
      render: (_, node) => editing === node.id
        ? <Space.Compact className="inline-name-editor"><Input autoFocus value={alias} onChange={event => setAlias(event.target.value)} onPressEnter={() => saveAlias(node)} placeholder="留空以清除别名" /><Button type="primary" onClick={() => saveAlias(node)}>保存</Button></Space.Compact>
        : <span onClick={() => { setEditing(node.id); setAlias(node.alias || node.name || ''); }} style={{ cursor: 'pointer', display: 'block' }}><EntityCell primary={node.alias || node.name || '待激活终端'} secondary={node.alias ? `设备名称：${node.name || '待上报'}` : undefined} id={node.id} /></span>,
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
      title: 'Site Portal',
      width: 150,
      render: (_, node) => (
        <Button data-testid={`node-site-portals-${node.id}`} icon={<SettingOutlined />} onClick={() => void openPortalConfig(node)}>配置入口</Button>
      ),
    },
    { title: '节点启用状态', width: 105, render: (_, node) => <Switch checked={node.enabled} disabled={node.registration_state === 'pending_activation'} onChange={value => toggle(node, value)} /> },
    { title: '操作', width: 170, render: (_, node) => <Space size="small"><Button type="link" icon={<EyeOutlined />} onClick={() => void openDetail(node)}>详情</Button><Button danger type="primary" icon={<DeleteOutlined />} onClick={() => remove(node)} /></Space> },
  ];

  return <div>
    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16, gap: 12, flexWrap: 'wrap' }}>
      <Space wrap>
        <Input.Search allowClear placeholder="搜索名称、位置或 ID" style={{ width: 240 }} value={keyword} onChange={(e) => setKeyword(e.target.value)} />
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
    <Card><Table rowKey="id" loading={loading} dataSource={visibleNodes} columns={columns} scroll={{ x: 1320 }} pagination={{ pageSize: 20, showSizeChanger: false }} /></Card>
    <Drawer title="节点详情" open={!!detailNode} onClose={() => setDetailNode(null)} width={460}>
      {detailNode ? <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label="节点名称">{detailNode.alias || detailNode.name || '待激活终端'}</Descriptions.Item>
        <Descriptions.Item label="节点位置">{detailNode.location || '-'}</Descriptions.Item>
        <Descriptions.Item label="连接状态">{statusTag(detailNode.connection_status)}</Descriptions.Item>
        <Descriptions.Item label="健康状态"><Tooltip title={detailNode.health_message}>{healthTag(detailNode.health_status)}</Tooltip></Descriptions.Item>
        <Descriptions.Item label="注册状态">{detailNode.registration_state || '-'}</Descriptions.Item>
        <Descriptions.Item label="最后心跳"><DateTimeValue value={detailNode.last_heartbeat} /></Descriptions.Item>
        <Descriptions.Item label="节点版本">{detailNode.version || '-'}</Descriptions.Item>
        <Descriptions.Item label="Site Portal">{detailLoading ? '加载中…' : `${portalConfigs[detailNode.id]?.portals?.length ?? 0} 个入口${portalConfigs[detailNode.id]?.default_code ? `，默认：${portalConfigs[detailNode.id].default_code}` : ''}`}</Descriptions.Item>
        <Descriptions.Item label="节点 ID"><FullIdentifier value={detailNode.id} /></Descriptions.Item>
      </Descriptions> : null}
    </Drawer>
    <Modal open={!!activation} footer={<Button type="primary" onClick={() => setActivation(null)}>我已保存</Button>} closable={false} title="一次性激活码">
      <Typography.Paragraph>请在 Edge 的初始激活界面填写 Cloud URL 和以下激活码。激活码仅显示一次，10 分钟后失效。</Typography.Paragraph>
      <Typography.Title level={3} copyable={{ text: activation?.code }}>{activation?.code}</Typography.Title>
      <Space align="start"><Typography.Text type="secondary">失效时间：</Typography.Text><DateTimeValue value={activation?.expiresAt} /></Space>
    </Modal>
    <Modal
      open={!!portalConfigNode}
      title={portalConfigNode ? `配置 ${portalConfigNode.alias || portalConfigNode.name || portalConfigNode.id} 的 Site Portal` : '配置 Site Portal'}
      okText="保存配置"
      cancelText="取消"
      confirmLoading={portalConfigSaving}
      onOk={() => void savePortalConfig()}
      onCancel={() => setPortalConfigNode(null)}
      destroyOnClose
    >
      {portalConfigLoading ? <Typography.Text>加载中...</Typography.Text> : <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <div>
          <Typography.Text strong>可选入口</Typography.Text>
          <Typography.Paragraph type="secondary">终端扫码后的入口只能从这里选择。已停用的 Portal 不能继续关联，请先完成迁移。</Typography.Paragraph>
          <Checkbox.Group
            value={portalConfigCodes}
            onChange={values => {
              const nextCodes = values as string[];
              setPortalConfigCodes(nextCodes);
              if (portalConfigDefault && !nextCodes.includes(portalConfigDefault)) setPortalConfigDefault('');
            }}
            options={sitePortals.map(portal => ({
              value: portal.code,
              label: portal.enabled ? portal.display_name : `${portal.display_name}（已停用）`,
              disabled: !portal.enabled && !portalConfigCodes.includes(portal.code),
            }))}
          />
        </div>
        <div>
          <Typography.Text strong>默认入口</Typography.Text>
          <Select
            value={portalConfigDefault || undefined}
            placeholder="请选择默认入口"
            style={{ width: '100%', marginTop: 8 }}
            options={sitePortals.filter(portal => portalConfigCodes.includes(portal.code)).map(portal => ({ value: portal.code, label: portal.enabled ? portal.display_name : `${portal.display_name}（已停用）`, disabled: !portal.enabled }))}
            onChange={setPortalConfigDefault}
          />
        </div>
      </Space>}
    </Modal>
  </div>;
};

export default EdgeNodes;
