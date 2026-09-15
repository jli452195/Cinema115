// 仅允许受支持的 115 分享地址，跳转时保留完整提取码。
export function is115Share(value: string): boolean {
  if (value.length > 4096) return false
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && !url.port
      && ['115.com', 'www.115.com', '115cdn.com'].includes(url.hostname)
      && /^\/s\/[a-zA-Z0-9]+\/?$/.test(url.pathname)
  } catch { return false }
}
