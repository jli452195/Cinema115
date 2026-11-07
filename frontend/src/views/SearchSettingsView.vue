<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import {
  IconAlertCircle,
  IconArrowDown,
  IconArrowUp,
  IconDeviceFloppy,
  IconGripVertical,
  IconRestore,
} from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import SortableList from '../components/SortableList.vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import DetailSearchButtons from '../components/DetailSearchButtons.vue'
import { useSearchSettingsEditor } from '../composables/useSearchSettingsEditor'
import { useDetailSearchAvailability } from '../composables/useDetailSearchAvailability'
import { isPortalAdmin } from '../composables/usePortalAuth'
import {
  detailSearchSources,
  visibleDetailSearchSources,
  type DetailSearchSource,
  type DetailSearchSourceID,
} from '../utils/detailSearchSources'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()

const root = ref<HTMLElement>()
const announcement = ref('')
const {
  order,
  loaded,
  busy,
  dirty,
  error,
  notice,
  load,
  save,
  reset,
} = useSearchSettingsEditor(() => props.token)

const availability = useDetailSearchAvailability(
  () => props.token,
  () => isPortalAdmin.value,
)

const orderedSources = computed<DetailSearchSource[]>({
  get: () => {
    const map = new Map(detailSearchSources.map(source => [source.id, source]))
    return order.value.map(id => map.get(id)!).filter(Boolean)
  },
  set: items => {
    if (busy.value || !loaded.value) return
    order.value = items.map(item => item.id)
  },
})

function sourceStatus(source: DetailSearchSource): {
  label: string
  type: 'anime' | 'configured' | 'unconfigured' | 'pending'
} {
  if (source.animationOnly) {
    return { label: '仅动画', type: 'anime' }
  }
  const state = availability.value[source.id]
  if (state === true) {
    return { label: '已配置', type: 'configured' }
  }
  if (state === false) {
    return { label: '未配置', type: 'unconfigured' }
  }
  return { label: '状态未确认', type: 'pending' }
}

function move(id: DetailSearchSourceID, delta: number, target: 'handle' | 'up' | 'down' = 'handle') {
  const index = order.value.indexOf(id)
  const nextIndex = index + delta
  if (busy.value || !loaded.value || index < 0 || nextIndex < 0 || nextIndex >= order.value.length) return

  const next = [...order.value]
  const [removed] = next.splice(index, 1)
  next.splice(nextIndex, 0, removed)
  order.value = next

  const source = detailSearchSources.find(s => s.id === id)
  announcement.value = `${source?.name || id}已移动到第 ${nextIndex + 1} 位`

  void nextTick(() => {
    const row = root.value?.querySelector<HTMLElement>(`[data-source-id="${id}"]`)
    if (!row) return
    if (target === 'up') {
      const upBtn = row.querySelector<HTMLButtonElement>('button[data-action="move-up"]')
      if (upBtn && !upBtn.disabled) upBtn.focus({ preventScroll: true })
      else row.querySelector<HTMLButtonElement>('button[data-action="move-down"]')?.focus({ preventScroll: true })
    } else if (target === 'down') {
      const downBtn = row.querySelector<HTMLButtonElement>('button[data-action="move-down"]')
      if (downBtn && !downBtn.disabled) downBtn.focus({ preventScroll: true })
      else row.querySelector<HTMLButtonElement>('button[data-action="move-up"]')?.focus({ preventScroll: true })
    } else {
      row.querySelector<HTMLButtonElement>('button[data-action="handle"]')?.focus({ preventScroll: true })
    }
  })
}

// 预览分段切换
const previewKind = ref<'movie' | 'anime'>('movie')
const previewOptions = [
  { value: 'movie', label: '普通影视' },
  { value: 'anime', label: '动画作品' },
] as const

const previewSources = computed(() => {
  return visibleDetailSearchSources(order.value, availability.value, previewKind.value === 'anime')
})

// 离开拦截
const leaveDialog = ref(false)
let leaveDecision: ((value: boolean) => void) | undefined

function decideLeave(value: boolean) {
  leaveDialog.value = false
  leaveDecision?.(value)
  leaveDecision = undefined
}

function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || busy.value && loaded.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}

onBeforeRouteLeave(() => {
  decideLeave(false)
  if (busy.value && loaded.value) {
    error.value = '正在处理保存请求，请稍候。'
    return false
  }
  if (!dirty.value) return true
  leaveDialog.value = true
  return new Promise<boolean>(resolve => {
    leaveDecision = resolve
  })
})

onMounted(() => {
  window.addEventListener('beforeunload', beforeUnload)
})

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', beforeUnload)
  decideLeave(false)
})
</script>

<template>
  <ManagementLayout
    class="ss-layout"
    title="搜索设置"
    description="调整详情页搜索按钮的排列顺序，并实时预览不同类型影视的展示效果。"
    :parent="{ label: '资源配置', to: '/resources' }"
    @logout="$emit('logout')"
  >
    <p v-if="error" role="alert" class="ss-feedback ss-error mb-4">
      {{ error }}
      <button v-if="!loaded" type="button" :disabled="busy" class="underline ml-2" @click="load">重新加载</button>
    </p>
    <p v-if="notice" role="status" class="ss-feedback ss-note mb-4">
      {{ notice }}
    </p>
    <p v-if="busy && !loaded" role="status" class="mb-4 text-sm text-content-secondary">
      正在读取搜索设置…
    </p>

    <div ref="root" class="ss-page">
      <div class="ss-grid">
        <!-- 左侧：来源排序卡片 -->
        <section
          class="ss-panel rounded-xl border border-line-default bg-surface-card p-4 shadow-sm sm:p-6"
          aria-labelledby="ss-order-title"
        >
          <div class="ss-panel-header mb-4">
            <h2 id="ss-order-title" class="text-base font-semibold text-content-primary">搜索来源顺序</h2>
            <p class="text-xs text-content-secondary mt-1">
              拖动行或使用右侧箭头/Alt+上下键调整顺序。只调整详情页按钮显示顺序，后台自动追更策略独立。
            </p>
          </div>

          <SortableList
            v-model="orderedSources"
            :item-key="item => item.id"
            direction="vertical"
            class="ss-list"
            :disabled="busy || !loaded"
          >
            <template #item="{ item, index }">
              <div
                class="ss-row"
                :data-source-id="item.id"
                data-drag-handle
              >
                <button
                  type="button"
                  class="ss-row-main"
                  data-drag-handle
                  data-action="handle"
                  :disabled="busy || !loaded"
                  :aria-label="`${item.name}，当前排在第 ${index + 1} 位，可按 Alt+上下键调整顺序`"
                  @keydown.alt.up.prevent="move(item.id, -1, 'handle')"
                  @keydown.alt.down.prevent="move(item.id, 1, 'handle')"
                >
                  <span class="ss-grip" aria-hidden="true">
                    <IconGripVertical :size="16" />
                  </span>
                  <span class="ss-index font-mono" aria-hidden="true">
                    {{ String(index + 1).padStart(2, '0') }}
                  </span>
                  <span class="ss-logo-wrap" aria-hidden="true">
                    <img
                      :src="item.logo"
                      alt=""
                      class="h-5 w-5 object-contain"
                      :class="{ 'rounded-[3px]': item.id === 're0' }"
                      draggable="false"
                    />
                  </span>
                  <span class="ss-name">{{ item.name }}</span>
                  <span class="ss-tag" :class="`is-${sourceStatus(item).type}`">
                    {{ sourceStatus(item).label }}
                  </span>
                </button>

                <div class="ss-row-actions" data-drag-disabled="true" @click.stop>
                  <button
                    type="button"
                    class="ss-icon-btn"
                    data-action="move-up"
                    :disabled="busy || !loaded || index === 0"
                    :aria-label="`上移 ${item.name}`"
                    @click="move(item.id, -1, 'up')"
                    @keydown.alt.up.prevent="move(item.id, -1, 'up')"
                    @keydown.alt.down.prevent="move(item.id, 1, 'up')"
                  >
                    <IconArrowUp :size="15" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    class="ss-icon-btn"
                    data-action="move-down"
                    :disabled="busy || !loaded || index === orderedSources.length - 1"
                    :aria-label="`下移 ${item.name}`"
                    @click="move(item.id, 1, 'down')"
                    @keydown.alt.up.prevent="move(item.id, -1, 'down')"
                    @keydown.alt.down.prevent="move(item.id, 1, 'down')"
                  >
                    <IconArrowDown :size="15" aria-hidden="true" />
                  </button>
                </div>
              </div>
            </template>
            <template #empty>
              <div class="ss-list-empty text-xs text-content-secondary py-4 text-center">暂无搜索来源配置</div>
            </template>
          </SortableList>

          <footer class="ss-panel-footer mt-5 pt-4 border-t border-line-default flex flex-wrap items-center justify-between gap-3">
            <button
              type="button"
              class="ss-button"
              :disabled="busy || !loaded"
              @click="reset"
            >
              <IconRestore :size="15" aria-hidden="true" />
              <span>恢复默认</span>
            </button>

            <div class="flex items-center gap-3 ml-auto">
              <span class="ss-save-state text-xs text-content-secondary">
                {{ dirty ? '有未保存的排序修改' : loaded ? '当前顺序已保存' : '' }}
              </span>
              <button
                type="button"
                class="ss-button ss-primary"
                :disabled="busy || !loaded || !dirty"
                @click="save"
              >
                <IconDeviceFloppy :size="15" aria-hidden="true" />
                <span>{{ busy && loaded ? '保存中…' : '保存排序' }}</span>
              </button>
            </div>
          </footer>
        </section>

        <!-- 右侧：实时预览卡片 -->
        <section
          class="ss-panel rounded-xl border border-line-default bg-surface-card p-4 shadow-sm sm:p-6"
          aria-labelledby="ss-preview-title"
        >
          <div class="ss-panel-header mb-4 flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 id="ss-preview-title" class="text-base font-semibold text-content-primary">实时预览</h2>
              <p class="text-xs text-content-secondary mt-1">按实际配置与影片类型过滤后的详情页展示效果</p>
            </div>
            <SegmentedControl
              v-model="previewKind"
              density="compact"
              label="预览类型"
              :options="previewOptions"
            />
          </div>

          <div class="ss-preview-stage rounded-lg border border-line-default bg-surface-page p-4">
            <div class="ss-preview-meta mb-3 flex items-center justify-between text-xs text-content-secondary">
              <span class="font-medium text-content-primary">
                {{ previewKind === 'anime' ? '动画作品' : '普通影视' }}
              </span>
              <span>可用来源 {{ previewSources.length }} 项</span>
            </div>

            <div class="ss-preview-body min-h-[56px] flex items-center">
              <DetailSearchButtons
                v-if="previewSources.length"
                :sources="previewSources"
                :interactive="false"
              />
              <div v-else class="ss-preview-empty flex items-center gap-2 text-xs text-content-secondary">
                <IconAlertCircle :size="16" class="text-amber-500 shrink-0" aria-hidden="true" />
                <span>当前类型下没有可用搜索来源，请先在资源配置中配置对应服务。</span>
              </div>
            </div>
          </div>

          <p class="mt-3 text-xs text-content-secondary leading-relaxed">
            仅动画来源（如 Mikan、Garden）在动画作品详情页显示；常规来源需在各外部服务中完成配置才会在详情页展示。
          </p>
        </section>
      </div>

      <p class="ss-boundary mt-4 text-xs text-content-secondary leading-relaxed">
        此处的排序设置仅调整影视与动漫详情页中各搜索胶囊按钮的展示顺序。后台自动追更、订阅下载及资源检索策略保持独立执行，不受此排序影响。
      </p>

      <p class="sr-only" role="status" aria-live="polite">{{ announcement }}</p>
    </div>

    <!-- 离开未保存确认弹窗 -->
    <P115ActionDialog
      v-if="leaveDialog"
      title="放弃未保存的搜索顺序？"
      @close="decideLeave(false)"
    >
      <p>离开后将丢弃当前未保存的排序，已保存的顺序不会改变。</p>
      <template #footer>
        <button type="button" class="ss-button" @click="decideLeave(false)">继续编辑</button>
        <button type="button" class="ss-button ss-primary" @click="decideLeave(true)">放弃修改并离开</button>
      </template>
    </P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.ss-page {
  color: rgb(var(--content-primary));
}
.ss-panel { min-width: 0; align-self: start; }

.ss-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 16px;
}

@media (min-width: 1024px) {
  .ss-grid {
    grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
    gap: 20px;
  }
}

.ss-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.ss-list :deep(.drag-list-item) {
  width: 100%;
  display: block;
}

.ss-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid rgb(var(--line-default));
  background: rgb(var(--surface-page));
  transition: background-color 150ms, border-color 150ms;
}

.ss-row:hover {
  background: rgb(var(--surface-hover));
}

.ss-row-main {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  text-align: left;
  background: transparent;
  border: 0;
  padding: 0;
  cursor: grab;
  user-select: none;
  touch-action: manipulation;
}

.ss-row-main:disabled {
  cursor: not-allowed;
}

.ss-row-main:focus-visible {
  outline: 2px solid rgb(var(--focus));
  outline-offset: 2px;
  border-radius: 6px;
}

.ss-grip {
  color: rgb(var(--content-secondary));
  opacity: 0.6;
  display: flex;
  align-items: center;
}

.ss-row:hover .ss-grip {
  opacity: 1;
}

.ss-index {
  font-size: 12px;
  color: rgb(var(--action-primary));
  min-width: 18px;
}

.ss-logo-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
}

.ss-name {
  font-size: 14px;
  font-weight: 500;
  color: rgb(var(--content-primary));
  white-space: nowrap;
}

.ss-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 7px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 500;
  line-height: 1.4;
  white-space: nowrap;
}

.ss-tag.is-anime {
  background: rgb(var(--selection-surface));
  color: rgb(var(--selection-content));
  border: 1px solid rgb(var(--action-primary) / 0.25);
}

.ss-tag.is-configured {
  background: rgb(var(--status-success-surface));
  color: rgb(var(--status-success));
  border: 1px solid rgb(var(--status-success) / 0.25);
}

.ss-tag.is-unconfigured {
  background: rgb(245 158 11 / 0.12);
  color: rgb(217 119 6);
  border: 1px solid rgb(245 158 11 / 0.25);
}

.ss-tag.is-pending {
  background: rgb(100 116 139 / 0.12);
  color: rgb(100 116 139);
  border: 1px solid rgb(100 116 139 / 0.25);
}

.dark .ss-tag.is-anime {
  background: rgb(var(--selection-surface));
  color: rgb(var(--selection-content));
  border-color: rgb(var(--action-primary) / 0.3);
}

.dark .ss-tag.is-configured {
  background: rgb(var(--status-success-surface));
  color: rgb(var(--status-success));
  border-color: rgb(var(--status-success) / 0.3);
}

.dark .ss-tag.is-unconfigured {
  background: rgb(245 158 11 / 0.16);
  color: rgb(251 191 36);
  border-color: rgb(245 158 11 / 0.3);
}

.dark .ss-tag.is-pending {
  background: rgb(148 163 184 / 0.16);
  color: rgb(148 163 184);
  border-color: rgb(148 163 184 / 0.3);
}

.ss-row-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: 8px;
}

.ss-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 6px;
  border: 1px solid rgb(var(--line-control));
  background: rgb(var(--surface-card));
  color: rgb(var(--content-secondary));
  cursor: pointer;
  transition: color 120ms, background-color 120ms, border-color 120ms;
}

.ss-icon-btn:hover:not(:disabled) {
  color: rgb(var(--content-primary));
  background: rgb(var(--surface-hover));
  border-color: rgb(var(--line-default));
}

.ss-icon-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.ss-icon-btn:focus-visible {
  outline: 2px solid rgb(var(--focus));
  outline-offset: 2px;
}

.ss-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 36px;
  padding: 6px 14px;
  border-radius: 8px;
  border: 1px solid rgb(var(--line-control));
  background: rgb(var(--surface-card));
  color: rgb(var(--content-primary));
  font-size: 13px;
  font-weight: 500;
  transition: background-color 150ms, border-color 150ms;
}

.ss-primary {
  border-color: rgb(var(--action-primary));
  background: rgb(var(--action-primary));
  color: rgb(var(--action-on-primary));
}

.ss-button:hover:not(:disabled) {
  background: rgb(var(--surface-hover));
}

.ss-primary:hover:not(:disabled) {
  background: rgb(var(--action-primary-hover));
  border-color: rgb(var(--action-primary-hover));
}

.ss-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ss-button:focus-visible {
  outline: 2px solid rgb(var(--focus));
  outline-offset: 2px;
}

.ss-feedback {
  border-radius: 8px;
  padding: 12px 16px;
  font-size: 13px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}

.ss-error {
  color: rgb(var(--status-danger));
  background: rgb(var(--status-danger-surface));
}

.ss-note {
  color: rgb(var(--content-secondary));
  background: rgb(var(--surface-page));
}

.ss-preview-stage {
  min-height: 104px;
}

@media (max-width: 639px) {
  .ss-row { padding: 8px; }
  .ss-row-main { gap: 6px; }
  .ss-row-actions { gap: 2px; margin-left: 4px; }
  .ss-tag { padding: 2px 5px; }
  .ss-button {
    min-height: 42px;
  }
  .ss-icon-btn {
    width: 40px;
    height: 40px;
  }
}
@media (max-width: 359px) {
  .ss-grip { display: none; }
  .ss-row-main { flex-wrap: wrap; }
  .ss-tag { margin-left: auto; }
}
@media (prefers-reduced-motion: reduce) {
  .ss-row, .ss-button, .ss-icon-btn { transition: none; }
}
</style>
