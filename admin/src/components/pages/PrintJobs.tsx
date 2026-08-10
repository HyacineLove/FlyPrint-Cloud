import React, { useCallback, useEffect, useState } from 'react';
import { Button, Card, Descriptions, Drawer, Input, Select, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { EyeOutlined } from '@ant-design/icons';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { buildApiUrl, buildAuthUrl } from '../../config';
import { DateTimeValue, EntityCell, FullIdentifier } from '../DisplayValue';

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
  const [detailJob, setDetailJob] = useState<PrintJob | null>(null);

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
      title: '任务',
      width: 240,
      sorter: (a, b) => a.id.localeCompare(b.id),
      render: (_, job) => <EntityCell primary={job.name || '未命名文件'} id={job.id} />,
    },
    {
      title: '打印人',
      width: 220,
      sorter: (a, b) => (a.user_email || '').localeCompare(b.user_email || ''),
      render: (_, job) => job.user_email ? (
        <EntityCell
          primary={<Link to={`/users?email=${encodeURIComponent(job.user_email || '')}`}>{job.user_email}</Link>}
          secondary={job.user_name ? <span style={{ color: '#8c8c8c', fontSize: 12 }}>{job.user_name}</span> : undefined}
        />
      ) : <div style={{ color: '#8c8c8c' }}>{job.user_name || '-'}</div>,
    },
    {
      title: '任务来源',
      width: 170,
      sorter: (a, b) => (a.initiator_code || a.initiator_name || '').localeCompare(b.initiator_code || b.initiator_name || ''),
      render: (_, job) => <EntityCell
        primary={job.initiator_name || job.site_portal_code || job.initiator_code || '主系统'}
        secondary={job.site_portal_code || job.initiator_code || undefined}
      />,
    },
    {
      title: '设备',
      width: 220,
      sorter: (a, b) => (a.edge_node_id || '').localeCompare(b.edge_node_id || ''),
      render: (_, job) => <EntityCell
        primary={job.printer_name || '未关联打印机'}
        secondary={job.node_name ? `节点：${job.node_name}` : job.edge_node_id ? '节点未命名' : undefined}
      />,
    },
    {
      title: '打印内容',
      width: 190,
      render: (_, job) => {
        const color = job.color_mode === 'color' ? '彩色' : '黑白';
        const duplex = job.duplex_mode === 'longedge' ? '双面长边'
          : job.duplex_mode === 'shortedge' ? '双面短边' : '单面';
        return <EntityCell
          primary={`${job.page_count ?? 0} 页 × ${job.copies ?? 0} 份`}
          secondary={`${job.paper_size || '-'} / ${color} / ${duplex}`}
        />;
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
    { title: '操作', width: 80, render: (_, job) => <Button type="link" icon={<EyeOutlined />} onClick={() => setDetailJob(job)}>详情</Button> },
  ];

  const hasUrlFilter = !!(edgeNodeFilter || printerFilter || initiatorFilter || userEmailFilter);

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Space wrap>
          <Input.Search allowClear placeholder="搜索名称、用户或 ID" style={{ width: 240 }} value={keyword} onChange={(e) => setKeyword(e.target.value)} onSearch={() => load(1)} />
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
      <Card><Table rowKey="id" loading={loading} dataSource={jobs} columns={columns} scroll={{ x: 1450 }} pagination={{ current: page, total, pageSize: 20, showSizeChanger: false, onChange: next => load(next) }} /></Card>
      <Drawer title="打印任务详情" open={!!detailJob} onClose={() => setDetailJob(null)} width={480}>
        {detailJob ? <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="文件名">{detailJob.name || '-'}</Descriptions.Item>
          <Descriptions.Item label="任务结果">{result(detailJob)}</Descriptions.Item>
          <Descriptions.Item label="打印人">{detailJob.user_name || detailJob.user_email || '-'}</Descriptions.Item>
          <Descriptions.Item label="任务来源">{detailJob.initiator_name || detailJob.site_portal_code || detailJob.initiator_code || '主系统'}</Descriptions.Item>
          <Descriptions.Item label="节点">{detailJob.node_name || '-'}</Descriptions.Item>
          <Descriptions.Item label="打印机">{detailJob.printer_name || '-'}</Descriptions.Item>
          <Descriptions.Item label="打印内容">{`${detailJob.page_count ?? 0} 页 × ${detailJob.copies ?? 0} 份`}</Descriptions.Item>
          <Descriptions.Item label="打印参数">{`${detailJob.paper_size || '-'} / ${detailJob.color_mode === 'color' ? '彩色' : '黑白'} / ${detailJob.duplex_mode === 'longedge' ? '双面长边' : detailJob.duplex_mode === 'shortedge' ? '双面短边' : '单面'}`}</Descriptions.Item>
          <Descriptions.Item label="额度">{`${detailJob.quota_reserved ?? 0} / ${detailJob.quota_consumed ?? '-'} 点`}</Descriptions.Item>
          <Descriptions.Item label="创建时间"><DateTimeValue value={detailJob.created_at} /></Descriptions.Item>
          <Descriptions.Item label="终态时间"><DateTimeValue value={detailJob.end_time} /></Descriptions.Item>
          <Descriptions.Item label="任务 ID"><FullIdentifier value={detailJob.id} /></Descriptions.Item>
          <Descriptions.Item label="用户邮箱"><FullIdentifier value={detailJob.user_email} /></Descriptions.Item>
          <Descriptions.Item label="节点 ID"><FullIdentifier value={detailJob.edge_node_id} /></Descriptions.Item>
          <Descriptions.Item label="打印机 ID"><FullIdentifier value={detailJob.printer_id} /></Descriptions.Item>
        </Descriptions> : null}
      </Drawer>
    </div>
  );
};

export default PrintJobs;
