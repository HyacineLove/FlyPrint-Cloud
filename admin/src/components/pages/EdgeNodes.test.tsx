import React from 'react';
import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import EdgeNodes from './EdgeNodes';

const node = {
  id: 'node-1',
  name: '终端一号',
  alias: '',
  location: '一楼',
  login_source: 'official',
  connection_status: 'online',
  health_status: 'healthy',
  enabled: true,
  registration_state: 'active',
  last_heartbeat: '2026-07-27T00:00:00Z',
};

const provider = {
  id: 'provider-1',
  code: 'campus-print',
  display_name: '校园入口',
  enabled: true,
  entry_visible: true,
};

const response = (data: unknown, ok = true) => ({
  ok,
  status: ok ? 200 : 400,
  json: async () => ({ code: ok ? 200 : 400, data }),
}) as Response;

describe('EdgeNodes login source', () => {
  beforeAll(() => {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: jest.fn(),
        removeListener: jest.fn(),
        addEventListener: jest.fn(),
        removeEventListener: jest.fn(),
        dispatchEvent: jest.fn(),
      }),
    });
  });

  it('shows the official and enabled provider login sources', async () => {
    global.fetch = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      if (url.includes('/admin/edge-nodes')) return response({ items: [node], total: 1 });
      if (url.includes('/admin/integration-providers')) return response([provider]);
      return response({});
    }) as jest.Mock;

    render(<MemoryRouter><EdgeNodes /></MemoryRouter>);

    expect((await screen.findAllByText('登录源')).length).toBeGreaterThan(0);
    const sourceSelect = await screen.findByTestId('node-login-source-node-1');
    expect(sourceSelect).toHaveAttribute('aria-label', '节点 终端一号 登录源');
    expect(screen.getByText('官方入口')).toBeInTheDocument();

    fireEvent.mouseDown(sourceSelect.querySelector('.ant-select-selector') as HTMLElement);
    expect(await screen.findByText('校园入口')).toBeInTheDocument();
  });

  it('updates the node login source through the existing admin endpoint', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      if (url.includes('/admin/edge-nodes') && init?.method === 'PATCH') return response({ id: node.id, login_source: provider.code });
      if (url.includes('/admin/edge-nodes')) return response({ items: [node], total: 1 });
      if (url.includes('/admin/integration-providers')) return response([provider]);
      return response({});
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter><EdgeNodes /></MemoryRouter>);

    const sourceSelect = await screen.findByTestId('node-login-source-node-1');
    fireEvent.mouseDown(sourceSelect.querySelector('.ant-select-selector') as HTMLElement);
    fireEvent.click(await screen.findByText('校园入口'));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/admin/edge-nodes/node-1/login-source'),
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ login_source: 'campus-print' }),
      }),
    ));
  });
});
