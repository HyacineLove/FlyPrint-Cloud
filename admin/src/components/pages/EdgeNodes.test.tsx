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
  connection_status: 'online',
  health_status: 'healthy',
  enabled: true,
  registration_state: 'active',
  last_heartbeat: '2026-07-27T00:00:00Z',
};

const sitePortal = {
  code: 'campus-print',
  display_name: '校园入口',
  enabled: true,
};

const response = (data: unknown, ok = true, message?: string) => ({
  ok,
  status: ok ? 200 : 400,
  json: async () => ({ code: ok ? 200 : 400, data, message }),
}) as Response;

describe('EdgeNodes Site Portal configuration', () => {
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

  it('loads the node Portal configuration and saves the selected set/default', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      if (url.includes('/admin/edge-nodes/node-1/site-portals')) {
        if (init?.method === 'PUT') return response({
          edge_node_id: 'node-1',
          portals: [sitePortal],
          default_code: 'campus-print',
        });
        return response({
          edge_node_id: 'node-1',
          portals: [sitePortal],
          default_code: 'campus-print',
        });
      }
      if (url.includes('/admin/edge-nodes')) return response({ items: [node], total: 1 });
      if (url.includes('/admin/site-portals')) return response([sitePortal]);
      return response({});
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter><EdgeNodes /></MemoryRouter>);

    const configButton = await screen.findByTestId('node-site-portals-node-1');
    expect(fetchMock.mock.calls.filter(([url]) => String(url).includes('/auth/me'))).toHaveLength(1);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('/admin/site-portals'))).toBe(false);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('/admin/edge-nodes/node-1/site-portals'))).toBe(false);

    fireEvent.click(configButton);
    expect((await screen.findAllByText('校园入口')).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: '保存配置' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/admin/edge-nodes/node-1/site-portals'),
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ portal_codes: ['campus-print'], default_code: 'campus-print' }),
      }),
    ));
  });
});
