<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { subtitleRequest, type SubtitleConfig } from '../utils/subtitleApi'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const config = ref<SubtitleConfig>(), apiToken = ref(''), busy = ref('load'), error = ref(''), notice = ref(''), confirmClear = ref(false)
async function load() {
  busy.value = 'load'; error.value = ''
  try { config.value = await subtitleRequest<SubtitleConfig>(props.token, 'config', lifecycle.signal) }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取字幕配置失败' }
  finally { busy.value = '' }
}
async function save(clear = false) {
  if (busy.value || !config.value) return
  busy.value = clear ? 'clear' : 'save'; error.value = ''; notice.value = ''
  try {
    config.value = await subtitleRequest<SubtitleConfig>(props.token, 'assrt/config', lifecycle.signal, { method: clear ? 'DELETE' : 'POST', ...(!clear ? { body: JSON.stringify({ token: apiToken.value.trim() }) } : {}) })
    apiToken.value = ''; confirmClear.value = false
    notice.value = clear ? '射手网配置已清除。' : '射手网验证成功，查找字幕时将同时显示搜索结果。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存字幕配置失败' }
  finally { busy.value = '' }
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); apiToken.value = '' })
</script>

<template>
  <ManagementLayout title="字幕" description="管理字幕来源，在下载整理时统一查找和匹配字幕。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <p v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}<button v-if="!config" class="ml-3 underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="mb-4 rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ notice }}</p>
    <div class="grid gap-4">
      <section class="rounded-2xl bg-white p-5 shadow-sm dark:bg-slate-900 sm:p-6">
        <div class="flex items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">字幕库</h2><span class="text-xs text-slate-500 dark:text-slate-400">无需配置</span></div>
        <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">使用 sub_share 动漫字幕库，按作品搜索并选择字幕版本。</p>
        <a href="https://github.com/foxofice/sub_share" target="_blank" rel="noopener noreferrer" class="mt-3 inline-block text-sm text-blue-600 dark:text-blue-400">查看字幕库</a>
      </section>
      <form class="min-w-0 rounded-2xl bg-white p-5 shadow-sm dark:bg-slate-900 sm:p-6" @submit.prevent="save()">
        <div class="flex items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">射手网 Assrt</h2><span class="text-xs text-slate-500 dark:text-slate-400">{{ config ? config.assrt.configured ? '已配置' : '未配置' : '读取中…' }}</span></div>
        <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">配置后，与字幕库一起显示搜索结果。字幕服务由 <a href="https://secure.assrt.net/" target="_blank" rel="noopener noreferrer" class="text-blue-600 dark:text-blue-400">assrt.net</a> 提供。</p>
        <fieldset :disabled="!!busy || !config" class="mt-5 min-w-0">
          <label for="assrt-token" class="text-sm font-medium text-slate-700 dark:text-slate-300">API Token</label>
          <div class="mt-2 flex flex-wrap gap-3"><input id="assrt-token" v-model="apiToken" type="password" required pattern="[A-Za-z0-9]{32}" maxlength="32" autocomplete="off" spellcheck="false" :placeholder="config?.assrt.configured ? '已保存，输入新 Token 可替换' : '输入 32 位 API Token'" class="min-h-11 min-w-0 flex-1 basis-64 rounded-xl border border-slate-200 bg-slate-50 px-3 text-sm text-slate-900 outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100" /><button type="submit" :disabled="!apiToken.trim() || !!busy" class="min-h-11 rounded-xl bg-blue-600 px-4 text-sm text-white disabled:opacity-40">{{ busy === 'save' ? '验证中…' : '保存并验证' }}</button></div>
        </fieldset>
        <p class="mt-3 text-xs leading-6 text-slate-500 dark:text-slate-400">在射手网注册登录后，从用户面板获取 Token。Token 加密保存，不会回显。初始配额为每分钟 20 次，搜索结果会缓存。</p>
        <div class="mt-3 flex flex-wrap items-center justify-between gap-3 text-sm"><a href="https://secure.assrt.net/api/doc#usetoken" target="_blank" rel="noopener noreferrer" class="text-blue-600 dark:text-blue-400">获取 Token 说明</a><button v-if="!confirmClear" type="button" :disabled="!!busy || !config?.assrt.configured" class="min-h-11 text-slate-500 disabled:opacity-40 dark:text-slate-400" @click="confirmClear = true">清除配置</button><div v-else class="flex gap-4"><button type="button" :disabled="!!busy" class="min-h-11 text-red-600 dark:text-red-400" @click="save(true)">确认清除</button><button type="button" :disabled="!!busy" class="min-h-11 text-slate-500 dark:text-slate-400" @click="confirmClear = false">取消</button></div></div>
      </form>
    </div>
  </ManagementLayout>
</template>
