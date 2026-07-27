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

class ApiService {
  private token: string | null = null;

  // 设置认证 token
  setToken(token: string) {
    this.token = token;
  }

  clearToken() {
    this.token = null;
  }

  // 获取认证 token
  async getToken(): Promise<string | null> {
    if (this.token) {
      return this.token;
    }

    try {
      const response = await fetch(buildAuthUrl('me'));
      const result = await response.json();
      
      if (result.code === 200 && result.data.access_token) {
        this.token = result.data.access_token;
        return this.token;
      }
    } catch (error) {
      console.error('获取 token 失败:', error);
    }
    
    return null;
  }

  // 通用请求方法
  private async request<T = any>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<ApiResponse<T>> {
    const token = await this.getToken();
    
    const config: RequestInit = {
      headers: {
        // 如果 body 是 FormData，不要设置 Content-Type，让浏览器自动处理
        ...(options.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
        ...(token && { 'Authorization': `Bearer ${token}` }),
        ...options.headers,
      },
      ...options,
    };

    try {
      const response = await fetch(buildApiUrl(endpoint), config);
      const result = await response.json();

      if (!response.ok) {
        throw new ApiError({
          code: response.status,
          message: result.message || '请求失败',
          details: result,
        });
      }

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

  // GET 请求
  async get<T = any>(endpoint: string): Promise<ApiResponse<T>> {
    return this.request<T>(endpoint, { method: 'GET' });
  }

  // POST 请求
  async post<T = any>(endpoint: string, data?: any): Promise<ApiResponse<T>> {
    return this.request<T>(endpoint, {
      method: 'POST',
      body: data ? JSON.stringify(data) : undefined,
    });
  }

  // PUT 请求
  async put<T = any>(endpoint: string, data?: any): Promise<ApiResponse<T>> {
    return this.request<T>(endpoint, {
      method: 'PUT',
      body: data ? JSON.stringify(data) : undefined,
    });
  }

  // DELETE 请求
  async delete<T = any>(endpoint: string): Promise<ApiResponse<T>> {
    return this.request<T>(endpoint, { method: 'DELETE' });
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
      // 官方终端上传同时携带 terminal token 与官方账号 JWT。
      if (uploadToken) {
        let url = buildApiUrl(`/files?token=${encodeURIComponent(uploadToken)}`);
        if (nodeId) {
          url += `&node_id=${encodeURIComponent(nodeId)}`;
        }
        if (printerId) {
          url += `&printer_id=${encodeURIComponent(printerId)}`;
        }
        
        const authToken = await this.getToken();
        const response = await fetch(url, {
          method: 'POST',
          headers: authToken ? { Authorization: `Bearer ${authToken}` } : undefined,
          body: formData,
        });
        
        const result = await response.json();
        
        if (!response.ok) {
          throw new ApiError({
            code: response.status,
            message: result.message || '请求失败',
            details: result,
          });
        }
        
        return result;
      }
      
      // 否则使用 OAuth2 认证
      let endpoint = '/files';
      if (nodeId) {
        endpoint += `?node_id=${nodeId}`;
      }
      return await this.request(endpoint, {
        method: 'POST',
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
      let url = buildApiUrl('/files/preflight');
      if (uploadToken) {
        url += `?token=${encodeURIComponent(uploadToken)}`;
      }
      const response = await fetch(url, {
        method: 'POST',
        body: formData,
      });
      const result = await response.json();

      if (!response.ok) {
        throw new ApiError({
          code: response.status,
          message: result.message || '请求失败',
          details: result,
        });
      }

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
