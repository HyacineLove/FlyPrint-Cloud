import React from 'react';
import '@testing-library/jest-dom';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { message } from 'antd';
import PublicUpload from './PublicUpload';
import { apiService } from '../../services/api';

jest.mock('../../services/api', () => ({
  apiService: {
    getToken: jest.fn().mockResolvedValue('test-user-token'),
    clearToken: jest.fn(),
    get: jest.fn(),
    request: jest.fn(),
    uploadFile: jest.fn(),
  },
}));

const mockedApiService = apiService as jest.Mocked<typeof apiService>;

const renderUploadPage = (path: string) => {
  window.history.replaceState({}, '', path);
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/upload" element={<PublicUpload />} />
      </Routes>
    </MemoryRouter>
  );
};

describe('PublicUpload', () => {
  beforeEach(() => {
    jest.useFakeTimers();
    jest.clearAllMocks();
    mockedApiService.getToken.mockResolvedValue('test-user-token');
    mockedApiService.get.mockImplementation(async (endpoint: string) => {
      const response = await fetch(`/api/v1${endpoint}`);
      return response.json();
    });
    mockedApiService.request.mockImplementation(async (endpoint: string, options?: RequestInit) => {
      const response = await fetch(`/api/v1${endpoint}`, options);
      return response.json();
    });
    jest.spyOn(message, 'open').mockImplementation(() => ({}) as any);
  });

  afterEach(() => {
    jest.clearAllTimers();
    jest.useRealTimers();
    jest.restoreAllMocks();
  });

  it('shows server-driven limits and disables upload for files that exceed them', async () => {
    global.fetch = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/files/upload-policy')) {
        return {
          ok: true,
          json: async () => ({
            code: 200,
            data: {
              max_file_size_bytes: 1024,
              max_pages: 5,
              allowed_extensions: ['.pdf'],
              allowed_mime_types: ['application/pdf'],
            },
          }),
        } as Response;
      }

      return {
        ok: true,
        json: async () => ({
          code: 200,
          valid: true,
          data: {
            node_id: 'node-1',
            printer_id: 'printer-1',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        }),
      } as Response;
    }) as jest.Mock;

    renderUploadPage('/upload#token=test-token&node_id=node-1&printer_id=printer-1');

    await screen.findByText(/1(\.0)? KB/i);
    expect(screen.queryByText(/剩余时间/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/倒计时结束前/i)).not.toBeInTheDocument();

    const fileInput = document.querySelector('#public-upload-input') as HTMLInputElement;
    const file = new File([new Uint8Array(2048)], 'too-large.pdf', { type: 'application/pdf' });
    fireEvent.change(fileInput, { target: { files: [file] } });

    expect(screen.getByRole('button', { name: /上传/i })).toBeDisabled();
    expect(screen.getByText(/文件大小超过当前限制/i)).toBeInTheDocument();
  });

  it('expires the page when the safe countdown reaches zero', async () => {
    global.fetch = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/files/upload-policy')) {
        return {
          ok: true,
          json: async () => ({
            code: 200,
            data: {
              max_file_size_bytes: 1024 * 1024,
              max_pages: 5,
              allowed_extensions: ['.pdf'],
              allowed_mime_types: ['application/pdf'],
            },
          }),
        } as Response;
      }

      return {
        ok: true,
        json: async () => ({
          code: 200,
          valid: true,
          data: {
            node_id: 'node-1',
            printer_id: 'printer-1',
            expires_at: Math.floor((Date.now() + 4000) / 1000),
          },
        }),
      } as Response;
    }) as jest.Mock;

    renderUploadPage('/upload#token=test-token&node_id=node-1&printer_id=printer-1');

    await screen.findByText(/文件上传/i);

    act(() => {
      jest.advanceTimersByTime(3000);
    });

    await screen.findByText(/访问失败/i);
    expect(screen.getByText(/二维码已过期.*飞印终端/i)).toBeInTheDocument();
  });

  it('keeps the user on the upload page and shows a top toast when upload fails', async () => {
    global.fetch = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/files/upload-policy')) {
        return {
          ok: true,
          json: async () => ({
            code: 200,
            data: {
              max_file_size_bytes: 1024 * 1024,
              max_pages: 5,
              allowed_extensions: ['.pdf'],
              allowed_mime_types: ['application/pdf'],
            },
          }),
        } as Response;
      }

      return {
        ok: true,
        json: async () => ({
          code: 200,
          valid: true,
          data: {
            node_id: 'node-1',
            printer_id: 'printer-1',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        }),
      } as Response;
    }) as jest.Mock;

    mockedApiService.uploadFile.mockRejectedValue({
      details: {
        message: 'Failed to save file',
      },
    });

    renderUploadPage('/upload#token=test-token&node_id=node-1&printer_id=printer-1');

    await screen.findByText(/文件上传/i);

    const fileInput = document.querySelector('#public-upload-input') as HTMLInputElement;
    const file = new File(['resume'], 'resume.pdf', { type: 'application/pdf' });
    fireEvent.change(fileInput, { target: { files: [file] } });

    fireEvent.click(screen.getByRole('button', { name: /上传/i }));

    await waitFor(() => {
      expect(message.open).toHaveBeenCalledWith(
        expect.objectContaining({
          key: 'public-upload-error',
          type: 'error',
          content: '文件保存失败，请稍后重试',
        })
      );
    });

    expect(screen.queryByText(/访问失败/i)).toBeNull();
  });
  it('replaces the upload page when the upload session becomes invalid during submit', async () => {
    global.fetch = jest.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/files/upload-policy')) {
        return {
          ok: true,
          json: async () => ({
            code: 200,
            data: {
              max_file_size_bytes: 1024 * 1024,
              max_pages: 5,
              allowed_extensions: ['.pdf'],
              allowed_mime_types: ['application/pdf'],
            },
          }),
        } as Response;
      }

      return {
        ok: true,
        json: async () => ({
          code: 200,
          valid: true,
          data: {
            node_id: 'node-1',
            printer_id: 'printer-1',
            expires_at: Math.floor(Date.now() / 1000) + 60,
          },
        }),
      } as Response;
    }) as jest.Mock;

    mockedApiService.uploadFile.mockRejectedValue({
      details: {
        error: 'token_revoked',
        message: 'Token has been revoked',
      },
    });

    renderUploadPage('/upload#token=test-token&node_id=node-1&printer_id=printer-1');

    await screen.findByRole('heading', { name: '文件上传' });

    const fileInput = document.querySelector('#public-upload-input') as HTMLInputElement;
    const file = new File(['resume'], 'resume.pdf', { type: 'application/pdf' });
    fireEvent.change(fileInput, { target: { files: [file] } });

    fireEvent.click(screen.getByRole('button', { name: '上传文件' }));

    await screen.findByText('访问失败');

    expect(screen.getByText('该二维码已经失效，请到飞印终端重新扫码后再上传。')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '上传文件' })).toBeNull();
    expect(message.open).not.toHaveBeenCalled();
  });
});
