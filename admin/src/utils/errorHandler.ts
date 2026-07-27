import { message } from 'antd';
import { buildAppPath } from '../config';

// 错误类型
export enum ErrorType {
  NETWORK = 'NETWORK',
  AUTH = 'AUTH',
  VALIDATION = 'VALIDATION',
  SERVER = 'SERVER',
  UNKNOWN = 'UNKNOWN',
}

// 错误消息映射
const errorMessages: Record<number, string> = {
  400: '请求参数错误',
  401: '未授权，请重新登录',
  403: '没有权限访问',
  404: '请求的资源不存在',
  408: '请求超时',
  429: '请求过于频繁，请稍后再试',
  500: '服务器内部错误',
  502: '网关错误',
  503: '服务暂时不可用',
  504: '网关超时',
};

// 错误处理器
export class ErrorHandler {
  /**
   * 根据错误码获取友好的错误消息
   */
  static getErrorMessage(code: number, defaultMessage?: string): string {
    return errorMessages[code] || defaultMessage || '发生未知错误';
  }

  /**
   * 判断错误类型
   */
  static getErrorType(code: number): ErrorType {
    if (code === 0 || code === -1) {
      return ErrorType.NETWORK;
    }
    if (code === 401 || code === 403) {
      return ErrorType.AUTH;
    }
    if (code >= 400 && code < 500) {
      return ErrorType.VALIDATION;
    }
    if (code >= 500) {
      return ErrorType.SERVER;
    }
    return ErrorType.UNKNOWN;
  }

  /**
   * 统一处理API错误
   */
  static handleApiError(error: any, customMessage?: string): void {
    console.error('API Error:', error);

    // 如果是网络错误
    if (!error.code || error.code === 0 || error.message?.includes('Failed to fetch')) {
      message.error(customMessage || '网络连接失败，请检查网络设置');
      return;
    }

    const errorType = this.getErrorType(error.code);
    
    // 认证错误 - 重定向到登录页
    if (errorType === ErrorType.AUTH) {
      message.error('登录已过期，请重新登录');
      setTimeout(() => {
        // SPA 登录页（builtin）；子路径部署由 buildAppPath 带前缀
        window.location.href = buildAppPath('/login');
      }, 1500);
      return;
    }

    // 获取错误消息
    let errorMessage = error.message || this.getErrorMessage(error.code);
    
    // 如果后端返回了详细的错误信息
    if (error.details?.message) {
      errorMessage = error.details.message;
    }

    // 使用自定义消息或默认消息
    message.error(customMessage || errorMessage);
  }

  /**
   * 显示成功消息
   */
  static showSuccess(content: string): void {
    message.success(content);
  }

  /**
   * 显示警告消息
   */
  static showWarning(content: string): void {
    message.warning(content);
  }

  /**
   * 显示信息消息
   */
  static showInfo(content: string): void {
    message.info(content);
  }

  /**
   * 显示加载中消息
   */
  static showLoading(content: string = '加载中...'): () => void {
    const hide = message.loading(content, 0);
    return hide;
  }

  /**
   * 处理表单验证错误
   */
  static handleValidationError(error: any): void {
    if (error.details?.errors) {
      const errors = error.details.errors;
      const firstError = Object.values(errors)[0] as string;
      message.error(firstError);
    } else {
      this.handleApiError(error);
    }
  }

  /**
   * 确认对话框
   */
  static async confirm(
    title: string,
    content?: string,
    onOk?: () => void | Promise<void>
  ): Promise<boolean> {
    return new Promise((resolve) => {
      // 使用 antd 的 Modal.confirm
      const { Modal } = require('antd');
      Modal.confirm({
        title,
        content,
        okText: '确定',
        cancelText: '取消',
        onOk: async () => {
          if (onOk) {
            try {
              await onOk();
              resolve(true);
            } catch (error) {
              this.handleApiError(error);
              resolve(false);
            }
          } else {
            resolve(true);
          }
        },
        onCancel: () => {
          resolve(false);
        },
      });
    });
  }
}

// 导出便捷方法
export const handleError = ErrorHandler.handleApiError.bind(ErrorHandler);
export const showSuccess = ErrorHandler.showSuccess.bind(ErrorHandler);
export const showWarning = ErrorHandler.showWarning.bind(ErrorHandler);
export const showInfo = ErrorHandler.showInfo.bind(ErrorHandler);
export const showLoading = ErrorHandler.showLoading.bind(ErrorHandler);
export const handleValidationError = ErrorHandler.handleValidationError.bind(ErrorHandler);
export const confirmAction = ErrorHandler.confirm.bind(ErrorHandler);

export default ErrorHandler;
