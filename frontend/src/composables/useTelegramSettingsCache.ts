import { watch } from 'vue'
import { telegramRequest } from '../lib/telegram-api'
import { telegramRelayRequest } from '../lib/telegram-relay-api'
import { telegramUserRequest, type TelegramUserStatus } from '../types/telegramUser'
import type { TelegramBotConfig, TelegramBotModule } from '../types/telegramBots'
import type { TelegramRelaySettings } from '../types/telegramRelay'
import { portalToken, portalUser } from './usePortalAuth'

const isCurrentSession = (token: string) => !!token && token === portalToken.value && portalUser.value?.role === 'admin'

function createSettingsCache<T>(maxAge: number, fetchSettings: (token: string, signal: AbortSignal) => Promise<T>) {
  let snapshot: { data: T; expiresAt: number } | undefined
  let pending: { controller: AbortController; promise: Promise<T | undefined> } | undefined
  let writing: Promise<unknown> | undefined
  let sessionVersion = 0

  function discardRead() {
    pending?.controller.abort()
    pending = undefined
    snapshot = undefined
  }
  function get(token: string) {
    if (isCurrentSession(token) && snapshot) return { ...snapshot, fresh: Date.now() < snapshot.expiresAt }
  }
  function read(token: string): Promise<T | undefined> {
    if (!isCurrentSession(token)) return Promise.resolve(undefined)
    const version = sessionVersion
    const owner = portalUser.value?.id
    const current = () => version === sessionVersion && isCurrentSession(token)
    const readLatest = () => {
      if (current()) return get(token)?.data ?? read(token)
      // 同一管理员续期时，用新令牌完成仍在等待的读取。
      const next = portalToken.value
      if (sessionVersion === version + 1 && next !== token && owner === portalUser.value?.id && isCurrentSession(next)) {
        return get(next)?.data ?? read(next)
      }
    }
    if (writing) return writing.then(readLatest, readLatest)
    if (pending) return pending.promise

    // 读取属于登录会话，离开面板时仍可让已发出的请求完成。
    const controller = new AbortController()
    const promise = fetchSettings(token, controller.signal).then(data => {
      if (!current() || controller.signal.aborted) return readLatest()
      snapshot = { data, expiresAt: Date.now() + maxAge }
      return data
    }, reason => {
      if (!current() || controller.signal.aborted) return readLatest()
      throw reason
    }).finally(() => {
      if (pending?.controller === controller) pending = undefined
    })
    pending = { controller, promise }
    return promise
  }
  function mutate<Result>(token: string, operation: () => Promise<Result>, updated?: (result: Result) => T | undefined) {
    const previous = writing
    const version = sessionVersion
    const current = () => version === sessionVersion && isCurrentSession(token)
    if (!current()) return Promise.reject<Result>(new DOMException('登录会话已变更', 'AbortError'))
    // 等待写入结果后再读取，避免较早发出的请求覆盖最新配置。
    discardRead()
    const request = Promise.resolve(previous).catch(() => undefined).then(async () => {
      if (!current()) throw new DOMException('登录会话已变更', 'AbortError')
      const result = await operation()
      if (writing === request && current()) {
        const data = updated?.(result)
        if (data !== undefined) snapshot = { data, expiresAt: Date.now() + maxAge }
      }
      return result
    }).finally(() => { if (writing === request) writing = undefined })
    writing = request
    return request
  }

  function clear() {
    sessionVersion++
    writing = undefined
    discardRead()
  }
  watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role], clear, { flush: 'sync' })
  return { get, read, mutate }
}

export const telegramAccountCache = createSettingsCache(30_000, (token, signal) =>
  telegramUserRequest<TelegramUserStatus>(token, 'config', signal))

export const telegramRelayCache = createSettingsCache(30_000, (token, signal) =>
  telegramRelayRequest<TelegramRelaySettings>(token, '', signal))

export const TELEGRAM_BOT_POLL_MS = 8_000
export const telegramBotCache = createSettingsCache(TELEGRAM_BOT_POLL_MS, async (token, signal) => {
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(15_000)])
  const [botData, moduleData] = await Promise.all([
    telegramRequest<{ bots: TelegramBotConfig[] }>(token, 'bots', { signal: requestSignal }),
    telegramRequest<{ modules: TelegramBotModule[] }>(token, 'modules', { signal: requestSignal }),
  ])
  return { bots: botData.bots, modules: moduleData.modules }
})

export function updateTelegramAccount(token: string, path: string, signal: AbortSignal, body: object | (() => Promise<object>)) {
  return telegramAccountCache.mutate(token, () => telegramRelayCache.mutate(token, async () => {
    const input = typeof body === 'function' ? await body() : body
    return telegramUserRequest<TelegramUserStatus>(token, path, signal, input)
  }), value => value)
}
