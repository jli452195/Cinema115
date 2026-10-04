<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive } from 'vue'
import { RouterLink } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import { gyingRequest, type GyingConfigView } from '../types/gying'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'
import { telegramUserRequest, type TelegramUserStatus } from '../types/telegramUser'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
type Source = 'gying' | 're0' | 'telegram'
type Summary = { label: string; bot?: string; tone: 'neutral' | 'success' | 'warning'; loading: boolean }
const sources: { id: Source; title: string; description: string; settings: string }[] = [
  { id: 'gying', title: '观影配置', description: '管理观影站地址、账号登录与资源优先级。', settings: '/gying' },
  { id: 're0', title: 'RE0 配置', description: '管理 NanShare 连接，检查 RE0 搜索与分享 STRM 配置。', settings: '/nanshare' },
  { id: 'telegram', title: 'Telegram 搜索配置', description: '配置 Telegram API、用户账号登录与资源 Bot。', settings: '/telegram/user' },
]
const initial = (): Summary => ({ label: '读取中', tone: 'neutral', loading: true })
const summaries = reactive<Record<Source, Summary>>({ gying: initial(), re0: initial(), telegram: initial() })
const lifecycle = new AbortController()
const toneClasses = {
  neutral: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300',
  success: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
  warning: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
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
    } else if (source === 're0') {
      const config = await nanShareRequest<NanShareConfigView>(props.token, 'config', signal)
      summary = config.configured
        ? { label: '已配置', tone: 'success' }
        : { label: '未配置', tone: 'neutral' }
    } else {
      const status = await telegramUserRequest<TelegramUserStatus>(props.token, 'config', signal)
      summary = {
        label: !status.configured ? '未配置' : status.authorized ? '已登录' : status.hasSession ? '会话已保存' : '待登录',
        bot: status.configured ? status.bot.replace(/^@/, '') : '',
        tone: status.authorized ? 'success' : status.configured ? 'warning' : 'neutral',
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
    <div class="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
      <RouterLink v-for="source in sources" :key="source.id" :to="source.settings" :aria-labelledby="`source-${source.id}`" class="group flex min-w-0 flex-col rounded-2xl border border-slate-200 bg-white p-6 shadow-sm transition-all duration-300 hover:-translate-y-1 hover:border-slate-300 hover:shadow-lg focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-amber-400 dark:border-slate-700 dark:bg-slate-900 dark:hover:border-slate-600">
        <div class="mb-5 flex items-center justify-between gap-3">
          <span class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400">
            <svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <template v-if="source.id === 'gying'"><rect x="3" y="4" width="18" height="16" rx="3" /><path d="m10 8 6 4-6 4V8Z" /></template>
              <template v-else-if="source.id === 're0'"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4.5 4.5M8 10.5h5m-2.5-2.5v5" /></template>
              <path v-else d="m21 3-5 18-5-7-8-3L21 3Zm-10 11 5-6" />
            </svg>
          </span>
          <span role="status" :aria-busy="summaries[source.id].loading" class="rounded-full px-3 py-1 text-xs" :class="toneClasses[summaries[source.id].tone]">{{ summaries[source.id].label }}</span>
        </div>
        <h2 :id="`source-${source.id}`" class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ source.title }}</h2>
        <div class="mb-6 mt-2 flex-1 text-sm leading-6 text-slate-500 dark:text-slate-400">
          <p>{{ source.description }}</p>
          <p v-if="summaries[source.id].bot" class="mt-2 break-words text-xs">资源 Bot：@{{ summaries[source.id].bot }}</p>
        </div>
        <span class="flex items-center justify-between border-t border-slate-100 pt-4 text-xs font-medium text-slate-500 group-hover:text-blue-600 dark:border-slate-800 dark:text-slate-400 dark:group-hover:text-blue-400">
          进入设置
          <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6" /></svg>
        </span>
      </RouterLink>
    </div>
  </ManagementLayout>
</template>
