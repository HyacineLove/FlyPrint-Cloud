// API 基础服务
import { buildApiUrl, buildAuthUrl } from '../config';

export interface ApiResponse<T = any> {
  code: number;
  message: string;
  data: T;
}

export interface ApiError {
  code: number;
  message: string;
  details?: any;
}

export class ApiError extends Error {
  code: number;
  details?: any;

  constructor({ code, message, details }: { code: number; message: string; details?: any }) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.details = details;
  }
}

async function readJsonResponse(response: Response): Promise<any> {
  if (typeof response.text === 'function') {
    const text = await response.text();
    if (!text) return {};
    try {
      return JSON.parse(text);
    } catch {
      return { message: text };
    }
  }
  if (typeof response.json === 'function') {
    return response.json();
  }
  return {};
}

class ApiService {
  private token: string | null = null;
  private currentUser: ApiResponse<any> | null = null;
  private currentUserRequest: Promise<ApiResponse<any>> | null = null;
  private authGeneration = 0;

  // 设置认证 token
  setToken(token: string) {
    this.authGeneration += 1;
    this.token = token;
    this.currentUser = null;
  }

  clearToken() {
    this.authGeneration += 1;
    this.token = null;
    this.currentUser = null;
  }

  async getMe(): Promise<ApiResponse<any>> {
    if (this.currentUser) {
      return this.currentUser;
    }
    if (this.currentUserRequest) {
      return this.currentUserRequest;
    }

    const requestGeneration = this.authGeneration;
    this.currentUserRequest = (async () => {
      const response = await fetch(buildAuthUrl('me'));
      const result: ApiResponse<any> = await readJsonResponse(response);

      if (requestGeneration !== this.authGeneration) {
        throw new ApiError({ code: 401, message: '登录状态已变更' });
      }

      if (!response.ok) {
        this.clearToken();
        throw new ApiError({
          code: response.status,
          message: result.message || '获取登录状态失败',
          details: result,
        });
      }

      this.currentUser = result;
      if (result.data?.access_token) {
        this.token = result.data.access_token;
      }
      return result;
    })().finally(() => {
      this.currentUserRequest = null;
    });

    return this.currentUserRequest;
  }

  // 获取认证 token
  async getToken(): Promise<string | null> {
    if (this.token) {
      return this.token;
    }

    try {
      const result = await this.getMe();
      return result.code === 200 ? result.data?.access_token || null : null;
    } catch (error) {
      console.error('获取 token 失败:', error);
    }
    
    return null;
  }

  // 通用请求方法
  async request<T = any>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<T> {
    const token = await this.getToken();

    const headers = new Headers(options.headers);
    if (!(options.body instanceof FormData) && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json');
    }
    if (token && !headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${token}`);
    }

    const config: RequestInit = {
      ...options,
      headers: {
        ...Object.fromEntries(headers.entries()),
      },
    };

    try {
      const response = await fetch(buildApiUrl(endpoint), config);
      const result = await readJsonResponse(response);

      if (!response.ok) {
        if (response.status === 401) {
          this.clearToken();
        }
        throw new ApiError({
          code: response.status,
          message: result.message || '请求失败',
          details: result,
        });
      }

      return result as T;
    } catch (error) {
      if (error instanceof ApiError) {
        throw error;
      }
      
      throw new ApiError({
        code: 500,
        message: error instanceof Error ? error.message : '网络错误',
      });
    }
  }

  // GET 请求
  async get<T = any>(endpoint: string): Promise<ApiResponse<T>> {
    return this.request<ApiResponse<T>>(endpoint, { method: 'GET' });
  }

  // POST 请求
  async post<T = any>(endpoint: string, data?: any): Promise<ApiResponse<T>> {
    return this.request<ApiResponse<T>>(endpoint, {
      method: 'POST',
      body: data === undefined ? undefined : JSON.stringify(data),
    });
  }

  // PUT 请求
  async put<T = any>(endpoint: string, data?: any): Promise<ApiResponse<T>> {
    return this.request<ApiResponse<T>>(endpoint, {
      method: 'PUT',
      body: data === undefined ? undefined : JSON.stringify(data),
    });
  }

  async patch<T = any>(endpoint: string, data?: any): Promise<ApiResponse<T>> {
    return this.request<ApiResponse<T>>(endpoint, {
      method: 'PATCH',
      body: data === undefined ? undefined : JSON.stringify(data),
    });
  }

  // DELETE 请求
  async delete<T = any>(endpoint: string): Promise<ApiResponse<T>> {
    return this.request<ApiResponse<T>>(endpoint, { method: 'DELETE' });
  }

  // 文件上传
  async uploadFile(
    file: File,
    uploadToken?: string,
    nodeId?: string,
    printerId?: string
  ): Promise<ApiResponse<any>> {
    const formData = new FormData();
    formData.append('file', file);
    
    try {
      const endpoint = uploadToken ? '/files' : nodeId ? `/files?node_id=${encodeURIComponent(nodeId)}` : '/files';
      return await this.request<ApiResponse<any>>(endpoint, {
        method: 'POST',
        headers: {
          ...(uploadToken ? { 'X-Fly-Print-File-Token': uploadToken } : {}),
          ...(nodeId ? { 'X-Fly-Print-Node-ID': nodeId } : {}),
          ...(printerId ? { 'X-Fly-Print-Printer-ID': printerId } : {}),
        },
        body: formData,
      });
    } catch (error) {
      if (error instanceof ApiError) {
        throw error;
      }
      
      throw new ApiError({
        code: 500,
        message: error instanceof Error ? error.message : '网络错误',
      });
    }
  }

  async preflightUpload(file: File, uploadToken?: string): Promise<ApiResponse<any>> {
    const formData = new FormData();
    formData.append('file', file);

    try {
      const result = await this.request<ApiResponse<any>>('/files/preflight', {
        method: 'POST',
        headers: uploadToken ? { 'X-Fly-Print-File-Token': uploadToken } : undefined,
        body: formData,
      });
      return result;
    } catch (error) {
      if (error instanceof ApiError) {
        throw error;
      }

      throw new ApiError({
        code: 500,
        message: error instanceof Error ? error.message : '网络错误',
      });
    }
  }

  // 文件下载 (返回 Blob)
  async downloadFile(url: string, token?: string): Promise<Blob> {
    const useToken = token || await this.getToken();
    const headers: HeadersInit = useToken ? { 'Authorization': `Bearer ${useToken}` } : {};
    
    const fullUrl = url.startsWith('http') ? url : url; 
    
    const response = await fetch(fullUrl, {
      method: 'GET',
      headers,
    });

    if (!response.ok) {
      throw new Error(`下载失败: ${response.statusText}`);
    }

    return await response.blob();
  }
}

// 创建单例实例
const apiService = new ApiService();

export { apiService };
export default apiService;
