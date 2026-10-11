/** 注册表模板展开后，只有绝对 HTTP(S) 地址可作为外链；裸标识仍可展示。 */
export function safeExternalAuthorityURL(candidate: string): string {
  const value = candidate.trim();
  if (!/^https?:\/\//i.test(value) || /[\u0000-\u001f\u007f\\]/.test(value)) return "";
  try {
    const url = new URL(value);
    if (!url.hostname || url.username || url.password) return "";
    return url.href;
  } catch {
    return "";
  }
}
