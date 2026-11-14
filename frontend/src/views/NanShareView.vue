<script setup lang="ts">
import '../styles/resourceSettings.css'
import SelectMenu from '../components/SelectMenu.vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { useToast } from '../composables/useToast'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const config = ref<NanShareConfigView>()
const baseUrl = ref('http://192.168.0.3:8115')
const username = ref('')
const password = ref('')
const busy = ref<'load' | 'save' | 'clear' | 'verify' | 'search' | ''>('load')
const searchKind = ref<'movie' | 'tv'>('tv')
const searchID = ref('106449')
const searchResult = ref<{ total: number; p115: number; kind: string; id: number }>()
const error = ref('')
const toast = useToast()
const confirmClear = ref(false)
const lifecycle = new AbortController()
const connectionDirty = computed(() => baseUrl.value.trim().replace(/\/$/, '') !== config.value?.baseUrl || username.value.trim() !== config.value?.username || !!password.value)

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
  busy.value = 'save'; error.value = ''
  try {
    config.value = await nanShareRequest<NanShareConfigView>(props.token, 'config', lifecycle.signal, 'POST', { baseUrl: baseUrl.value.trim(), username: username.value.trim(), password: password.value })
    baseUrl.value = config.value.baseUrl; username.value = config.value.username
    searchResult.value = undefined
    if (!lifecycle.signal.aborted) toast.success('NanShare 登录成功，会话已加密保存。下方显示各项配置的检查结果。')
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : 'RE0 登录失败' }
  finally { busy.value = ''; password.value = '' }
}

async function verify() {
  if (busy.value || connectionDirty.value) return
  busy.value = 'verify'; error.value = ''; searchResult.value = undefined
  try { config.value = await nanShareRequest<NanShareConfigView>(props.token, 'verify', lifecycle.signal, 'POST') }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '验证连接失败' }
  finally { busy.value = '' }
}

async function search() {
  if (busy.value || connectionDirty.value) return
  const id = Number(searchID.value)
  if (!/^[1-9]\d*$/.test(searchID.value) || !Number.isSafeInteger(id)) { error.value = '请输入有效的 TMDB 编号'; return }
  busy.value = 'search'; error.value = ''; searchResult.value = undefined
  const kind = searchKind.value
  try {
    const result = await nanShareRequest<{ total: number; p115: number }>(props.token, 'search-test', lifecycle.signal, 'POST', { kind, tmdbId: id })
    searchResult.value = { ...result, kind, id }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : 'RE0 搜索测试失败' }
  finally { busy.value = '' }
}

async function clear() {
  if (busy.value) return
  busy.value = 'clear'; error.value = ''
  try {
    config.value = await nanShareRequest<NanShareConfigView>(props.token, 'config', lifecycle.signal, 'DELETE')
    username.value = ''; password.value = ''; searchResult.value = undefined; confirmClear.value = false
    if (!lifecycle.signal.aborted) toast.success('NanShare 登录信息已清除。')
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '清除 RE0 配置失败' }
  finally { busy.value = '' }
}

onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); password.value = '' })
</script>

<template>
  <ManagementLayout title="RE0 配置" description="登录 NanShare，检查 RE0 搜索与分享 STRM 配置，并测试资源搜索。" content-class="resource-settings" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <div class="settings-stack">
      <p v-if="error" role="alert" class="settings-feedback settings-feedback-error">
        {{ error }}
        <button v-if="!config" type="button" class="ml-3 underline" @click="load">重新加载</button>
      </p>

      <!-- 卡片 1：NanShare 账号配置 -->
      <form id="nanshare-config-form" class="settings-card" @submit.prevent="save">
        <div class="settings-heading">
          <h2 class="settings-title">NanShare 账号</h2>
          <span class="settings-status" :class="config?.configured ? 'settings-status-success' : 'settings-status-neutral'">
            {{ config ? (config.configured ? '已保存会话' : '未配置') : (error ? '读取失败' : '读取中…') }}
          </span>
        </div>

        <fieldset :disabled="!!busy || !config" class="settings-fields">
          <label class="settings-label settings-field-full">
            NanShare 地址
            <input v-model="baseUrl" type="url" required autocomplete="url" spellcheck="false" class="settings-input" />
            <span class="settings-hint">例如 http://192.168.0.3:8115；地址只用于服务端连接。</span>
          </label>
          <label class="settings-label">
            用户名
            <input v-model="username" required maxlength="100" autocomplete="username" class="settings-input" />
          </label>
          <label class="settings-label">
            密码
            <input v-model="password" type="password" required maxlength="4096" autocomplete="current-password" class="settings-input" />
          </label>
        </fieldset>

        <p class="settings-hint">账号密码和会话在服务端加密保存。会话过期时自动登录一次，并重试搜索和配置读取。</p>

        <div class="settings-actions">
          <button type="submit" :disabled="!!busy || !config" class="settings-button settings-button-primary">
            {{ busy === 'save' ? '登录中…' : '保存并登录' }}
          </button>
        </div>

        <div v-if="config?.configured" class="settings-clear-row">
          <p class="settings-hint">
            {{ confirmClear ? '确定清除服务端保存的 RE0 会话与账号配置？' : '清除本项目的 NanShare 登录信息与会话，服务端账号本身不会被删除。' }}
          </p>
          <div class="settings-clear-actions">
            <button v-if="!confirmClear" type="button" :disabled="!!busy" class="settings-button settings-button-danger" @click="confirmClear = true">
              清除登录信息
            </button>
            <template v-else>
              <button type="button" :disabled="!!busy" class="settings-button settings-button-danger-solid" @click="clear">
                {{ busy === 'clear' ? '清除中…' : '确认清除' }}
              </button>
              <button type="button" :disabled="!!busy" class="settings-button settings-button-secondary" @click="confirmClear = false">
                取消
              </button>
            </template>
          </div>
        </div>
      </form>

      <!-- 卡片 2：已保存连接的检查结果 -->
      <section v-if="config?.configured" class="settings-card">
        <div class="settings-heading">
          <h2 class="settings-title">已保存连接的检查结果</h2>
          <button type="button" :disabled="!!busy || connectionDirty" class="settings-button settings-button-secondary" @click="verify">
            {{ busy === 'verify' ? '检查中…' : '重新检查' }}
          </button>
        </div>

        <p v-if="connectionDirty" class="settings-feedback settings-feedback-warning mt-3">
          当前账号表单存在未保存修改，请先保存并登录，再重新检查连接。
        </p>

        <div class="mt-4 grid gap-3 text-sm sm:grid-cols-3">
          <div
            v-for="item in [
              { label: '登录会话', value: config.session },
              { label: 'RE0 搜索配置', value: config.re0 },
              { label: '分享 STRM 配置', value: config.share },
            ]"
            :key="item.label"
            class="rounded-xl border border-slate-200/70 bg-slate-50 p-4 dark:border-slate-800 dark:bg-slate-950"
          >
            <h3 class="font-medium text-slate-800 dark:text-slate-200">{{ item.label }}</h3>
            <p class="mt-2 leading-6" :class="item.value.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'">
              {{ item.value.message || '尚未验证' }}
            </p>
          </div>
        </div>

        <p v-if="config.checkedAt" class="settings-hint">最近检查：{{ new Date(config.checkedAt).toLocaleString() }}</p>
        <p class="settings-hint">{{ config.autoLogin ? '已启用自动登录：密码已加密保存。' : '自动登录未启用：请用用户名和密码重新登录一次。' }}</p>
      </section>

      <!-- 卡片 3：测试 RE0 搜索 -->
      <section v-if="config?.configured" class="settings-card">
        <div class="settings-heading">
          <h2 class="settings-title">测试 RE0 搜索</h2>
        </div>
        <p class="settings-desc">输入影视的 TMDB 编号，检查是否能获取资源列表。仅搜索，不解锁资源或生成 STRM。</p>

        <p v-if="connectionDirty" class="settings-feedback settings-feedback-warning mt-3">
          当前账号表单存在未保存修改，测试搜索已禁用，请先保存并登录。
        </p>

        <form class="mt-4" @submit.prevent="search">
          <fieldset :disabled="!!busy || connectionDirty || !config.session.ok || !config.re0.ok" class="flex flex-wrap items-end gap-4 border-0 p-0">
            <label class="settings-label">
              影视类型
              <SelectMenu v-model="searchKind" label="影视类型" :options="[{ value: 'tv', label: '剧集' }, { value: 'movie', label: '电影' }]" />
            </label>
            <label class="settings-label">
              TMDB 编号
              <input v-model="searchID" required inputmode="numeric" pattern="[1-9][0-9]*" class="settings-input max-w-56" />
            </label>
            <button type="submit" class="settings-button settings-button-primary">
              {{ busy === 'search' ? '搜索中…' : '测试搜索' }}
            </button>
          </fieldset>
        </form>

        <p v-if="searchResult" role="status" class="settings-feedback settings-feedback-success mt-4">
          搜索调用成功：{{ searchResult.kind === 'tv' ? '剧集' : '电影' }} {{ searchResult.id }}，共 {{ searchResult.total }} 条资源，其中 115 资源 {{ searchResult.p115 }} 条。
        </p>
      </section>
    </div>
  </ManagementLayout>
</template>
