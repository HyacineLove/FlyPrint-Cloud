import React from 'react';
import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import PrintJobs from './PrintJobs';

const response = (data: unknown) => ({ ok: true, status: 200, json: async () => ({ code: 200, data }) }) as Response;

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
      return response({ jobs: [{ id: 'job-1', name: 'document.pdf', status: 'completed', created_at: '2026-07-27T00:00:00Z', user_email: 'alice@example.com', user_name: 'Alice' }], pagination: { total: 1 } });
    });
    global.fetch = fetchMock as jest.Mock;

    render(<MemoryRouter initialEntries={['/print-jobs?user_email=alice%40example.com']}><PrintJobs /></MemoryRouter>);

    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
    expect(screen.getByText('Alice')).toHaveStyle({ color: '#8c8c8c' });
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('user_email=alice%40example.com'))).toBe(true);
  });
});
