// 全局配置：统一管理后端 API 与认证路径
//
// 设计目标：
// - 单独 build：直接读取前端项目下的 .env（REACT_APP_*）
// - 整体 build（Docker）：由 docker-compose 在构建/运行阶段注入环境变量
//
// 约定环境变量（按优先级从高到低）：
// - REACT_APP_API_BASE_PATH：如 /api/v1 或 /fly-print-api/api/v1
// - REACT_APP_API_URL：旧配置兼容（如 http://host/fly-print-api），只取路径部分
// - REACT_APP_AUTH_BASE_PATH：如 /auth 或 /fly-print-api/auth
// - REACT_APP_BASENAME / PUBLIC_URL：SPA 子路径前缀，如 /fly-print-web（根路径部署留空）
// - 默认值：/api/v1

const getEnv = (key: string): string | undefined => {
  // CRA 中只会注入以 REACT_APP_ 开头的变量，这里直接从 process.env 读取
  return (process.env as any)[key] as string | undefined;
};

// 从完整 URL 中提取 path 部分（兼容 REACT_APP_API_URL=http://host/fly-print-api）
const extractPathFromUrl = (url?: string): string | undefined => {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    return u.pathname || '/';
  } catch {
    // 不是合法 URL，则按原样返回（视作 path）
    return url;
  }
};

// 规范化前缀：去掉多余尾部斜杠，确保以单个 / 开头
const normalizeBasePath = (raw?: string, fallback: string = '/api/v1'): string => {
  if (!raw) return fallback;
  let base = raw.trim();
  if (!base) return fallback;

  // 如果是完整 URL，只取 path
  base = extractPathFromUrl(base) || base;

  // 确保以 /
  if (!base.startsWith('/')) {
    base = '/' + base;
  }

  // 去掉尾部多余的 /
  base = base.replace(/\/+$/, '');
  return base || fallback;
};

// API 基础路径：示例
// - /api/v1
// - /fly-print-api/api/v1
const API_BASE_PATH = normalizeBasePath(
  getEnv('REACT_APP_API_BASE_PATH') || getEnv('REACT_APP_API_URL'),
  '/api/v1'
);

// 认证基础路径：通常为 /auth 或 /fly-print-api/auth
const AUTH_BASE_PATH = normalizeBasePath(
  getEnv('REACT_APP_AUTH_BASE_PATH'),
  '/auth'
);

// SPA 挂载前缀：子路径部署时与 PUBLIC_URL 一致（如 /fly-print-web）；根路径为空
const rawBasename =
  getEnv('REACT_APP_BASENAME') ||
  getEnv('PUBLIC_URL') ||
  '';
const APP_BASENAME =
  !rawBasename || rawBasename === '/'
    ? ''
    : normalizeBasePath(rawBasename, '');

// 构造 API 完整路径：buildApiUrl('admin/printers') -> /api/v1/admin/printers
export const buildApiUrl = (path: string): string => {
  const p = path.startsWith('/') ? path.slice(1) : path;
  return `${API_BASE_PATH}/${p}`;
};

// 构造认证路径：buildAuthUrl('me') -> /auth/me
export const buildAuthUrl = (path: string): string => {
  const p = path.startsWith('/') ? path.slice(1) : path;
  return `${AUTH_BASE_PATH}/${p}`;
};

// 构造前端路由绝对路径（含 basename）：buildAppPath('/login') -> /fly-print-web/login
export const buildAppPath = (path: string): string => {
  const p = path.startsWith('/') ? path : `/${path}`;
  if (!APP_BASENAME) return p || '/';
  if (p === '/') return APP_BASENAME;
  return `${APP_BASENAME}${p}`;
};

export { API_BASE_PATH, AUTH_BASE_PATH, APP_BASENAME };

