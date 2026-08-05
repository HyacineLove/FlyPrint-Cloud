/**
 * Map Cloud API error payloads to Chinese toast text for Admin UI.
 * Prefer code / known English phrases; never surface raw OAuth jargon.
 */

const CODE_MESSAGES: Record<string, string> = {
  invalid_grant: '用户名或密码错误',
  invalid_client: '客户端认证失败',
  invalid_request: '请求参数无效',
  unauthorized_client: '客户端未授权',
  access_denied: '访问被拒绝',
  unsupported_grant_type: '不支持的登录方式',
  server_error: '服务暂时不可用，请稍后重试',
  temporarily_unavailable: '服务暂时不可用，请稍后重试',
};

const PHRASE_MESSAGES: Array<[RegExp | string, string]> = [
  ['invalid email or password', '邮箱或密码错误'],
  ['invalid username or password', '用户名或密码错误'],
  ['invalid_grant', '用户名或密码错误'],
  ['invalid_client', '客户端认证失败'],
  ['upload_max_size_bytes must be greater than 0', '上传大小上限必须大于 0'],
  ['max_document_pages must be greater than 0', '文档页数上限必须大于 0'],
  ['upload_token_ttl_seconds must be greater than 0', '上传凭证有效期必须大于 0'],
  ['download_token_ttl_seconds must be greater than 0', '下载凭证有效期必须大于 0'],
  ['allowed_extensions must not be empty', '允许的文件扩展名不能为空'],
  ['display_name is required', '请填写显示名称'],
  ['invalid Site Portal configuration', 'Site Portal 配置无效'],
  ['Site Portal not found', 'Site Portal 不存在'],
  ['Failed to fetch', '网络连接失败，请检查网络后重试'],
  ['Network Error', '网络连接失败，请检查网络后重试'],
];

function looksTechnical(text: string): boolean {
  if (!text) return true;
  if (/^[a-z][a-z0-9_]*(\s|:|$)/i.test(text) && /[a-z]{3,}_[a-z]/.test(text)) return true;
  if (/^[A-Za-z][A-Za-z0-9_ .:-]*$/.test(text) && /[A-Za-z]{4,}/.test(text)) {
    // Pure Latin / snake_case / OAuth style → treat as technical unless short Chinese-safe.
    return !/[\u4e00-\u9fff]/.test(text);
  }
  return false;
}

function mapPhrase(raw: string): string | null {
  const text = raw.trim();
  if (!text) return null;
  const lower = text.toLowerCase();
  for (const [pattern, zh] of PHRASE_MESSAGES) {
    if (typeof pattern === 'string') {
      if (lower.includes(pattern.toLowerCase())) return zh;
    } else if (pattern.test(text)) {
      return zh;
    }
  }
  // OAuth style: "invalid_grant: something"
  const codeMatch = text.match(/^([a-z_]+)(?:\s*:|\s|$)/i);
  if (codeMatch) {
    const code = codeMatch[1].toLowerCase();
    if (CODE_MESSAGES[code]) return CODE_MESSAGES[code];
  }
  return null;
}

export type ApiErrorLike = {
  error?: string;
  error_description?: string;
  message?: string;
  code?: string | number;
  details?: { message?: string };
};

/**
 * Resolve the best user-facing Chinese message from an API error object or Error.
 */
export function mapApiError(
  error: unknown,
  fallback = '操作失败，请稍后重试',
): string {
  if (error == null) return fallback;

  if (typeof error === 'string') {
    return mapPhrase(error) || (looksTechnical(error) ? fallback : error) || fallback;
  }

  if (error instanceof Error) {
    return mapPhrase(error.message) || (looksTechnical(error.message) ? fallback : error.message) || fallback;
  }

  const payload = error as ApiErrorLike;
  const candidates = [
    payload.error_description,
    typeof payload.message === 'string' ? payload.message : undefined,
    payload.details?.message,
    typeof payload.error === 'string' ? payload.error : undefined,
    payload.code != null ? String(payload.code) : undefined,
  ].filter((item): item is string => Boolean(item && String(item).trim()));

  for (const candidate of candidates) {
    const mapped = mapPhrase(candidate);
    if (mapped) return mapped;
  }

  for (const candidate of candidates) {
    if (!looksTechnical(candidate)) return candidate;
  }

  return fallback;
}

export function showMappedError(
  messageApi: { error: (content: string) => void },
  error: unknown,
  fallback = '操作失败，请稍后重试',
): void {
  messageApi.error(mapApiError(error, fallback));
}
