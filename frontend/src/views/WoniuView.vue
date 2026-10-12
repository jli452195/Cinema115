<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { woniuRequest, type WoniuConfigView } from '../types/woniu'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const config = ref<WoniuConfigView>()
const username = ref('')
const password = ref('')
const busy = ref('load')
const error = ref('')
const notice = ref('')
const confirmClear = ref(false)
function apply(view: WoniuConfigView) { config.value = view; username.value = view.username }
async function load() {
  busy.value = 'load'; error.value = ''
  try { apply(await woniuRequest<WoniuConfigView>(props.token, 'config', lifecycle.signal)) }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取蜗牛配置失败' }
  finally { busy.value = '' }
}
async function save() {
  if (busy.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''
  try {
    apply(await woniuRequest<WoniuConfigView>(props.token, 'config', lifecycle.signal, 'POST', { username: username.value.trim(), password: password.value }))
    password.value = ''; notice.value = '蜗牛账号登录成功，详情页搜索入口已开启。'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '蜗牛登录失败' }
  finally { busy.value = '' }
}
async function clear() {
  if (busy.value) return
  busy.value = 'clear'; error.value = ''; notice.value = ''
  try {
    apply(await woniuRequest<WoniuConfigView>(props.token, 'config', lifecycle.signal, 'DELETE'))
    password.value = ''; confirmClear.value = false; notice.value = '本项目保存的蜗牛登录信息已清除。'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '清除蜗牛配置失败' }
  finally { busy.value = '' }
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); password.value = '' })
</script>

<template>
  <ManagementLayout title="蜗牛配置" description="登录蜗牛 I 4K，用当前影片名称搜索网盘资源。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <template #actions><button type="submit" form="woniu-config-form" :disabled="!!busy || !config" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">{{ busy === 'save' ? '登录中…' : '保存并登录' }}</button></template>
    <p v-if="error" role="alert" class="mb-5 rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}<button v-if="!config" class="ml-3 underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="mb-5 rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ notice }}</p>
    <form id="woniu-config-form" class="rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900 sm:p-7" @submit.prevent="save">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">站点与账号</h2><span class="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300">{{ config?.configured ? '已配置' : '未配置' }}</span></div>
      <p class="mt-3 text-sm text-slate-500 dark:text-slate-400">https://www.wn4k.com · 游客能搜索影片，完整资源链接需要登录。</p>
      <fieldset :disabled="!!busy || !config" class="mt-5 grid min-w-0 gap-5 sm:grid-cols-2">
        <label class="text-sm text-slate-700 dark:text-slate-300">用户名<input v-model="username" required maxlength="100" autocomplete="username" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950" /></label>
        <label class="text-sm text-slate-700 dark:text-slate-300">密码<input v-model="password" type="password" :required="!config?.configured || username.trim() !== config.username" maxlength="512" autocomplete="current-password" :placeholder="config?.configured ? '留空保留当前账号密码' : '输入蜗牛站密码'" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950" /></label>
      </fieldset>
      <p class="mt-4 text-xs leading-6 text-slate-500 dark:text-slate-400">保存并登录成功后，电影和剧集详情页自动显示“蜗牛搜索”，仅管理员可见。密码和会话加密保存。</p>
    </form>
    <section class="mt-6 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900 sm:p-7">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">清除登录信息</h2><p class="mt-3 text-sm leading-7 text-slate-500 dark:text-slate-400">清除本项目的账号、会话和搜索缓存，蜗牛站账号本身不会被删除。</p>
      <button v-if="!confirmClear" type="button" :disabled="!!busy || !config?.configured" class="mt-4 min-h-11 text-sm text-red-600 disabled:opacity-40 dark:text-red-400" @click="confirmClear = true">清除蜗牛配置</button>
      <div v-else class="mt-4 flex flex-wrap items-center gap-4"><button type="button" :disabled="!!busy" class="min-h-11 rounded-xl bg-red-600 px-4 text-sm text-white disabled:opacity-40" @click="clear">确认清除</button><button type="button" :disabled="!!busy" class="min-h-11 text-sm text-slate-600 dark:text-slate-300" @click="confirmClear = false">取消</button></div>
    </section>
  </ManagementLayout>
</template>
