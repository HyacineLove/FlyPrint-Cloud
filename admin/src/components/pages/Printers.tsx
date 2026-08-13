import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Descriptions, Drawer, Input, Modal, Select, Space, Switch, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EyeOutlined, FileTextOutlined } from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { apiService } from '../../services/api';
import type { ApiResponse } from '../../services/api';
import { EntityCell, FullIdentifier, TwoLineLink } from '../DisplayValue';
import { RelationStack } from '../RelationLinks';

interface Node { id: string; name: string; alias?: string; connection_status: string; }
interface Printer {
  id: string; name: string; display_name?: string; model?: string; printer_status: string;
  enabled: boolean; edge_node_id: string; job_count?: number;
}

export const PRINTER_STATUS_META: Record<string, { color: string; label: string }> = {
  idle: { color: 'success', label: '就绪' },
  printing: { color: 'processing', label: '打印中' },
  printer_out_of_paper: { color: 'error', label: '缺纸' },
  printer_out_of_toner: { color: 'error', label: '缺粉' },
  printer_jammed: { color: 'error', label: '卡纸' },
  printer_cover_open: { color: 'error', label: '机盖打开' },
  printer_user_intervention: { color: 'error', label: '待处理' },
  printer_other_fault: { color: 'error', label: '设备故障' },
  printer_not_accepting_jobs: { color: 'warning', label: '拒绝新任务' },
  printer_unconfirmed_lock: { color: 'warning', label: '待确认' },
  printer_stopped: { color: 'error', label: '已停止' },
  ipp_unreachable: { color: 'warning', label: '连接失败' },
  error: { color: 'error', label: '异常' },
  offline: { color: 'default', label: '离线' },
  printer_state_unknown: { color: 'default', label: '未知' },
};

export const printerStatusLabel = (value: string) => PRINTER_STATUS_META[value]?.label || '未知状态';

async function request(path: string, init?: RequestInit) {
  const body = await apiService.request<ApiResponse<any>>(path, init);
  if (body.code !== 200 && body.code !== 201) throw new Error(body.message || '请求失败');
  return body.data;
}

const stateTag = (value: string) => {
  const state = PRINTER_STATUS_META[value] || { color: 'default', label: '未知状态' };
  return <Tag color={state.color}>{state.label}</Tag>;
};

export const effectivePrinterStatus = (printerStatus: string, nodeStatus?: string) =>
  nodeStatus === 'offline' ? 'offline' : printerStatus;

const Printers: React.FC = () => {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const edgeNodeFilter = searchParams.get('edge_node_id') || searchParams.get('node_id') || '';
  const printerFilter = searchParams.get('printer_id') || '';
  const [printers, setPrinters] = useState<Printer[]>([]);
  const [nodes, setNodes] = useState<Record<string, Node>>({});
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState<string | undefined>();
  const [enabledFilter, setEnabledFilter] = useState<string | undefined>();
  const [detailPrinter, setDetailPrinter] = useState<Printer | null>(null);

  const load = useCallback(async () => {
    try {
      const query = new URLSearchParams({ page: '1', page_size: '100' });
      if (printerFilter) query.set('printer_id', printerFilter);
      else if (edgeNodeFilter) query.set('edge_node_id', edgeNodeFilter);
      const [printerData, nodeData] = await Promise.all([
        request(`/admin/printers?${query}`),
        request('/admin/edge-nodes?page=1&page_size=100'),
      ]);
      setPrinters(printerData?.items || []); setNodes(Object.fromEntries((nodeData?.items || []).map((node: Node) => [node.id, node])));
    } catch { message.error('打印机信息加载失败'); } finally { setLoading(false); }
  }, [edgeNodeFilter, printerFilter]);

  useEffect(() => { load(); const timer = window.setInterval(load, 30000); return () => window.clearInterval(timer); }, [load]);
  useEffect(() => {
    if (!editing) return undefined;
    const closeEditor = (event: MouseEvent) => { if (!(event.target as HTMLElement).closest('.inline-name-editor')) setEditing(null); };
    document.addEventListener('mousedown', closeEditor);
    return () => document.removeEventListener('mousedown', closeEditor);
  }, [editing]);

  const visiblePrinters = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return printers.filter((printer) => {
      if (statusFilter && effectivePrinterStatus(printer.printer_status, nodes[printer.edge_node_id]?.connection_status) !== statusFilter) return false;
      if (enabledFilter === 'enabled' && !printer.enabled) return false;
      if (enabledFilter === 'disabled' && printer.enabled) return false;
      if (!q) return true;
      return [printer.id, printer.name, printer.display_name, printer.model, printer.edge_node_id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(q));
    });
  }, [printers, nodes, keyword, statusFilter, enabledFilter]);

  const update = async (printer: Printer, payload: object) => { try { await request(`/admin/printers/${printer.id}`, { method: 'PUT', body: JSON.stringify(payload) }); load(); } catch { message.error('打印机更新失败'); } };
  const saveName = (printer: Printer) => { update(printer, { display_name: name.trim() }); setEditing(null); };
  const remove = (printer: Printer) => Modal.confirm({ title: '删除打印机？', content: `${printer.display_name || printer.name}\n${printer.id}`, okText: '删除', okType: 'danger', cancelText: '取消', onOk: async () => { try { await request(`/admin/printers/${printer.id}`, { method: 'DELETE' }); message.success('打印机已删除'); load(); } catch { message.error('删除失败'); } } });

  const columns: ColumnsType<Printer> = [
    {
      title: '打印机',
      width: 330,
      sorter: (a, b) => (a.display_name || a.name || '').localeCompare(b.display_name || b.name || ''),
      render: (_, printer) => editing === printer.id
        ? <Space.Compact className="inline-name-editor"><Input autoFocus value={name} onChange={event => setName(event.target.value)} onPressEnter={() => saveName(printer)} placeholder="留空以清除别名" /><Button type="primary" onClick={() => saveName(printer)}>保存</Button></Space.Compact>
        : <span style={{ cursor: 'pointer', display: 'block' }} onClick={() => { setEditing(printer.id); setName(printer.display_name || printer.name || ''); }}><EntityCell primary={printer.display_name || printer.name || '未命名打印机'} secondary={printer.model || undefined} id={printer.id} /></span>,
    },
    {
      title: '所属节点',
      width: 250,
      sorter: (a, b) => (nodes[a.edge_node_id]?.alias || nodes[a.edge_node_id]?.name || a.edge_node_id).localeCompare(nodes[b.edge_node_id]?.alias || nodes[b.edge_node_id]?.name || b.edge_node_id),
      render: (_, printer) => (
        <TwoLineLink
          to={`/edge-nodes?node_id=${encodeURIComponent(printer.edge_node_id)}`}
          id={printer.edge_node_id}
          name={nodes[printer.edge_node_id]?.alias || nodes[printer.edge_node_id]?.name}
        />
      ),
    },
    {
      title: '任务',
      width: 90,
      sorter: (a, b) => (a.job_count || 0) - (b.job_count || 0),
      render: (_, printer) => (
        <RelationStack
          items={[{
            key: 'jobs',
            title: '打印任务',
            icon: <FileTextOutlined />,
            value: printer.job_count ?? 0,
            to: `/print-jobs?printer_id=${encodeURIComponent(printer.id)}`,
          }]}
        />
      ),
    },
    {
      title: '打印机当前状态',
      width: 130,
      filters: [
        { text: '就绪', value: 'idle' }, { text: '打印中', value: 'printing' }, { text: '异常', value: 'error' },
        { text: '缺纸', value: 'printer_out_of_paper' }, { text: '离线', value: 'offline' }, { text: '已停止', value: 'printer_stopped' },
      ],
      onFilter: (value, record) => effectivePrinterStatus(record.printer_status, nodes[record.edge_node_id]?.connection_status) === value,
      render: (_, printer) => stateTag(effectivePrinterStatus(printer.printer_status, nodes[printer.edge_node_id]?.connection_status)),
    },
    { title: '打印机启用状态', width: 110, render: (_, printer) => <Switch checked={printer.enabled} onChange={enabled => update(printer, { enabled })} /> },
    { title: '操作', width: 130, render: (_, printer) => <Space size="small"><Button type="link" icon={<EyeOutlined />} onClick={() => setDetailPrinter(printer)}>详情</Button><Button danger type="primary" icon={<DeleteOutlined />} onClick={() => remove(printer)} /></Space> },
  ];

  const hasUrlFilter = !!(edgeNodeFilter || printerFilter);

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Space wrap>
          <Input.Search allowClear placeholder="搜索名称、型号或 ID" style={{ width: 240 }} value={keyword} onChange={(e) => setKeyword(e.target.value)} />
          <Select allowClear placeholder="打印机状态" style={{ width: 140 }} value={statusFilter} onChange={setStatusFilter}
            options={[
              { value: 'idle', label: '就绪' }, { value: 'printing', label: '打印中' }, { value: 'error', label: '异常' },
              { value: 'printer_out_of_paper', label: '缺纸' }, { value: 'offline', label: '离线' }, { value: 'printer_stopped', label: '已停止' },
            ]} />
          <Select allowClear placeholder="启用状态" style={{ width: 120 }} value={enabledFilter} onChange={setEnabledFilter}
            options={[{ value: 'enabled', label: '已启用' }, { value: 'disabled', label: '已停用' }]} />
          {hasUrlFilter ? (
            <>
              <span>已筛选{printerFilter ? '打印机' : '节点'}</span>
              <Button onClick={() => navigate('/printers')}>清除筛选</Button>
            </>
          ) : null}
        </Space>
      </div>
      <Card><Table rowKey="id" loading={loading} dataSource={visiblePrinters} columns={columns} scroll={{ x: 1080 }} pagination={{ pageSize: 20, showSizeChanger: false }} /></Card>
      <Drawer title="打印机详情" open={!!detailPrinter} onClose={() => setDetailPrinter(null)} width={440}>
        {detailPrinter ? <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="打印机名称">{detailPrinter.display_name || detailPrinter.name || '-'}</Descriptions.Item>
          <Descriptions.Item label="设备型号">{detailPrinter.model || '-'}</Descriptions.Item>
          <Descriptions.Item label="当前状态">{stateTag(effectivePrinterStatus(detailPrinter.printer_status, nodes[detailPrinter.edge_node_id]?.connection_status))}</Descriptions.Item>
          <Descriptions.Item label="启用状态">{detailPrinter.enabled ? '启用' : '停用'}</Descriptions.Item>
          <Descriptions.Item label="所属节点">{nodes[detailPrinter.edge_node_id]?.alias || nodes[detailPrinter.edge_node_id]?.name || '-'}</Descriptions.Item>
          <Descriptions.Item label="任务数">{detailPrinter.job_count ?? 0}</Descriptions.Item>
          <Descriptions.Item label="打印机 ID"><FullIdentifier value={detailPrinter.id} /></Descriptions.Item>
          <Descriptions.Item label="节点 ID"><FullIdentifier value={detailPrinter.edge_node_id} /></Descriptions.Item>
        </Descriptions> : null}
      </Drawer>
    </div>
  );
};

export default Printers;
