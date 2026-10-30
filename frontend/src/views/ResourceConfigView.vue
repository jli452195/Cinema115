<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive } from 'vue'
import { IconArrowsShuffle, IconSubtitles } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import NavigationCard from '../components/NavigationCard.vue'
import telegramIcon from '../assets/telegram/telegram.svg'
import gyingLogo from '../assets/gying/logo-gying.png'
import re0Logo from '../assets/re0/logo-re0-square.webp'
import { gyingRequest, type GyingConfigView } from '../types/gying'
import { woniuRequest, type WoniuConfigView } from '../types/woniu'
import woniuLogo from '../assets/woniu.svg'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'
import { telegramUserRequest, type TelegramUserStatus, type TelegramChannelConfig } from '../types/telegramUser'
import { resourceSources as sources } from '../utils/managementFeatures'
import { subtitleRequest, type SubtitleConfig } from '../utils/subtitleApi'
import { networkProxyRequest, type NetworkProxyConfig } from '../lib/network-proxy-api'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
type Source = typeof sources[number]['id']
type Summary = { label: string; tone: 'neutral' | 'success' | 'warning'; loading: boolean }
const initial = (): Summary => ({ label: '读取中', tone: 'neutral', loading: true })
const summaries = reactive<Record<Source, Summary>>({ gying: initial(), woniu: initial(), re0: initial(), telegram: initial(), telegramChannels: initial(), subtitles: initial(), network: initial() })
const lifecycle = new AbortController()
const toneClasses = {
  neutral: 'text-slate-500 dark:text-slate-400',
  success: 'text-emerald-600 dark:text-emerald-400',
  warning: 'text-amber-600 dark:text-amber-400',
}

// Only read saved configuration; opening this page never logs in or starts a resource search.
async function load(source: Source) {
  summaries[source] = initial()
  const signal = AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])
  try {
    let summary: Omit<Summary, 'loading'>
    if (source === 'gying') {
      const config = await gyingRequest<GyingConfigView>(props.token, 'config', signal)
      summary = config.configured
        ? { label: '已配置', tone: 'success' }
        : { label: '未配置', tone: 'neutral' }
    } else if (source === 'woniu') {
      const config = await woniuRequest<WoniuConfigView>(props.token, 'config', signal)
      summary = config.configured ? { label: '已配置', tone: 'success' } : { label: '未配置', tone: 'neutral' }
    } else if (source === 're0') {
      const config = await nanShareRequest<NanShareConfigView>(props.token, 'config', signal)
      summary = config.configured
        ? { label: '已配置', tone: 'success' }
        : { label: '未配置', tone: 'neutral' }
    } else if (source === 'telegramChannels') {
      const config = await telegramUserRequest<TelegramChannelConfig>(props.token, 'channels', signal)
      summary = config.channels.length ? { label: `已选择 ${config.channels.length} 个频道`, tone: 'success' } : { label: '未选择频道', tone: 'neutral' }
    } else if (source === 'subtitles') {
      const config = await subtitleRequest<SubtitleConfig>(props.token, 'config', signal)
      summary = { label: config.assrt.configured ? '字幕库可用，射手网已配置' : '字幕库可用', tone: 'success' }
    } else if (source === 'network') {
      const config = await networkProxyRequest<NetworkProxyConfig>(props.token, signal)
      summary = { label: config.enabled ? `已启用 ${config.services.length} 项服务` : '未启用', tone: config.enabled ? 'success' : 'neutral' }
    } else {
      const status = await telegramUserRequest<TelegramUserStatus>(props.token, 'config', signal)
      summary = {
        label: !status.configured ? '未配置' : status.authorized ? '已登录' : status.hasSession ? '会话已保存' : '待登录',
        tone: !status.configured ? 'neutral' : status.authorized ? 'success' : 'warning',
      }
    }
    if (!lifecycle.signal.aborted) summaries[source] = { ...summary, loading: false }
  } catch {
    if (!lifecycle.signal.aborted) summaries[source] = { label: '状态读取失败', tone: 'warning', loading: false }
  }
}

onMounted(() => { void Promise.allSettled(sources.map(source => load(source.id))) })
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="资源配置" description="管理资源来源的账号、连接与搜索设置。" @logout="$emit('logout')">
    <div class="grid items-start gap-3 sm:gap-4 md:grid-cols-2">
      <NavigationCard v-for="source in sources" :key="source.id" :to="source.settings" :title="source.title" :description="source.description" :aria-describedby="`source-status-${source.id}`" class="resource-card">
        <template #icon>
          <img v-if="source.id === 'telegram' || source.id === 'telegramChannels'" :src="telegramIcon" alt="" class="h-6 w-6 object-contain" draggable="false" />
          <img v-else-if="source.id === 'woniu'" :src="woniuLogo" alt="" class="h-8 w-8 object-contain" draggable="false" />
          <img v-else-if="source.id === 'gying'" :src="gyingLogo" alt="" class="h-8 w-8 object-contain" draggable="false" />
          <img v-else-if="source.id === 're0'" :src="re0Logo" alt="" class="h-8 w-8 object-contain" draggable="false" />
          <IconSubtitles v-else-if="source.id === 'subtitles'" :size="28" aria-hidden="true" />
          <IconArrowsShuffle v-else-if="source.id === 'network'" :size="28" aria-hidden="true" />
        </template>
        <template #trailing>
          <span role="status" :aria-busy="summaries[source.id].loading" :title="summaries[source.id].label" class="inline-flex h-6 w-4 shrink-0 items-center justify-end" :class="toneClasses[summaries[source.id].tone]">
            <svg aria-hidden="true" class="h-2 w-2" :class="{ 'resource-status-success': summaries[source.id].tone === 'success' }" viewBox="0 0 8 8" fill="currentColor"><circle cx="4" cy="4" r="4" /></svg>
            <span :id="`source-status-${source.id}`" class="sr-only">{{ summaries[source.id].label }}</span>
          </span>
        </template>
      </NavigationCard>
    </div>
  </ManagementLayout>
</template>

<style scoped>
.resource-status-success {
  --status-glow-color: 16 185 129;
  filter: drop-shadow(0 0 3px rgb(var(--status-glow-color) / 0.45));
  transform-origin: center;
}

:global(.dark .resource-status-success) {
  --status-glow-color: 52 211 153;
}

@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .resource-card:hover .resource-status-success {
    animation: resource-status-glow 1050ms ease-in-out;
  }
}

@keyframes resource-status-glow {
  0%, 100% {
    filter: drop-shadow(0 0 3px rgb(var(--status-glow-color) / 0.45));
    transform: scale(1);
  }
  30%, 40% {
    filter: drop-shadow(0 0 8px rgb(var(--status-glow-color) / 0.8));
    transform: scale(1.15);
  }
}
</style>
