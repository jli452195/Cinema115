<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowLeft, IconArrowRight } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import SortableList from '../components/SortableList.vue'
import { gyingRequest, type GyingConfigView, type GyingResourceType } from '../types/gying'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const config = ref<GyingConfigView>()
const baseUrl = ref('https://www.xn--wcv59z.com')
const username = ref('')
const password = ref('')
const busy = ref('load')
const error = ref('')
const notice = ref('')
const confirmClear = ref(false)
const resourceOrder = ref<string[]>([])
const savedResourceOrder = ref<string[]>([])
const selectedResourceType = ref('')
const lifecycle = new AbortController()
const resourceCatalog = computed(() => new Map((config.value?.resourceTypes || []).map(item => [item.type, item])))
const orderedResources = computed<GyingResourceType[]>({
  get: () => resourceOrder.value.flatMap(type => {
    const resourceType = resourceCatalog.value.get(type)
    return resourceType ? [resourceType] : []
  }),
  set: items => {
    if (busy.value) return
    resourceOrder.value = items.map(item => item.type)
    notice.value = ''
  },
})
const selectedResourceIndex = computed(() => resourceOrder.value.indexOf(selectedResourceType.value))
const orderDirty = computed(() => resourceOrder.value.join('\u0000') !== savedResourceOrder.value.join('\u0000'))

function applySavedResourceOrder(view: GyingConfigView) {
  resourceOrder.value = [...view.resourceOrder]
  savedResourceOrder.value = [...view.resourceOrder]
}

async function load() {
  busy.value = 'load'; error.value = ''
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'config', lifecycle.signal)
    baseUrl.value = config.value.baseUrl || 'https://www.xn--wcv59z.com'
    username.value = config.value.username
    applySavedResourceOrder(config.value)
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取观影配置失败' }
  finally { busy.value = '' }
}

async function saveResourceOrder() {
  if (busy.value || !orderDirty.value) return
  busy.value = 'order'; error.value = ''; notice.value = ''
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'resource-order', lifecycle.signal, 'PUT', { types: resourceOrder.value })
    applySavedResourceOrder(config.value)
    notice.value = '资源优先级已保存，新的观影搜索弹窗会立即使用该顺序。'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存资源优先级失败' }
  finally { busy.value = '' }
}

function resetResourceOrder() {
  orderedResources.value = config.value?.resourceTypes || []
}

function moveResource(index: number, offset: number) {
  const target = index + offset
  if (busy.value || index < 0 || target < 0 || target >= orderedResources.value.length) return
  const next = [...orderedResources.value]
  const [item] = next.splice(index, 1)
  next.splice(target, 0, item)
  orderedResources.value = next
}

async function resourceKey(event: KeyboardEvent, index: number) {
  if (!event.altKey || !['ArrowLeft', 'ArrowRight'].includes(event.key)) return
  event.preventDefault()
  const button = event.currentTarget as HTMLButtonElement
  selectedResourceType.value = resourceOrder.value[index] || ''
  moveResource(index, event.key === 'ArrowLeft' ? -1 : 1)
  await nextTick()
  button.focus()
}

async function save() {
  if (busy.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'config', lifecycle.signal, 'POST', { baseUrl: baseUrl.value.trim(), username: username.value.trim(), password: password.value })
    password.value = ''
    notice.value = '观影账号登录成功，配置和会话已加密保存。'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '观影账号登录失败' }
  finally { busy.value = '' }
}

async function clear() {
  if (busy.value) return
  busy.value = 'clear'; error.value = ''; notice.value = ''
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'config', lifecycle.signal, 'DELETE')
    username.value = ''; password.value = ''; confirmClear.value = false
    notice.value = '观影账号和本项目保存的登录会话已清除。'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '清除观影配置失败' }
  finally { busy.value = '' }
}

onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); password.value = '' })
</script>

<template>
  <ManagementLayout title="观影配置" description="配置观影站账号，用影视原名搜索全部资源；没有有效资源时自动使用 IMDb 编号重试。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <template #actions><button type="submit" form="gying-config-form" :disabled="!!busy || !config" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">{{ busy === 'save' ? '登录中…' : '保存并登录' }}</button></template>
    <p v-if="error" role="alert" class="mb-5 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button v-if="!config" class="ml-3 underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="mb-5 rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700">{{ notice }}</p>
    <form id="gying-config-form" class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7" @submit.prevent="save">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">站点与账号</h2><span class="rounded-full px-3 py-1 text-xs" :class="config?.configured ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 dark:text-slate-500'">{{ config?.configured ? '已配置' : '未配置' }}</span></div>
      <fieldset :disabled="!!busy || !config" class="mt-5 grid min-w-0 gap-5 sm:grid-cols-2">
        <label class="text-sm text-slate-700 dark:text-slate-300 sm:col-span-2">观影站地址<input v-model="baseUrl" type="url" required autocomplete="url" spellcheck="false" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/><span class="mt-2 block text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">只接受无路径、无端口的 HTTPS 公网域名。</span></label>
        <label class="text-sm text-slate-700 dark:text-slate-300">用户名<input v-model="username" required maxlength="100" autocomplete="username" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label>
        <label class="text-sm text-slate-700 dark:text-slate-300">密码<input v-model="password" type="password" :required="!config?.configured" autocomplete="current-password" :placeholder="config?.configured ? '留空保留已保存密码' : '输入观影站密码'" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label>
      </fieldset>
      <p class="mt-5 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">保存时会立即尝试登录并完成站点安全验证；验证成功后才替换原配置。密码和 Cookie 只在服务端加密保存，不会返回浏览器或写入日志。</p>
    </form>
    <section v-if="config" class="gy-order mt-5 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7" aria-labelledby="gy-order-title">
      <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h2 id="gy-order-title" class="font-semibold text-slate-900 dark:text-slate-100">资源优先级<span v-if="orderDirty" class="ml-3 text-xs font-normal text-slate-500 dark:text-slate-400">尚未保存</span></h2>
        <div class="gy-order-moves" role="group" aria-label="调整选中资源的位置">
          <button type="button" :disabled="!!busy || selectedResourceIndex <= 0" aria-label="前移选中资源" aria-controls="gy-order-list" @click="moveResource(selectedResourceIndex, -1)"><IconArrowLeft :size="15" aria-hidden="true" />前移</button>
          <button type="button" :disabled="!!busy || selectedResourceIndex < 0 || selectedResourceIndex >= orderedResources.length - 1" aria-label="后移选中资源" aria-controls="gy-order-list" @click="moveResource(selectedResourceIndex, 1)">后移<IconArrowRight :size="15" aria-hidden="true" /></button>
        </div>
      </div>
      <p id="gy-order-hint" class="mt-2 text-xs leading-5 text-slate-500 dark:text-slate-400">从左到右，越靠前越优先；同时用于来源筛选和搜索结果排序。</p>
      <SortableList id="gy-order-list" v-model="orderedResources" class="gy-order-list mt-4" item-class="!rounded-full" :item-key="item => item.type" :disabled="!!busy" direction="horizontal" role="group" aria-label="观影资源优先级" aria-describedby="gy-order-help" @start="item => selectedResourceType = item.type">
        <template #item="{ item, index }">
          <button type="button" class="gy-order-chip" data-drag-handle :data-resource-type="item.type" :class="{ selected: selectedResourceType === item.type }" :disabled="!!busy" :aria-label="`${item.label}，优先级 ${index + 1}`" :aria-pressed="selectedResourceType === item.type" @click="selectedResourceType = item.type" @keydown="resourceKey($event, index)"><span class="gy-order-number" aria-hidden="true">{{ index + 1 }}</span>{{ item.label }}</button>
        </template>
      </SortableList>
      <p id="gy-order-help" class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">拖动排序，点击后可用按钮微调；手机长按拖动。<span class="sr-only">选中资源后也可按 Alt 和左右方向键调整。</span></p>
      <div class="mt-4 flex flex-wrap items-center gap-2">
        <button type="button" :disabled="!!busy || !orderDirty" class="gy-order-save rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" @click="saveResourceOrder">{{ busy === 'order' ? '保存中…' : '保存排序' }}</button>
        <button type="button" :disabled="!!busy" class="gy-order-reset rounded-lg px-3 py-2 text-sm text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800 disabled:opacity-40" @click="resetResourceOrder">恢复默认排序</button>
        <span class="text-xs leading-5 text-slate-500 dark:text-slate-400 sm:ml-1">“全部资源”固定在筛选首位。</span>
      </div>
    </section>
    <section v-if="config?.configured" class="mt-5 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">清除登录信息</h2><p class="mt-3 text-sm leading-7 text-slate-500 dark:text-slate-400 dark:text-slate-500">清除后详情页将不能使用观影搜索，站点账号本身不会被删除或修改。</p>
      <button v-if="!confirmClear" :disabled="!!busy" class="mt-4 text-sm text-red-600 disabled:opacity-40" @click="confirmClear = true">清除观影配置</button>
      <div v-else class="mt-4 flex flex-wrap items-center gap-3 rounded-xl bg-red-50 p-4 text-sm text-red-700"><span>确定清除服务端保存的账号、密码和 Cookie？</span><button :disabled="!!busy" class="underline" @click="clear">{{ busy === 'clear' ? '清除中…' : '确认清除' }}</button><button @click="confirmClear = false">取消</button></div>
    </section>
  </ManagementLayout>
</template>

<style scoped>
.gy-order-list { gap: 8px; }
.gy-order-chip { display: inline-flex; align-items: center; gap: 7px; min-height: 36px; max-width: 100%; padding: 6px 13px 6px 10px; border: 1px solid transparent; border-radius: 999px; background: rgb(var(--color-slate-100)); color: rgb(var(--color-slate-700)); font-size: 13px; font-weight: 500; white-space: nowrap; transition: background-color .15s, color .15s, border-color .15s; }
.gy-order-chip.selected { color: #1d4ed8; background: rgb(59 130 246 / .09); border-color: rgb(59 130 246 / .3); }
.gy-order-number { width: 2ch; flex-shrink: 0; text-align: center; font-size: 11px; font-weight: 400; font-variant-numeric: tabular-nums; color: rgb(var(--color-slate-500)); }
.gy-order-moves { display: flex; gap: 4px; }
.gy-order-moves button { display: inline-flex; align-items: center; justify-content: center; gap: 4px; min-height: 34px; padding: 5px 8px; border-radius: 8px; font-size: 12px; color: rgb(var(--color-slate-600)); }
.gy-order button:disabled { cursor: default; }.gy-order-moves button:disabled { opacity: .35; }
.gy-order button:focus-visible { outline: 2px solid #3b82f6; outline-offset: 2px; }
.dark .gy-order-chip { background: rgb(var(--color-slate-800)); color: rgb(var(--color-slate-200)); }.dark .gy-order-chip.selected { color: #93c5fd; background: rgb(59 130 246 / .13); border-color: rgb(96 165 250 / .4); }.dark .gy-order-number { color: rgb(var(--color-slate-400)); }.dark .gy-order-moves button { color: rgb(var(--color-slate-400)); }
@media(hover: hover) and (pointer: fine) { .gy-order-chip:not(:disabled):not(.selected):hover { background: rgb(var(--color-slate-200) / .7); color: rgb(var(--color-slate-800)); }.gy-order-moves button:not(:disabled):hover { background: rgb(var(--color-slate-100)); }.dark .gy-order-chip:not(:disabled):not(.selected):hover { background: rgb(var(--color-slate-700)); color: rgb(var(--color-slate-100)); }.dark .gy-order-moves button:not(:disabled):hover { background: rgb(var(--color-slate-800)); color: rgb(var(--color-slate-200)); } }
@media(max-width: 639px) { .gy-order-chip, .gy-order-moves button, .gy-order-save, .gy-order-reset { min-height: 42px; } }
@media(prefers-reduced-motion: reduce) { .gy-order-chip { transition: none; } }
</style>
