<script setup lang="ts">
import '../styles/resourceSettings.css'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { useToast } from '../composables/useToast'
import { woniuRequest, type WoniuConfigView } from '../types/woniu'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const config = ref<WoniuConfigView>()
const username = ref('')
const password = ref('')
const busy = ref('load')
const error = ref('')
const toast = useToast()
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
  busy.value = 'save'; error.value = ''
  try {
    apply(await woniuRequest<WoniuConfigView>(props.token, 'config', lifecycle.signal, 'POST', { username: username.value.trim(), password: password.value }))
    password.value = ''
    if (!lifecycle.signal.aborted) toast.success('蜗牛账号登录成功，详情页搜索入口已开启。')
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '蜗牛登录失败' }
  finally { busy.value = '' }
}
async function clear() {
  if (busy.value) return
  busy.value = 'clear'; error.value = ''
  try {
    apply(await woniuRequest<WoniuConfigView>(props.token, 'config', lifecycle.signal, 'DELETE'))
    password.value = ''; confirmClear.value = false
    if (!lifecycle.signal.aborted) toast.success('本项目保存的蜗牛登录信息已清除。')
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '清除蜗牛配置失败' }
  finally { busy.value = '' }
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); password.value = '' })
</script>

<template>
  <ManagementLayout title="蜗牛配置" description="登录蜗牛 I 4K，用当前影片名称搜索网盘资源。" content-class="resource-settings" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <div class="settings-stack">
      <p v-if="error" role="alert" class="settings-feedback settings-feedback-error">
        {{ error }}
        <button v-if="!config" type="button" class="ml-3 underline" @click="load">重新加载</button>
      </p>

      <form id="woniu-config-form" class="settings-card" @submit.prevent="save">
        <div class="settings-heading">
          <h2 class="settings-title">站点与账号</h2>
          <span class="settings-status" :class="config?.configured ? 'settings-status-success' : 'settings-status-neutral'">
            {{ config ? (config.configured ? '已配置' : '未配置') : (error ? '读取失败' : '读取中…') }}
          </span>
        </div>
        <p class="settings-desc">https://www.wn4k.com · 游客可搜索影片，获取完整资源链接需先登录。</p>

        <fieldset :disabled="!!busy || !config" class="settings-fields">
          <label class="settings-label">
            用户名
            <input v-model="username" required maxlength="100" autocomplete="username" class="settings-input" />
          </label>
          <label class="settings-label">
            密码
            <input
              v-model="password"
              type="password"
              :required="!config?.configured || username.trim() !== config.username"
              maxlength="512"
              autocomplete="current-password"
              :placeholder="config?.configured ? '留空保留当前账号密码' : '输入蜗牛站密码'"
              class="settings-input"
            />
          </label>
        </fieldset>

        <p class="settings-hint">保存并登录成功后，电影和剧集详情页自动显示“蜗牛搜索”，仅管理员可见。密码和会话加密保存。</p>

        <div class="settings-actions">
          <button type="submit" :disabled="!!busy || !config" class="settings-button settings-button-primary">
            {{ busy === 'save' ? '登录中…' : '保存并登录' }}
          </button>
        </div>

        <div class="settings-clear-row">
          <p class="settings-hint">清除本项目的账号、会话和搜索缓存，蜗牛站账号本身不会被删除。</p>
          <div class="settings-clear-actions">
            <button v-if="!confirmClear" type="button" :disabled="!!busy || !config?.configured" class="settings-button settings-button-danger" @click="confirmClear = true">
              清除蜗牛配置
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
    </div>
  </ManagementLayout>
</template>
