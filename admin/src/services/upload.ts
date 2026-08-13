import { apiService, ApiError } from './api';

export interface UploadPolicy {
  maxFileSizeBytes: number;
  maxPages: number;
  allowedExtensions: string[];
  allowedMimeTypes: string[];
}

export interface UploadSession {
  expiresAt: string;
  nodeId: string;
  printerId: string;
}

class UploadService {
  async getPolicy(): Promise<UploadPolicy> {
    const result = await apiService.get<any>('/files/upload-policy');
    if (result.code !== 200) throw new ApiError({ code: result.code, message: result.message || 'Failed to fetch upload policy', details: result });

    return {
      maxFileSizeBytes: result.data?.max_file_size_bytes,
      maxPages: result.data?.max_pages,
      allowedExtensions: result.data?.allowed_extensions || [],
      allowedMimeTypes: result.data?.allowed_mime_types || [],
    };
  }

  async verifySession(token: string, nodeId: string, printerId: string): Promise<UploadSession> {
    const result = await apiService.request<any>('/files/verify-upload-token', {
      headers: {
        'X-Fly-Print-File-Token': token,
        'X-Fly-Print-Node-ID': nodeId,
        'X-Fly-Print-Printer-ID': printerId,
      },
    });

    if (result.valid === false) {
      throw new ApiError({
        code: 401,
        message: result.message || 'Failed to verify upload session',
        details: result,
      });
    }

    return {
      expiresAt: new Date(result.data.expires_at * 1000).toISOString(),
      nodeId: result.data.node_id,
      printerId: result.data.printer_id,
    };
  }
}

export const uploadService = new UploadService();
