import React from 'react';
import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { message } from 'antd';
import BusinessSettings from './BusinessSettings';
import { apiService } from '../../services/api';

const authResponse = {
  code: 200,
  data: {
    access_token: 'access-token',
  },
};

const settingsResponse = {
  code: 200,
  data: {
    upload_max_size_bytes: 1048576,
    max_document_pages: 3,
    upload_token_ttl_seconds: 90,
    download_token_ttl_seconds: 120,
    allowed_extensions: ['.pdf', '.png'],
    max_contacts_per_node: 5,
  },
};

describe('BusinessSettings', () => {
  beforeEach(() => {
    apiService.clearToken();
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: jest.fn().mockImplementation((query) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: jest.fn(),
        removeListener: jest.fn(),
        addEventListener: jest.fn(),
        removeEventListener: jest.fn(),
        dispatchEvent: jest.fn(),
      })),
    });
    jest.spyOn(message, 'success').mockImplementation(() => ({} as any));
    jest.spyOn(message, 'error').mockImplementation(() => ({} as any));
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  it('loads current business settings', async () => {
    global.fetch = jest.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => authResponse })
      .mockResolvedValueOnce({ ok: true, json: async () => settingsResponse }) as jest.Mock;

    render(<BusinessSettings />);

    await waitFor(() => expect(global.fetch).toHaveBeenCalledTimes(2));
    expect(screen.getByLabelText('上传大小上限（MB）')).toHaveValue('1.00');
    expect(screen.getByLabelText('文档页数上限')).toHaveValue('3');
    expect(screen.getByLabelText('上传凭证有效期（秒）')).toHaveValue('90');
    expect(screen.getByLabelText('下载凭证有效期（秒）')).toHaveValue('120');
    expect(screen.getByLabelText('允许上传扩展名')).toHaveValue('.pdf, .png');
    expect(screen.getByLabelText('每节点运维联系人上限')).toHaveValue('5');
  });

  it('saves valid settings and refreshes the form', async () => {
    global.fetch = jest.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => authResponse })
      .mockResolvedValueOnce({ ok: true, json: async () => settingsResponse })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          code: 200,
          data: {
            upload_max_size_bytes: 2097152,
            max_document_pages: 4,
            upload_token_ttl_seconds: 180,
            download_token_ttl_seconds: 240,
            allowed_extensions: ['.pdf', '.docx'],
            max_contacts_per_node: 5,
          },
        }),
      }) as jest.Mock;

    render(<BusinessSettings />);

    const uploadSize = await screen.findByLabelText('上传大小上限（MB）');
    fireEvent.change(uploadSize, { target: { value: '2' } });
    fireEvent.change(screen.getByLabelText('文档页数上限'), { target: { value: '4' } });
    fireEvent.change(screen.getByLabelText('上传凭证有效期（秒）'), { target: { value: '180' } });
    fireEvent.change(screen.getByLabelText('下载凭证有效期（秒）'), { target: { value: '240' } });
    fireEvent.change(screen.getByLabelText('允许上传扩展名'), { target: { value: '.pdf, .docx' } });
    fireEvent.click(screen.getByRole('button', { name: /保存配置/ }));

    await waitFor(() => {
      expect(global.fetch).toHaveBeenCalledWith(
        '/api/v1/admin/business-settings',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify({
            upload_max_size_bytes: 2097152,
            max_document_pages: 4,
            upload_token_ttl_seconds: 180,
            download_token_ttl_seconds: 240,
            allowed_extensions: ['.pdf', '.docx'],
            max_contacts_per_node: 5,
          }),
        })
      );
    });
    expect(message.success).toHaveBeenCalledWith('业务配置已更新');
  });

  it('shows backend validation errors', async () => {
    global.fetch = jest.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => authResponse })
      .mockResolvedValueOnce({ ok: true, json: async () => settingsResponse })
      .mockResolvedValueOnce({
        ok: false,
        json: async () => ({
          code: 400,
          message: 'upload_max_size_bytes must be greater than 0',
        }),
      }) as jest.Mock;

    render(<BusinessSettings />);

    await waitFor(() => expect(global.fetch).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole('button', { name: /保存配置/ }));

    await waitFor(() => {
      expect(message.error).toHaveBeenCalledWith('上传大小上限必须大于 0');
    });
  });
});
