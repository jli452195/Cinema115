<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ token: string }>()
interface Invite { embyName?: string; id: string; note: string; createdBy: string; createdAt: number; expiresAt: number; revokedAt: number; usedAt: number; usedBy: string; username: string; playbackSource: string }
interface Generated extends Invite { code: string }
const invites = ref<Invite[]>([]), generated = ref<Generated[]>([])
const count = ref(1), days = ref(7), note = ref('')
const playbackSource = ref('personal')
const baseURL = ref(localStorage.getItem('invite_site_url') || location.origin)
const offset = ref(0), hasMore = ref(false), busy = ref(false), loading = ref(false)
const error = ref(''), notice = ref('')
const copying = ref(false)
const copyResult = ref<{ id: string; kind: 'code' | 'link'; success: boolean }>()
const manualCopy = ref<HTMLTextAreaElement>()
const lifecycle = new AbortController()
const origin = computed(() => {
  try {
    const url = new URL(baseURL.value.trim())
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.pathname !== '/') return ''
    return url.origin
  } catch { return '' }
})
function link(code: string) { return origin.value ? `${origin.value}/register#invite=${encodeURIComponent(code)}` : '' }
function status(invite: Invite) { return invite.usedAt ? '已使用' : invite.revokedAt ? '已撤销' : invite.expiresAt <= Date.now() / 1000 ? '已过期' : '未使用' }
function date(value: number) { return new Date(value * 1000).toLocaleString() }
async function request(path: string, body?: object) {
  const response = await fetch(`/api/admin/invites${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '邀请操作失败')
  return data
}
async function load(next = offset.value) {
  if (loading.value) return
  loading.value = true
  try { const data = await request(`?offset=${next}`); invites.value = data.invites; hasMore.value = data.hasMore; offset.value = next }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '邀请列表读取失败' }
  finally { loading.value = false }
}
async function generate() {
  if (busy.value || !origin.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const data = await request('', { count: count.value, days: days.value, note: note.value, playbackSource: playbackSource.value })
    generated.value = [...data.invites, ...generated.value]
    localStorage.setItem('invite_site_url', origin.value)
    await load(0)
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '生成邀请失败'
  } finally { busy.value = false }
}
async function revoke(invite: Invite) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await request(`/${encodeURIComponent(invite.id)}/revoke`, {})
    generated.value = generated.value.filter(item => item.id !== invite.id)
    notice.value = '邀请已撤销'
    await load()
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '撤销邀请失败' }
  finally { busy.value = false }
}
function legacyCopy(value: string) {
  const previous = document.activeElement
  const box = document.createElement('textarea')
  box.value = value; box.readOnly = true
  box.style.cssText = 'position:fixed;left:0;top:0;opacity:0;font-size:16px;pointer-events:none'
  document.body.appendChild(box)
  try {
    box.focus({ preventScroll: true }); box.select(); box.setSelectionRange(0, value.length)
    return document.execCommand('copy')
  } finally {
    box.remove()
    if (previous instanceof HTMLElement) previous.focus({ preventScroll: true })
  }
}
async function copy(value: string, id: string, kind: 'code' | 'link') {
  if (!value || copying.value) return
  copying.value = true; copyResult.value = undefined
  let success = false
  try {
    if (window.isSecureContext && navigator.clipboard?.writeText) {
      try { await navigator.clipboard.writeText(value); success = true } catch { /* Try the HTTP-compatible fallback. */ }
    }
    if (!success) success = legacyCopy(value)
  } catch { /* Keep the value selectable when both clipboard mechanisms fail. */ }
  finally { copying.value = false }
  if (lifecycle.signal.aborted || !generated.value.some(invite => invite.id === id)) return
  copyResult.value = { id, kind, success }
  if (!success) {
    await nextTick()
    manualCopy.value?.focus({ preventScroll: true }); manualCopy.value?.select()
  }
}
onMounted(() => { void load() })
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <div class="space-y-5">
    <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300">{{ notice }}</p>
    <form class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900" @submit.prevent="generate">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">生成注册邀请</h2>
      <p class="text-sm text-slate-500 dark:text-slate-400">每个邀请码限注册一个普通网站账号。完整码和链接仅本次生成后显示，请及时复制。</p>
      <label class="block text-sm text-slate-600 dark:text-slate-300">播放开通方式<select v-model="playbackSource" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950"><option value="personal">自行绑定 115（默认）</option><option value="primary">朋友邀请：允许使用主账号</option></select></label>
      <p class="text-sm text-slate-500 dark:text-slate-400">{{ playbackSource === 'primary' ? '受邀人注册 Emby 后可使用主账号播放，无需扫码。请仅发给信任的朋友。' : '受邀人可先注册 Emby 并浏览媒体库，绑定自己的 115 后才能播放。' }}</p>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="text-sm text-slate-600 dark:text-slate-300">数量<input v-model.number="count" type="number" required min="1" max="20" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /></label>
        <label class="text-sm text-slate-600 dark:text-slate-300">有效期<select v-model.number="days" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950"><option :value="1">1 天</option><option :value="7">7 天</option><option :value="30">30 天</option></select></label>
        <label class="text-sm text-slate-600 dark:text-slate-300 sm:col-span-2">备注<input v-model="note" maxlength="200" placeholder="例如：给朋友小王" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /></label>
        <label class="text-sm text-slate-600 dark:text-slate-300 sm:col-span-2">邀请链接的站点地址<input v-model="baseURL" type="url" required class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /><span class="mt-2 block text-xs text-slate-500 dark:text-slate-400">默认当前地址；发给外网用户时填写他们能访问的 HTTP(S) 域名和端口，不含路径。</span></label>
      </div>
      <p v-if="!origin" class="text-sm text-red-600 dark:text-red-300">请输入有效的站点地址，例如 https://cinema.example.com</p>
      <button :disabled="busy || !origin" class="rounded-lg bg-blue-600 px-5 py-2.5 text-sm text-white disabled:opacity-50">{{ busy ? '处理中…' : '生成邀请' }}</button>
    </form>
    <section v-if="generated.length" aria-label="新生成的邀请" class="space-y-4 rounded-2xl border border-blue-200 bg-blue-50/50 p-5 dark:border-blue-400/20 dark:bg-blue-400/5">
      <div class="flex flex-wrap justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">请保存新生成的邀请</h2><button class="text-sm text-slate-500 dark:text-slate-400" @click="generated = []">隐藏完整邀请码</button></div>
      <div v-for="invite in generated" :key="invite.id" class="space-y-2 border-t border-blue-200/50 pt-3 dark:border-blue-400/20">
        <p class="text-xs text-slate-500 dark:text-slate-400">{{ invite.note || '注册邀请' }} · {{ date(invite.expiresAt) }} 到期</p>
        <p class="text-xs text-slate-500 dark:text-slate-400">{{ invite.embyName ? 'Emby 关联：' + invite.embyName : invite.playbackSource === 'primary' ? '允许使用主账号播放' : '需绑定自己的 115 才能播放' }}</p>
        <div class="flex flex-wrap items-center gap-2"><code class="break-all text-sm text-slate-800 dark:text-slate-200">{{ invite.code }}</code><button type="button" :disabled="copying" class="text-xs text-blue-600 disabled:opacity-50 dark:text-blue-300" @click="copy(invite.code, invite.id, 'code')">复制邀请码</button></div>
        <textarea :value="link(invite.code)" readonly aria-label="邀请链接" rows="2" class="w-full resize-none rounded-lg border border-slate-200 bg-white p-2 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-300" @focus="($event.target as HTMLTextAreaElement).select()"></textarea>
        <button type="button" :disabled="!origin || copying" class="text-sm text-blue-600 disabled:opacity-50 dark:text-blue-300" @click="copy(link(invite.code), invite.id, 'link')">复制邀请链接</button>
        <template v-if="copyResult?.id === invite.id">
          <p role="status" class="text-xs" :class="copyResult.success ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">{{ copyResult.success ? (copyResult.kind === 'code' ? '邀请码已复制' : '邀请链接已复制') : '自动复制失败，已选中下方内容，请按 Ctrl+C 或长按复制。' }}</p>
          <textarea v-if="!copyResult.success" :ref="el => manualCopy = el as HTMLTextAreaElement" :value="copyResult.kind === 'code' ? invite.code : link(invite.code)" readonly aria-label="手动复制内容" rows="2" class="w-full rounded-lg border border-amber-300 bg-white p-2 text-xs text-slate-700 dark:border-amber-500/40 dark:bg-slate-950 dark:text-slate-300" @focus="($event.target as HTMLTextAreaElement).select()"></textarea>
        </template>
      </div>
    </section>
    <section class="rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
      <div class="mb-4 flex justify-between"><h2 class="font-semibold text-slate-900 dark:text-slate-100">邀请记录</h2><button :disabled="busy || loading" class="text-sm text-blue-600 disabled:opacity-50 dark:text-blue-300" @click="load()">刷新</button></div>
      <p v-if="loading" class="py-4 text-sm text-slate-500 dark:text-slate-400">正在加载…</p>
      <p v-else-if="!invites.length" class="py-4 text-sm text-slate-500 dark:text-slate-400">暂无邀请记录。</p>
      <div v-for="invite in invites" :key="invite.id" class="flex flex-wrap items-center justify-between gap-3 border-t border-slate-100 py-4 dark:border-slate-800">
        <div class="min-w-0 space-y-1"><p class="break-words text-sm font-medium text-slate-800 dark:text-slate-200">{{ invite.note || '注册邀请' }} <span class="ml-2 text-xs text-slate-500 dark:text-slate-400">{{ status(invite) }}</span></p><p class="text-xs text-slate-500 dark:text-slate-400">编号 {{ invite.id.slice(0, 8) }} · {{ date(invite.createdAt) }} 创建 · {{ invite.createdBy }}</p><p class="text-xs text-slate-500 dark:text-slate-400">{{ invite.usedAt ? `${invite.username} 于 ${date(invite.usedAt)} 注册` : `${date(invite.expiresAt)} 到期` }}</p></div>
        <span class="text-xs text-slate-500 dark:text-slate-400">{{ invite.embyName ? 'Emby 关联：' + invite.embyName : invite.playbackSource === 'primary' ? '主账号播放' : '自带 115' }}</span>
        <button v-if="status(invite) === '未使用'" :disabled="busy" class="text-sm text-red-600 disabled:opacity-50 dark:text-red-300" @click="revoke(invite)">撤销</button>
      </div>
      <div v-if="offset || hasMore" class="mt-4 flex justify-end gap-4 text-sm text-slate-600 dark:text-slate-300"><button :disabled="!offset || loading || busy" class="disabled:opacity-30" @click="load(offset - 50)">上一页</button><button :disabled="!hasMore || loading || busy" class="disabled:opacity-30" @click="load(offset + 50)">下一页</button></div>
    </section>
  </div>
</template>
