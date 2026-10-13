import { onBeforeUnmount, ref } from 'vue'

export class AuthRequestError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = 'AuthRequestError'
  }
}

// Invitation checks and registration share the server's anonymous rate limit.
export function useAuthRequest() {
  const lifecycle = new AbortController()
  const retrySeconds = ref(0)
  let retryAt = 0
  let timer: ReturnType<typeof setInterval> | undefined

  function updateCooldown() {
    retrySeconds.value = Math.max(0, Math.ceil((retryAt - performance.now()) / 1000))
    if (!retrySeconds.value && timer) {
      clearInterval(timer)
      timer = undefined
    }
  }

  async function request<T>(endpoint: string, input: object, timeout = 30000): Promise<T> {
    updateCooldown()
    if (retrySeconds.value) throw new AuthRequestError('操作过于频繁，请稍后重试', 429)
    const response = await fetch(endpoint, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(timeout)]),
    })
    const data = await response.json().catch(() => null)
    if (response.status === 429 && !lifecycle.signal.aborted) {
      const header = response.headers.get('Retry-After')
      const seconds = header ? Number(header) : NaN
      const date = header ? Date.parse(header) : NaN
      const serverDate = Date.parse(response.headers.get('Date') || '')
      const wait = Number.isFinite(seconds) && seconds >= 0 ? seconds * 1000 : Number.isFinite(date) ? Math.max(0, date - (Number.isFinite(serverDate) ? serverDate : Date.now())) : 60000
      retryAt = performance.now() + wait
      updateCooldown()
      if (retrySeconds.value && !timer) timer = setInterval(updateCooldown, 1000)
    }
    if (!response.ok) throw new AuthRequestError(typeof data?.error === 'string' ? data.error : '服务暂时不可用，请稍后重试', response.status)
    return data as T
  }

  onBeforeUnmount(() => {
    lifecycle.abort()
    if (timer) clearInterval(timer)
  })
  return { request, retrySeconds, isActive: () => !lifecycle.signal.aborted }
}
