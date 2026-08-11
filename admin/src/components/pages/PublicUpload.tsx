import React, { useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, Result, Spin, Typography, message } from 'antd';
import { CheckCircleOutlined, FileOutlined, LogoutOutlined } from '@ant-design/icons';
import { apiService } from '../../services/api';
import { UploadPolicy, uploadService } from '../../services/upload';
import { buildAppPath, buildAuthUrl } from '../../config';

const { Title, Text, Paragraph } = Typography;

const EXPIRED_SESSION_MESSAGE = '二维码已过期，请到飞印终端重新扫码后再上传。';

const ERROR_MESSAGES: Record<string, string> = {
  missing_token: '缺少上传凭证，请到飞印终端重新扫码后再试。',
  invalid_format: '二维码格式无效，请到飞印终端重新扫码后再试。',
  invalid_signature: '二维码校验失败，请到飞印终端重新扫码后再试。',
  invalid_type: '二维码类型无效，请使用飞印终端当前生成的二维码。',
  invalid_context: '当前二维码与终端不匹配，请到飞印终端重新扫码。',
  token_expired: EXPIRED_SESSION_MESSAGE,
  token_already_used: '该二维码已经使用过，请到飞印终端重新扫码后再上传。',
  token_revoked: '该二维码已经失效，请到飞印终端重新扫码后再上传。',
  node_not_found: '当前终端未在飞印平台登记，请联系工作人员。',
  node_disabled: '当前终端已停用，请联系工作人员。',
  printer_not_found: '当前打印机不可用，请到飞印终端重新选择可用设备。',
  printer_disabled: '当前打印机已停用，请到飞印终端重新选择可用设备。',
  printer_not_belong_to_node: '打印机与终端不匹配，请到飞印终端重新扫码上传。',
  file_type_not_allowed: '文件类型不受支持，请选择页面提示范围内的文件类型。',
  FILE_INVALID_TYPE: '文件类型不受支持，请选择页面提示范围内的文件类型。',
  FILE_TOO_LARGE: '文件大小超过当前限制，请压缩后再上传。',
  FILE_TOO_MANY_PAGES: '文档页数超过当前限制，请拆分后再上传。',
  '6003': '文件类型不受支持，请选择页面提示范围内的文件类型。',
  '6004': '文件大小超过当前限制，请压缩后再上传。',
  '6005': '文档页数超过当前限制，请拆分后再上传。',
};

const ENGLISH_ERROR_MESSAGES: Record<string, string> = {
  'Upload token is required': ERROR_MESSAGES.missing_token,
  'Upload token or OAuth2 authentication required': ERROR_MESSAGES.missing_token,
  'Invalid token format': ERROR_MESSAGES.invalid_format,
  'Invalid token signature': ERROR_MESSAGES.invalid_signature,
  'Invalid token type': ERROR_MESSAGES.invalid_type,
  'Token context mismatch': ERROR_MESSAGES.invalid_context,
  'Token has expired': ERROR_MESSAGES.token_expired,
  'Token has already been used': ERROR_MESSAGES.token_already_used,
  'Token has been revoked': ERROR_MESSAGES.token_revoked,
  'Failed to verify token usage': '二维码状态校验失败，请稍后刷新页面后重试。',
  'No file uploaded': '未检测到上传文件，请先选择文件。',
  'Failed to open uploaded file': '文件读取失败，请重新选择文件后再试。',
  'Failed to validate file': '文件校验失败，请检查文件大小、类型和页数限制后重试。',
  'Failed to save file': '文件保存失败，请稍后重试',
  'Failed to save file metadata': '文件信息保存失败，请稍后重试。',
  'Failed to fetch': '网络连接失败，请检查网络后重试。',
  'Network Error': '网络连接失败，请检查网络后重试。',
};

type PageState = 'verifying' | 'ready' | 'success' | 'invalid';
type UploadPhase = 'idle' | 'uploading';

const TERMINAL_UPLOAD_ERROR_CODES = new Set([
  'missing_token',
  'invalid_format',
  'invalid_signature',
  'invalid_type',
  'invalid_context',
  'token_expired',
  'token_already_used',
  'token_revoked',
  'node_not_found',
  'node_disabled',
  'printer_not_found',
  'printer_disabled',
  'printer_not_belong_to_node',
]);

const TERMINAL_UPLOAD_MESSAGES: Record<string, string> = {
  'Invalid token format': 'invalid_format',
  'Invalid token signature': 'invalid_signature',
  'Invalid token type': 'invalid_type',
  'Token context mismatch': 'invalid_context',
  'Token has expired': 'token_expired',
  'Token has already been used': 'token_already_used',
  'Token has been revoked': 'token_revoked',
};

const getFriendlyErrorMessage = (
  rawMessage?: string,
  errorCode?: string | number,
  fallback = '操作失败，请稍后重试。'
): string => {
  const codeKey = errorCode !== undefined && errorCode !== null ? String(errorCode) : '';
  if (codeKey && ERROR_MESSAGES[codeKey]) {
    return ERROR_MESSAGES[codeKey];
  }

  const messageText = (rawMessage || '').trim();
  if (!messageText) {
    return fallback;
  }

  if (ERROR_MESSAGES[messageText]) {
    return ERROR_MESSAGES[messageText];
  }

  if (ENGLISH_ERROR_MESSAGES[messageText]) {
    return ENGLISH_ERROR_MESSAGES[messageText];
  }

  if (messageText.split('').every((char) => char.charCodeAt(0) <= 127)) {
    return fallback;
  }

  return messageText;
};

const extractErrorDetails = (err: any) => {
  let rawMessage = '';
  let errorCode: string | number | undefined;

  if (err?.details?.message) {
    rawMessage = err.details.message;
  } else if (err?.message) {
    rawMessage = err.message;
  }

  if (err?.details?.error !== undefined && err.details.error !== null) {
    errorCode = err.details.error;
  } else if (err?.details?.code !== undefined && err.details.code !== null) {
    errorCode = String(err.details.code);
  }

  return { rawMessage, errorCode };
};

const resolveUploadErrorCode = (rawMessage?: string, errorCode?: string | number): string => {
  if (errorCode !== undefined && errorCode !== null && String(errorCode).trim() !== '') {
    return String(errorCode);
  }

  const messageText = (rawMessage || '').trim();
  return TERMINAL_UPLOAD_MESSAGES[messageText] || '';
};

const isTerminalUploadError = (rawMessage?: string, errorCode?: string | number): boolean => {
  const resolvedCode = resolveUploadErrorCode(rawMessage, errorCode);
  return TERMINAL_UPLOAD_ERROR_CODES.has(resolvedCode);
};

const formatFileSize = (bytes: number): string => {
  if (bytes < 1024) {
    return `${bytes} B`;
  }

  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }

  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
};

const formatAllowedTypes = (policy: UploadPolicy): string => {
  if (policy.allowedExtensions.length > 0) {
    return policy.allowedExtensions.map((extension) => extension.replace('.', '').toUpperCase()).join(' / ');
  }

  if (policy.allowedMimeTypes.length > 0) {
    return policy.allowedMimeTypes.join(' / ');
  }

  return '按服务端配置';
};

const validateSelectedFile = (file: File, policy: UploadPolicy): string | null => {
  const fileName = file.name.toLowerCase();
  const matchesExtension =
    policy.allowedExtensions.length === 0 ||
    policy.allowedExtensions.some((extension) => fileName.endsWith(extension.toLowerCase()));
  const matchesMimeType =
    policy.allowedMimeTypes.length === 0 ||
    policy.allowedMimeTypes.includes(file.type);

  if (!matchesExtension && !matchesMimeType) {
    return '文件类型不受支持，请重新选择符合限制的文件。';
  }

  if (file.size > policy.maxFileSizeBytes) {
    return '文件大小超过当前限制，请重新选择更小的文件。';
  }

  return null;
};

const PublicUpload: React.FC = () => {
  const [pageState, setPageState] = useState<PageState>('verifying');
  const [pageErrorMessage, setPageErrorMessage] = useState('');
  const [policy, setPolicy] = useState<UploadPolicy | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [nodeId, setNodeId] = useState<string | null>(null);
  const [printerId, setPrinterId] = useState<string | null>(null);
  const [expiresAtMs, setExpiresAtMs] = useState<number | null>(null);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);
  const [uploadPhase, setUploadPhase] = useState<UploadPhase>('idle');
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const fragment = window.location.hash || window.sessionStorage.getItem('fly-print-upload-fragment') || '';
    window.sessionStorage.removeItem('fly-print-upload-fragment');
    if (window.location.hash) {
      window.history.replaceState(null, document.title, window.location.pathname);
    }
    const fragmentParams = new URLSearchParams(fragment.replace(/^#/, ''));
    const tokenParam = fragmentParams.get('token');
    const nodeIdParam = fragmentParams.get('node_id');
    const printerIdParam = fragmentParams.get('printer_id');

    if (!tokenParam || !nodeIdParam || !printerIdParam) {
      setPageErrorMessage('缺少上传参数，请到飞印终端重新扫码后再试。');
      setPageState('invalid');
      return;
    }

    let cancelled = false;

    const loadPage = async () => {
      try {
        const authToken = await apiService.getToken();
        if (!authToken) {
          window.sessionStorage.setItem('fly-print-upload-fragment', fragment);
          const returnTo = window.location.pathname;
          window.location.assign(`${buildAppPath('/login')}?return_to=${encodeURIComponent(returnTo)}`);
          return;
        }
        const [uploadPolicy, uploadSession] = await Promise.all([
          uploadService.getPolicy(),
          uploadService.verifySession(tokenParam, nodeIdParam, printerIdParam),
        ]);

        if (cancelled) {
          return;
        }

        setPolicy(uploadPolicy);
        setToken(tokenParam);
        setNodeId(uploadSession.nodeId);
        setPrinterId(uploadSession.printerId);
        setExpiresAtMs(new Date(uploadSession.expiresAt).getTime());
        setPageState('ready');
      } catch (err: any) {
        if (cancelled) {
          return;
        }

        const { rawMessage, errorCode } = extractErrorDetails(err);
        setPageErrorMessage(
          getFriendlyErrorMessage(rawMessage, errorCode, '上传页面校验失败，请到飞印终端重新扫码后再试。')
        );
        setPageState('invalid');
      }
    };

    void loadPage();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (pageState !== 'ready' || !expiresAtMs) {
      return;
    }

    const syncCountdown = () => {
      const remainingMs = expiresAtMs - Date.now() - 2000;
      if (remainingMs <= 0) {
        setPageErrorMessage(EXPIRED_SESSION_MESSAGE);
        setPageState('invalid');
        return;
      }
    };

    syncCountdown();
    const timer = window.setInterval(syncCountdown, 500);

    return () => {
      window.clearInterval(timer);
    };
  }, [expiresAtMs, pageState]);

  const openFilePicker = () => {
    if (pageState !== 'ready' || uploadPhase !== 'idle') {
      return;
    }

    fileInputRef.current?.click();
  };

  const showUploadError = (errorMsg: string) => {
    message.open({
      key: 'public-upload-error',
      type: 'error',
      content: errorMsg,
      duration: 4,
      style: {
        marginTop: 16,
      },
    });
  };

  const handleUpload = async () => {
    if (!selectedFile || !token || !nodeId || !printerId) {
      showUploadError('上传参数不完整，请到飞印终端重新扫码后再试。');
      return;
    }

    if (validationError) {
      showUploadError(validationError);
      return;
    }

    setUploadPhase('uploading');

    try {
      const response = await apiService.uploadFile(selectedFile, token, nodeId, printerId);
      if (response.code === 200) {
        setPageState('success');
        return;
      }

      const responseErrorCode = (response as any).error;
      const friendlyMessage = getFriendlyErrorMessage(response.message, responseErrorCode, '上传失败，请稍后重试。');
      if (isTerminalUploadError(response.message, responseErrorCode)) {
        setPageErrorMessage(friendlyMessage);
        setPageState('invalid');
        return;
      }

      showUploadError(friendlyMessage);
    } catch (err: any) {
      const { rawMessage, errorCode } = extractErrorDetails(err);
      const friendlyMessage = getFriendlyErrorMessage(rawMessage, errorCode, '上传失败，请稍后重试。');
      if (isTerminalUploadError(rawMessage, errorCode)) {
        setPageErrorMessage(friendlyMessage);
        setPageState('invalid');
        return;
      }

      showUploadError(friendlyMessage);
    } finally {
      setUploadPhase('idle');
    }
  };

  const handleFileInputChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) {
      return;
    }

    setSelectedFile(file);
    setValidationError(policy ? validateSelectedFile(file, policy) : null);
    event.target.value = '';
  };

  const handleLogout = async () => {
    apiService.clearToken();
    try {
      await fetch(buildAuthUrl('logout'), { method: 'POST' });
    } finally {
      document.cookie = 'access_token=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT';
      const returnTo = `${window.location.pathname}${window.location.search}`;
      window.location.assign(`${buildAppPath('/login')}?return_to=${encodeURIComponent(returnTo)}`);
    }
  };

  if (pageState === 'verifying') {
    return <Spin fullscreen size="large" tip="正在验证上传二维码..." />;
  }

  if (pageState === 'invalid') {
    return (
      <div className="fp-upload-shell">
        <Alert
          message="访问失败"
          description={pageErrorMessage}
          type="error"
          showIcon
          action={<Button icon={<LogoutOutlined />} onClick={() => void handleLogout()}>退出账号</Button>}
          style={{ maxWidth: 520, padding: 24 }}
        />
      </div>
    );
  }

  if (pageState === 'success') {
    return (
      <div className="fp-upload-shell">
        <Result
          status="success"
          title="上传成功"
          subTitle="文件已提交到飞印平台，请在终端屏幕上继续确认打印。"
          icon={<CheckCircleOutlined style={{ color: '#52c41a' }} />}
          extra={<Button icon={<LogoutOutlined />} onClick={() => void handleLogout()}>退出账号</Button>}
          style={{ maxWidth: 520 }}
        />
      </div>
    );
  }

  const uploadDisabled = !selectedFile || !!validationError || uploadPhase !== 'idle';

  return (
    <div className="fp-upload-shell">
      <div className="fp-upload-content">
        <Card className="fp-upload-card" style={{ textAlign: 'center' }}>
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
            <Button type="link" danger icon={<LogoutOutlined />} onClick={() => void handleLogout()}>
              退出账号
            </Button>
          </div>
          <Title level={2}>文件上传</Title>
          <Paragraph type="secondary" style={{ marginBottom: 12 }}>
            二维码当前可用，请尽快完成上传。
          </Paragraph>

          {policy ? (
            <>
              <Paragraph type="secondary" style={{ marginBottom: 4 }}>
                文件类型：{formatAllowedTypes(policy)}
              </Paragraph>
              <Paragraph type="secondary" style={{ marginBottom: 20 }}>
                大小上限：{formatFileSize(policy.maxFileSizeBytes)}，文档页数上限：{policy.maxPages} 页
              </Paragraph>
            </>
          ) : null}

          <input
            id="public-upload-input"
            ref={fileInputRef}
            type="file"
            style={{ display: 'none' }}
            onChange={handleFileInputChange}
          />

          <button
            type="button"
            onClick={openFilePicker}
            disabled={uploadPhase !== 'idle'}
            style={{
              width: '100%',
              marginTop: 8,
              border: '1px dashed #d9d9d9',
              borderRadius: 8,
              padding: selectedFile ? '28px 24px' : '40px 24px',
              background: uploadPhase === 'idle' ? '#fafafa' : '#f5f5f5',
              cursor: uploadPhase === 'idle' ? 'pointer' : 'not-allowed',
              textAlign: 'center',
              transition: 'border-color 0.2s ease, background 0.2s ease',
            }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 }}>
              <FileOutlined style={{ fontSize: 48, color: '#1890ff' }} />
              {selectedFile ? (
                <>
                  <Text strong style={{ fontSize: 16, wordBreak: 'break-word' }}>
                    {selectedFile.name}
                  </Text>
                  <Text type="secondary">{formatFileSize(selectedFile.size)}</Text>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    点击这里可重新选择文件
                  </Text>
                </>
              ) : (
                <>
                  <Text strong style={{ fontSize: 16 }}>点击选择文件</Text>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    请先选择要上传的文件
                  </Text>
                </>
              )}
            </div>
          </button>

          {validationError ? (
            <Alert
              message={validationError}
              type="warning"
              showIcon
              style={{ marginTop: 16, textAlign: 'left' }}
            />
          ) : null}

          {selectedFile ? (
            <Button
              type="primary"
              size="large"
              block
              disabled={uploadDisabled}
              loading={uploadPhase !== 'idle'}
              onClick={handleUpload}
              style={{ marginTop: 24, height: 48 }}
            >
              上传文件
            </Button>
          ) : null}
        </Card>
      </div>
    </div>
  );
};

export default PublicUpload;
