<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useToast } from '../composables/useToast'
import type { MemberProxyPlaybackState } from '../types/memberPlayback'

const props = defineProps<{ token: string; userId: string; name: string; enabled: boolean; revision: number; disabled: boolean; embyDisabled?: boolean }>()
const emit = defineEmits<{ updated: [state: MemberProxyPlaybackState]; busy: [value: boolean] }>()
const toast = useToast(), lifecycle = new AbortController()
const pending = ref(false), uncertain = ref(false), opened = ref(false), error = ref('')
const dialog = ref<HTMLDialogElement>(), cancelButton = ref<HTMLButtonElement>(), trigger = ref<HTMLButtonElement>()
const titleID = useId(), descriptionID = useId()
const blocked = computed(() => pending.value || props.disabled)
let previousOverflow = ''
watch(() => pending.value || opened.value, value => emit('busy', value), { flush: 'sync' })

const switchTitle = computed(() => pending.value ? '正在保存反代播放设置' : props.disabled ? '请先完成当前操作' :
 `${props.enabled ? '反代播放已启用，点击暂停' : '反代播放已暂停，点击恢复'}。仅控制 Cinema115 的新播放取链${props.embyDisabled ? '；Emby 账号本身已停用，仍需在 Emby 中恢复' : ''}`)
function validState(value: unknown): value is MemberProxyPlaybackState {
 const state = value as MemberProxyPlaybackState | undefined
 return !!state && state.userId === props.userId && typeof state.enabled === 'boolean' && Number.isSafeInteger(state.revision) && state.revision >= 0
}
function close() {
 if (pending.value) return
 dialog.value?.close(); opened.value = false
 document.body.style.overflow = previousOverflow
 void nextTick(() => { if (trigger.value?.isConnected && !trigger.value.disabled) trigger.value.focus({ preventScroll: true }) })
}
async function open() {
 if (blocked.value) return
 if (uncertain.value) { await request(); return }
 if (!props.enabled) { await request(true); return }
 previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'
 opened.value = true; error.value = ''
 await nextTick()
 if (lifecycle.signal.aborted) return
 dialog.value?.showModal(); cancelButton.value?.focus({ preventScroll: true })
}
async function request(enabled?: boolean) {
 if (blocked.value) return
 const writing = enabled !== undefined
 pending.value = true; error.value = ''
 try {
  const response = await fetch(`/api/admin/member-playback/${encodeURIComponent(props.userId)}/proxy-playback`, {
   method: writing ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
   signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000)]),
   body: writing ? JSON.stringify({ enabled, expectedRevision: props.revision }) : undefined,
  })
  const data = await response.json().catch(() => null)
  if (lifecycle.signal.aborted) return
  if (!response.ok) {
   uncertain.value = !data || data.unconfirmed === true || response.status >= 500
   if (!uncertain.value && validState(data?.playback)) emit('updated', data.playback)
   throw new Error(data?.error || '反代播放设置待核实，请重新读取')
  }
  if (!validState(data?.playback) || writing && data.playback.enabled !== enabled) { uncertain.value = true; throw new Error('未返回可核实的反代播放设置，请重新读取') }
  uncertain.value = false; emit('updated', data.playback)
  toast.success(`${props.name}：${writing ? data.playback.enabled ? '已恢复反代播放' : '已暂停反代播放' : '已核实反代播放设置'}`)
  pending.value = false
  if (opened.value) close()
 } catch (e) {
  if (lifecycle.signal.aborted) return
  // A transport error cannot prove that a local settings write failed.
  const transportError = !(e instanceof Error) || ['TypeError', 'TimeoutError', 'AbortError'].includes(e.name)
  if (transportError) uncertain.value = true
  error.value = transportError ? '连接中断或请求超时，反代播放设置待核实，请重新读取' : e instanceof Error ? e.message : '反代播放设置待核实，请重新读取'
  if (!opened.value) toast.error(`${props.name}：${error.value}`)
 } finally { pending.value = false }
}
onBeforeUnmount(() => {
 lifecycle.abort()
 if (opened.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow }
 emit('busy', false)
})
</script>

<template>
 <button v-if="uncertain" ref="trigger" type="button" class="account-verify" :disabled="blocked" :aria-label="`重新核实反代播放设置 ${name}`" title="提交结果待核实，重新读取本工具的反代播放设置" @click="request()">{{ pending ? '读取中' : '待核实' }}</button>
 <button v-else ref="trigger" type="button" role="switch" class="account-switch" :class="{ 'is-enabled': enabled, 'is-pending': pending }" :aria-checked="enabled" :aria-busy="pending" :aria-label="`反代播放：${name}`" :title="switchTitle" :disabled="blocked" @click="open">
  <span class="account-switch-track" aria-hidden="true"><span class="account-switch-thumb"><svg v-if="pending" class="account-spinner" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="5" stroke="currentColor" stroke-width="1.5" stroke-dasharray="22 10" /></svg></span></span>
  <span class="sr-only">{{ pending ? '提交中' : enabled ? '已启用' : '已停用' }}</span>
 </button>
 <Teleport to="body">
  <dialog v-if="opened" ref="dialog" class="account-confirm" :aria-labelledby="titleID" :aria-describedby="descriptionID" :aria-busy="pending" @cancel.prevent="close">
   <h2 :id="titleID">暂停反代播放</h2>
   <div :id="descriptionID" class="account-confirm-copy"><p>确定暂停 <strong>{{ name }}</strong> 的反代播放？</p><p>仅拦截该用户通过 Cinema115 发起的新播放取链。此开关不修改 Emby 账号及权限，登录、浏览、下载和其他工具不受影响。</p><p>关联子账号不受此开关影响。已签发的直链可能继续有效。</p></div>
   <p v-if="error" class="account-confirm-error" role="alert">{{ error }}</p>
   <footer><button ref="cancelButton" type="button" class="account-cancel" :disabled="pending" @click="close">{{ uncertain ? '关闭' : '取消' }}</button><button v-if="uncertain" type="button" class="account-recheck" :disabled="pending" @click="request()">{{ pending ? '读取中…' : '重新读取设置' }}</button><button v-else type="button" class="account-disable" :disabled="pending || disabled" @click="request(false)">{{ pending ? '提交中…' : '暂停播放' }}</button></footer>
  </dialog>
 </Teleport>
</template>

<style scoped>
.account-switch, .account-verify { display: inline-flex; flex: 0 0 52px; width: 52px; min-height: 44px; align-items: center; justify-content: center; border-radius: 9px; cursor: pointer; }
.account-switch-track { position: relative; width: 42px; height: 24px; border-radius: 12px; background: #cbd5e1; transition: background-color 160ms ease; }
.account-switch-thumb { position: absolute; top: 2px; left: 2px; display: grid; place-items: center; width: 20px; height: 20px; border-radius: 50%; background: #fff; color: #2563eb; box-shadow: 0 1px 3px #0f172a26; transition: transform 160ms ease; }
.account-switch-thumb svg { width: 12px; height: 12px; }
.is-enabled .account-switch-track { background: #2563eb; }
.is-enabled .account-switch-thumb { transform: translateX(18px); }
.account-switch:disabled, .account-verify:disabled { cursor: default; opacity: .5; }.account-switch.is-pending { opacity: .8; }
.account-switch:focus-visible, .account-verify:focus-visible, .account-confirm button:focus-visible { outline: 2px solid #6095ff; outline-offset: 3px; }
.account-verify { color: #96601d; font-size: 11px; white-space: nowrap; }
.account-spinner { animation: account-spin 1s linear infinite; }
.account-confirm { box-sizing: border-box; width: min(440px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); margin: auto; overflow-y: auto; padding: 24px; border: 0; border-radius: 18px; background: #fff; color: #192b47; box-shadow: 0 24px 80px #0f172a33; }
.account-confirm::backdrop { background: rgb(15 23 42 / 38%); }
.account-confirm h2 { font-size: 17px; font-weight: 650; }
.account-confirm-copy { margin-top: 16px; font-size: 13px; line-height: 1.8; color: #61718a; overflow-wrap: anywhere; }
.account-confirm-copy p + p { margin-top: 10px; }.account-confirm-copy strong { color: #192b47; font-weight: 600; }
.account-confirm-error { margin-top: 16px; font-size: 12px; line-height: 1.7; color: #be3446; overflow-wrap: anywhere; }
.account-confirm footer { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 10px; margin-top: 24px; }
.account-confirm button { min-height: 44px; padding: 0 16px; border-radius: 10px; font-size: 13px; font-weight: 600; cursor: pointer; }.account-confirm button:disabled { opacity: .5; cursor: default; }
.account-cancel { background: #f1f4f8; color: #43536b; }.account-disable { background: #be3446; color: #fff; }.account-recheck { background: #2563eb; color: #fff; }
.dark .account-switch-track { background: #475569; }.dark .is-enabled .account-switch-track { background: #3b82f6; }.dark .account-switch-thumb { box-shadow: 0 1px 3px #00000040; }.dark .account-verify { color: #dcb068; }
.dark .account-confirm { background: rgb(var(--color-slate-900)); color: #e6edf7; color-scheme: dark; }.dark .account-confirm-copy { color: #a6b3c7; }.dark .account-confirm-copy strong { color: #e6edf7; }.dark .account-cancel { background: rgb(var(--color-slate-800)); color: #d0daea; }.dark .account-confirm-error { color: #f095a2; }.dark .account-disable { background: #a83244; }.dark .account-recheck { background: #3769b5; }
@media (hover: hover) { .account-switch:not(:disabled):hover .account-switch-track { background: #b9c6d7; }.account-switch.is-enabled:not(:disabled):hover .account-switch-track { background: #1d4ed8; }.dark .account-switch:not(:disabled):hover .account-switch-track { background: #596b83; }.dark .account-switch.is-enabled:not(:disabled):hover .account-switch-track { background: #60a5fa; }.account-verify:not(:disabled):hover { background: rgb(37 99 235 / 8%); }.account-cancel:not(:disabled):hover { filter: brightness(.96); }.account-disable:not(:disabled):hover, .account-recheck:not(:disabled):hover { filter: brightness(1.08); } }
@keyframes account-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .account-switch-track, .account-switch-thumb { transition: none; }.account-spinner { animation: none; } }
@media (max-width: 380px) { .account-confirm { padding: 20px; } }
</style>
