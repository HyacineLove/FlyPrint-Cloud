import React from 'react';
import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Users from './Users';
import { apiService } from '../../services/api';

const response = (data: unknown, ok = true) => ({
  ok,
  status: ok ? 200 : 409,
  json: async () => ({ code: ok ? 200 : 2006, data, message: ok ? '' : '用户存在打印中的任务，无法删除' }),
}) as Response;

const user = { id: 'user-1', username: 'Alice', email: 'alice@example.com', role: 'viewer', status: 'active', last_login: '', created_at: '2026-07-27T00:00:00Z' };

const buttonsByText = (text: string) => Array.from(document.querySelectorAll('button'))
  .filter((button) => button.textContent?.replace(/\s/g, '') === text);

describe('Users operations', () => {
  beforeAll(() => {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: () => ({ matches: false, media: '', onchange: null, addListener: jest.fn(), removeListener: jest.fn(), addEventListener: jest.fn(), removeEventListener: jest.fn(), dispatchEvent: jest.fn() }),
    });
  });

  afterEach(() => {
    apiService.clearToken();
    document.body.innerHTML = '';
  });

  it('loads by email, toggles enabled state, and keeps email read-only in edit form', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      if (url.includes('/enabled')) return response(user);
      if (url.includes('/admin/users')) return response({ items: [user], pagination: { total: 1 } });
      return response({});
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter initialEntries={['/users?email=alice%40example.com']}><Users /></MemoryRouter>);

    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('search=alice%40example.com'))).toBe(true);

    fireEvent.click(screen.getByText('编辑'));
    expect(document.querySelector('#email')).toBeDisabled();

    const toggle = screen.getByRole('switch', { name: 'alice@example.com启用状态' });
    fireEvent.click(toggle);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/admin/users/user-1/enabled'),
      expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ enabled: false }) }),
    ));
  });

  it('uses DELETE for deletion and preserves backend conflict feedback', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      if (url.includes('/admin/users/user-1') && init?.method === 'DELETE') return response(undefined, false);
      if (url.includes('/admin/users')) return response({ items: [user], pagination: { total: 1 } });
      return response({});
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter><Users /></MemoryRouter>);
    await screen.findByText('alice@example.com');
    fireEvent.click(buttonsByText('删除')[0]);
    await waitFor(() => expect(buttonsByText('删除')).toHaveLength(2));
    fireEvent.click(buttonsByText('删除')[1]);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/admin/users/user-1'),
      expect.objectContaining({ method: 'DELETE' }),
    ));
  });
});
