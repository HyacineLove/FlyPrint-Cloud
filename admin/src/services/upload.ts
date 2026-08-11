import { ApiError } from './api';
import { buildApiUrl } from '../config';

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
    const response = await fetch(buildApiUrl('/files/upload-policy'));
    const result = await response.json();

    if (!response.ok) {
      throw new ApiError({
        code: response.status,
        message: result.message || 'Failed to fetch upload policy',
        details: result,
      });
    }

    return {
      maxFileSizeBytes: result.data.max_file_size_bytes,
      maxPages: result.data.max_pages,
      allowedExtensions: result.data.allowed_extensions || [],
      allowedMimeTypes: result.data.allowed_mime_types || [],
    };
  }

  async verifySession(token: string, nodeId: string, printerId: string): Promise<UploadSession> {
    const response = await fetch(buildApiUrl('/files/verify-upload-token'), {
      headers: {
        'X-Fly-Print-File-Token': token,
        'X-Fly-Print-Node-ID': nodeId,
        'X-Fly-Print-Printer-ID': printerId,
      },
    });
    const result = await response.json();

    if (!response.ok || result.valid === false) {
      throw new ApiError({
        code: response.status,
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
