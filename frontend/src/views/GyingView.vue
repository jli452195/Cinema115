<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
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
const draggedResourceType = ref('')
const lifecycle = new AbortController()
const resourceCatalog = computed(() => new Map((config.value?.resourceTypes || []).map(item => [item.type, item])))
const orderedResources = computed<GyingResourceType[]>(() => resourceOrder.value.flatMap(type => {
  const resourceType = resourceCatalog.value.get(type)
  return resourceType ? [resourceType] : []
}))
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
  resourceOrder.value = (config.value?.resourceTypes || []).map(item => item.type)
}

function moveResource(index: number, offset: number) {
  const target = index + offset
  if (target < 0 || target >= resourceOrder.value.length) return
  const next = [...resourceOrder.value]
  const [item] = next.splice(index, 1)
  next.splice(target, 0, item)
  resourceOrder.value = next
}

function moveResourceBefore(source: string, target: string) {
  if (!source || source === target) return
  const next = [...resourceOrder.value]
  const sourceIndex = next.indexOf(source)
  const targetIndex = next.indexOf(target)
  if (sourceIndex < 0 || targetIndex < 0) return
  const movingDown = sourceIndex < targetIndex
  const [item] = next.splice(sourceIndex, 1)
  const insertionIndex = next.indexOf(target) + (movingDown ? 1 : 0)
  next.splice(insertionIndex, 0, item)
  resourceOrder.value = next
}

function startResourceDrag(event: DragEvent, type: string) {
  draggedResourceType.value = type
  event.dataTransfer?.setData('text/plain', type)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function dropResource(event: DragEvent, target: string) {
  event.preventDefault()
  moveResourceBefore(draggedResourceType.value || event.dataTransfer?.getData('text/plain') || '', target)
  draggedResourceType.value = ''
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
  <ManagementLayout title="观影搜索" description="配置观影站账号，用影视原名搜索全部资源；没有有效资源时自动使用 IMDb 编号重试。" @logout="$emit('logout')">
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
    <section v-if="config" class="mt-5 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div><h2 class="font-semibold text-slate-900 dark:text-slate-100">资源优先级</h2><p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">拖动调整顺序，也可以使用右侧按钮。该顺序同时控制搜索弹窗的来源筛选和每个条目内的资源排列。</p></div>
        <span v-if="orderDirty" class="rounded-full bg-amber-50 px-3 py-1 text-xs text-amber-700">尚未保存</span>
      </div>
      <ol class="mt-5 grid gap-2 sm:grid-cols-2" aria-label="观影资源优先级">
        <li v-for="(resourceType, index) in orderedResources" :key="resourceType.type" class="flex items-center gap-3 rounded-xl border bg-slate-50 dark:bg-slate-950 px-3 py-2.5 transition" :class="draggedResourceType === resourceType.type ? 'border-blue-400 opacity-60' : 'border-slate-200 dark:border-slate-700'" @dragover.prevent @drop="dropResource($event, resourceType.type)">
          <span draggable="true" class="cursor-grab select-none rounded-lg px-2 py-1 text-lg leading-none text-slate-400 dark:text-slate-500 active:cursor-grabbing" :aria-label="`拖动${resourceType.label}`" title="拖动调整顺序" @dragstart="startResourceDrag($event, resourceType.type)" @dragend="draggedResourceType = ''">☰</span>
          <span class="min-w-0 flex-1 truncate text-sm font-medium text-slate-700 dark:text-slate-300"><span class="mr-2 text-xs tabular-nums text-slate-400 dark:text-slate-500">{{ index + 1 }}</span>{{ resourceType.label }}</span>
          <button type="button" :disabled="!!busy || index === 0" class="rounded-lg px-2 py-1 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-white dark:bg-slate-900 disabled:opacity-25" :aria-label="`${resourceType.label}上移`" @click="moveResource(index, -1)">↑</button>
          <button type="button" :disabled="!!busy || index === orderedResources.length - 1" class="rounded-lg px-2 py-1 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-white dark:bg-slate-900 disabled:opacity-25" :aria-label="`${resourceType.label}下移`" @click="moveResource(index, 1)">↓</button>
        </li>
      </ol>
      <div class="mt-5 flex flex-wrap items-center gap-3 border-t border-slate-100 dark:border-slate-800 pt-5">
        <button type="button" :disabled="!!busy || !orderDirty" class="rounded-lg bg-slate-900 px-4 py-2.5 text-sm text-white disabled:opacity-40" @click="saveResourceOrder">{{ busy === 'order' ? '保存中…' : '保存排序' }}</button>
        <button type="button" :disabled="!!busy" class="px-2 py-2 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 disabled:opacity-40" @click="resetResourceOrder">恢复默认排序</button>
        <span class="text-xs text-slate-400 dark:text-slate-500">“全部资源”固定在筛选首位，不参与排序。</span>
      </div>
    </section>
    <section v-if="config?.configured" class="mt-5 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">清除登录信息</h2><p class="mt-3 text-sm leading-7 text-slate-500 dark:text-slate-400 dark:text-slate-500">清除后详情页将不能使用观影搜索，站点账号本身不会被删除或修改。</p>
      <button v-if="!confirmClear" :disabled="!!busy" class="mt-4 text-sm text-red-600 disabled:opacity-40" @click="confirmClear = true">清除观影配置</button>
      <div v-else class="mt-4 flex flex-wrap items-center gap-3 rounded-xl bg-red-50 p-4 text-sm text-red-700"><span>确定清除服务端保存的账号、密码和 Cookie？</span><button :disabled="!!busy" class="underline" @click="clear">{{ busy === 'clear' ? '清除中…' : '确认清除' }}</button><button @click="confirmClear = false">取消</button></div>
    </section>
  </ManagementLayout>
</template>
