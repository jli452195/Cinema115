<script setup lang="ts">
import { computed, ref } from 'vue'
import { IconBell, IconChevronDown, IconCloudDownload, IconGripVertical, IconHeartbeat, IconShare, IconUnlink } from '@tabler/icons-vue'
import type { TelegramBotConfig, TelegramBotModule } from '../types/telegramBots'

const props = defineProps<{ module: TelegramBotModule; bots: TelegramBotConfig[]; placement: 'pool' | 'assigned'; assignedBot?: TelegramBotConfig; busy?: boolean }>()
const emit = defineEmits<{
  assign: [module: TelegramBotModule, botId: string]
  unassign: [module: TelegramBotModule]
  dragStart: [moduleId: string]
  dragEnd: []
}>()
const choosing = ref(false)
const target = ref('')
const dragging = ref(false)
const availableBots = computed(() => props.bots)
const moduleKind = computed(() => props.module.kind || (props.module.id.startsWith('cloud_download:') ? 'cloud_download' : props.module.id))
const isCloudDownload = computed(() => moduleKind.value === 'cloud_download')
const icon = computed(() => ({ share_notifications: IconShare, cloud_download: IconCloudDownload, follow_notifications: IconBell, pool_notifications: IconHeartbeat }[moduleKind.value as 'share_notifications' | 'cloud_download' | 'follow_notifications' | 'pool_notifications']))
const setupHint = computed(() => {
  const bot = props.assignedBot
  if (!bot) return ''
  if (!bot.configured) return '待配置 Token'
  if (!bot.enabled) return 'Bot 已停用'
  if (isCloudDownload.value) {
    return props.module.setupHint || (props.module.ready === false ? '待配置转存目标' : '')
  } else if (!bot.notificationChatID) return '待配置通知聊天 ID'
  return ''
})

function dragStart(event: DragEvent) {
  if (props.busy || (event.target as HTMLElement).closest('button, input, select, label')) { event.preventDefault(); return }
  event.dataTransfer?.setData('application/x-cinema115-telegram-module', props.module.id)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
  dragging.value = true
  choosing.value = false
  emit('dragStart', props.module.id)
}
function dragEnd() { dragging.value = false; emit('dragEnd') }

function assign() {
  if (!target.value || props.busy) return
  emit('assign', props.module, target.value)
  choosing.value = false
  target.value = ''
}
</script>

<template>
  <article class="telegram-module-card min-w-0 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-700 dark:bg-slate-900"
    :class="[{ 'is-dragging': dragging }, placement === 'pool' ? 'module-pool shadow-sm' : 'module-assigned']"
    :draggable="!busy"
    @dragstart="dragStart" @dragend="dragEnd">
    <div v-if="placement === 'assigned'" class="flex min-w-0 items-center gap-2">
      <span class="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-blue-100 bg-blue-50 text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/60 dark:text-blue-300"><component :is="icon" :size="16" aria-hidden="true" /></span>
      <div class="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
        <h3 class="max-w-[10rem] shrink truncate text-sm font-semibold text-slate-800 dark:text-slate-100" :title="module.name">{{ module.name }}</h3>
        <span v-if="setupHint" class="shrink-0 rounded-full border border-amber-200 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300" :title="setupHint">待配置</span>
        <span v-else class="shrink-0 rounded-full border border-blue-200 bg-blue-50 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:border-blue-900 dark:bg-blue-950/50 dark:text-blue-300">{{ assignedBot?.connected ? '已连接' : assignedBot?.enabled ? '已启用' : '已停用' }}</span>
        <p class="min-w-0 flex-1 truncate text-xs text-slate-500 dark:text-slate-400" :title="module.description">{{ module.description }}</p>
      </div>
      <button type="button" class="inline-flex h-7 shrink-0 items-center gap-1 rounded-lg bg-slate-100 px-2 text-xs text-slate-600 hover:bg-red-50 hover:text-red-700 disabled:opacity-40 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-red-950/40 dark:hover:text-red-300" :disabled="busy" :aria-label="`解除${module.name}分配`" title="解除分配" @click="emit('unassign', module)"><IconUnlink :size="13" aria-hidden="true" />解除</button>
    </div>
    <div v-else class="flex min-w-0 items-start gap-3">
      <span class="mt-0.5 grid h-9 w-9 shrink-0 place-items-center rounded-xl border border-blue-100 bg-blue-50 text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/60 dark:text-blue-300"><component :is="icon" :size="18" aria-hidden="true" /></span>
      <div class="min-w-0 flex-1">
        <h3 class="min-w-0 text-sm font-semibold text-slate-800 dark:text-slate-100">{{ module.name }}</h3>
        <p class="mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400" :title="module.description">{{ module.description }}</p>
      </div>
      <IconGripVertical :size="16" class="mt-1 shrink-0 text-slate-400 dark:text-slate-500" aria-hidden="true" />
    </div>
    <div v-if="placement === 'pool'" class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-2.5 dark:border-slate-800">
      <span class="text-[11px] text-slate-500 dark:text-slate-400">待分配</span>
      <button v-if="!choosing" type="button" class="inline-flex items-center gap-1 rounded-full bg-blue-50 px-3 py-1.5 text-xs font-medium text-blue-700 hover:bg-blue-100 disabled:opacity-40 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60" :disabled="busy || !availableBots.length" @click="choosing = true">选择 Bot<IconChevronDown :size="13" aria-hidden="true" /></button>
      <div v-else class="flex min-w-0 flex-1 items-center justify-end gap-2">
        <select v-model="target" class="min-w-0 max-w-40 rounded-md border border-slate-200 bg-white px-2 py-1.5 text-xs text-slate-700 focus:border-blue-500 focus:outline-none dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200" :disabled="busy" :aria-label="`选择使用${module.name}的 Bot`">
          <option value="" disabled>选择 Bot</option>
          <option v-for="bot in availableBots" :key="bot.id" :value="bot.id">{{ bot.name }}{{ !bot.configured ? '（未配置 Token）' : !bot.enabled ? '（已停用）' : '' }}</option>
        </select>
        <button type="button" class="rounded-md bg-blue-600 px-2.5 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="busy || !target" @click="assign">分配</button>
        <button type="button" class="rounded-md px-2 py-1.5 text-xs text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800" :disabled="busy" @click="choosing = false">取消</button>
      </div>
    </div>
  </article>
</template>

<style scoped>
.telegram-module-card[draggable="true"] { cursor: grab; }
.telegram-module-card[draggable="true"]:active { cursor: grabbing; }
.telegram-module-card { overflow-wrap: anywhere; }
.module-pool:hover { border-color: rgb(147 197 253); }
@media (prefers-reduced-motion: no-preference) {
  .telegram-module-card { transition: transform 180ms cubic-bezier(.22,1,.36,1), border-color 180ms ease, box-shadow 180ms ease, opacity 180ms ease; }
  .module-pool:hover { transform: translateY(-1px); box-shadow: 0 4px 12px rgb(15 23 42 / .06); }
  .telegram-module-card.is-dragging { opacity: .35; transform: scale(.97); }
  button { transition: background-color 150ms ease, transform 150ms ease; }
  button:not(:disabled):active { transform: scale(.97); }
}
</style>
