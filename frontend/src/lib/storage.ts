// 存储服务（metafusion-storage）的前端契约封装：/api/storage/*。
//
// 契约以该服务仓库的 README 与 internal/handler 为准，这里只做搬运与错误归一：
// 所有请求走网关同源相对路径（deploy/nginx.conf 把 /api/storage/ 转到 storage:8082），
// 客户端不直连对象存储——预签名地址一律用服务端返回的那一个。
//
// 只在前端使用（浏览器）：Web Crypto 与 XHR 上传进度都依赖 DOM 环境。

import { ApiError, fetchApi } from "./api";

/** 网关统一前缀。浏览器端 getApiBase() 恒为 "/api"，与 lib/api 的既有约定一致。 */
const STORAGE_API_BASE = "/api/storage";

/** 默认绑定用途，与存储服务 handler 的 defaultRole 一致。 */
export const DEFAULT_BINDING_ROLE = "master_archive";

/** binding_role 是字段码：与 internal/handler 的 codePattern 同口径。 */
export const BINDING_ROLE_PATTERN = /^[a-z][a-z0-9_]{0,31}$/;

/** assets 主键是 UUID：编辑器据此给行内提示，服务端 invalid_picture_asset 仍是最终判据。 */
const ASSET_UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** 空值放过（asset_id 可选），非空时必须是标准写法 UUID；不合规不会被存储服务认。 */
export function isAssetUuid(value?: string | null): boolean {
  const v = String(value || "").trim();
  return !v || ASSET_UUID_PATTERN.test(v);
}

/** 预设用途码：README 列出的运维/编目约定项。不是封闭枚举，界面允许自定义。 */
export const BINDING_ROLE_PRESETS = [
  "master_archive",
  "track_audio",
  "disc_image",
  "video",
  "scans",
  "subtitle",
  "ebook",
] as const;

/**
 * 自托管图片的可直链地址：`GET /api/storage/assets/{id}/content`。
 * 该路由按请求鉴权、按绑定实体的可见性判定，因此浏览器 `<img>` 能直接引用（不必先换
 * 预签名地址）。返回的是同源相对路径，写进 `pictures[].url` 后详情页与 OG 标签都能用
 * （seo.ts 会用站点绝对地址包一次）。
 */
export function assetContentUrl(assetId: string): string {
  return `${STORAGE_API_BASE}/assets/${encodeURIComponent(assetId.trim())}/content`;
}

/** 一份物理文件的内容寻址记录（storage.assets）。身份是 sha256，不含目录语义。 */
export interface StorageAsset {
  id: string;
  sha256: string;
  size_bytes: number;
  declared_size: number;
  mime_type: string;
  file_name: string;
  object_key: string;
  /** pending / complete；只有 complete 的文件才可下载。 */
  status: string;
  multipart_upload_id?: string;
  hash_verified: boolean;
  uploader_id: string;
  /** 只在服务端回读校验失败时写入（不参与任何判定），是"为什么停在 pending"的唯一线索。 */
  fail_reason?: string;
  created_at: string;
  completed_at?: string | null;
}

/** 「文件 → 目录实体」的挂载（storage.bindings）。 */
export interface StorageBinding {
  id: string;
  asset_id: string;
  target_entity_id: string;
  target_kind: string;
  binding_role: string;
  created_by: string;
  created_at: string;
}

/** 列表视图：绑定 + 它指向的文件元数据（GET /entities/{id}/files 的元素形状）。 */
export interface StorageFileBinding extends StorageBinding {
  asset: StorageAsset;
}

export interface EntityFilesResponse {
  target_entity_id: string;
  target_kind: string;
  /** 没有任何绑定时服务端返回 null（Go 的 nil slice），不是空数组。 */
  files: StorageFileBinding[] | null;
}

export interface UploadedPart {
  part_number: number;
  etag: string;
}

/** POST /upload/initiate 的响应：命中已有 sha256 即秒传，否则给直传地址。 */
export interface InitiateUploadResponse {
  is_instant_upload: boolean;
  asset_id: string;
  object_key: string;
  upload_id?: string;
  /** 对象存储模式：分片（或单次 PUT）预签名地址。 */
  presigned_urls?: string[] | null;
  /** 本地对象模式：服务端流式接收地址，也可能作为预签名不可用时的兜底。 */
  direct_upload_url?: string;
  part_size_hint?: number;
  expires_at: string;
  asset?: StorageAsset | null;
}

/**
 * 上传/下载失败：code 取服务端 error 字段或客户端自定义码，status 为 HTTP 状态
 * （网络层失败记为 0，用于和 4xx/5xx 区分）。界面据此给不同文案，不吞错误。
 */
export class StorageRequestError extends Error {
  readonly code: string;
  readonly status: number;
  constructor(code: string, status: number) {
    super(code);
    this.name = "StorageRequestError";
    this.code = code;
    this.status = status;
    // 目标为 ES5 时 Error 子类的原型链会断开，显式接回（与 lib/api 的 ApiError 同样处理）。
    Object.setPrototypeOf(this, StorageRequestError.prototype);
  }
}

/** 读取错误响应里的 error 字段；响应体不是 JSON 时退回 HTTP 状态描述。 */
async function errorCodeOf(res: Response): Promise<string> {
  try {
    const data = await res.json();
    if (data && typeof data.error === "string" && data.error) return data.error;
  } catch {
    /* 非 JSON 响应（如网关的 HTML 错误页）：用状态码描述 */
  }
  return `HTTP ${res.status}`;
}

function errorCodeOfText(text: string, status: number): string {
  try {
    const data = JSON.parse(text);
    if (data && typeof data.error === "string" && data.error) return data.error;
  } catch {
    /* 忽略：非 JSON */
  }
  return `HTTP ${status}`;
}

/** 「这个实体上挂了哪些文件」列表入口（读接口，匿名可读由实体可见性决定）。 */
export function fetchEntityFiles(entityId: string): Promise<EntityFilesResponse> {
  return fetchApi<EntityFilesResponse>(`/storage/entities/${encodeURIComponent(entityId)}/files`);
}

/**
 * 直传第一步。刻意不带 target_entity_id：绑定由调用方在 complete 之后单独发
 * POST /bind，避免服务端在 initiate 时就建一次绑定、随后 bind 再撞唯一键。
 */
export function initiateUpload(payload: {
  fileName: string;
  fileSize: number;
  sha256Hash: string;
  mimeType: string;
  bindingRole: string;
}, signal?: AbortSignal): Promise<InitiateUploadResponse> {
  return fetchApi<InitiateUploadResponse>("/storage/upload/initiate", {
    method: "POST",
    signal,
    body: JSON.stringify({
      file_name: payload.fileName,
      file_size: payload.fileSize,
      sha256_hash: payload.sha256Hash,
      mime_type: payload.mimeType,
      binding_role: payload.bindingRole,
      part_count: 1,
    }),
  });
}

/** 分片合并/落定；单次 PUT 场景服务端只做存在性确认（本地模式甚至直接幂等返回）。 */
export function completeUpload(
  assetId: string,
  uploadId?: string,
  parts?: UploadedPart[],
  signal?: AbortSignal
): Promise<{ asset: StorageAsset; already_complete?: boolean }> {
  const body: Record<string, unknown> = { asset_id: assetId };
  if (uploadId) body.upload_id = uploadId;
  // 只有真正建立了分片会话才需要带 parts：单次 PUT 时 uploadID 为空，服务端按 HEAD 回读落定。
  if (uploadId && parts && parts.length > 0) body.parts = parts;
  return fetchApi<{ asset: StorageAsset; already_complete?: boolean }>("/storage/upload/complete", {
    method: "POST",
    signal,
    body: JSON.stringify(body),
  });
}

/** 绑定到目录实体；target_entity_type 不传，由服务端按目录服务的权威 kind 判定。 */
export function bindAsset(
  assetId: string,
  entityId: string,
  bindingRole: string
): Promise<{ binding: StorageBinding }> {
  return fetchApi<{ binding: StorageBinding }>("/storage/bind", {
    method: "POST",
    body: JSON.stringify({
      asset_id: assetId,
      target_entity_id: entityId,
      binding_role: bindingRole,
    }),
  });
}

/**
 * 浏览器端算 SHA-256。Web Crypto 没有增量摘要接口，只能整份读进内存
 * ——大文件（GB 级原档）会占同等内存，这是内容寻址去重在前端的固有代价。
 * 非安全上下文（http 且非 localhost）没有 crypto.subtle，抛 hash_unavailable。
 */
export async function sha256HexOfFile(file: File): Promise<string> {
  const subtle = typeof crypto !== "undefined" ? crypto.subtle : undefined;
  if (!subtle) throw new StorageRequestError("hash_unavailable", 0);
  const digest = await subtle.digest("SHA-256", await file.arrayBuffer());
  const bytes = new Uint8Array(digest);
  let hex = "";
  for (let i = 0; i < bytes.length; i++) {
    const b = bytes[i];
    hex += (b < 16 ? "0" : "") + b.toString(16);
  }
  return hex;
}

export interface PutHandlers {
  headers?: Record<string, string>;
  onProgress?: (loaded: number, total: number) => void;
  /** 上传阶段的服务端错误码与状态：网络层失败 status 为 0，供调用方决定是否回退。 */
  errorCode: (code: string, status: number) => StorageRequestError;
}

/**
 * 用 XHR 而不是 fetch：fetch 拿不到上传进度事件。
 * 返回 abort()，供界面「取消」真正中断请求，而不是只清空状态。
 */
export function putFile(
  url: string,
  file: File,
  handlers: PutHandlers
): { promise: Promise<string>; abort: () => void } {
  let abort = () => {};
  const promise = new Promise<string>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    abort = () => xhr.abort();
    xhr.open("PUT", url, true);
    const headers = handlers.headers || {};
    for (const name of Object.keys(headers)) xhr.setRequestHeader(name, headers[name]);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && handlers.onProgress) handlers.onProgress(e.loaded, e.total);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(xhr.responseText);
        return;
      }
      reject(handlers.errorCode(errorCodeOfText(xhr.responseText, xhr.status), xhr.status));
    };
    xhr.onerror = () => reject(handlers.errorCode("network_error", 0));
    xhr.onabort = () => reject(handlers.errorCode("aborted", 0));
    xhr.send(file);
  });
  return { promise, abort: () => abort() };
}

/** 服务端流式接收地址：本地对象模式的主要上传方式，也是预签名不可用时的兜底。 */
export function streamUploadUrl(assetId: string): string {
  return `${STORAGE_API_BASE}/upload/stream/${encodeURIComponent(assetId)}`;
}

/** 下载结果：presigned 表示已把预签名直链交给浏览器（能否打开取决于对象存储对外地址）。 */
export interface DownloadResult {
  mode: "presigned" | "stream";
}

/**
 * 下载入口：本地对象模式由服务端流式下发，对象存储模式返回预签名地址。
 * 下载接口要判身份（上传者/审核者直通，其余按绑定实体可见性），
 * 因此这里用带同域 Cookie 的 fetch 请求：
 * JSON 响应＝对象存储模式的预签名地址（直接跳转）；其余按流处理，落成临时 Blob 保存。
 */
export async function downloadAssetFile(assetId: string, fileName: string): Promise<DownloadResult> {
  const res = await fetch(`${STORAGE_API_BASE}/download/${encodeURIComponent(assetId)}`, {
    method: "GET",
    credentials: "same-origin",
  });
  if (!res.ok) throw new StorageRequestError(await errorCodeOf(res), res.status);
  const contentType = res.headers.get("content-type") || "";
  if (contentType.indexOf("application/json") >= 0) {
    const data = await res.json();
    if (typeof data.download_url === "string" && data.download_url) {
      window.open(data.download_url, "_blank", "noopener,noreferrer");
      return { mode: "presigned" };
    }
    throw new StorageRequestError("download_url_missing", res.status);
  }
  const blob = await res.blob();
  const objectUrl = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = objectUrl;
  link.download = fileName || "download";
  document.body.appendChild(link);
  link.click();
  link.remove();
  // 立刻 revoke 会让部分浏览器取消保存，延后释放。
  window.setTimeout(() => URL.revokeObjectURL(objectUrl), 60000);
  return { mode: "stream" };
}

/** 界面错误归一：ApiError（JSON 端点）与 StorageRequestError（直传/流式）统一成文案键。 */
export function storageErrorKey(err: unknown): { key: string; vars?: Record<string, string | number> } {
  const status = err instanceof ApiError ? err.status : err instanceof StorageRequestError ? err.status : 0;
  const code = err instanceof ApiError ? err.message : err instanceof StorageRequestError ? err.code : "";
  if (code === "hash_unavailable") return { key: "storage.files.errCrypto" };
  if (code === "network_error") return { key: "storage.files.errNetwork" };
  if (code === "aborted") return { key: "storage.files.cancelled" };
  if (code === "hash_mismatch" || code === "size_mismatch") return { key: "storage.files.errMismatch" };
  if (code === "upload_in_progress") return { key: "storage.files.errInProgress" };
  if (code === "invalid_binding_role") return { key: "storage.files.errRole" };
  if (code === "upload_incomplete") return { key: "storage.files.errIncomplete" };
  if (status === 0) return { key: "storage.files.errNetwork" };
  if (status === 401) return { key: "storage.files.errAuth" };
  if (status === 403) return { key: "storage.files.errForbidden" };
  if (status === 404) return { key: "storage.files.errNotFound" };
  if (status === 409) return { key: "storage.files.errConflict" };
  if (status === 413) return { key: "storage.files.errTooLarge" };
  if (status === 429) return { key: "storage.files.errRateLimited" };
  if (status === 502 || status === 503 || status === 504) return { key: "storage.files.errUnavailable" };
  return { key: "storage.files.errGeneric", vars: { status } };
}

// ── 管理台入口：全局用量、按 id 查资产、解绑 ──

/**
 * GET /stats：全局（跨所有上传者）status='complete' 的资产条数与字节数。
 * 契约里**没有配额字段**（没有 quota / limit / used / remaining，也没有按上传者拆分），
 * 界面不得据此推算剩余空间、占用率或人均用量。
 */
/**
 * 解绑：不带 body，也不要求 storage.asset.upload——所有权在处理器内判定
 * （绑定创建者 == 我 / 资产上传者 == 我 / 持 storage.asset.moderate）。
 * **不幂等**：重复删同一个 id 的第二次是 404 not_found，调用方必须据此重新取数，
 * 不能乐观地留下幽灵行。
 */
export function deleteStorageBinding(bindingId: string): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>(`/storage/bindings/${encodeURIComponent(bindingId)}`, { method: "DELETE" });
}
