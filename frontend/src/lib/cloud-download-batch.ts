export interface CloudBatchEvent {
  type: 'ready' | 'start' | 'result' | 'done'
  index: number
  total: number
  accepted?: boolean
  outcomeUncertain?: boolean
  error?: string
  stopped?: boolean
}

export function trackCloudBatch(total: number) {
  const active = new Set<number>()
  const completed = new Set<number>()
  let done = false

  function accept(event: CloudBatchEvent) {
    if (event.total !== total) throw new Error('批量提交回执数量异常')
    if (done) throw new Error('批量提交回执顺序异常')
    if (event.type === 'ready') return
    if (event.type === 'done') {
      if (active.size || (!event.stopped && completed.size !== total)) throw new Error('批量提交缺少资源回执')
      done = true
      return
    }
    if (event.index < 0 || event.index >= total || completed.has(event.index)) throw new Error('批量提交回执编号异常')
    if (event.type === 'start') {
      if (active.has(event.index)) throw new Error('批量提交回执顺序异常')
      active.add(event.index)
      return
    }
    if (!active.delete(event.index)) throw new Error('批量提交回执顺序异常')
    completed.add(event.index)
  }

  return { accept, completed, get done() { return done } }
}

export function magnetKey(raw: string): string {
  try {
    if (!/^magnet:\?xt=urn:btih:/i.test(raw.trim())) return ''
    const xt = new URL(raw.trim()).searchParams.get('xt') || ''
    if (!/^urn:btih:/i.test(xt)) return ''
    const hash = xt.replace(/^urn:btih:/i, '')
    if (/^[a-f\d]{40}$/i.test(hash)) return hash.toLowerCase()
    if (!/^[a-z2-7]{32}$/i.test(hash)) return ''
    const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
    let bits = 0, value = 0, hex = ''
    for (const char of hash.toUpperCase()) {
      value = (value << 5) | alphabet.indexOf(char)
      bits += 5
      if (bits >= 8) { bits -= 8; hex += ((value >>> bits) & 255).toString(16).padStart(2, '0') }
    }
    return hex
  } catch { return '' }
}

export async function submitCloudBatch(token: string, target: { profileId: string; accountId: string; targetCID: string }, urls: string[], signal: AbortSignal, onEvent: (event: CloudBatchEvent) => void): Promise<void> {
  const response = await fetch('/api/admin/p115/cloud-download/tasks/batch', {
    method: 'POST', cache: 'no-store', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ profileId: target.profileId, accountId: target.accountId, targetCID: target.targetCID, urls }), signal,
  })
  await readCloudBatch(response, onEvent)
}

// 处理数据块边界并要求最终回执，断流后未确认的资源不能直接作为安全重试候选。
export async function readCloudBatch(response: Response, onEvent: (event: CloudBatchEvent) => void): Promise<void> {
  if (!response.ok) {
    const data = await response.json().catch(() => null)
    const rejectedBeforeStart = [400, 401, 403, 404, 409, 413, 422].includes(response.status) && !!data?.error
    throw Object.assign(new Error(data?.error || `批量提交失败（HTTP ${response.status}）`), {
      beforeStart: rejectedBeforeStart,
      status: response.status,
      code: data?.code,
      responseData: data,
    })
  }
  if (!response.body || !response.headers.get('Content-Type')?.includes('application/x-ndjson')) throw new Error('批量提交未返回完整回执，请先核对 115 云下载')
  const reader = response.body.getReader(), decoder = new TextDecoder()
  let pending = '', done = false
  const consume = (line: string) => {
    if (!line.trim()) return
    const event = JSON.parse(line) as CloudBatchEvent
    if (!['ready', 'start', 'result', 'done'].includes(event.type) || !Number.isSafeInteger(event.total) || (['start', 'result'].includes(event.type) && !Number.isInteger(event.index)) || event.total < 1) throw new Error('批量提交回执格式异常，请核对 115 云下载')
    if (event.type === 'done') done = true
    onEvent(event)
  }
  try {
    while (true) {
      const chunk = await reader.read()
      pending += decoder.decode(chunk.value, { stream: !chunk.done })
      let newline: number
      while ((newline = pending.indexOf('\n')) !== -1) {
        consume(pending.slice(0, newline)); pending = pending.slice(newline + 1)
      }
      if (pending.length > 65536) throw new Error('批量提交回执过长')
      if (chunk.done) break
    }
    consume(pending)
    if (!done) throw new Error('批量提交连接中断，请核对已发送资源的结果')
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
}
