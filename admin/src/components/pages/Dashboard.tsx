import React, { useCallback, useEffect, useRef, useState } from 'react';
import * as echarts from 'echarts';
import { Card, Col, Collapse, Empty, Row, Segmented, Space, Table, Tooltip, Typography, message } from 'antd';
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table';
import { CloudServerOutlined, FileTextOutlined, PrinterOutlined } from '@ant-design/icons';
import { buildApiUrl, buildAuthUrl } from '../../config';
import { DateTimeValue, TwoLineLink, TwoLineValue } from '../DisplayValue';

interface TrendBucket { label: string; completed: number; failed: number; }
interface Overview { fault_nodes: number; online_nodes: number; total_nodes: number; fault_printers: number; online_printers: number; total_printers: number; }
interface AlertRecord {
  id: string; resource_type: 'node' | 'printer' | 'job'; resource_id: string;
  node_id?: string; node_name?: string; printer_id?: string; printer_name?: string;
  job_id?: string; job_name?: string; title: string; status: 'open' | 'resolved';
  first_seen_at: string; last_seen_at: string; resolved_at?: string; duration_seconds?: number;
}
interface AlertPage { items: AlertRecord[]; total: number; page: number; page_size: number; }
interface Maintenance extends AlertPage { summary: Overview; }

const emptyOverview: Overview = { fault_nodes: 0, online_nodes: 0, total_nodes: 0, fault_printers: 0, online_printers: 0, total_printers: 0 };
const emptyAlerts: AlertPage = { items: [], total: 0, page: 1, page_size: 10 };

async function api(path: string) {
  const auth = await fetch(buildAuthUrl('me'));
  const token = (await auth.json())?.data?.access_token;
  const response = await fetch(buildApiUrl(path), { headers: token ? { Authorization: `Bearer ${token}` } : {} });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return (await response.json())?.data || {};
}

const axisLabel = (period: string, label: string) => {
  if (period === 'month') return label.split('-').pop() || label;
  if (period === 'year') return label.slice(-2);
  return label;
};

const resourceCell = (id?: string, name?: string) => <TwoLineValue id={id} name={name} />;
const nodeCell = (id?: string, name?: string) =>
  id ? <TwoLineLink to={`/edge-nodes?node_id=${encodeURIComponent(id)}`} id={id} name={name} /> : resourceCell(id, name);
const printerCell = (id?: string, name?: string) =>
  id ? <TwoLineLink to={`/printers?printer_id=${encodeURIComponent(id)}`} id={id} name={name} /> : resourceCell(id, name);

const TrendChart: React.FC<{ buckets: TrendBucket[]; period: string }> = ({ buckets, period }) => {
  const element = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!element.current) return undefined;
    const chart = echarts.init(element.current);
    const interval = period === 'day' ? 2 : period === 'month' ? 2 : 0;
    chart.setOption({
      grid: { top: 22, right: 18, bottom: 42, left: 42 }, tooltip: { trigger: 'axis' },
      legend: { bottom: 0, data: ['成功任务', '失败任务'] },
      xAxis: { type: 'category', boundaryGap: false, data: buckets.map(item => item.label), axisLabel: { interval, hideOverlap: true, formatter: (label: string) => axisLabel(period, label) } },
      yAxis: { type: 'value', min: 0, max: (value: { max: number }) => Math.max(10, Math.ceil(value.max / 10) * 10), minInterval: 1, splitLine: { lineStyle: { color: '#f0f0f0' } } },
      series: [
        { name: '成功任务', type: 'line', smooth: true, showSymbol: false, data: buckets.map(item => item.completed), lineStyle: { color: '#52c41a' }, itemStyle: { color: '#52c41a' } },
        { name: '失败任务', type: 'line', smooth: true, showSymbol: false, data: buckets.map(item => item.failed), lineStyle: { color: '#ff4d4f' }, itemStyle: { color: '#ff4d4f' } },
      ],
    });
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(element.current);
    return () => { observer.disconnect(); chart.dispose(); };
  }, [buckets, period]);
  return buckets.length ? <div ref={element} style={{ height: 240, width: '100%' }} /> : <Empty description="暂无打印任务数据" />;
};

const StateCard: React.FC<{ icon: React.ReactNode; title: string; fault: number; online: number; total: number }> = ({ icon, title, fault, online, total }) => (
  <Card size="small" style={{ height: '100%' }}>
    <Space align="center" style={{ marginBottom: 12 }}>{icon}<Typography.Text strong style={{ fontSize: 16 }}>{title}</Typography.Text></Space>
    <Row gutter={8}>
      <Col span={8}><div style={{ color: '#cf1322', fontSize: 22, fontWeight: 600 }}>{fault}</div><Typography.Text type="secondary">故障</Typography.Text></Col>
      <Col span={8}><div style={{ color: '#389e0d', fontSize: 22, fontWeight: 600 }}>{online}</div><Typography.Text type="secondary">在线</Typography.Text></Col>
      <Col span={8}><div style={{ fontSize: 22, fontWeight: 600 }}>{total}</div><Typography.Text type="secondary">总数</Typography.Text></Col>
    </Row>
  </Card>
);

const Dashboard: React.FC = () => {
  const [period, setPeriod] = useState<'day' | 'month' | 'year'>('day');
  const [trends, setTrends] = useState<TrendBucket[]>([]);
  const [taskPeriod, setTaskPeriod] = useState<'day' | 'month' | 'year'>('day');
  const [taskTrends, setTaskTrends] = useState<TrendBucket[]>([]);
  const [maintenance, setMaintenance] = useState<Maintenance>({ ...emptyAlerts, page_size: 20, summary: emptyOverview });
  const [history, setHistory] = useState<AlertPage>(emptyAlerts);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [loading, setLoading] = useState(true);

  const loadCurrent = useCallback(async (page = 1) => {
    const [trend, alert] = await Promise.all([api(`/admin/dashboard/trends?period=${period}`), api(`/admin/dashboard/maintenance?page=${page}&page_size=20`)]);
    setTrends(trend.buckets || []);
    setMaintenance({ items: alert.items || [], total: alert.total || 0, page: alert.page || page, page_size: alert.page_size || 20, summary: alert.summary || emptyOverview });
  }, [period]);

  const loadHistory = useCallback(async (page = 1) => {
    setHistoryLoading(true);
    try {
      const data = await api(`/admin/alerts/history?status=resolved&page=${page}&page_size=10`);
      setHistory({ items: data.items || [], total: data.total || 0, page: data.page || page, page_size: data.page_size || 10 });
    } finally { setHistoryLoading(false); }
  }, []);

  useEffect(() => { setLoading(true); loadCurrent().catch(() => message.error('仪表盘加载失败')).finally(() => setLoading(false)); }, [loadCurrent]);
  useEffect(() => { api(`/admin/dashboard/trends?period=${taskPeriod}`).then(data => setTaskTrends(data.buckets || [])).catch(() => message.error('打印任务汇总加载失败')); }, [taskPeriod]);
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) {
        loadCurrent().catch(() => undefined);
        api(`/admin/dashboard/trends?period=${taskPeriod}`).then(data => setTaskTrends(data.buckets || [])).catch(() => undefined);
      }
    }, 30000);
    return () => window.clearInterval(timer);
  }, [loadCurrent, taskPeriod]);

  const taskSummary = taskTrends.reduce((summary, item) => ({ completed: summary.completed + item.completed, failed: summary.failed + item.failed }), { completed: 0, failed: 0 });
  const currentColumns: ColumnsType<AlertRecord> = [
    { title: '节点', key: 'node', width: 220, render: (_, row) => nodeCell(row.node_id || (row.resource_type === 'node' ? row.resource_id : undefined), row.node_name) },
    { title: '打印机', key: 'printer', width: 220, render: (_, row) => printerCell(row.printer_id || (row.resource_type === 'printer' ? row.resource_id : undefined), row.printer_name) },
    { title: '任务', key: 'job', width: 220, render: (_, row) => resourceCell(row.job_id || (row.resource_type === 'job' ? row.resource_id : undefined), row.job_name) },
    { title: '问题', dataIndex: 'title', width: 220, render: value => <Tooltip title={value}>{value}</Tooltip> },
    { title: '起始时间', dataIndex: 'first_seen_at', width: 180, render: value => <DateTimeValue value={value} /> },
  ];
  const historyColumns: ColumnsType<AlertRecord> = [...currentColumns, { title: '恢复时间', dataIndex: 'resolved_at', width: 180, render: value => <DateTimeValue value={value} /> }];
  const pagination = (data: AlertPage, onChange: (page: number) => void): TablePaginationConfig => ({ current: data.page, pageSize: data.page_size, total: data.total, showSizeChanger: false, showQuickJumper: true, onChange });

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Row gutter={[16, 16]}>
      <Col xs={24} lg={17}><Card style={{ height: '100%' }} extra={<Segmented value={period} onChange={value => setPeriod(value as typeof period)} options={[{ label: '当天', value: 'day' }, { label: '本月', value: 'month' }, { label: '本年', value: 'year' }]} />} loading={loading}><TrendChart buckets={trends} period={period} /></Card></Col>
      <Col xs={24} lg={7}><Space direction="vertical" size={16} style={{ width: '100%' }}>
        <StateCard icon={<CloudServerOutlined />} title="节点状态" fault={maintenance.summary.fault_nodes} online={maintenance.summary.online_nodes} total={maintenance.summary.total_nodes} />
        <StateCard icon={<PrinterOutlined />} title="打印机状态" fault={maintenance.summary.fault_printers} online={maintenance.summary.online_printers} total={maintenance.summary.total_printers} />
        <Card size="small"><div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 12 }}><Space align="center"><FileTextOutlined /><Typography.Text strong style={{ fontSize: 16 }}>打印任务汇总</Typography.Text></Space><Segmented size="small" value={taskPeriod} onChange={value => setTaskPeriod(value as typeof taskPeriod)} options={[{ label: '当天', value: 'day' }, { label: '本月', value: 'month' }, { label: '本年', value: 'year' }]} /></div><Row gutter={8}><Col span={8}><div style={{ color: '#cf1322', fontSize: 22, fontWeight: 600 }}>{taskSummary.failed}</div><Typography.Text type="secondary">失败</Typography.Text></Col><Col span={8}><div style={{ color: '#389e0d', fontSize: 22, fontWeight: 600 }}>{taskSummary.completed}</div><Typography.Text type="secondary">成功</Typography.Text></Col><Col span={8}><div style={{ fontSize: 22, fontWeight: 600 }}>{taskSummary.failed + taskSummary.completed}</div><Typography.Text type="secondary">总数</Typography.Text></Col></Row></Card>
      </Space></Col>
    </Row>
    <Card title="当前告警"><Table rowKey="id" loading={loading} dataSource={maintenance.items} columns={currentColumns} pagination={pagination(maintenance, page => loadCurrent(page).catch(() => message.error('告警加载失败')))} locale={{ emptyText: <Empty description="暂无当前告警" /> }} /></Card>
    <Collapse activeKey={historyOpen ? ['history'] : []} onChange={keys => { const open = (Array.isArray(keys) ? keys : [keys]).includes('history'); setHistoryOpen(open); if (open && history.items.length === 0 && history.total === 0) loadHistory(1).catch(() => message.error('告警历史加载失败')); }} items={[{ key: 'history', label: '告警历史', children: <Table rowKey="id" size="small" loading={historyLoading} dataSource={history.items} columns={historyColumns} pagination={pagination(history, page => loadHistory(page).catch(() => message.error('告警历史加载失败')))} locale={{ emptyText: <Empty description="暂无告警历史" /> }} /> }]} />
  </Space>;
};

export default Dashboard;
