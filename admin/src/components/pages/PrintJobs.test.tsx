import React from 'react';
import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import PrintJobs from './PrintJobs';

const response = (data: unknown) => ({ ok: true, status: 200, json: async () => ({ code: 200, data }) }) as Response;
const rawResponse = (data: unknown) => ({ ok: true, status: 200, json: async () => data }) as Response;

describe('PrintJobs user navigation', () => {
  beforeAll(() => {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: () => ({ matches: false, media: '', onchange: null, addListener: jest.fn(), removeListener: jest.fn(), addEventListener: jest.fn(), removeEventListener: jest.fn(), dispatchEvent: jest.fn() }),
    });
  });

  it('filters by email from the URL and shows email with gray username', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      return rawResponse({ jobs: [{ id: 'job-1', name: 'document.pdf', status: 'completed', created_at: '2026-07-27T00:00:00Z', user_email: 'alice@example.com', user_name: 'Alice' }], pagination: { total: 1 } });
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter initialEntries={['/print-jobs?user_email=alice%40example.com']}><PrintJobs /></MemoryRouter>);

    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
    expect(screen.getByText('Alice')).toHaveStyle({ color: '#8c8c8c' });
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('user_email=alice%40example.com'))).toBe(true);
  });

  it('shows a unified Site Portal print audit without cloud-file fields', async () => {
    const fetchMock = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/auth/me')) return response({ access_token: 'admin-token' });
      return rawResponse({
        jobs: [{
          id: 'job-1', name: 'document.pdf', status: 'completed',
          created_at: '2026-07-27T00:00:00Z', end_time: '2026-07-27T00:01:00Z',
          user_email: 'alice@example.com', user_name: 'Alice',
          site_portal_code: 'official', initiator_name: 'Official Site Portal',
          edge_node_id: 'edge-1', node_name: 'Lobby Edge',
          printer_id: 'printer-1', printer_name: 'HP',
          page_count: 3, copies: 2, paper_size: 'A4',
          color_mode: 'color', duplex_mode: 'longedge',
          quota_reserved: 8, quota_consumed: 8,
        }],
        pagination: { total: 1 },
      });
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter><PrintJobs /></MemoryRouter>);

    expect(await screen.findByText('Official Site Portal')).toBeInTheDocument();
    expect(screen.getByText('3 页 × 2 份')).toBeInTheDocument();
    expect(screen.getByText('A4 / 彩色 / 双面长边')).toBeInTheDocument();
    expect(screen.getByText('8 / 8 点')).toBeInTheDocument();
    expect(screen.queryByText('Cloud 文件')).not.toBeInTheDocument();
  });
});
