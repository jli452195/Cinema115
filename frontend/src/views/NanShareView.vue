<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const config = ref<NanShareConfigView>()
const baseUrl = ref('http://192.168.0.3:8115')
const username = ref('')
const password = ref('')
const sessionToken = ref('')
const loginMode = ref<'password' | 'token'>('password')
const busy = ref<'load' | 'save' | 'clear' | 'verify' | 'search' | ''>('load')
const searchKind = ref<'movie' | 'tv'>('tv')
const searchID = ref('106449')
const searchResult = ref<{ total: number; p115: number; kind: string; id: number }>()
const error = ref('')
const notice = ref('')
const confirmClear = ref(false)
const lifecycle = new AbortController()
const connectionDirty = computed(() => baseUrl.value.trim().replace(/\/$/, '') !== config.value?.baseUrl || username.value.trim() !== config.value?.username || !!password.value || !!sessionToken.value)
watch(loginMode, () => { password.value = ''; sessionToken.value = '' })

async function load() {
  busy.value = 'load'; error.value = ''
  try {
    config.value = await nanShareRequest<NanShareConfigView>(props.token, 'config', lifecycle.signal)
    baseUrl.value = config.value.baseUrl || baseUrl.value
    username.value = config.value.username
    if (config.value.configured) config.value = await nanShareRequest<NanShareConfigView>(props.token, 'verify', lifecycle.signal, 'POST')
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取 RE0 配置失败' }
  finally { busy.value = '' }
}

async function save() {
  if (busy.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''
  try {
    config.value = await nanShareRequest<NanShareConfigView>(props.token, 'config', lifecycle.signal, 'POST', { baseUrl: baseUrl.value.trim(), username: username.value.trim(), ...(loginMode.value === 'password' ? { password: password.value } : { token: sessionToken.value.trim() }) })
    baseUrl.value = config.value.baseUrl; username.value = config.value.username
    searchResult.value = undefined
    notice.value = 'NanShare 登录成功，会话已加密保存。下方显示各项配置的检查结果。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : 'RE0 登录失败' }
  finally { busy.value = ''; password.value = ''; sessionToken.value = '' }
}

async function verify() {
  if (busy.value || connectionDirty.value) return
  busy.value = 'verify'; error.value = ''; notice.value = ''; searchResult.value = undefined
  try { config.value = await nanShareRequest<NanShareConfigView>(props.token, 'verify', lifecycle.signal, 'POST') }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '验证连接失败' }
  finally { busy.value = '' }
}

async function search() {
  if (busy.value || connectionDirty.value) return
  const id = Number(searchID.value)
  if (!/^[1-9]\d*$/.test(searchID.value) || !Number.isSafeInteger(id)) { error.value = '请输入有效的 TMDB 编号'; return }
  busy.value = 'search'; error.value = ''; notice.value = ''; searchResult.value = undefined
  const kind = searchKind.value
  try {
    const result = await nanShareRequest<{ total: number; p115: number }>(props.token, 'search-test', lifecycle.signal, 'POST', { kind, tmdbId: id })
    searchResult.value = { ...result, kind, id }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : 'RE0 搜索测试失败' }
  finally { busy.value = '' }
}

async function clear() {
  if (busy.value) return
  busy.value = 'clear'; error.value = ''; notice.value = ''
  try {
    config.value = await nanShareRequest<NanShareConfigView>(props.token, 'config', lifecycle.signal, 'DELETE')
    username.value = ''; password.value = ''; sessionToken.value = ''; searchResult.value = undefined; confirmClear.value = false; notice.value = 'NanShare 登录信息已清除。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '清除 RE0 配置失败' }
  finally { busy.value = '' }
}

onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); password.value = ''; sessionToken.value = '' })
</script>

<template>
  <ManagementLayout title="RE0 资源搜索" description="登录 NanShare，检查 RE0 搜索与分享 STRM 配置，并测试资源搜索。" @logout="$emit('logout')">
    <template #actions><button type="submit" form="nanshare-config-form" :disabled="!!busy || !config" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">{{ busy === 'save' ? '登录中…' : '保存并登录' }}</button></template>
    <p v-if="error" role="alert" class="mb-5 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button v-if="!config" class="ml-3 underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="mb-5 rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700">{{ notice }}</p>
    <form id="nanshare-config-form" class="rounded-2xl border border-slate-200 bg-white p-5 sm:p-7" @submit.prevent="save">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900">NanShare 账号</h2><span class="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-600">{{ config?.configured ? '已保存会话' : '未配置' }}</span></div>
      <fieldset :disabled="!!busy || !config" class="mt-5 grid min-w-0 gap-5 sm:grid-cols-2">
        <label class="text-sm text-slate-700 sm:col-span-2">NanShare 地址<input v-model="baseUrl" type="url" required autocomplete="url" spellcheck="false" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/><span class="mt-2 block text-xs text-slate-500">例如 http://192.168.0.3:8115；地址只用于服务端连接。</span></label>
        <label class="text-sm text-slate-700 sm:col-span-2">登录方式<select v-model="loginMode" class="mt-2 block w-full rounded-xl border border-slate-200 bg-slate-50 p-3"><option value="password">用户名与密码</option><option value="token">已有会话令牌</option></select></label>
        <label class="text-sm text-slate-700">用户名<input v-model="username" :required="loginMode === 'password'" maxlength="100" autocomplete="username" :placeholder="loginMode === 'token' ? '可留空，从会话读取' : ''" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/></label>
        <label v-if="loginMode === 'password'" class="text-sm text-slate-700">密码<input v-model="password" type="password" required maxlength="4096" autocomplete="current-password" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/></label>
        <label v-else class="text-sm text-slate-700">会话令牌<input v-model="sessionToken" type="password" required maxlength="16384" autocomplete="off" spellcheck="false" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/></label>
      </fieldset>
      <p class="mt-5 text-xs leading-6 text-slate-500">使用 NanShare 的登录账号。密码仅用于本次登录；会话令牌在服务端加密保存，过期后需重新登录。</p>
    </form>
    <section v-if="config?.configured" class="mt-5 rounded-2xl border border-slate-200 bg-white p-5 sm:p-7">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900">已保存连接的检查结果</h2><button type="button" :disabled="!!busy || connectionDirty" class="text-sm text-blue-600 disabled:opacity-40" @click="verify">{{ busy === 'verify' ? '检查中…' : '重新检查' }}</button></div>
      <p v-if="connectionDirty" class="mt-3 text-xs text-amber-700">请先保存修改后的登录信息，再检查或搜索。</p>
      <div class="mt-4 grid gap-3 text-sm sm:grid-cols-3"><div v-for="item in [{ label: '登录会话', value: config.session }, { label: 'RE0 搜索配置', value: config.re0 }, { label: '分享 STRM 配置', value: config.share }]" :key="item.label" class="rounded-xl bg-slate-50 p-4"><h3 class="font-medium text-slate-800">{{ item.label }}</h3><p class="mt-2 leading-6" :class="item.value.ok ? 'text-emerald-700' : 'text-amber-700'">{{ item.value.message || '尚未验证' }}</p></div></div>
      <p v-if="config.checkedAt" class="mt-3 text-xs text-slate-400">最近检查：{{ new Date(config.checkedAt).toLocaleString() }}</p>
      <button v-if="!confirmClear" :disabled="!!busy" class="mt-6 text-sm text-red-600 disabled:opacity-40" @click="confirmClear = true">清除登录信息</button>
      <div v-else class="mt-5 flex flex-wrap items-center gap-3 rounded-xl bg-red-50 p-4 text-sm text-red-700"><span>确定清除服务端保存的 RE0 会话？</span><button :disabled="!!busy" class="underline" @click="clear">{{ busy === 'clear' ? '清除中…' : '确认清除' }}</button><button @click="confirmClear = false">取消</button></div>
    </section>
    <section v-if="config?.configured" class="mt-5 rounded-2xl border border-slate-200 bg-white p-5 sm:p-7">
      <h2 class="font-semibold text-slate-900">测试 RE0 搜索</h2><p class="mt-2 text-sm leading-6 text-slate-500">输入影视的 TMDB 编号，检查是否能获取资源列表。仅搜索，不解锁资源或生成 STRM。</p>
      <form class="mt-5" @submit.prevent="search"><fieldset :disabled="!!busy || connectionDirty || !config.session.ok || !config.re0.ok" class="flex flex-wrap items-end gap-4"><label class="text-sm text-slate-700">影视类型<select v-model="searchKind" class="mt-2 block rounded-lg border border-slate-200 bg-slate-50 p-3"><option value="tv">剧集</option><option value="movie">电影</option></select></label><label class="min-w-0 text-sm text-slate-700">TMDB 编号<input v-model="searchID" required inputmode="numeric" pattern="[1-9][0-9]*" class="mt-2 block w-full max-w-56 rounded-lg border border-slate-200 bg-slate-50 p-3"/></label><button type="submit" class="rounded-lg bg-blue-600 px-4 py-3 text-sm text-white disabled:opacity-40">{{ busy === 'search' ? '搜索中…' : '测试搜索' }}</button></fieldset></form>
      <p v-if="searchResult" role="status" class="mt-5 rounded-xl bg-emerald-50 p-4 text-sm leading-6 text-emerald-700">搜索调用成功：{{ searchResult.kind === 'tv' ? '剧集' : '电影' }} {{ searchResult.id }}，共 {{ searchResult.total }} 条资源，其中 115 资源 {{ searchResult.p115 }} 条。</p>
    </section>
  </ManagementLayout>
</template>
