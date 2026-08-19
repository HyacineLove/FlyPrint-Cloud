import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Descriptions, Drawer, Form, Input, InputNumber, Modal, Select, Space, Switch, Table, message } from 'antd';
import { EditOutlined, EyeOutlined, KeyOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table';
import type { FilterValue, SorterResult } from 'antd/es/table/interface';
import { Link, useSearchParams } from 'react-router-dom';
import { apiService } from '../../services/api';
import type { ApiResponse } from '../../services/api';
import { mapApiError } from '../../utils/mapApiError';
import { EntityCell, FullIdentifier } from '../DisplayValue';

interface ManagedUser {
  id: string;
  username: string;
  email: string;
  account_kind: 'operator' | 'external' | string;
  role: 'admin' | 'operator' | 'viewer' | string;
  status: 'active' | 'inactive' | string;
  last_login?: string;
  created_at?: string;
  print_quota_balance: number;
}

interface UserFormValues {
  username: string;
  email: string;
  password?: string;
  role: string;
}

const roleOptions = [
  { value: 'admin', label: '管理员' },
  { value: 'operator', label: '运维人员' },
  { value: 'viewer', label: '普通用户' },
];

const operatorRoleOptions = roleOptions.filter((item) => item.value !== 'viewer');
const accountKindLabel = (value: string) => value === 'external' ? 'SSO 打印用户' : '运维账号';

const formatDate = (value?: string) => (value ? new Date(value).toLocaleString() : '-');

const Users: React.FC = () => {
  const [searchParams] = useSearchParams();
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<ManagedUser>();
  const [formVisible, setFormVisible] = useState(false);
  const [passwordUser, setPasswordUser] = useState<ManagedUser>();
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [quotaUser, setQuotaUser] = useState<ManagedUser>();
  const [quotaVisible, setQuotaVisible] = useState(false);
  const [savingEnabled, setSavingEnabled] = useState<string>();
  const [deleting, setDeleting] = useState<string>();
  const [detailUser, setDetailUser] = useState<ManagedUser | null>(null);
  const [editingUsernameId, setEditingUsernameId] = useState<string>();
  const [usernameDraft, setUsernameDraft] = useState('');
  const usernameEditorRef = useRef<HTMLDivElement>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [total, setTotal] = useState(0);
  const [search, setSearch] = useState(searchParams.get('email') || '');
  const [role, setRole] = useState<string>();
  const [status, setStatus] = useState<string>();
  const [sortBy, setSortBy] = useState('created_at');
  const [sortOrder, setSortOrder] = useState('desc');
  const [form] = Form.useForm<UserFormValues>();
  const [passwordForm] = Form.useForm<{ new_password: string }>();
  const [quotaForm] = Form.useForm<{ amount: number; reason: string }>();

  const load = useCallback(async (nextPage = page) => {
    setLoading(true);
    try {
      const query = new URLSearchParams({ page: String(nextPage), page_size: String(pageSize) });
      if (search.trim()) query.set('search', search.trim());
      if (role) query.set('role', role);
      if (status) query.set('status', status);
      if (sortBy) query.set('sort_by', sortBy);
      if (sortOrder) query.set('sort_order', sortOrder);
      const result = await apiService.get<{ items: ManagedUser[]; pagination?: { total?: number } }>(`/admin/users?${query.toString()}`);
      if (result.code !== 200) throw new Error(result.message || '加载用户失败');
      setUsers(result.data?.items || []);
      setTotal(result.data?.pagination?.total || 0);
      setPage(nextPage);
    } catch (error) {
      message.error(mapApiError(error, '加载用户失败'));
    } finally {
      setLoading(false);
    }
  }, [page, pageSize, role, search, sortBy, sortOrder, status]);

  useEffect(() => {
    void load(1);
  }, [load]);

  useEffect(() => {
    const handleOutsideClick = (event: MouseEvent) => {
      if (editingUsernameId && usernameEditorRef.current && !usernameEditorRef.current.contains(event.target as Node)) {
        setEditingUsernameId(undefined);
      }
    };
    document.addEventListener('mousedown', handleOutsideClick);
    return () => document.removeEventListener('mousedown', handleOutsideClick);
  }, [editingUsernameId]);

  const reload = () => void load(page);

  const openCreate = () => {
    setEditing(undefined);
    form.resetFields();
    form.setFieldsValue({ email: '', username: '', password: '', role: 'operator' });
    setFormVisible(true);
  };

  const openEdit = (user: ManagedUser) => {
    setEditing(user);
    form.setFieldsValue({ email: user.email, username: user.username, role: user.role });
    setFormVisible(true);
  };

  const save = async (values: UserFormValues) => {
    const payload = editing
      ? { username: values.username.trim(), role: values.role }
      : { username: values.username.trim(), email: values.email.trim(), password: values.password, role: values.role };
    try {
      const result = await apiService.request<ApiResponse<any>>(editing ? `/admin/users/${editing.id}` : '/admin/users', {
        method: editing ? 'PUT' : 'POST',
        body: JSON.stringify(payload),
      });
      if (result.code !== 200 && result.code !== 201) throw new Error(result.message || '保存用户失败');
      message.success(editing ? '用户已更新' : '用户已创建');
      setFormVisible(false);
      await load(page);
    } catch (error) {
      message.error(mapApiError(error, '保存用户失败'));
    }
  };

  const toggleEnabled = async (user: ManagedUser, enabled: boolean) => {
    setSavingEnabled(user.id);
    try {
      const result = await apiService.request<ApiResponse<any>>(`/admin/users/${user.id}/enabled`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled }),
      });
      if (result.code !== 200) throw new Error(result.message || '更新用户状态失败');
      await load(page);
    } catch (error) {
      message.error(mapApiError(error, '更新用户状态失败'));
    } finally {
      setSavingEnabled(undefined);
    }
  };

  const remove = (user: ManagedUser) => {
    Modal.confirm({
      title: '删除用户',
      content: `确定删除 ${user.email}？该用户的打印任务会一并删除，上传文件不会删除。`,
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        setDeleting(user.id);
        try {
          const result = await apiService.request<ApiResponse<any>>(`/admin/users/${user.id}`, {
            method: 'DELETE',
          });
          if (result.code !== 200) throw new Error(result.message || '删除用户失败');
          message.success('用户已删除');
          await load(page);
        } catch (error) {
          message.error(mapApiError(error, '删除用户失败'));
        } finally {
          setDeleting(undefined);
        }
      },
    });
  };

  const saveUsername = async (user: ManagedUser) => {
    const nextUsername = usernameDraft.trim();
    if (!nextUsername || nextUsername === user.username) {
      setEditingUsernameId(undefined);
      return;
    }
    try {
      const result = await apiService.request<ApiResponse<any>>(`/admin/users/${user.id}`, {
        method: 'PUT',
        body: JSON.stringify({ username: nextUsername, role: user.role }),
      });
      if (result.code !== 200) throw new Error(result.message || '更新用户名失败');
      setEditingUsernameId(undefined);
      await load(page);
    } catch (error) {
      message.error(mapApiError(error, '更新用户名失败'));
    }
  };

  const startUsernameEdit = (user: ManagedUser) => {
    setEditingUsernameId(user.id);
    setUsernameDraft(user.username);
  };

  const handleTableChange = (pagination: TablePaginationConfig, _filters: Record<string, FilterValue | null>, sorter: SorterResult<ManagedUser> | SorterResult<ManagedUser>[]) => {
    const item = Array.isArray(sorter) ? sorter[0] : sorter;
    const nextSortBy = typeof item?.field === 'string' ? item.field : 'created_at';
    const nextSortOrder = item?.order === 'ascend' ? 'asc' : 'desc';
    setSortBy(nextSortBy);
    setSortOrder(nextSortOrder);
    setPage(pagination.current || 1);
  };

  const columns: ColumnsType<ManagedUser> = [
    {
      title: '用户', dataIndex: 'email', width: 360, sorter: true,
      render: (email: string, user) => <EntityCell
        primary={<Link to={`/print-jobs?user_email=${encodeURIComponent(email)}`}>{email}</Link>}
        secondary={editingUsernameId === user.id ? (
          <div className="inline-username-editor" ref={usernameEditorRef}>
            <Input autoFocus value={usernameDraft} onChange={(event) => setUsernameDraft(event.target.value)} onPressEnter={() => void saveUsername(user)} onKeyDown={(event) => { if (event.key === 'Escape') setEditingUsernameId(undefined); }} />
          </div>
        ) : user.account_kind === 'external'
          ? <span>{user.username || '-'}</span>
          : <Button type="text" onClick={() => startUsernameEdit(user)}>{user.username || '-'}</Button>}
        id={user.id}
      />,
    },
    { title: '账号类型', dataIndex: 'account_kind', width: 130, render: accountKindLabel },
    { title: '角色', dataIndex: 'role', sorter: true, render: (value: string) => roleOptions.find((item) => item.value === value)?.label || value },
    {
      title: '启用', dataIndex: 'status', width: 90, sorter: true,
      render: (value: string, user) => <Switch checked={value === 'active'} loading={savingEnabled === user.id} onChange={(checked) => void toggleEnabled(user, checked)} aria-label={`${user.email}启用状态`} />,
    },
    { title: '最后登录', dataIndex: 'last_login', sorter: true, render: formatDate },
    {
      title: '打印额度', dataIndex: 'print_quota_balance', width: 110,
      render: (value: number) => `${value ?? 0} 点`,
    },
    {
      title: '操作', width: 420,
      render: (_, user) => (
        <Space>
          <Button icon={<EyeOutlined />} onClick={() => setDetailUser(user)}>详情</Button>
          {user.account_kind !== 'external' ? <Button icon={<EditOutlined />} onClick={() => openEdit(user)}>编辑</Button> : null}
          {user.account_kind !== 'external' ? <Button icon={<KeyOutlined />} onClick={() => { setPasswordUser(user); passwordForm.resetFields(); setPasswordVisible(true); }}>改密码</Button> : null}
          <Button onClick={() => {
            setQuotaUser(user);
            quotaForm.resetFields();
            setQuotaVisible(true);
          }}>增加额度</Button>
          <Button danger loading={deleting === user.id} onClick={() => remove(user)}>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div><h2 style={{ marginBottom: 4 }}>用户管理</h2><span style={{ color: '#666' }}>普通打印用户由第三方 SSO 自动建立；此处只能新建运维账号。</span></div>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建运维账号</Button>
      </div>
      <Space wrap style={{ marginBottom: 16 }}>
        <Input.Search value={search} allowClear placeholder="搜索邮箱或用户名" onChange={(event) => setSearch(event.target.value)} onSearch={() => reload()} style={{ width: 260 }} />
        <Select allowClear value={role} placeholder="角色" options={roleOptions} onChange={setRole} style={{ width: 140 }} />
        <Select allowClear value={status} placeholder="状态" options={[{ value: 'active', label: '启用' }, { value: 'inactive', label: '停用' }]} onChange={setStatus} style={{ width: 120 }} />
      </Space>
      <Table rowKey="id" loading={loading} dataSource={users} columns={columns} onChange={handleTableChange} pagination={{ current: page, total, pageSize, showSizeChanger: true, onChange: (nextPage, nextPageSize) => { setPageSize(nextPageSize); void load(nextPage); } }} scroll={{ x: 1250 }} />

      <Drawer title="用户详情" open={!!detailUser} onClose={() => setDetailUser(null)} width={440}>
        {detailUser ? <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="邮箱"><Link to={`/print-jobs?user_email=${encodeURIComponent(detailUser.email)}`}>{detailUser.email}</Link></Descriptions.Item>
          <Descriptions.Item label="用户名">{detailUser.username || '-'}</Descriptions.Item>
          <Descriptions.Item label="账号类型">{accountKindLabel(detailUser.account_kind)}</Descriptions.Item>
          <Descriptions.Item label="角色">{roleOptions.find((item) => item.value === detailUser.role)?.label || detailUser.role}</Descriptions.Item>
          <Descriptions.Item label="状态">{detailUser.status === 'active' ? '启用' : '停用'}</Descriptions.Item>
          <Descriptions.Item label="最后登录">{formatDate(detailUser.last_login)}</Descriptions.Item>
          <Descriptions.Item label="打印额度">{`${detailUser.print_quota_balance ?? 0} 点`}</Descriptions.Item>
          <Descriptions.Item label="用户 ID"><FullIdentifier value={detailUser.id} /></Descriptions.Item>
        </Descriptions> : null}
      </Drawer>

      <Modal open={formVisible} title={editing ? '编辑用户' : '新建用户'} onCancel={() => setFormVisible(false)} onOk={() => form.submit()} destroyOnClose okText="保存" cancelText="取消">
        <Form form={form} layout="vertical" onFinish={save}>
          <Form.Item name="email" label="邮箱" rules={[{ required: !editing, type: 'email', message: '请输入有效的邮箱' }]}>{editing ? <Input disabled /> : <Input type="email" autoComplete="email" />}</Form.Item>
          <Form.Item name="username" label="用户名" rules={[{ required: true, min: 3, max: 50, message: '用户名长度为 3-50 个字符' }]}><Input autoComplete="username" /></Form.Item>
          {!editing ? <Form.Item name="password" label="初始密码" rules={[{ required: true, min: 6, message: '密码至少需要 6 个字符' }]}><Input.Password autoComplete="new-password" /></Form.Item> : null}
          <Form.Item name="role" label="角色" rules={[{ required: true }]}><Select options={operatorRoleOptions} /></Form.Item>
        </Form>
      </Modal>

      <Modal open={passwordVisible} title={`修改密码：${passwordUser?.email || ''}`} onCancel={() => setPasswordVisible(false)} onOk={() => passwordForm.submit()} destroyOnClose okText="保存" cancelText="取消">
        <Form form={passwordForm} layout="vertical" onFinish={async (values) => {
          if (!passwordUser) return;
          try {
            const result = await apiService.request<ApiResponse<any>>(`/admin/users/${passwordUser.id}/password`, { method: 'PUT', body: JSON.stringify(values) });
            if (result.code !== 200) throw new Error(result.message || '修改密码失败');
            message.success('密码已修改'); setPasswordVisible(false); passwordForm.resetFields();
          } catch (error) { message.error(mapApiError(error, '修改密码失败')); }
        }}>
          <Form.Item name="new_password" label="新密码" rules={[{ required: true, min: 6, message: '密码至少需要 6 个字符' }]}><Input.Password autoComplete="new-password" /></Form.Item>
        </Form>
      </Modal>

      <Modal open={quotaVisible} title={`增加打印额度：${quotaUser?.email || ''}`} onCancel={() => setQuotaVisible(false)} onOk={() => quotaForm.submit()} destroyOnClose okText="确认增加" cancelText="取消">
        <Form form={quotaForm} layout="vertical" onFinish={async (values) => {
          if (!quotaUser) return;
          const payload = { amount: Number(values.amount), reason: values.reason.trim() };
          try {
            const result = await apiService.request<ApiResponse<any>>(`/admin/users/${quotaUser.id}/print-quota-grants`, {
              method: 'POST',
              body: JSON.stringify(payload),
            });
            if (result.code !== 200) throw new Error(result.message || '增加打印额度失败');
            message.success('打印额度已增加');
            setQuotaVisible(false);
            quotaForm.resetFields();
            await load(page);
          } catch (error) {
            message.error(mapApiError(error, '增加打印额度失败'));
          }
        }}>
          <Form.Item name="amount" label="增加点数" rules={[{ required: true, type: 'number', min: 1, message: '请输入正整数' }]}>
            <InputNumber min={1} precision={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="reason" label="增加原因" rules={[{ required: true, whitespace: true, max: 500, message: '请填写增加原因' }]}>
            <Input maxLength={500} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default Users;
