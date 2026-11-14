<script setup lang="ts">
import '../styles/resourceSettings.css'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { useToast } from '../composables/useToast'
import { subtitleRequest, type SubtitleConfig, type SubtitleGithubConfig } from '../utils/subtitleApi'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const toast = useToast()
const config = ref<SubtitleConfig>(), apiToken = ref(''), busy = ref('load'), error = ref(''), confirmClear = ref(false)
const githubConfig = ref<SubtitleGithubConfig>(), githubToken = ref(''), githubBusy = ref('load'), githubError = ref(''), confirmGithubClear = ref(false)
async function loadGithub() {
  githubBusy.value = 'load'; githubError.value = ''
  try { githubConfig.value = await subtitleRequest<SubtitleGithubConfig>(props.token, 'github/config', lifecycle.signal) }
  catch (reason) { if (!lifecycle.signal.aborted) githubError.value = reason instanceof Error ? reason.message : '读取 GitHub 配置失败' }
  finally { githubBusy.value = '' }
}
async function saveGithub(clear = false) {
  if (githubBusy.value || !githubConfig.value) return
  githubBusy.value = clear ? 'clear' : 'save'; githubError.value = ''
  try {
    githubConfig.value = await subtitleRequest<SubtitleGithubConfig>(props.token, 'github/config', lifecycle.signal, { method: clear ? 'DELETE' : 'POST', ...(!clear ? { body: JSON.stringify({ token: githubToken.value.trim() }) } : {}) })
    githubToken.value = ''; confirmGithubClear.value = false
    if (!lifecycle.signal.aborted) toast.success(clear ? 'GitHub Token 已清除，将使用匿名请求。' : 'GitHub Token 验证成功，字幕库将使用此 Token 请求。')
  } catch (reason) { if (!lifecycle.signal.aborted) githubError.value = reason instanceof Error ? reason.message : '保存 GitHub 配置失败' }
  finally { githubBusy.value = '' }
}
async function load() {
  busy.value = 'load'; error.value = ''
  try { config.value = await subtitleRequest<SubtitleConfig>(props.token, 'config', lifecycle.signal) }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取字幕配置失败' }
  finally { busy.value = '' }
}
async function save(clear = false) {
  if (busy.value || !config.value) return
  busy.value = clear ? 'clear' : 'save'; error.value = ''
  try {
    config.value = await subtitleRequest<SubtitleConfig>(props.token, 'assrt/config', lifecycle.signal, { method: clear ? 'DELETE' : 'POST', ...(!clear ? { body: JSON.stringify({ token: apiToken.value.trim() }) } : {}) })
    apiToken.value = ''; confirmClear.value = false
    if (!lifecycle.signal.aborted) toast.success(clear ? '射手网配置已清除。' : '射手网验证成功，查找字幕时将同时显示搜索结果。')
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存字幕配置失败' }
  finally { busy.value = '' }
}
onMounted(() => { void load(); void loadGithub() })
onBeforeUnmount(() => { lifecycle.abort(); apiToken.value = ''; githubToken.value = '' })
</script>

<template>
  <ManagementLayout title="字幕" description="管理字幕来源，在下载整理时统一查找和匹配字幕。" content-class="resource-settings" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <div class="settings-stack">
      <!-- 卡片 1：sub_share 字幕库（内置库可用与 GitHub 鉴权状态区分展示） -->
      <form class="settings-card" @submit.prevent="saveGithub()">
        <div class="settings-heading">
          <h2 class="settings-title">字幕库</h2>
          <div class="settings-status-group">
            <span class="settings-status settings-status-success">内置库可用</span>
            <span class="settings-status" :class="githubConfig?.configured ? 'settings-status-info' : 'settings-status-neutral'">
              {{ githubConfig ? (githubConfig.configured ? '已配置 Token' : '匿名访问') : (githubError ? '读取失败' : '读取中…') }}
            </span>
          </div>
        </div>
        <p class="settings-desc">使用 sub_share 动漫字幕库，按作品搜索并选择字幕版本。</p>

        <p v-if="githubError" role="alert" class="settings-feedback settings-feedback-error mt-4">
          {{ githubError }}
          <button v-if="!githubConfig" type="button" class="ml-3 underline" @click="loadGithub">重新加载</button>
        </p>

        <fieldset :disabled="!!githubBusy || !githubConfig" class="mt-5 min-w-0 border-0 p-0">
          <label for="github-token" class="settings-label">GitHub Token（可选）</label>
          <div class="settings-inline-row">
            <input
              id="github-token"
              v-model="githubToken"
              type="password"
              required
              maxlength="255"
              autocomplete="off"
              spellcheck="false"
              :placeholder="githubConfig?.configured ? '已保存，输入新 Token 可替换' : '输入 GitHub Personal Access Token'"
              class="settings-input"
            />
            <button type="submit" :disabled="!githubToken.trim() || !!githubBusy" class="settings-button settings-button-primary">
              {{ githubBusy === 'save' ? '验证中…' : '保存并验证' }}
            </button>
          </div>
        </fieldset>

        <p class="settings-hint">未配置时仍可匿名使用。配置 Token 可提高 GitHub API 请求额度；只需读取公开仓库，无需私有仓库或写入权限。Token 加密保存，不会回显。</p>

        <div class="settings-clear-row">
          <div class="settings-links">
            <a href="https://github.com/foxofice/sub_share" target="_blank" rel="noopener noreferrer" class="settings-link">查看字幕库</a>
            <a href="https://github.com/settings/personal-access-tokens" target="_blank" rel="noopener noreferrer" class="settings-link">创建 GitHub Token</a>
          </div>
          <div class="settings-clear-actions">
            <button v-if="!confirmGithubClear" type="button" :disabled="!!githubBusy || !githubConfig?.configured" class="settings-button settings-button-danger" @click="confirmGithubClear = true">
              清除配置
            </button>
            <template v-else>
              <button type="button" :disabled="!!githubBusy" class="settings-button settings-button-danger-solid" @click="saveGithub(true)">
                确认清除
              </button>
              <button type="button" :disabled="!!githubBusy" class="settings-button settings-button-secondary" @click="confirmGithubClear = false">
                取消
              </button>
            </template>
          </div>
        </div>
      </form>

      <!-- 卡片 2：射手网 Assrt 独立来源 -->
      <form class="settings-card" @submit.prevent="save()">
        <div class="settings-heading">
          <h2 class="settings-title">射手网 Assrt</h2>
          <span class="settings-status" :class="config?.assrt.configured ? 'settings-status-success' : 'settings-status-neutral'">
            {{ config ? (config.assrt.configured ? '已配置' : '未配置') : (error ? '读取失败' : '读取中…') }}
          </span>
        </div>
        <p class="settings-desc">
          配置后，与字幕库一起显示搜索结果。字幕服务由
          <a href="https://secure.assrt.net/" target="_blank" rel="noopener noreferrer" class="settings-link">assrt.net</a>
          提供。
        </p>

        <p v-if="error" role="alert" class="settings-feedback settings-feedback-error mt-4">
          {{ error }}
          <button v-if="!config" type="button" class="ml-3 underline" @click="load">重新加载</button>
        </p>

        <fieldset :disabled="!!busy || !config" class="mt-5 min-w-0 border-0 p-0">
          <label for="assrt-token" class="settings-label">API Token</label>
          <div class="settings-inline-row">
            <input
              id="assrt-token"
              v-model="apiToken"
              type="password"
              required
              pattern="[A-Za-z0-9]{32}"
              maxlength="32"
              autocomplete="off"
              spellcheck="false"
              :placeholder="config?.assrt.configured ? '已保存，输入新 Token 可替换' : '输入 32 位 API Token'"
              class="settings-input"
            />
            <button type="submit" :disabled="!apiToken.trim() || !!busy" class="settings-button settings-button-primary">
              {{ busy === 'save' ? '验证中…' : '保存并验证' }}
            </button>
          </div>
        </fieldset>

        <p class="settings-hint">在射手网注册登录后，从用户面板获取 Token。Token 加密保存，不会回显。初始配额为每分钟 20 次，搜索结果会缓存。</p>

        <div class="settings-clear-row">
          <div class="settings-links">
            <a href="https://secure.assrt.net/api/doc#usetoken" target="_blank" rel="noopener noreferrer" class="settings-link">获取 Token 说明</a>
          </div>
          <div class="settings-clear-actions">
            <button v-if="!confirmClear" type="button" :disabled="!!busy || !config?.assrt.configured" class="settings-button settings-button-danger" @click="confirmClear = true">
              清除配置
            </button>
            <template v-else>
              <button type="button" :disabled="!!busy" class="settings-button settings-button-danger-solid" @click="save(true)">
                确认清除
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
