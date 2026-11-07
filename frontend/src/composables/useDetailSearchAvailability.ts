import { ref, watch } from 'vue'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'
import { telegramUserRequest, type TelegramUserStatus, type TelegramChannelConfig } from '../types/telegramUser'
import { gyingRequest, type GyingConfigView } from '../types/gying'
import { woniuRequest, type WoniuConfigView } from '../types/woniu'
import type { DetailSearchAvailability, DetailSearchSourceID } from '../utils/detailSearchSources'

// 只读取已保存的配置，不登录来源、不启动搜索；未知或失败的来源不出现在预览和详情页。
export function useDetailSearchAvailability(token: () => string, enabled: () => boolean) {
  const availability = ref<DetailSearchAvailability>({})
  watch(() => [token(), enabled()] as const, ([value, allowed], _, onCleanup) => {
    availability.value = {}
    if (!allowed || !value) return
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(10000)])
    async function check(id: DetailSearchSourceID, request: Promise<{ configured: boolean }>) {
      try {
        const config = await request
        if (!signal.aborted) availability.value[id] = config.configured === true
      } catch {
        if (!signal.aborted) availability.value[id] = false
      }
    }
    void check('re0', nanShareRequest<NanShareConfigView>(value, 'config', signal))
    void check('bot', telegramUserRequest<TelegramUserStatus>(value, 'config', signal).then(status => ({ configured: status.botConfigured })))
    void check('channels', telegramUserRequest<TelegramChannelConfig>(value, 'channels', signal).then(config => ({ configured: config.account.hasSession && config.channels.length > 0 })))
    void check('gying', gyingRequest<GyingConfigView>(value, 'config', signal))
    void check('woniu', woniuRequest<WoniuConfigView>(value, 'config', signal))
  }, { immediate: true })
  return availability
}
