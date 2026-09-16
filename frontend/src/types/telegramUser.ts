export interface TelegramUserStatus { apiId: number; configured: boolean; hasSession: boolean; authorized: boolean; name: string; username: string; stage: 'idle' | 'code' | 'password'; bot: string }
export interface BotSearchResult { text: string; items: { id: string; title: string; tmdbId?: string; size?: string; kind: 'resource' | 'page' }[]; links: string[]; remaining?: number; page?: number; empty: boolean; allLoaded?: boolean; cached?: boolean; expiresAt?: string }
export interface BotSearchStreamEvent { complete: boolean; result: BotSearchResult }
export async function telegramUserRequest<T>(token: string, path: string, signal: AbortSignal, body?: object): Promise<T> {
  const response = await fetch(`/api/admin/telegram-user/${path}`, { method: body === undefined ? 'GET' : 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([signal, AbortSignal.timeout(75000)]), body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || 'Telegram 请求失败，请稍后重试')
  return data
}

export async function telegramUserSearchStream(token: string, signal: AbortSignal, body: object, onResult: (event: BotSearchStreamEvent) => void): Promise<void> {
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(75000)])
  const response = await fetch('/api/admin/telegram-user/search-stream', { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', Accept: 'text/event-stream' }, signal: requestSignal, body: JSON.stringify(body) })
  if (!response.ok || !response.body) {
    const data = await response.json().catch(() => null)
    throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || 'Telegram 搜索请求失败')
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let complete = false
  const consume = (block: string) => {
    let event = ''
    const data: string[] = []
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith('event:')) event = line.slice(6).trim()
      else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
    }
    if (!data.length) return
    const value = JSON.parse(data.join('\n'))
    if (event === 'error') throw new Error(value?.error || 'Telegram 搜索请求失败')
    if (event === 'result') {
      const update = value as BotSearchStreamEvent
      complete ||= update.complete
      onResult(update)
    }
  }
  while (true) {
    const { done, value } = await reader.read()
    buffer += decoder.decode(value, { stream: !done })
    buffer = buffer.replace(/\r\n/g, '\n')
    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      consume(buffer.slice(0, boundary))
      buffer = buffer.slice(boundary + 2)
      boundary = buffer.indexOf('\n\n')
    }
    if (done) break
  }
  if (buffer.trim()) consume(buffer)
  if (!complete) throw new Error('Telegram 流式搜索意外中断，请重试')
}
