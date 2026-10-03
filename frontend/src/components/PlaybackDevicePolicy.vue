<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { memberRequest } from '../types/memberPlayback'

const props = defineProps<{ token: string }>()
const policy = ref({ mode: 'parallel', silenceSeconds: 60 })
const loaded = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
onBeforeUnmount(() => lifecycle.abort())
onMounted(async () => {
  try {
    policy.value = await memberRequest(props.token, lifecycle.signal, '/device-policy')
    loaded.value = true
  } catch (e) {
    if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取设备策略失败'
  }
})
async function save() {
  if (!loaded.value || busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    policy.value = await memberRequest(props.token, lifecycle.signal, '/device-policy', policy.value)
    notice.value = '设备策略已保存，对后续取链请求生效。'
  } catch (e) {
    if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存设备策略失败'
  } finally { busy.value = false }
}
</script>

<template>
  <form class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900" @submit.prevent="save">
    <h2 class="font-semibold text-slate-900 dark:text-slate-100">播放设备策略</h2>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-emerald-700">{{ notice }}</p>
    <label class="block text-sm text-slate-600 dark:text-slate-300">同一 Emby 用户的设备使用方式
      <select v-model="policy.mode" :disabled="!loaded || busy" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950">
        <option value="parallel">多设备并行</option>
        <option value="heartbeat_takeover">单设备心跳熔断</option>
      </select>
    </label>
    <p v-if="policy.mode === 'parallel'" class="text-sm leading-6 text-slate-500">保留当前行为：同一用户可在多个设备播放，各设备独立使用播放池名额。</p>
    <template v-else-if="policy.mode === 'heartbeat_takeover'">
      <label class="block text-sm text-slate-600 dark:text-slate-300">心跳静默时间（秒）
        <input v-model.number="policy.silenceSeconds" type="number" min="30" max="180" step="1" required :disabled="!loaded || busy" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" />
      </label>
      <p class="text-sm leading-6 text-slate-500">原设备持续上报播放状态时拒绝其他设备；静默超时后，新设备成功取得播放授权才接替。默认 60 秒，可设为 30 至 180 秒。暂停仍上报状态时继续占位，同设备切集与预加载可复用。</p>
      <p class="text-sm leading-6 text-slate-500">优先沿用原设备的健康小号，准备时借用原名额，成功接替后直接转交，旧会话不再占用容量。正常停止后的名额也可继承；必须更换小号时，新小号需有可用容量，失败保留原设备。</p>
      <p class="text-sm leading-6 text-slate-500">适用于所有网盘播放来源。设备信息缺失时需通过反代重新登录。切换策略不主动停止已有播放；接替只拦截旧会话后续取链，不能撤回已签发直链。名额不代表真实视频连接数。</p>
    </template>
    <button :disabled="!loaded || busy" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">保存设备策略</button>
  </form>
</template>
