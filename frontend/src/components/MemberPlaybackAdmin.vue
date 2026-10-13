<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import SettingSwitch from './SettingSwitch.vue'
import { memberRequest } from '../types/memberPlayback'

const props = defineProps<{ token: string }>()
const settings = ref({ allowDownload: false, publicUrl: '' })
const saved = ref({ ...settings.value })
const loaded = ref(false), busy = ref(false)
const error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const dirty = computed(() => loaded.value && JSON.stringify(settings.value) !== JSON.stringify(saved.value))
async function load() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, '/settings')
    if (!loaded.value) settings.value = { allowDownload: !!data.settings.allowDownload, publicUrl: data.settings.publicUrl || '' }
    saved.value = { allowDownload: !!data.settings.allowDownload, publicUrl: data.settings.publicUrl || '' }; loaded.value = true
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取访问设置失败' }
  finally { busy.value = false }
}
async function save() {
  if (busy.value || !loaded.value) return false
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, '', settings.value)
    settings.value = { allowDownload: !!data.settings.allowDownload, publicUrl: data.settings.publicUrl || '' }
    saved.value = { ...settings.value }; notice.value = '访问设置已保存。'; return true
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存访问设置失败'; return false }
  finally { busy.value = false }
}
defineExpose({ loaded, dirty })
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section id="proxy-access" class="proxy-card space-y-4" aria-labelledby="proxy-access-title">
    <div class="proxy-card-heading"><h2 id="proxy-access-title">访问设置</h2><span class="proxy-draft-label">{{ dirty ? '待保存' : loaded ? '已同步' : '读取中' }}</span></div>
    <p class="proxy-help">对外地址和原文件下载共用同一组设置。首次启动反代前，请先保存一次以确认 Emby 服务身份。</p>
    <p v-if="error" role="alert" class="proxy-alert is-error">{{ error }} <button v-if="!loaded" type="button" :disabled="busy" class="underline" @click="load">重试读取</button></p>
    <p v-if="notice && !dirty" role="status" class="proxy-alert is-success">{{ notice }}</p>
    <form class="space-y-4" @submit.prevent="save">
      <label class="block text-sm">提供给用户的 Emby 连接地址（选填）
        <input v-model.trim="settings.publicUrl" :disabled="busy || !loaded" type="url" placeholder="https://emby.example.com" class="proxy-input mt-2" />
      </label>
      <p class="proxy-help">用于个人中心展示，留空则不显示链接。需要 Cinema115 管理播放时，应最终转发到 302 入口。</p>
      <SettingSwitch v-model="settings.allowDownload" :disabled="busy || !loaded" label="允许下载原文件" description="仅开放 Cinema115 下载入口，仍遵守 Emby 下载权限和网盘绑定；反代关闭时入口停止。" />
      <button type="submit" :disabled="busy || !loaded" class="proxy-button">{{ busy ? '正在保存…' : '保存访问设置' }}</button>
    </form>
    <RouterLink to="/emby/users" class="proxy-text-link">管理用户与网盘绑定</RouterLink>
  </section>
</template>
