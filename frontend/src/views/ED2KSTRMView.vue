<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  IconAlertTriangle,
  IconCheck,
  IconCloud,
  IconDatabase,
  IconFile,
  IconFolder,
  IconLink,
  IconLoader2,
  IconPlayerPlay,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconSettings,
  IconTrash,
} from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import ED2KSTRMTargetCards from '../components/ED2KSTRMTargetCards.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'
import STRMContentPreview from '../components/STRMContentPreview.vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import SelectMenu from '../components/SelectMenu.vue'
import { useED2KSTRM } from '../composables/useED2KSTRM'
import type { ED2KSTRMResource } from '../types/ed2kSTRM'
import {
  ed2kCacheStatus,
  ed2kDate,
  ed2kResourceActive,
  ed2kResourceAttention,
  ed2kResourceKey,
  ed2kResourceStatus,
  ed2kSize,
} from '../utils/ed2kSTRM'
import { strmDirectoryPath } from '../utils/strmDirectory'

// 页签类型定义：默认 generate（生成 STRM）、resources（资源缓存）、settings（基础设置）
type ED2KTab = 'generate' | 'resources' | 'settings'
type ResourceFilter = 'all' | 'active' | 'attention'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()

const route = useRoute()
const router = useRouter()

const {
  state,
  draft,
  loading,
  refreshing,
  busy,
  error,
  dialogError,
  notice,
  input,
  query,
  targetId,
  selectedTarget,
  targetOptions,
  editor,
  deleting,
  links,
  resources,
  settingsDirty,
  needsSettingsSave,
  settingsValid,
  strmPreview,
  cacheDirectoryInvalid,
  canStart,
  canRestore,
  canImport,
  canSaveTarget,
  refresh,
  saveSettings,
  resetSettings,
  selectCacheDirectory,
  clearCacheDirectory,
  editTarget,
  closeEditor,
  saveTarget,
  requestDelete,
  deleteTarget,
  importLinks,
  prepareResource,
  resourceAction,
  resumeCredentials,
  taskAction,
} = useED2KSTRM(() => props.token)

const directoryOpen = ref(false)
const cacheDirectoryAccountId = ref('')
const importPanel = ref<HTMLElement>()
const baseUrlInput = ref<HTMLInputElement>()
const resourceFilter = ref<ResourceFilter>('all')
const cacheDirectoryLabel = computed(() => {
  if (draft.value?.cacheCID) return draft.value.cachePath || (draft.value.cacheCID === '0' ? '/' : `目录 ${draft.value.cacheCID}`)
  return '/Cinema115-ED2K缓存目录'
})
const cacheDirectoryHint = computed(() => {
  if (!state.value?.accountReady) return '请先在 115 助手中登录主账号。'
  if (cacheDirectoryInvalid.value) return '主账号已变化，请重新选择或清除缓存目录。'
  if (!settingsDirty.value && state.value.cacheReady === false) return state.value.cacheSetupHint || '缓存目录不可用，请重新选择或清除目录。'
  return ''
})

function openCacheDirectory() {
  if (!busy.value && state.value?.accountReady && state.value.primaryAccountId) cacheDirectoryAccountId.value = state.value.primaryAccountId
}

function chooseCacheDirectory(directory: { id: string; path: string }) {
  selectCacheDirectory(cacheDirectoryAccountId.value, directory)
  cacheDirectoryAccountId.value = ''
}

// 浏览期间固定账号，账号变化后关闭选择器，避免沿用旧目录。
watch([() => state.value?.primaryAccountId, () => state.value?.accountReady], () => {
  if (cacheDirectoryAccountId.value && (!state.value?.accountReady || state.value.primaryAccountId !== cacheDirectoryAccountId.value)) cacheDirectoryAccountId.value = ''
})

// 解析当前 URL query 中的 tab，非法值或缺省均回落到默认生成页签
const activeTab = computed<ED2KTab>(() => {
  const raw = Array.isArray(route.query.tab) ? route.query.tab[0] : route.query.tab
  if (raw === 'resources' || raw === 'settings') return raw
  return 'generate'
})

// 切换页签：仅更新路由 query，不触发任何后台写操作，完整保留内存草稿与输入状态
async function switchTab(tab: ED2KTab) {
  if (activeTab.value === tab) return
  const nextQuery = { ...route.query }
  if (tab === 'generate') delete nextQuery.tab
  else nextQuery.tab = tab
  await router.push({ query: nextQuery })
}

// 统计任务与资源状态，用于页签角标与筛选器计数
const failedTaskCount = computed(() => state.value?.tasks.filter(t => t.status === 'failed').length || 0)
const activeResourceCount = computed(() => state.value?.resources.filter(ed2kResourceActive).length || 0)
const attentionResourceCount = computed(() => state.value?.resources.filter(resourceNeedsAttention).length || 0)

// 已核验为缺失的缓存也需关注，恢复进行中时仍归入处理中。
function resourceNeedsAttention(resource: ED2KSTRMResource) {
  return ed2kResourceAttention(resource) || (!ed2kResourceActive(resource) && resource.cacheState === 'missing')
}

// 基础设置页签是否需要提示待完善或未保存
const settingsNeedsAttention = computed(() => {
  if (!state.value) return false
  return !state.value.settingsSaved || !state.value.targets.length || state.value.cacheReady === false || settingsDirty.value
})

// 资源缓存页签内的状态分段筛选配置
const resourceFilterOptions = computed(() => [
  { value: 'all' as const, label: '全部', count: state.value?.resources.length || 0 },
  { value: 'active' as const, label: '处理中', count: activeResourceCount.value },
  { value: 'attention' as const, label: '需关注', count: attentionResourceCount.value },
])

// 结合搜索词（useED2KSTRM 已过滤）与状态分段筛选后的最终资源列表
const filteredResources = computed(() => {
  if (resourceFilter.value === 'active') return resources.value.filter(ed2kResourceActive)
  if (resourceFilter.value === 'attention') return resources.value.filter(resourceNeedsAttention)
  return resources.value
})

// 从资源缓存行点击“生成 STRM”：预填原 ED2K 链接，保留已选输出目录，切回生成页签并聚焦文本框
async function generate(resource: ED2KSTRMResource) {
  prepareResource(resource)
  if (activeTab.value !== 'generate') {
    await switchTab('generate')
  }
  await nextTick()
  importPanel.value?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  importPanel.value?.querySelector<HTMLTextAreaElement>('textarea')?.focus({ preventScroll: true })
}

// 引导用户前往基础设置填写或保存播放地址
async function goToSettingsAddress() {
  await switchTab('settings')
  await nextTick()
  baseUrlInput.value?.focus()
}

// 当筛选分类下无数据且总资源清空时自动复位分段筛选
watch(
  () => state.value?.resources.length,
  count => {
    if (!count && resourceFilter.value !== 'all') resourceFilter.value = 'all'
  },
)

onMounted(() => {
  document.title = 'ED2K STRM · Cinema115'
})
</script>

<template>
  <ManagementLayout
    title="ED2K STRM"
    description="选择本地目录生成 STRM，按需复用或恢复云端缓存。"
    :parent="{ label: '115 网盘', to: '/115' }"
    @logout="$emit('logout')"
  >
    <template #actions>
      <RouterLink class="es-text-button" :to="{ path: '/logs', query: { module: 'STRM', level: 'ALL' } }">查看日志</RouterLink>
      <button type="button" class="es-button" :disabled="refreshing || !!busy" @click="refresh">
        <IconRefresh :size="17" aria-hidden="true" :class="{ 'es-spin': refreshing }" />
        {{ refreshing ? '刷新中…' : '刷新状态' }}
      </button>
    </template>

    <!-- 全局错误、操作反馈与主账号未就绪提醒 -->
    <p v-if="error" role="alert" class="es-alert">
      <span>{{ error }}</span>
      <button v-if="!state" type="button" :disabled="refreshing" class="es-text-button" @click="refresh">重新加载</button>
    </p>
    <p v-if="notice" role="status" class="es-notice">{{ notice }}</p>
    <p v-if="state && !state.accountReady" class="es-alert">
      <span>请先在 <RouterLink to="/helper115">115 助手</RouterLink> 配置并登录主账号。</span>
    </p>

    <p v-if="loading" role="status" class="es-empty">正在读取 ED2K STRM 配置…</p>

    <template v-else-if="state && draft">
      <div v-if="state.credentialPaused > 0" class="es-setup-banner" role="region" aria-label="暂停任务继续">
        <div class="es-setup-text">
          <IconAlertTriangle :size="17" aria-hidden="true" />
          <span>当前登录的是原主账号，可继续处理 {{ state.credentialPaused }} 个暂停的资源。</span>
        </div>
        <button type="button" class="es-button es-secondary" :disabled="!!busy" @click="resumeCredentials">
          <IconLoader2 v-if="busy === 'resume-credentials'" class="es-spin" :size="15" aria-hidden="true" />
          <IconPlayerPlay v-else :size="15" aria-hidden="true" />
          {{ busy === 'resume-credentials' ? '提交中…' : '继续暂停任务' }}
        </button>
      </div>

      <!-- 顶部模块页签导航：对齐 115 分享 STRM 页签风格 -->
      <nav class="es-tabs" aria-label="ED2K STRM 功能视图">
        <button
          type="button"
          class="es-tab"
          aria-label="生成 STRM"
          :class="{ 'is-active': activeTab === 'generate' }"
          :aria-current="activeTab === 'generate' ? 'page' : undefined"
          @click="switchTab('generate')"
        >
          <IconLink :size="17" aria-hidden="true" />
          <span>生成 STRM</span>
          <span
            v-if="state.tasks.length"
            class="es-tab-badge"
            :class="failedTaskCount ? 'is-warning' : 'is-working'"
            :title="failedTaskCount ? `${failedTaskCount} 个失败任务待处理` : `${state.tasks.length} 个任务处理中`"
          >
            {{ state.tasks.length }}
          </span>
        </button>

        <button
          type="button"
          class="es-tab"
          aria-label="资源缓存"
          :class="{ 'is-active': activeTab === 'resources' }"
          :aria-current="activeTab === 'resources' ? 'page' : undefined"
          @click="switchTab('resources')"
        >
          <IconDatabase :size="17" aria-hidden="true" />
          <span>资源缓存</span>
          <span
            class="es-tab-badge"
            :class="{ 'is-warning': attentionResourceCount > 0 }"
          >
            {{ state.resources.length }}
          </span>
        </button>

        <button
          type="button"
          class="es-tab"
          aria-label="基础设置"
          :class="{ 'is-active': activeTab === 'settings' }"
          :aria-current="activeTab === 'settings' ? 'page' : undefined"
          @click="switchTab('settings')"
        >
          <IconSettings :size="17" aria-hidden="true" />
          <span>基础设置</span>
          <span v-if="settingsDirty" class="es-tab-badge is-warning">未保存</span>
          <span v-else-if="!state.settingsSaved || !state.targets.length || state.cacheReady === false" class="es-tab-badge is-warning">待配置</span>
          <span v-else class="es-tab-badge">{{ state.targets.length }} 目录</span>
        </button>
      </nav>

      <!-- ==================== 页签 1：生成 STRM（默认首页） ==================== -->
      <div v-if="activeTab === 'generate'" class="es-tab-view">
        <!-- 前置配置未就绪或草稿未保存时的就地引导条 -->
        <div
          v-if="settingsNeedsAttention"
          class="es-setup-banner"
          role="region"
          aria-label="生成前置条件提示"
        >
          <div class="es-setup-text">
            <IconAlertTriangle :size="17" aria-hidden="true" />
            <span v-if="!state.settingsSaved">尚未保存 STRM 播放地址，完成设置后即可导入生成。</span>
            <span v-else-if="settingsDirty">基础设置有未保存的修改，请先保存或还原。</span>
            <span v-else-if="state.cacheReady === false">{{ state.cacheSetupHint || '主网盘缓存目录不可用，请前往基础设置检查。' }}</span>
            <span v-else>尚未配置本地输出目录，请先添加一个输出目录。</span>
          </div>
          <div class="es-actions">
            <button
              v-if="!state.settingsSaved || settingsDirty || state.cacheReady === false"
              type="button"
              class="es-button es-secondary"
              :disabled="!!busy"
              @click="goToSettingsAddress"
            >
              <IconSettings :size="15" aria-hidden="true" />前往基础设置
            </button>
            <button
              v-if="!state.targets.length"
              type="button"
              class="es-button es-secondary"
              :disabled="!!busy"
              @click="editTarget()"
            >
              <IconPlus :size="15" aria-hidden="true" />添加输出目录
            </button>
          </div>
        </div>

        <!-- 导入 ED2K 表单 -->
        <form ref="importPanel" class="es-panel" aria-label="导入 ED2K" @submit.prevent="importLinks">
          <header class="es-heading">
            <h2>
              <IconLink :size="18" aria-hidden="true" />导入 ED2K
            </h2>
          </header>

          <div class="es-import-grid">
            <div class="es-import-options">
              <div class="es-field">
                <div class="es-field-header">
                  <span>输出目录</span>
                  <button
                    type="button"
                    class="es-text-button"
                    :disabled="!!busy"
                    @click="editTarget()"
                  >
                    <IconPlus :size="14" aria-hidden="true" />新建目录
                  </button>
                </div>
                <SelectMenu
                  v-model="targetId"
                  label="导入输出目录"
                  :options="targetOptions"
                  placeholder="选择输出目录"
                  :disabled="!!busy || !state.targets.length"
                  required
                />
                <small v-if="selectedTarget" class="es-mono-hint">{{ strmDirectoryPath(selectedTarget.outputDir) }}</small>
                <small v-else>选择本次生成 STRM 的写入位置。</small>
              </div>
              <p class="es-help">
                同一资源的不同文件名共用缓存。再次导入可生成到其他目录，也可补回已删除的 STRM。
              </p>
            </div>

            <label class="es-field">
              <div class="es-field-header">
                <span>ED2K 链接</span>
                <span v-if="links.length" class="es-link-count">
                  已识别 {{ links.length }} 条
                </span>
              </div>
              <textarea
                v-model="input"
                aria-label="ED2K 链接"
                rows="5"
                maxlength="409600"
                placeholder="粘贴 ED2K 链接或包含链接的消息（支持多条混合文本自动提取）"
                :disabled="!!busy"
              />
            </label>
          </div>

          <footer class="es-form-actions">
            <span class="es-help">
              {{
                !state.accountReady
                  ? '请先登录主账号。'
                  : !state.settingsSaved
                    ? '请先保存播放地址。'
                    : settingsDirty
                      ? '请先保存或还原播放地址。'
                      : !state.targets.length
                        ? '请先添加本地输出目录。'
                        : `已识别 ${links.length} 条链接`
              }}
            </span>
            <div class="es-actions">
              <button
                v-if="input.trim()"
                type="button"
                class="es-button"
                :disabled="!!busy"
                @click="input = ''"
              >
                清空输入
              </button>
              <button type="submit" class="es-button es-primary" :disabled="!canImport">
                <IconLoader2 v-if="busy === 'imports'" class="es-spin" :size="17" aria-hidden="true" />
                <IconFile v-else :size="17" aria-hidden="true" />
                {{ busy === 'imports' ? '提交中…' : '导入并生成 STRM' }}
              </button>
            </div>
          </footer>
        </form>

        <!-- 生成任务区：无任务时压成紧凑单行状态条，有任务时展开列表卡片 -->
        <div v-if="!state.tasks.length" class="es-task-idle" role="status" aria-label="STRM 生成任务状态">
          <div class="es-task-idle-main">
            <IconCheck :size="16" aria-hidden="true" />
            <span>暂无待处理或失败的生成任务。任务生成完成后会自动从列表移出。</span>
          </div>
          <button
            v-if="state.resources.length"
            type="button"
            class="es-text-button"
            @click="switchTab('resources')"
          >
            管理已缓存资源 ({{ state.resources.length }})
          </button>
        </div>

        <section v-else class="es-panel" aria-label="STRM 生成任务">
          <header class="es-heading">
            <h2>
              生成任务<span class="es-counter">{{ state.tasks.length }}</span>
            </h2>
            <span class="es-counter">仅显示当前队列与失败任务</span>
          </header>
          <ul class="es-tasks">
            <li v-for="task in state.tasks" :key="task.id" class="es-task" :aria-label="task.name">
              <div class="es-task-content">
                <strong>{{ task.name }}</strong>
                <p>
                  <span>输出至：{{ task.targetName }}</span>
                  <span class="es-task-time">{{ ed2kDate(task.updatedAt) }}</span>
                </p>
                <p v-if="task.message" class="es-help" :class="{ 'es-error-text': task.status === 'failed' }">
                  {{ task.message }}
                </p>
              </div>
              <span class="es-status" :class="task.status === 'failed' ? 'es-attention' : 'es-working'">
                <IconLoader2 v-if="task.status !== 'failed'" class="es-spin" :size="13" aria-hidden="true" />
                {{ { queued: '等待生成', waiting: '等待缓存', failed: '生成失败' }[task.status] }}
              </span>
              <div v-if="task.status === 'failed'" class="es-actions">
                <button
                  type="button"
                  class="es-button"
                  :disabled="!!busy || !canStart"
                  @click="taskAction(task, 'retry')"
                >
                  <IconRefresh :size="15" aria-hidden="true" />重试
                </button>
                <button
                  type="button"
                  class="es-icon-button"
                  :disabled="!!busy"
                  :aria-label="'清理失败任务 ' + task.name"
                  @click="taskAction(task, 'delete')"
                >
                  <IconTrash :size="17" aria-hidden="true" />
                </button>
              </div>
            </li>
          </ul>
        </section>
      </div>

      <!-- ==================== 页签 2：资源缓存 ==================== -->
      <div v-else-if="activeTab === 'resources'" class="es-tab-view">
        <section class="es-panel es-resources" aria-label="ED2K 资源缓存">
          <header class="es-resource-toolbar">
            <div class="es-resource-title">
              <h2>
                <IconDatabase :size="18" aria-hidden="true" />资源缓存
                <span class="es-counter">{{ state.resources.length }}</span>
              </h2>
              <p class="es-help">
                保留首次核验的内容身份。刷新列表不会触发离线；资源缓存状态与本地 STRM 文件分开管理。
              </p>
            </div>

            <div class="es-resource-controls">
              <SegmentedControl
                v-if="state.resources.length > 0"
                v-model="resourceFilter"
                label="按缓存状态筛选"
                density="compact"
                appearance="neutral"
                :options="resourceFilterOptions"
              />
              <label class="es-search">
                <IconSearch :size="16" aria-hidden="true" />
                <input
                  v-model="query"
                  type="search"
                  aria-label="筛选 ED2K 资源"
                  placeholder="搜索名称、ED2K 或 SHA1"
                />
              </label>
            </div>
          </header>

          <div v-if="!filteredResources.length" class="es-empty es-inline-empty">
            <p>
              {{
                query.trim() || resourceFilter !== 'all'
                  ? '没有匹配当前筛选条件的资源'
                  : '尚无缓存资源。导入 ED2K 链接后将在此记录内容身份。'
              }}
            </p>
            <div class="es-actions">
              <button
                v-if="query.trim() || resourceFilter !== 'all'"
                type="button"
                class="es-text-button"
                @click="query = ''; resourceFilter = 'all'"
              >
                清除筛选条件
              </button>
              <button
                v-else
                type="button"
                class="es-button es-secondary"
                @click="switchTab('generate')"
              >
                前往导入 ED2K
              </button>
            </div>
          </div>

          <table v-else class="es-table">
            <thead>
              <tr>
                <th>文件</th>
                <th>缓存状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="resource in filteredResources"
                :key="ed2kResourceKey(resource)"
                :aria-label="resource.name"
              >
                <td class="es-file-cell">
                  <strong>{{ resource.name }}</strong>
                  <p class="es-file-meta">
                    <span>{{ ed2kSize(resource.size) }}</span>
                    <span>{{ ed2kDate(resource.updatedAt) }}</span>
                  </p>
                  <details class="es-identities">
                    <summary>文件资料</summary>
                    <dl>
                      <div>
                        <dt>ED2K</dt>
                        <dd>{{ resource.hash }}</dd>
                      </div>
                      <div>
                        <dt>SHA1</dt>
                        <dd>{{ resource.sha1 || '文件可用后读取' }}</dd>
                      </div>
                      <div>
                        <dt>字节大小</dt>
                        <dd>{{ resource.size.toLocaleString('zh-CN') }}</dd>
                      </div>
                    </dl>
                  </details>
                </td>

                <td class="es-status-cell">
                  <span
                    class="es-status"
                    :class="{
                      'es-working': ed2kResourceActive(resource),
                      'es-attention': resourceNeedsAttention(resource),
                    }"
                  >
                    <IconLoader2 v-if="ed2kResourceActive(resource)" class="es-spin" :size="13" aria-hidden="true" />
                    {{ resource.status === 'ready' ? ed2kCacheStatus(resource) : ed2kResourceStatus(resource) }}
                  </span>
                  <p
                    v-if="resource.message && (ed2kResourceActive(resource) || resourceNeedsAttention(resource))"
                    class="es-resource-message"
                    :class="{ 'es-error-text': ed2kResourceAttention(resource) }"
                  >
                    {{ resource.message }}
                  </p>
                </td>

                <td class="es-action-cell">
                  <div class="es-actions">
                    <button
                      type="button"
                      class="es-button es-secondary"
                      :disabled="!!busy || !state.targets.length"
                      title="填入该资源的 ED2K 链接并返回生成页面"
                      @click="generate(resource)"
                    >
                      生成 STRM
                    </button>
                    <button
                      type="button"
                      class="es-button"
                      :disabled="!!busy || !canStart || ed2kResourceActive(resource)"
                      title="检查主网盘缓存文件是否仍在记录位置"
                      @click="resourceAction(resource, 'check')"
                    >
                      核验缓存
                    </button>
                    <button
                      type="button"
                      class="es-button"
                      :disabled="!!busy || !canRestore || ed2kResourceActive(resource)"
                      title="当主缓存缺失时，优先用秒传资料恢复，无法秒传时重新提交离线"
                      @click="resourceAction(resource, 'restore')"
                    >
                      恢复缓存
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </section>
      </div>

      <!-- ==================== 页签 3：基础设置 ==================== -->
      <div v-else-if="activeTab === 'settings'" class="es-tab-view">
        <form class="es-panel" aria-label="ED2K STRM 基础设置" @submit.prevent="saveSettings">
          <header class="es-heading">
            <h2>
              <IconSettings :size="18" aria-hidden="true" />基础设置
            </h2>
            <span v-if="settingsDirty" class="es-draft">未保存</span>
          </header>

          <label class="es-field es-settings-address">
            <span>STRM 播放地址</span>
            <input
              ref="baseUrlInput"
              v-model="draft.baseUrl"
              type="url"
              required
              :disabled="!!busy"
              placeholder="http://NAS_IP:8080"
            />
            <small>填写播放器或 Emby 能访问的 Cinema115 服务地址。</small>
          </label>

          <div class="es-cache es-field" role="group" aria-labelledby="es-cache-label">
            <label id="es-cache-label" for="es-cache-directory"><IconCloud :size="16" aria-hidden="true" />主网盘缓存目录</label>
            <div class="es-cache-selection">
              <output id="es-cache-directory" :aria-invalid="cacheDirectoryInvalid || undefined">{{ cacheDirectoryLabel }}</output>
              <div class="es-actions">
                <button type="button" class="es-button" :disabled="!!busy || !state.accountReady || !state.primaryAccountId" @click="openCacheDirectory">
                  <IconFolder :size="16" aria-hidden="true" />选择目录
                </button>
                <button v-if="draft.cacheCID" type="button" class="es-text-button" :disabled="!!busy" @click="clearCacheDirectory">清除选择</button>
              </div>
            </div>
            <small>未选择时使用 /Cinema115-ED2K缓存目录，按需创建。修改保存后生效，已有文件不移动。</small>
            <p v-if="cacheDirectoryHint" role="status" class="es-cache-hint">{{ cacheDirectoryHint }}</p>
          </div>

          <STRMContentPreview :content="strmPreview" description="示例字段，生成时替换为实际文件信息；播放地址不依赖本地 STRM 所在目录。" />

          <footer class="es-settings-actions">
            <button
              v-if="settingsDirty"
              type="button"
              class="es-text-button"
              :disabled="!!busy"
              @click="resetSettings"
            >
              还原修改
            </button>
            <button
              v-if="needsSettingsSave"
              type="submit"
              class="es-button es-primary"
              :disabled="!!busy || !settingsValid"
            >
              {{ busy === 'settings' ? '保存中…' : '保存设置' }}
            </button>
            <span v-else role="status" aria-label="基础设置保存状态" class="es-settings-saved"><IconCheck :size="16" aria-hidden="true" />已保存</span>
          </footer>
        </form>

        <ED2KSTRMTargetCards
          :targets="state.targets"
          :busy="!!busy"
          @add="editTarget()"
          @edit="editTarget"
          @delete="requestDelete"
        />
      </div>
    </template>

    <!-- 弹窗区（跨页签共享，在任意页签均可打开新建/编辑目录） -->
    <P115ActionDialog
      v-if="editor"
      :title="editor.id ? '编辑输出目录' : '添加输出目录'"
      :busy="!!busy"
      layout="contained"
      @close="closeEditor"
    >
      <form id="es-target-editor" class="es-editor" aria-label="输出目录设置" @submit.prevent="saveTarget">
        <p v-if="dialogError" role="alert" class="es-alert">{{ dialogError }}</p>
        <label class="es-field">
          <span>目录名称</span>
          <input v-model="editor.name" required maxlength="80" :disabled="!!busy" placeholder="例如：电影" />
        </label>
        <div class="es-field">
          <label for="es-output">本地输出目录</label>
          <div class="es-directory-input">
            <input
              id="es-output"
              v-model="editor.outputDir"
              required
              :disabled="!!busy"
              placeholder="例如：ed2k/电影"
            />
            <button type="button" class="es-button" :disabled="!!busy" @click="directoryOpen = true">
              <IconFolder :size="16" aria-hidden="true" />选择目录
            </button>
          </div>
          <small>STRM 根目录内的相对路径。文件直接写入 {{ strmDirectoryPath(editor.outputDir) }}。</small>
        </div>
      </form>
      <template #footer>
        <button type="button" class="es-button" :disabled="!!busy" @click="closeEditor">取消</button>
        <button
          type="submit"
          form="es-target-editor"
          class="es-button es-primary"
          :disabled="!!busy || !canSaveTarget"
        >
          {{ busy === 'target' ? '保存中…' : '保存目录' }}
        </button>
      </template>
    </P115ActionDialog>

    <P115ActionDialog
      v-if="deleting"
      title="删除输出目录"
      :busy="!!busy"
      layout="contained"
      @close="deleting = undefined"
    >
      <p v-if="dialogError" role="alert" class="es-alert">{{ dialogError }}</p>
      <p>删除“{{ deleting.name }}”的输出配置？</p>
      <p class="es-help">已有文件和缓存资源保留，已接受的生成任务继续处理。关联的 Bot 分配会解除。</p>
      <template #footer>
        <button type="button" class="es-button" :disabled="!!busy" @click="deleting = undefined">取消</button>
        <button type="button" class="es-button es-danger" :disabled="!!busy" @click="deleteTarget">
          {{ busy === 'delete-target' ? '删除中…' : '删除配置' }}
        </button>
      </template>
    </P115ActionDialog>

    <STRMDirectoryDialog
      v-if="directoryOpen && editor"
      :token="token"
      title="选择 ED2K STRM 输出目录"
      @close="directoryOpen = false"
      @select="editor.outputDir = $event; directoryOpen = false"
    />
    <P115DirectoryDialog
      v-if="cacheDirectoryAccountId && state?.accountReady && cacheDirectoryAccountId === state.primaryAccountId"
      :key="cacheDirectoryAccountId"
      :token="token"
      :account-id="cacheDirectoryAccountId"
      title="选择主网盘缓存目录"
      description="选择后请保存基础设置。新缓存和恢复使用此目录，已有文件不移动。"
      @close="cacheDirectoryAccountId = ''"
      @select="chooseCacheDirectory"
    />
  </ManagementLayout>
</template>

<style scoped>
/* 顶部横向页签导航栏（对齐 115 分享 STRM 蓝线选中风格） */
.es-tabs {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 18px;
  border-bottom: 1px solid rgb(var(--line-default));
}
.es-tab {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 42px;
  padding: 9px 14px 11px;
  border: 0;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  background: transparent;
  color: rgb(var(--content-secondary));
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  transition: color 150ms, border-color 150ms;
}
.es-tab svg {
  color: currentColor;
  opacity: 0.85;
}
.es-tab.is-active {
  color: rgb(var(--action-link));
  border-bottom-color: rgb(var(--action-link));
  font-weight: 600;
}
.es-tab.is-active svg {
  opacity: 1;
}
.es-tab:focus-visible {
  outline: 2px solid #3b82f6;
  outline-offset: -2px;
  border-radius: 8px 8px 0 0;
}
@media (hover: hover) {
  .es-tab:not(.is-active):hover {
    color: rgb(var(--content-primary));
  }
}

/* 页签内联计数与状态角标 */
.es-tab-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 19px;
  padding: 0 6px;
  border-radius: 999px;
  background: rgb(var(--surface-elevated));
  color: rgb(var(--content-secondary));
  font-size: 11px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.es-tab.is-active .es-tab-badge:not(.is-warning) {
  background: rgb(59 130 246 / .12);
  color: rgb(var(--action-link));
}
.es-tab-badge.is-working {
  background: rgb(59 130 246 / .12);
  color: rgb(var(--action-link));
}
.es-tab-badge.is-warning {
  background: rgb(245 158 11 / .14);
  color: rgb(var(--status-warning));
}

/* 前置未就绪引导横幅 */
.es-setup-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding: 12px 16px;
  margin-bottom: 16px;
  border: 1px solid rgb(245 158 11 / .28);
  border-radius: 12px;
  background: rgb(245 158 11 / .07);
  font-size: 13px;
  color: rgb(var(--content-primary));
}
.es-setup-text {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.es-setup-text svg {
  flex: none;
  color: rgb(var(--status-warning));
}

/* 通用面板与排版 */
.es-panel {
  min-width: 0;
  padding: 20px;
  margin-bottom: 18px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 14px;
  background: rgb(var(--surface-card));
  color: rgb(var(--content-primary));
}
.es-heading,
.es-actions,
.es-form-actions,
.es-task {
  display: flex;
  align-items: center;
  gap: 12px;
}
.es-heading,
.es-form-actions {
  justify-content: space-between;
}
.es-heading h2,
.es-resource-title h2 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
}
.es-heading h2 svg,
.es-resource-title h2 svg {
  color: rgb(var(--action-link));
}
.es-actions {
  flex-wrap: wrap;
  gap: 7px;
}
.es-counter,
.es-help,
.es-field small {
  font-size: 12px;
  line-height: 1.75;
  color: rgb(var(--content-secondary));
}
.es-counter {
  font-weight: 400;
}
.es-help {
  margin-top: 6px;
}
.es-draft {
  color: rgb(var(--status-warning));
  font-size: 12px;
  font-weight: 500;
}

/* 表单字段 */
.es-field {
  display: grid;
  gap: 7px;
  min-width: 0;
  color: rgb(var(--content-secondary));
  font-size: 13px;
}
.es-field > span,
.es-field > label,
.es-field-header > span:first-child {
  font-weight: 500;
}
.es-field-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.es-link-count {
  font-size: 11px;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}
.es-mono-hint {
  overflow-wrap: anywhere;
}
.es-field input,
.es-field textarea {
  width: 100%;
  min-width: 0;
  padding: 10px 12px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 9px;
  background: rgb(var(--surface-page));
  color: rgb(var(--content-primary));
  outline: none;
}
.es-field input:focus,
.es-field textarea:focus {
  border-color: #3b82f6;
  box-shadow: 0 0 0 2px rgb(59 130 246 / .12);
}
.es-field textarea {
  resize: vertical;
  font-size: 12px;
  line-height: 1.7;
}
.es-settings-address {
  margin-top: 15px;
}
.es-settings-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 12px;
  min-height: 36px;
  margin-top: 16px;
}
.es-settings-saved {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: rgb(var(--content-secondary));
  font-size: 12px;
}
.es-cache {
  padding-top: 12px;
  margin-top: 12px;
  border-top: 1px solid rgb(var(--line-default));
  color: rgb(var(--content-secondary));
  font-size: 12px;
  overflow-wrap: anywhere;
}
.es-cache > label {
  display: flex;
  align-items: center;
  gap: 8px;
}
.es-cache svg {
  flex: none;
}
.es-cache-selection {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
.es-cache-selection output {
  flex: 1 1 240px;
  min-width: 0;
  padding: 10px 12px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 9px;
  background: rgb(var(--surface-page));
  color: rgb(var(--content-primary));
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.es-cache-hint {
  color: rgb(var(--status-warning));
  font-size: 12px;
  line-height: 1.75;
}

/* 按钮体系（严格复用蓝色与中性色，不用青绿/薄荷绿） */
.es-button,
.es-icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 36px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 8px;
  padding: 7px 11px;
  background: rgb(var(--surface-card));
  font-size: 12px;
  font-weight: 500;
  color: rgb(var(--content-secondary));
  white-space: nowrap;
  cursor: pointer;
}
.es-primary {
  background: rgb(var(--action-primary));
  border-color: rgb(var(--action-link));
  color: #fff;
}
.es-secondary {
  background: rgb(59 130 246 / .08);
  color: rgb(var(--action-link));
  border-color: rgb(59 130 246 / .22);
}
.es-danger {
  color: rgb(var(--status-danger));
  border-color: rgb(239 68 68 / .3);
  background: rgb(239 68 68 / .06);
}
.es-icon-button {
  padding: 7px;
  min-width: 36px;
  border-color: transparent;
  background: transparent;
}
.es-button:disabled,
.es-icon-button:disabled {
  opacity: 0.45;
  cursor: default;
}
.es-button:focus-visible,
.es-icon-button:focus-visible,
.es-text-button:focus-visible,
summary:focus-visible {
  outline: 2px solid #3b82f6;
  outline-offset: 2px;
}
.es-text-button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: rgb(var(--action-link));
  font-size: 12px;
  text-decoration: underline;
  background: transparent;
  border: 0;
  padding: 0;
  cursor: pointer;
}

/* 提示横幅 */
.es-alert,
.es-notice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 14px;
  padding: 11px 14px;
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.75;
  overflow-wrap: anywhere;
}
.es-alert {
  background: rgb(239 68 68 / .07);
  color: rgb(var(--status-danger));
}
.es-notice {
  background: rgb(59 130 246 / .08);
  color: rgb(var(--selection-content));
}
.es-alert a {
  text-decoration: underline;
}
.es-error-text {
  color: rgb(var(--status-danger)) !important;
}

/* 生成任务列表 */
.es-tasks {
  margin-top: 13px;
}
.es-task {
  padding: 12px 0;
  border-top: 1px solid rgb(var(--line-default));
}
.es-task-content {
  min-width: 0;
  flex: 1;
}
.es-task-content strong {
  font-size: 13px;
  font-weight: 500;
  overflow-wrap: anywhere;
  word-break: break-word;
}
.es-task-content > p:not(.es-help) {
  margin-top: 3px;
  color: rgb(var(--content-secondary));
  font-size: 12px;
  overflow-wrap: anywhere;
}
.es-task-time {
  margin-left: 10px;
}

/* 无任务时的紧凑静默状态条 */
.es-task-idle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
  padding: 11px 16px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 12px;
  background: rgb(var(--surface-card));
  color: rgb(var(--content-secondary));
  font-size: 12px;
}
.es-task-idle-main {
  display: flex;
  align-items: center;
  gap: 8px;
}
.es-task-idle-main svg {
  flex: none;
  color: rgb(var(--content-secondary));
}

/* 导入布局 */
.es-import-grid {
  display: grid;
  grid-template-columns: minmax(210px, 300px) minmax(0, 1fr);
  gap: 22px;
  margin-top: 16px;
}
.es-import-options {
  min-width: 0;
}
.es-import-options .es-help {
  margin-top: 12px;
}
.es-form-actions {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid rgb(var(--line-default));
}
.es-form-actions > .es-help {
  margin: 0;
}

/* 空状态与状态胶囊 */
.es-empty {
  display: grid;
  place-items: center;
  gap: 10px;
  padding: 28px 14px;
  color: rgb(var(--content-secondary));
  text-align: center;
  font-size: 13px;
}
.es-inline-empty {
  padding: 28px 0 12px;
}
.es-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border-radius: 6px;
  padding: 4px 8px;
  background: rgb(var(--surface-hover));
  color: rgb(var(--content-secondary));
  font-size: 11px;
  white-space: nowrap;
}
.es-working {
  background: rgb(59 130 246 / .09);
  color: rgb(var(--action-link));
}
.es-attention {
  background: rgb(245 158 11 / .12);
  color: rgb(var(--status-warning));
}

/* 资源缓存页签工具栏与表格 */
.es-resource-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 14px;
}
.es-resource-title {
  min-width: 0;
  flex: 1 1 280px;
}
.es-resource-controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.es-search {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 240px;
  min-width: 0;
  min-height: 36px;
  padding: 6px 10px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 9px;
  background: rgb(var(--surface-page));
  color: rgb(var(--content-secondary));
}
.es-search input {
  width: 100%;
  min-width: 0;
  background: transparent;
  outline: none;
  font-size: 12px;
  color: rgb(var(--content-primary));
}
.es-search:focus-within {
  border-color: #3b82f6;
  box-shadow: 0 0 0 2px rgb(59 130 246 / .12);
}

.es-table {
  width: 100%;
  margin-top: 14px;
  border-collapse: collapse;
  text-align: left;
  table-layout: fixed;
}
.es-table th {
  padding: 10px 0;
  color: rgb(var(--content-secondary));
  font-size: 11px;
  font-weight: 500;
}
.es-table th:first-child {
  width: 46%;
}
.es-table th:nth-child(2) {
  width: 22%;
}
.es-table td {
  padding: 14px 12px 14px 0;
  border-top: 1px solid rgb(var(--line-default));
  vertical-align: top;
  min-width: 0;
}
.es-table td:last-child {
  padding-right: 0;
}
.es-file-cell strong {
  display: block;
  font-size: 13px;
  font-weight: 500;
  line-height: 1.45;
  overflow-wrap: anywhere;
  word-break: break-word;
}
.es-file-meta {
  display: flex;
  gap: 4px 12px;
  flex-wrap: wrap;
  margin-top: 4px;
  color: rgb(var(--content-secondary));
  font-size: 11px;
}
.es-resource-message {
  margin-top: 6px;
  font-size: 11px;
  line-height: 1.7;
  color: rgb(var(--content-secondary));
  overflow-wrap: anywhere;
}
.es-identities {
  margin-top: 6px;
  font-size: 11px;
  color: rgb(var(--content-secondary));
}
.es-identities summary {
  width: fit-content;
  cursor: pointer;
  user-select: none;
}
.es-identities dl {
  display: grid;
  gap: 4px;
  margin-top: 6px;
  padding: 8px 10px;
  border-radius: 8px;
  background: rgb(var(--surface-page));
}
.es-identities dl > div {
  display: grid;
  grid-template-columns: 56px minmax(0, 1fr);
  gap: 8px;
}
.es-identities dt {
  color: rgb(var(--content-secondary));
}
.es-identities dd {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  color: rgb(var(--content-primary));
  overflow-wrap: anywhere;
}
.es-action-cell .es-button {
  padding: 6px 9px;
  min-height: 32px;
  font-size: 11px;
}

/* 弹窗内部样式 */
.es-editor {
  display: grid;
  gap: 18px;
}
.es-directory-input {
  display: flex;
  gap: 8px;
}
.es-directory-input input {
  flex: 1;
}

.es-spin {
  animation: es-spin 0.85s linear infinite;
}
@keyframes es-spin {
  to {
    transform: rotate(360deg);
  }
}

/* ==================== 响应式断点：平板与移动端（<= 767px） ==================== */
@media (max-width: 767px) {
  .es-tabs {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0;
  }
  .es-tab {
    justify-content: center;
    gap: 5px;
    min-width: 0;
    padding: 9px 4px 11px;
    font-size: 12px;
  }
  .es-tab-badge {
    display: none;
  }
  .es-panel {
    padding: 16px;
  }
  .es-import-grid {
    grid-template-columns: 1fr;
    gap: 15px;
  }
  .es-import-options .es-help {
    margin-top: 7px;
  }
  .es-resource-controls {
    width: 100%;
  }
  .es-search {
    flex: 1 1 100%;
    width: 100%;
  }
  .es-table thead {
    display: none;
  }
  .es-table,
  .es-table tbody {
    display: block;
  }
  .es-table tr {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
    padding: 14px 0;
    border-top: 1px solid rgb(var(--line-default));
  }
  .es-table td {
    display: block;
    border: 0;
    padding: 0;
  }
  .es-action-cell .es-actions {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 6px;
  }
  .es-action-cell .es-button {
    width: 100%;
    min-height: 38px;
    padding: 8px 6px;
    font-size: 12px;
  }
  .es-task {
    flex-wrap: wrap;
    align-items: flex-start;
  }
  .es-task-content {
    flex-basis: 100%;
  }
  .es-task > .es-actions {
    margin-left: auto;
  }
}

/* ==================== 响应式断点：小屏手机（<= 479px，覆盖 390px / 320px） ==================== */
@media (max-width: 479px) {
  .es-heading {
    flex-wrap: wrap;
    gap: 8px;
  }
  .es-heading h2 {
    font-size: 13px;
  }
  .es-button,
  .es-icon-button {
    min-height: 42px;
  }
  .es-form-actions {
    align-items: stretch;
    flex-direction: column;
    gap: 10px;
  }
  .es-form-actions > .es-actions {
    width: 100%;
  }
  .es-form-actions > .es-actions > .es-button {
    flex: 1;
  }
  .es-task-time {
    display: block;
    margin-left: 0;
  }
  .es-field input,
  .es-field textarea,
  .es-search input {
    font-size: 16px;
  }
  .es-directory-input {
    flex-wrap: wrap;
  }
  .es-directory-input input {
    flex-basis: 100%;
  }
  .es-directory-input .es-button {
    margin-left: auto;
  }
  .es-cache-selection > .es-actions {
    width: 100%;
  }
}

/* 设置操作在手机和触控设备上保留足够的点击区域。 */
@media (max-width: 767px), (pointer: coarse) {
  .es-settings-actions {
    min-height: 44px;
  }
  .es-settings-actions > button,
  .es-cache-selection .es-text-button {
    min-height: 44px;
  }
  .es-settings-actions .es-text-button,
  .es-cache-selection .es-text-button {
    padding-inline: 8px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .es-spin,
  .es-tab {
    animation: none;
    transition: none;
  }
}
</style>
