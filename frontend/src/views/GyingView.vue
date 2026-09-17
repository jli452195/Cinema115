<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { gyingRequest, type GyingConfigView } from '../types/gying'

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
const lifecycle = new AbortController()

async function load() {
  busy.value = 'load'; error.value = ''
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'config', lifecycle.signal)
    baseUrl.value = config.value.baseUrl || 'https://www.xn--wcv59z.com'
    username.value = config.value.username
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取观影配置失败' }
  finally { busy.value = '' }
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
    <form id="gying-config-form" class="rounded-2xl border border-slate-200 bg-white p-5 sm:p-7" @submit.prevent="save">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900">站点与账号</h2><span class="rounded-full px-3 py-1 text-xs" :class="config?.configured ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'">{{ config?.configured ? '已配置' : '未配置' }}</span></div>
      <fieldset :disabled="!!busy || !config" class="mt-5 grid min-w-0 gap-5 sm:grid-cols-2">
        <label class="text-sm text-slate-700 sm:col-span-2">观影站地址<input v-model="baseUrl" type="url" required autocomplete="url" spellcheck="false" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/><span class="mt-2 block text-xs text-slate-500">只接受无路径、无端口的 HTTPS 公网域名。</span></label>
        <label class="text-sm text-slate-700">用户名<input v-model="username" required maxlength="100" autocomplete="username" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/></label>
        <label class="text-sm text-slate-700">密码<input v-model="password" type="password" :required="!config?.configured" autocomplete="current-password" :placeholder="config?.configured ? '留空保留已保存密码' : '输入观影站密码'" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500"/></label>
      </fieldset>
      <p class="mt-5 text-xs leading-6 text-slate-500">保存时会立即尝试登录并完成站点安全验证；验证成功后才替换原配置。密码和 Cookie 只在服务端加密保存，不会返回浏览器或写入日志。</p>
    </form>
    <section v-if="config?.configured" class="mt-5 rounded-2xl border border-slate-200 bg-white p-5 sm:p-7">
      <h2 class="font-semibold text-slate-900">清除登录信息</h2><p class="mt-3 text-sm leading-7 text-slate-500">清除后详情页将不能使用观影搜索，站点账号本身不会被删除或修改。</p>
      <button v-if="!confirmClear" :disabled="!!busy" class="mt-4 text-sm text-red-600 disabled:opacity-40" @click="confirmClear = true">清除观影配置</button>
      <div v-else class="mt-4 flex flex-wrap items-center gap-3 rounded-xl bg-red-50 p-4 text-sm text-red-700"><span>确定清除服务端保存的账号、密码和 Cookie？</span><button :disabled="!!busy" class="underline" @click="clear">{{ busy === 'clear' ? '清除中…' : '确认清除' }}</button><button @click="confirmClear = false">取消</button></div>
    </section>
  </ManagementLayout>
</template>
