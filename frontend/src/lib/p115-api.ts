// 统一处理鉴权和非 JSON 错误，避免代理异常时出现 JSON 解析乱码提示。
export async function p115Request<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/admin/p115/${path}`, {
    ...init,
    cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, ...(init.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }) },
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => null)
  if (!response.ok) {
    // 保留状态和结构化回执，云下载可区分明确失败与网关中断后结果未知。
    const message = response.status === 401 ? '管理员登录已过期，请返回首页重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`
    throw Object.assign(new Error(message), { status: response.status, responseData: data })
  }
  if (!data) throw new Error('接口未返回有效数据，请检查后端服务')
  return data as T
}
