import React, { useCallback, useEffect, useState } from 'react';
import { Button, Card, Input, Select, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { buildApiUrl, buildAuthUrl } from '../../config';
import { DateTimeValue, TwoLineLink } from '../DisplayValue';

interface PrintJob {
  id: string; name: string; initiator_name?: string; initiator_code?: string;
  user_email?: string; user_name?: string;
  edge_node_id?: string; node_name?: string;
  printer_id?: string; printer_name?: string; copies?: number; created_at: string; end_time?: string;
  site_portal_code?: string; page_count?: number; paper_size?: string; color_mode?: string; duplex_mode?: string;
  quota_reserved?: number; quota_consumed?: number;
  status: string; error_code?: string; error_message?: string;
}

async function listJobs(page: number, filters: {
  edgeNodeId?: string; printerId?: string; initiatorCode?: string; userEmail?: string; status?: string; keyword?: string;
}) {
  const me = await fetch(buildAuthUrl('me')); const token = (await me.json())?.data?.access_token;
  const query = new URLSearchParams({ page: String(page), pageSize: '20' });
  if (filters.edgeNodeId) query.set('edge_node_id', filters.edgeNodeId);
  if (filters.printerId) query.set('printer_id', filters.printerId);
  if (filters.initiatorCode) query.set('initiator_code', filters.initiatorCode);
  if (filters.userEmail) query.set('user_email', filters.userEmail);
  if (filters.status) query.set('status', filters.status);
  const response = await fetch(buildApiUrl(`/admin/print-jobs?${query}`), { headers: token ? { Authorization: `Bearer ${token}` } : {} });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.json();
}

const result = (job: PrintJob) => {
  const labels: Record<string, [string, string]> = {
    completed: ['success', '完成'], failed: ['error', '失败'], canceled: ['default', '已取消'], cancelled: ['default', '已取消'],
    unconfirmed: ['warning', '结果未确认'], pending: ['default', '等待中'], dispatched: ['processing', '已投递'], processing: ['processing', '打印中'],
  };
  const [color, text] = labels[job.status] || ['default', job.status];
  return <span><Tag color={color}>{text}</Tag>{job.error_message && <div style={{ color: '#8c8c8c', fontSize: 12 }}>{job.error_message}</div>}</span>;
};

const PrintJobs: React.FC = () => {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const edgeNodeFilter = searchParams.get('edge_node_id') || searchParams.get('node_id') || '';
  const printerFilter = searchParams.get('printer_id') || '';
  const initiatorFilter = searchParams.get('initiator_code') || '';
  const userEmailFilter = searchParams.get('user_email') || '';
  const [jobs, setJobs] = useState<PrintJob[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [statusFilter, setStatusFilter] = useState<string | undefined>();
  const [keyword, setKeyword] = useState('');

  const load = useCallback(async (nextPage = page) => {
    try {
      setLoading(true);
      const data = await listJobs(nextPage, {
        edgeNodeId: edgeNodeFilter,
        printerId: printerFilter,
        initiatorCode: initiatorFilter,
        userEmail: userEmailFilter,
        status: statusFilter,
      });
      let nextJobs: PrintJob[] = data.jobs || [];
      const q = keyword.trim().toLowerCase();
      if (q) {
        nextJobs = nextJobs.filter((job) => [job.id, job.name, job.user_email, job.user_name, job.node_name, job.printer_name, job.initiator_name, job.edge_node_id, job.printer_id]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(q)));
      }
      setJobs(nextJobs);
      setTotal(data.pagination?.total || 0);
      setPage(nextPage);
    } catch {
      message.error('打印任务加载失败');
    } finally {
      setLoading(false);
    }
  }, [page, edgeNodeFilter, printerFilter, initiatorFilter, userEmailFilter, statusFilter, keyword]);

  useEffect(() => { load(1); }, [edgeNodeFilter, printerFilter, initiatorFilter, userEmailFilter, statusFilter]); // eslint-disable-line react-hooks/exhaustive-deps
  // 打印任务通常在几十秒内完成；30 秒轮询会让 processing 状态经常被直接跳过。
  useEffect(() => { const timer = window.setInterval(() => { if (!document.hidden) load(); }, 5000); return () => window.clearInterval(timer); }, [load]);
  const terminal = (status: string) => ['completed', 'failed', 'cancelled', 'canceled', 'unconfirmed'].includes(status);

  const columns: ColumnsType<PrintJob> = [
    {
      title: '任务 ID',
      width: 260,
      sorter: (a, b) => a.id.localeCompare(b.id),
      render: (_, job) => (
        <span>
          <div style={{ wordBreak: 'break-all' }}>{job.id}</div>
          <div style={{ color: '#8c8c8c', fontSize: 12 }}>{job.name || '-'}</div>
        </span>
      ),
    },
    {
      title: '打印人',
      width: 220,
      sorter: (a, b) => (a.user_email || '').localeCompare(b.user_email || ''),
      render: (_, job) => job.user_email ? (
        <span>
          <Link to={`/users?email=${encodeURIComponent(job.user_email || '')}`}>{job.user_email}</Link>
          <div style={{ color: '#8c8c8c', fontSize: 12 }}>{job.user_name || '-'}</div>
        </span>
      ) : <div style={{ color: '#8c8c8c' }}>{job.user_name || '-'}</div>,
    },
    {
      title: '任务来源',
      width: 160,
      sorter: (a, b) => (a.initiator_code || a.initiator_name || '').localeCompare(b.initiator_code || b.initiator_name || ''),
      render: (_, job) => {
        if (job.site_portal_code) {
          return <span>
            <div>{job.initiator_name || job.site_portal_code}</div>
            <div style={{ color: '#8c8c8c', fontSize: 12 }}>{job.site_portal_code}</div>
          </span>;
        }
        if (job.initiator_code) {
          return <TwoLineLink to={`/site-portals?code=${encodeURIComponent(job.initiator_code)}`} id={job.initiator_code} name={job.initiator_name || job.initiator_code} />;
        }
        return job.initiator_name || '主系统';
      },
    },
    {
      title: '节点 ID',
      width: 220,
      sorter: (a, b) => (a.edge_node_id || '').localeCompare(b.edge_node_id || ''),
      render: (_, job) => job.edge_node_id
        ? <TwoLineLink to={`/edge-nodes?node_id=${encodeURIComponent(job.edge_node_id)}`} id={job.edge_node_id} name={job.node_name} />
        : <>-</>,
    },
    {
      title: '打印机 ID',
      width: 220,
      sorter: (a, b) => (a.printer_id || '').localeCompare(b.printer_id || ''),
      render: (_, job) => job.printer_id
        ? <TwoLineLink to={`/printers?printer_id=${encodeURIComponent(job.printer_id)}`} id={job.printer_id} name={job.printer_name} />
        : <>-</>,
    },
    {
      title: '页数 / 份数',
      width: 120,
      render: (_, job) => `${job.page_count ?? 0} 页 × ${job.copies ?? 0} 份`,
    },
    {
      title: '打印参数',
      width: 180,
      render: (_, job) => {
        const color = job.color_mode === 'color' ? '彩色' : '黑白';
        const duplex = job.duplex_mode === 'longedge' ? '双面长边'
          : job.duplex_mode === 'shortedge' ? '双面短边' : '单面';
        return `${job.paper_size || '-'} / ${color} / ${duplex}`;
      },
    },
    {
      title: '额度（预占 / 消耗）',
      width: 160,
      render: (_, job) => `${job.quota_reserved ?? 0} / ${job.quota_consumed ?? '-'} 点`,
    },
    { title: '任务创建时间', dataIndex: 'created_at', width: 150, sorter: (a, b) => a.created_at.localeCompare(b.created_at), render: value => <DateTimeValue value={value} /> },
    { title: '任务终态时间', width: 150, render: (_, job) => terminal(job.status) && job.end_time ? <DateTimeValue value={job.end_time} /> : '-' },
    {
      title: '任务结果',
      width: 140,
      filters: [
        { text: '完成', value: 'completed' }, { text: '失败', value: 'failed' }, { text: '打印中', value: 'processing' },
        { text: '等待中', value: 'pending' }, { text: '已投递', value: 'dispatched' }, { text: '结果未确认', value: 'unconfirmed' },
      ],
      onFilter: (value, record) => record.status === value,
      render: (_, job) => result(job),
    },
  ];

  const hasUrlFilter = !!(edgeNodeFilter || printerFilter || initiatorFilter || userEmailFilter);

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Space wrap>
          <Input.Search allowClear placeholder="搜索当前页 ID / 名称" style={{ width: 240 }} value={keyword} onChange={(e) => setKeyword(e.target.value)} onSearch={() => load(1)} />
          <Select allowClear placeholder="任务状态" style={{ width: 140 }} value={statusFilter} onChange={(value) => setStatusFilter(value)}
            options={[
              { value: 'completed', label: '完成' }, { value: 'failed', label: '失败' }, { value: 'processing', label: '打印中' },
              { value: 'pending', label: '等待中' }, { value: 'dispatched', label: '已投递' }, { value: 'unconfirmed', label: '结果未确认' },
            ]} />
          {hasUrlFilter ? (
            <>
              <span>
                已筛选
                {edgeNodeFilter ? '节点' : ''}
                {edgeNodeFilter && (printerFilter || initiatorFilter) ? '/' : ''}
                {printerFilter ? '打印机' : ''}
                {(edgeNodeFilter || printerFilter) && initiatorFilter ? '/' : ''}
                {initiatorFilter ? '来源' : ''}
                {(edgeNodeFilter || printerFilter || initiatorFilter) && userEmailFilter ? '/' : ''}
                {userEmailFilter ? '打印人' : ''}
              </span>
              <Button onClick={() => navigate('/print-jobs')}>清除筛选</Button>
            </>
          ) : null}
        </Space>
      </div>
      <Card><Table rowKey="id" loading={loading} dataSource={jobs} columns={columns} scroll={{ x: 1900 }} pagination={{ current: page, total, pageSize: 20, showSizeChanger: false, onChange: next => load(next) }} /></Card>
    </div>
  );
};

export default PrintJobs;
