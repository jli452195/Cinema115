export const CLOUD_BATCH_LIMIT = 100
export interface CloudBatchEvent {
  type: 'ready' | 'start' | 'result' | 'done'
  index: number
  total: number
  accepted?: boolean
  removedOldTask?: boolean
  outcomeUncertain?: boolean
  error?: string
  stopped?: boolean
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

// Parse chunk boundaries and require the final receipt. A dropped stream must
// not turn submitted-but-unacknowledged rows into safe retry candidates.
export async function readCloudBatch(response: Response, onEvent: (event: CloudBatchEvent) => void): Promise<void> {
  if (!response.ok) {
    const data = await response.json().catch(() => null)
    const rejectedBeforeStart = [400, 401, 403, 404, 409, 413, 422].includes(response.status) && !!data?.error
    throw Object.assign(new Error(data?.error || `批量提交失败（HTTP ${response.status}）`), { beforeStart: rejectedBeforeStart })
  }
  if (!response.body || !response.headers.get('Content-Type')?.includes('application/x-ndjson')) throw new Error('批量提交未返回完整回执，请先核对 115 云下载')
  const reader = response.body.getReader(), decoder = new TextDecoder()
  let pending = '', done = false
  const consume = (line: string) => {
    if (!line.trim()) return
    const event = JSON.parse(line) as CloudBatchEvent
    if (!['ready', 'start', 'result', 'done'].includes(event.type) || !Number.isInteger(event.total) || (['start', 'result'].includes(event.type) && !Number.isInteger(event.index)) || event.total < 1 || event.total > CLOUD_BATCH_LIMIT) throw new Error('批量提交回执格式异常，请核对 115 云下载')
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
