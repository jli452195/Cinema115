<script setup lang="ts">
import { computed } from 'vue'
import {
  IconBell,
  IconCloudDownload,
  IconDownload,
  IconExternalLink,
  IconFile,
  IconGripVertical,
  IconHeartbeat,
  IconPlus,
  IconShare,
  IconUnlink,
} from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import type { TelegramBotConfig, TelegramBotModule } from '../types/telegramBots'

const props = defineProps<{
  module: TelegramBotModule
  bots: TelegramBotConfig[]
  placement: 'pool' | 'assigned'
  assignedBot?: TelegramBotConfig
  busy?: boolean
}>()

const emit = defineEmits<{
  assign: [module: TelegramBotModule, botId: string]
  unassign: [module: TelegramBotModule]
  createBot: []
  move: [module: TelegramBotModule, direction: 'up' | 'down']
}>()

const availableBots = computed(() => props.bots)

// 仅存在 1 个 Bot 时用于单步直分配
const singleBot = computed(() => (availableBots.value.length === 1 ? availableBots.value[0] : null))

// 识别功能类型（兼容动态前缀 ID）
const moduleKind = computed(
  () =>
    props.module.kind ||
    (['cloud_download', 'share_transfer', 'ed2k_strm'].find(kind => props.module.id.startsWith(kind + ':')) || props.module.id),
)

const isCloudDownload = computed(() => moduleKind.value === 'cloud_download')
const isShareTransfer = computed(() => moduleKind.value === 'share_transfer')
const isED2KSTRM = computed(() => moduleKind.value === 'ed2k_strm')

const icon = computed(
  () =>
    ({
      share_notifications: IconShare,
      share_transfer: IconDownload,
      cloud_download: IconCloudDownload,
      ed2k_strm: IconFile,
      follow_notifications: IconBell,
      pool_notifications: IconHeartbeat,
      anime_notifications: IconBell,
    })[
      moduleKind.value as
        | 'share_notifications'
        | 'share_transfer'
        | 'cloud_download'
        | 'ed2k_strm'
        | 'follow_notifications'
        | 'pool_notifications'
        | 'anime_notifications'
    ],
)

// 已分配卡片的配置就绪状态提示
const setupHint = computed(() => {
  const bot = props.assignedBot
  if (!bot) return ''
  if (!bot.configured) return '待配置 Token'
  if (!bot.enabled) return 'Bot 已停用'
  if (isCloudDownload.value) {
    return props.module.setupHint || (props.module.ready === false ? '待配置转存目标' : '')
  } else if (isShareTransfer.value || isED2KSTRM.value) {
    return props.module.setupHint || ''
  } else if (moduleKind.value === 'share_notifications' && props.module.setupHint) {
    return props.module.setupHint
  } else if (!bot.notificationChatID) return '待配置通知聊天 ID'
  return ''
})

// 多 Bot 下拉选项：直接展示名称、用户名与当前状态
const botSelectOptions = computed(() =>
  availableBots.value.map((bot) => {
    const parts = [bot.name]
    if (bot.identity?.username) parts.push(`@${bot.identity.username}`)
    if (!bot.configured) parts.push('未配置 Token')
    else if (!bot.enabled) parts.push('已停用')
    else if (bot.lastError) parts.push('连接失败')
    else if (bot.connected) parts.push('已连接')
    else parts.push('已启用')
    return {
      value: bot.id,
      label: parts.join(' · '),
    }
  }),
)

// 多 Bot 选单变更后立即触发分配
function handleSelectBot(value: string) {
  if (!value || props.busy) return
  emit('assign', props.module, value)
}

// busy 状态下阻止模板链接跳转（配合 aria-disabled 与 tabindex 保证可访问性）
function handleTemplateClick(event: MouseEvent) {
  if (props.busy) {
    event.preventDefault()
  }
}

// 键盘 Alt + 上下方向键排序支持
function handleDragHandleKeydown(event: KeyboardEvent) {
  if (!event.altKey || props.busy) return
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    emit('move', props.module, 'up')
  } else if (event.key === 'ArrowDown') {
    event.preventDefault()
    emit('move', props.module, 'down')
  }
}
</script>

<template>
  <article
    class="telegram-module-card min-w-0 rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-700 dark:bg-slate-900"
    :class="[placement === 'pool' ? 'module-pool shadow-sm' : 'module-assigned', { 'is-busy': busy }]"
  >
    <!-- 已分配形态：左侧图标+标题描述，右侧行内动作（模板链接 / 解除 / 拖拽手柄） -->
    <div v-if="placement === 'assigned'" class="flex min-w-0 flex-wrap items-center gap-2">
      <div class="flex min-w-[160px] flex-1 items-center gap-2">
        <span
          class="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-blue-100 bg-blue-50 text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/60 dark:text-blue-300"
        >
          <component :is="icon" :size="16" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1 space-y-1">
          <div class="flex min-w-0 items-start gap-2">
            <h3
              class="min-w-0 flex-1 line-clamp-2 break-words text-sm font-semibold leading-5 text-slate-800 dark:text-slate-100"
              :title="module.name"
            >
              {{ module.name }}
            </h3>
            <span
              v-if="setupHint"
              class="shrink-0 rounded-full border border-amber-200 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300"
              :title="setupHint"
            >
              待配置
            </span>
            <span
              v-else
              class="shrink-0 rounded-full border border-blue-200 bg-blue-50 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:border-blue-900 dark:bg-blue-950/50 dark:text-blue-300"
            >
              {{ assignedBot?.connected ? '已连接' : assignedBot?.enabled ? '已启用' : '已停用' }}
            </span>
          </div>
          <p class="min-w-0 truncate text-xs text-slate-500 dark:text-slate-400" :title="module.description">
            {{ module.description }}
          </p>
        </div>
      </div>

      <!-- 右侧操作区：极窄卡片下自动整体右对齐换行，不挤压左侧标题 -->
      <div class="ml-auto flex shrink-0 items-center gap-1.5">
        <RouterLink
          v-if="moduleKind === 'anime_notifications'"
          to="/anime/settings?tab=notifications"
          class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-lg px-2 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-950/50"
          :class="{ 'cursor-not-allowed opacity-40 hover:bg-transparent dark:hover:bg-transparent': busy }"
          :aria-disabled="busy ? 'true' : undefined"
          :tabindex="busy ? -1 : undefined"
          aria-label="编辑动漫通知模板"
          title="编辑通知模板"
          @click="handleTemplateClick"
        >
          <span>模板</span>
          <IconExternalLink :size="12" aria-hidden="true" />
        </RouterLink>

        <button
          type="button"
          class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-lg bg-slate-100 px-2 text-xs text-slate-600 hover:bg-red-50 hover:text-red-700 disabled:opacity-40 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-red-950/40 dark:hover:text-red-300"
          :disabled="busy"
          :aria-label="`解除${module.name}分配`"
          title="解除分配"
          @click="emit('unassign', module)"
        >
          <IconUnlink :size="13" aria-hidden="true" />
          解除
        </button>

        <button
          type="button"
          data-drag-handle
          class="drag-handle inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-slate-400 hover:bg-slate-100 hover:text-slate-600 disabled:cursor-not-allowed disabled:opacity-40 dark:text-slate-500 dark:hover:bg-slate-800 dark:hover:text-slate-300"
          :disabled="busy"
          :aria-label="`拖动${module.name}`"
          title="拖动排序，也可按 Alt + 上下方向键"
          @keydown="handleDragHandleKeydown"
        >
          <IconGripVertical :size="15" aria-hidden="true" />
        </button>
      </div>
    </div>

    <!-- 待分配池形态：顶部信息 + 行内模板入口与拖拽手柄 -->
    <div v-else class="flex min-w-0 items-start gap-3">
      <span
        class="mt-0.5 grid h-9 w-9 shrink-0 place-items-center rounded-xl border border-blue-100 bg-blue-50 text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/60 dark:text-blue-300"
      >
        <component :is="icon" :size="18" aria-hidden="true" />
      </span>
      <div class="min-w-0 flex-1">
        <h3 class="min-w-0 break-words text-sm font-semibold text-slate-800 dark:text-slate-100">
          {{ module.name }}
        </h3>
        <p class="mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400" :title="module.description">
          {{ module.description }}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-1">
        <RouterLink
          v-if="moduleKind === 'anime_notifications'"
          to="/anime/settings?tab=notifications"
          class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-lg px-2 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-950/50"
          :class="{ 'cursor-not-allowed opacity-40 hover:bg-transparent dark:hover:bg-transparent': busy }"
          :aria-disabled="busy ? 'true' : undefined"
          :tabindex="busy ? -1 : undefined"
          aria-label="编辑动漫通知模板"
          title="编辑通知模板"
          @click="handleTemplateClick"
        >
          <span>模板</span>
          <IconExternalLink :size="12" aria-hidden="true" />
        </RouterLink>

        <button
          type="button"
          data-drag-handle
          class="drag-handle inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-slate-400 hover:bg-slate-100 hover:text-slate-600 disabled:cursor-not-allowed disabled:opacity-40 dark:text-slate-500 dark:hover:bg-slate-800 dark:hover:text-slate-300"
          :disabled="busy"
          :aria-label="`拖动${module.name}`"
          title="拖动排序，也可按 Alt + 上下方向键"
          @keydown="handleDragHandleKeydown"
        >
          <IconGripVertical :size="16" aria-hidden="true" />
        </button>
      </div>
    </div>

    <!-- 待分配池底部操作栏：按 0 / 1 / 多 Bot 数量一步直达 -->
    <div
      v-if="placement === 'pool'"
      class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-2.5 dark:border-slate-800"
    >
      <span class="text-[11px] text-slate-500 dark:text-slate-400">待分配</span>

      <!-- 0 个 Bot：直接提供创建入口 -->
      <button
        v-if="!availableBots.length"
        type="button"
        class="inline-flex items-center gap-1 rounded-full bg-blue-50 px-3 py-1.5 text-xs font-medium text-blue-700 hover:bg-blue-100 disabled:opacity-40 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60"
        :disabled="busy"
        @click="emit('createBot')"
      >
        <IconPlus :size="13" aria-hidden="true" />
        新增 Bot
      </button>

      <!-- 仅 1 个 Bot：显示状态提示并一键直分配 -->
      <div v-else-if="singleBot" class="flex min-w-0 items-center gap-1.5">
        <span
          v-if="!singleBot.configured || !singleBot.enabled"
          class="shrink-0 rounded-full border border-amber-200 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300"
        >
          {{ !singleBot.configured ? '未配置 Token' : '已停用' }}
        </span>
        <button
          type="button"
          class="inline-flex max-w-48 items-center gap-1 rounded-full bg-blue-50 px-3 py-1.5 text-xs font-medium text-blue-700 hover:bg-blue-100 disabled:opacity-40 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60"
          :disabled="busy"
          :title="`分配给 ${singleBot.name}`"
          @click="emit('assign', module, singleBot.id)"
        >
          <span class="truncate">分配给 {{ singleBot.name }}</span>
        </button>
      </div>

      <!-- 2 个及以上 Bot：常驻紧凑 SelectMenu，选中即分配 -->
      <div v-else class="min-w-0 max-w-48 flex-1 sm:flex-initial">
        <SelectMenu
          model-value=""
          :options="botSelectOptions"
          density="compact"
          :menu-min-width="220"
          :disabled="busy"
          :label="`选择使用${module.name}的 Bot`"
          placeholder="分配至 Bot…"
          @change="handleSelectBot"
        />
      </div>
    </div>
  </article>
</template>

<style scoped>
.telegram-module-card {
  width: 100%;
  overflow-wrap: anywhere;
  user-select: none;
  -webkit-touch-callout: none;
  touch-action: pan-y;
}
.telegram-module-card:not(.is-busy) {
  cursor: grab;
}
.telegram-module-card:not(.is-busy):active {
  cursor: grabbing;
}
.telegram-module-card.is-busy {
  cursor: progress;
  opacity: .72;
}
.telegram-module-card :is(button:not([data-drag-handle]), a, [role='combobox']):not(:disabled):not([aria-disabled='true']) {
  cursor: pointer;
}
.drag-handle:focus-visible {
  outline: 2px solid rgb(59 130 246 / .5);
  outline-offset: 2px;
}
.drag-handle:not(:disabled) {
  cursor: grab;
}
.drag-handle:not(:disabled):active {
  cursor: grabbing;
}
.module-pool:hover {
  border-color: rgb(147 197 253);
}
@media (prefers-reduced-motion: no-preference) {
  .telegram-module-card {
    transition:
      transform 180ms cubic-bezier(0.22, 1, 0.36, 1),
      border-color 180ms ease,
      box-shadow 180ms ease,
      opacity 180ms ease;
  }
  .module-pool:hover {
    transform: translateY(-1px);
    box-shadow: 0 4px 12px rgb(15 23 42 / 0.06);
  }
  button {
    transition:
      background-color 150ms ease,
      transform 150ms ease;
  }
  button:not(:disabled):active {
    transform: scale(0.97);
  }
}
</style>
