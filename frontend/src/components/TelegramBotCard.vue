<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { IconBrandTelegram, IconEdit, IconLock, IconPlus, IconRefresh, IconTrash } from '@tabler/icons-vue'
import TelegramModuleCard from './TelegramModuleCard.vue'
import type { TelegramBotConfig, TelegramBotModule } from '../types/telegramBots'

const props = defineProps<{ bot: TelegramBotConfig; modules: TelegramBotModule[]; busyModuleIds: Set<string>; draggingModuleId?: string; testing?: boolean; disabled?: boolean }>()
const emit = defineEmits<{
  edit: [bot: TelegramBotConfig]
  remove: [bot: TelegramBotConfig]
  test: [bot: TelegramBotConfig]
  dropModule: [moduleId: string, botId: string]
  unassignModule: [module: TelegramBotModule]
  dragStart: [moduleId: string]
  dragEnd: []
}>()
const dragOver = ref(false)
const highlighted = computed(() => dragOver.value && !!props.draggingModuleId && !props.disabled)
const status = computed(() => {
  if (!props.bot.configured) return { label: '未配置 Token', tone: 'warning' }
  if (!props.bot.enabled) return { label: '已停用', tone: 'neutral' }
  if (props.bot.lastError) return { label: '连接失败', tone: 'error' }
  if (props.bot.connected) return { label: '已连接', tone: 'success' }
  if (props.bot.running) return { label: '连接中', tone: 'info' }
  return { label: '已启用', tone: 'info' }
})
function enter(event: DragEvent) {
  if (!event.dataTransfer?.types.includes('application/x-cinema115-telegram-module') || props.disabled) return
  event.preventDefault()
  event.dataTransfer.dropEffect = 'move'
  dragOver.value = true
}
function leave(event: DragEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)) dragOver.value = false
}
function drop(event: DragEvent) {
  const moduleId = event.dataTransfer?.getData('application/x-cinema115-telegram-module')
  if (!moduleId || props.disabled) return
  event.preventDefault()
  dragOver.value = false
  emit('dropModule', moduleId, props.bot.id)
}
watch(() => props.draggingModuleId, () => { dragOver.value = false })
</script>

<template>
  <article class="telegram-bot-card min-w-0 overflow-hidden rounded-2xl border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900"
    :class="{ 'is-drop-target': highlighted }"
    @dragenter="enter" @dragover="enter" @dragleave="leave" @drop="drop">
    <div class="space-y-4 p-4 sm:p-5">
      <header class="flex min-w-0 flex-wrap items-start gap-3">
        <span class="bot-avatar grid h-11 w-11 shrink-0 place-items-center rounded-2xl bg-blue-600 text-white"><IconBrandTelegram :size="24" aria-hidden="true" /></span>
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-base font-semibold text-slate-900 dark:text-slate-100" :title="bot.name">{{ bot.name }}</h2>
          <p class="mt-1 truncate text-xs" :class="bot.identity?.username ? 'font-mono text-blue-600 dark:text-blue-400' : 'text-slate-500 dark:text-slate-400'">{{ bot.identity?.username ? '@' + bot.identity.username : '配置后验证 Bot 身份' }}</p>
        </div>
        <span class="bot-status inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-1 text-[11px] font-medium" :class="'status-' + status.tone"><span class="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true"></span>{{ status.label }}</span>
      </header>
      <div class="bot-token-row flex min-w-0 items-center gap-2 rounded-xl border border-zinc-200/70 bg-zinc-100/80 px-3 py-2.5 text-xs text-zinc-500 dark:border-zinc-700/60 dark:bg-zinc-950/50 dark:text-zinc-400">
        <span class="shrink-0 rounded bg-zinc-200/70 px-1.5 py-0.5 font-mono text-[10px] font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">TOKEN</span>
        <span class="min-w-0 flex-1 truncate">{{ bot.configured ? '已安全保存' : '尚未填写 Bot Token' }}</span>
        <IconLock v-if="bot.configured" :size="14" class="shrink-0 text-zinc-400" aria-label="Token 内容隐藏" />
        <button v-else type="button" class="shrink-0 rounded-md px-1.5 py-0.5 text-xs font-medium text-blue-600 hover:bg-blue-100/70 disabled:opacity-40 dark:text-blue-400 dark:hover:bg-blue-950/50" :disabled="disabled" @click="emit('edit', bot)">填写 Token</button>
      </div>
      <p v-if="bot.lastError" class="line-clamp-2 break-words rounded-lg bg-red-50 px-3 py-2 text-xs leading-5 text-red-700 dark:bg-red-950/30 dark:text-red-300" :title="bot.lastError">{{ bot.lastError }}</p>
      <section aria-label="已分配功能">
        <div class="mb-2 flex items-center justify-between gap-2">
          <h3 class="flex items-center gap-2 text-xs font-semibold text-slate-600 dark:text-slate-300">已分配功能<span class="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] tabular-nums dark:bg-slate-800">{{ modules.length }}</span></h3>
          <span v-if="highlighted" class="text-[11px] text-blue-700 dark:text-blue-300">松开放入此 Bot</span>
        </div>
        <div class="bot-module-zone relative min-h-24 rounded-2xl border-2 border-dashed border-zinc-200 bg-zinc-50/60 p-2.5 dark:border-zinc-800 dark:bg-zinc-950/40">
          <TransitionGroup name="bot-module" tag="div" class="relative space-y-2">
            <TelegramModuleCard v-for="module in modules" :key="module.id" :module="module" :bots="[]" :assigned-bot="bot" placement="assigned" :busy="disabled || busyModuleIds.has(module.id)" @unassign="value => emit('unassignModule', value)" @drag-start="emit('dragStart', $event)" @drag-end="emit('dragEnd')" />
          </TransitionGroup>
          <div v-if="!modules.length" class="grid min-h-16 place-content-center justify-items-center gap-1.5 px-2 py-2 text-center">
            <span class="grid h-7 w-7 place-items-center rounded-lg bg-slate-100 text-slate-400 dark:bg-slate-800 dark:text-slate-500"><IconPlus :size="15" aria-hidden="true" /></span>
            <p class="text-xs text-slate-500 dark:text-slate-400">{{ highlighted ? '松开放入此 Bot' : '拖入功能，或在右侧选择 Bot' }}</p>
          </div>
        </div>
      </section>
    </div>
    <footer class="flex flex-wrap items-center gap-2 border-t border-slate-100 bg-slate-50/80 px-4 py-3 dark:border-slate-800 dark:bg-slate-950/30 sm:px-5">
      <button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-full bg-slate-100 px-3 text-xs font-medium text-slate-700 hover:bg-blue-50 hover:text-blue-700 disabled:opacity-40 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-blue-950/60 dark:hover:text-blue-300" :disabled="disabled || testing || !bot.configured" :aria-label="'测试' + bot.name + '连接'" @click="emit('test', bot)"><IconRefresh :size="14" :class="{ 'motion-safe:animate-spin': testing }" aria-hidden="true" />{{ testing ? '测试中' : '测试连接' }}</button>
      <button type="button" class="inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-xs font-medium text-slate-600 hover:bg-slate-100 disabled:opacity-40 dark:text-slate-300 dark:hover:bg-slate-800" :disabled="disabled" @click="emit('edit', bot)"><IconEdit :size="14" aria-hidden="true" />编辑配置</button>
      <button type="button" class="ml-auto inline-flex h-8 items-center gap-1 rounded-lg px-2 text-xs text-slate-500 hover:bg-red-50 hover:text-red-700 disabled:opacity-40 dark:text-slate-400 dark:hover:bg-red-950/40 dark:hover:text-red-300" :disabled="disabled" :aria-label="'删除' + bot.name" @click="emit('remove', bot)"><IconTrash :size="14" aria-hidden="true" />删除</button>
    </footer>
  </article>
</template>

<style scoped>
.telegram-bot-card { width: 100%; max-width: 560px; justify-self: start; border-color: #e4e4e7; box-shadow: 0 2px 15px rgb(0 0 0 / .04), 0 0 1px rgb(0 0 0 / .1); }
:global(.dark) .telegram-bot-card { border-color: rgb(255 255 255 / .08); background: #18181b; box-shadow: inset 0 1px 0 rgb(255 255 255 / .04), 0 8px 24px rgb(0 0 0 / .15); }
.bot-avatar { box-shadow: 0 4px 10px rgb(37 99 235 / .16); }
.status-success { @apply border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300; }
.status-warning { @apply border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300; }
.status-error { @apply border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300; }
.status-info { @apply border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950/40 dark:text-blue-300; }
.status-neutral { @apply border-slate-200 bg-slate-100 text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300; }
.is-drop-target { border-color: rgb(59 130 246); box-shadow: 0 0 0 3px rgb(59 130 246 / .18), 0 8px 24px rgb(37 99 235 / .1); }
.is-drop-target .bot-module-zone { border-color: rgb(96 165 250); background: rgb(59 130 246 / .06); }
@media (prefers-reduced-motion: no-preference) {
  .telegram-bot-card { transition: border-color 180ms ease, box-shadow 180ms ease, transform 180ms cubic-bezier(.22,1,.36,1); }
  .telegram-bot-card:hover { box-shadow: 0 8px 24px rgb(15 23 42 / .06); }
  .telegram-bot-card.is-drop-target { transform: translateY(-2px); }
  .bot-module-zone, button { transition: background-color 150ms ease, transform 150ms ease; }
  button:not(:disabled):active { transform: scale(.97); }
  .bot-module-enter-active, .bot-module-move { transition: opacity 180ms ease, transform 180ms cubic-bezier(.22,1,.36,1); }
  .bot-module-enter-from { opacity: 0; transform: translateY(6px) scale(.98); }
}
</style>
