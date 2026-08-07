import React from 'react';
import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import SitePortals from './SitePortals';

const portal = {
  code: 'campus-print',
  display_name: '校园入口',
  entry_url: 'https://portal.example.test/entry',
  claim_base_url: 'https://portal.example.test',
  enabled: true,
  oauth_client_id: 'site-portal-campus-print',
  edge_node_count: 2,
};

const response = (data: unknown) => ({
  ok: true,
  status: 200,
  json: async () => ({ code: 200, data }),
}) as Response;

describe('SitePortals management', () => {
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

  it('shows Edge associations and edits Portal metadata', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      if (url.includes('/admin/site-portals/')) return response(portal);
      return response([portal]);
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter><SitePortals /></MemoryRouter>);

    expect(await screen.findByText('校园入口')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    fireEvent.click(screen.getByText('编辑'));
    fireEvent.change(screen.getByLabelText('显示名称'), { target: { value: '校园打印入口' } });
    fireEvent.click(document.querySelector('.ant-modal-footer .ant-btn-primary') as HTMLElement);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/admin/site-portals/campus-print'),
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({
          code: 'campus-print',
          display_name: '校园打印入口',
          entry_url: 'https://portal.example.test/entry',
          claim_base_url: 'https://portal.example.test',
        }),
      }),
    ));
  });
});
