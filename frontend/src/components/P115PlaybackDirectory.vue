<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
const props = defineProps<{ token: string; account: P115Account }>()
const emit = defineEmits<{ changed: [] }>()
const cid = ref(props.account.playbackParentCid || '')
const selecting = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
watch(() => props.account, (next, previous) => {
  if (next.uid !== previous.uid || next.playbackParentCid !== previous.playbackParentCid) {
    cid.value = next.playbackParentCid || ''; selecting.value = false
  }
})
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await p115Request(props.token, `accounts/${props.account.id}/playback-directory`, { method: 'PUT', body: JSON.stringify({ parentCid: cid.value.trim(), revision: props.account.revision }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    notice.value = '秒传播放缓存目录已保存。'; emit('changed')
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存目录失败' }
  finally { busy.value = false }
}
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
  <form class="space-y-3 border-t border-slate-100 pt-5" @submit.prevent="save">
    <h3 class="text-sm font-semibold text-slate-800">秒传播放缓存目录</h3>
    <p class="text-xs leading-6 text-slate-500">仅用于绑定此账号的 Emby 用户。分享直取不需要目录；需要秒传时直接保存到此目录，不创建子目录，网盘文件由你手动删除。</p>
    <label class="block text-sm text-slate-700">秒传保存目录 ID<input v-model.trim="cid" :disabled="busy" pattern="[0-9]*" inputmode="numeric" placeholder="未设置；根目录请明确填写 0" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 px-3 py-2" /></label>
    <div class="flex flex-wrap gap-3"><button type="button" :disabled="busy || !account.configured" class="text-sm text-blue-600 disabled:opacity-40" @click="selecting = true">选择此账号的父目录</button><button type="submit" :disabled="busy" class="rounded-lg border border-blue-200 px-3 py-2 text-sm text-blue-600 disabled:opacity-40">{{ busy ? '保存中…' : '保存缓存目录' }}</button></div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
  </form>
  <P115DirectoryDialog v-if="selecting" :token="token" :account-id="account.id" :title="'选择 ' + account.name + ' 的秒传保存目录'" @close="selecting = false" @select="cid = $event.id; selecting = false; notice = '已选择，点击保存缓存目录后生效。'" />
</template>
