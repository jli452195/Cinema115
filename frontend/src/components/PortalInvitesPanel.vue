<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ token: string; registrationPaused?: boolean; registrationError?: string }>()
defineEmits<{ editLibraries: [] }>()
interface Invite { id: string; note: string; createdBy: string; createdAt: number; expiresAt: number; revokedAt: number; usedAt: number; usedBy: string; username: string; playbackSource: string }
interface Generated extends Invite { code: string }
type CopyKind = 'code' | 'link' | 'all'
const invites = ref<Invite[]>([]), generated = ref<Generated[]>([])
const count = ref(1), days = ref(7), note = ref('')
const playbackSource = ref('personal')
const baseURL = ref(localStorage.getItem('invite_site_url') || location.origin)
const addressEditing = ref(false), hideConfirm = ref(false)
const offset = ref(0), hasMore = ref(false), generating = ref(false), loading = ref(false)
const revokingId = ref(''), confirmRevoke = ref('')
const formError = ref(''), recordError = ref(''), recordNotice = ref('')
const copying = ref(false)
const copyResult = ref<{ id: string; kind: CopyKind; success: boolean; value: string }>()
const manualCopy = ref<HTMLTextAreaElement>()
const resultSection = ref<HTMLElement>()
const lifecycle = new AbortController()
let listController: AbortController | undefined
const busy = computed(() => generating.value || !!revokingId.value)
const origin = computed(() => {
  try {
    const url = new URL(baseURL.value.trim())
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.pathname !== '/') return ''
    return url.origin
  } catch { return '' }
})
const usableGenerated = computed(() => generated.value.filter(invite => status(invite) === '未使用'))
const allLinks = computed(() => usableGenerated.value.map(invite => link(invite.code)).join('\n'))
function link(code: string) { return origin.value ? `${origin.value}/register#invite=${encodeURIComponent(code)}` : '' }
function status(invite: Invite) { return invite.usedAt ? '已使用' : invite.revokedAt ? '已撤销' : invite.expiresAt <= Date.now() / 1000 ? '已过期' : '未使用' }
function statusClass(invite: Invite) { return invite.usedAt ? 'is-used' : invite.revokedAt || invite.expiresAt <= Date.now() / 1000 ? 'is-inactive' : 'is-pending' }
function date(value: number) { return new Date(value * 1000).toLocaleString() }
async function request(path: string, body?: object, signal?: AbortSignal) {
  const response = await fetch(`/api/admin/invites${path}`, {
    method: body ? 'POST' : 'GET',
    headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.any([lifecycle.signal, ...(signal ? [signal] : []), AbortSignal.timeout(15000)]),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '邀请操作失败')
  return data
}
async function load(next = offset.value) {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  loading.value = true; recordError.value = ''
  try {
    const data = await request(`?offset=${next}`, undefined, controller.signal)
    if (controller.signal.aborted || lifecycle.signal.aborted) return
    invites.value = data.invites; hasMore.value = data.hasMore; offset.value = next
    // Keep freshly generated codes in memory, while reflecting statuses returned by the server.
    generated.value = generated.value.map(item => {
      const current = invites.value.find(invite => invite.id === item.id)
      return current ? { ...item, ...current, code: item.code } : item
    })
    confirmRevoke.value = ''
  } catch (reason) {
    if (!lifecycle.signal.aborted && !controller.signal.aborted) recordError.value = reason instanceof Error ? reason.message : '邀请列表读取失败'
  } finally { if (listController === controller) loading.value = false }
}
async function generate() {
  if (busy.value || !origin.value) return
  if (!Number.isInteger(count.value) || count.value < 1 || count.value > 20) { formError.value = '数量须为 1–20 的整数'; return }
  generating.value = true; formError.value = ''; hideConfirm.value = false
  try {
    const data = await request('', { count: count.value, days: days.value, note: note.value, playbackSource: playbackSource.value })
    if (lifecycle.signal.aborted) return
    generated.value = [...data.invites, ...generated.value]
    copyResult.value = undefined
    try { localStorage.setItem('invite_site_url', origin.value) } catch { /* Generated codes remain available if preferences cannot be saved. */ }
    await nextTick()
    resultSection.value?.focus({ preventScroll: true })
    resultSection.value?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
    await load(0)
  } catch (reason) {
    if (!lifecycle.signal.aborted) formError.value = reason instanceof Error ? reason.message : '生成邀请失败'
  } finally { generating.value = false }
}
async function revoke(invite: Invite) {
  if (busy.value || confirmRevoke.value !== invite.id || status(invite) !== '未使用') return
  revokingId.value = invite.id; recordError.value = ''; recordNotice.value = ''
  try {
    await request(`/${encodeURIComponent(invite.id)}/revoke`, {})
    if (lifecycle.signal.aborted) return
    generated.value = generated.value.filter(item => item.id !== invite.id)
    recordNotice.value = '邀请已撤销'
    confirmRevoke.value = ''
    await load()
  } catch (reason) {
    if (!lifecycle.signal.aborted) recordError.value = reason instanceof Error ? reason.message : '撤销邀请失败'
  } finally { revokingId.value = '' }
}
function hideGenerated() { generated.value = []; copyResult.value = undefined; hideConfirm.value = false }
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
async function copy(value: string, id: string, kind: CopyKind) {
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
  if (lifecycle.signal.aborted || !generated.value.length || (kind !== 'all' && !generated.value.some(invite => invite.id === id))) return
  copyResult.value = { id, kind, success, value }
  if (!success) {
    await nextTick()
    manualCopy.value?.focus({ preventScroll: true }); manualCopy.value?.select()
    manualCopy.value?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
  }
}
function copied(id: string, kind: CopyKind) { return copyResult.value?.id === id && copyResult.value.kind === kind && copyResult.value.success }
onMounted(() => { void load() })
onBeforeUnmount(() => { lifecycle.abort(); listController?.abort() })
</script>

<template>
  <div class="invite-workspace invite-theme">
    <form class="invite-card invite-generate-form" @submit.prevent="generate">
      <div class="invite-form-heading"><h2 class="invite-title-with-icon"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H4a1 1 0 0 0-1 1v4a2 2 0 0 1 0 4v4a1 1 0 0 0 1 1h16a1 1 0 0 0 1-1v-4a2 2 0 0 1 0-4V6a1 1 0 0 0-1-1h-5M12 4v4m0 3v2m0 3v4"/></svg>生成注册邀请</h2><p class="invite-muted invite-caption">邀请仅可注册一次，生成后请复制链接。</p></div>
      <p v-if="registrationPaused" class="invite-message invite-message-warning">尚未选择默认媒体库，受邀人暂时无法注册。<button type="button" class="invite-button invite-button-text" @click="$emit('editLibraries')">设置媒体库</button></p>
      <p v-else-if="registrationError" class="invite-message invite-message-warning">默认媒体库设置暂未确认，请检查后再分享邀请。<button type="button" class="invite-button invite-button-text" @click="$emit('editLibraries')">检查设置</button></p>
      <fieldset class="invite-source-fieldset" :disabled="busy">
        <legend class="invite-field-label">播放开通方式</legend>
        <div class="invite-source-options">
          <label class="invite-source-choice" :class="{ 'is-selected': playbackSource === 'personal' }"><input v-model="playbackSource" type="radio" name="invite-playback-source" value="personal" class="invite-sr-only" /><span class="invite-source-title"><span>自行绑定 115</span><svg class="invite-radio-indicator" aria-hidden="true" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" fill="none" stroke="currentColor" stroke-width="1.6"/><circle class="invite-radio-dot" cx="10" cy="10" r="4.5" fill="currentColor"/></svg></span><span class="invite-source-caption">受邀人使用自己的网盘</span></label>
          <label class="invite-source-choice" :class="{ 'is-selected': playbackSource === 'assigned' }"><input v-model="playbackSource" type="radio" name="invite-playback-source" value="assigned" class="invite-sr-only" /><span class="invite-source-title"><span>管理员共享小号池</span><svg class="invite-radio-indicator" aria-hidden="true" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" fill="none" stroke="currentColor" stroke-width="1.6"/><circle class="invite-radio-dot" cx="10" cy="10" r="4.5" fill="currentColor"/></svg></span><span class="invite-source-caption">由共享池提供播放网盘</span></label>
        </div>
        <p class="invite-source-description invite-muted invite-caption">{{ playbackSource === 'assigned' ? '注册后使用共享池，无需绑定小号；池不可用时停止新取链，不回退主网盘。' : '受邀人可先注册并浏览媒体库，绑定自己的 115 后才能播放。' }}</p>
      </fieldset>
      <div class="invite-form-grid">
        <label class="invite-field-label">数量<input v-model.number="count" type="number" inputmode="numeric" required min="1" max="20" step="1" :disabled="busy" class="invite-input" /></label>
        <label class="invite-field-label">有效期<select v-model.number="days" :disabled="busy" class="invite-input"><option :value="1">1 天</option><option :value="7">7 天</option><option :value="30">30 天</option></select></label>
        <label class="invite-field-label invite-field-wide">备注<span class="invite-muted invite-optional">选填</span><input v-model="note" maxlength="200" placeholder="例如：给朋友小王" :disabled="busy" class="invite-input" /></label>
      </div>
      <div class="invite-address">
        <div class="invite-address-summary"><div><span class="invite-muted invite-caption">分享地址</span><p>{{ origin || baseURL || '尚未填写' }}</p></div><button type="button" class="invite-button invite-button-text" :disabled="busy" :aria-expanded="addressEditing" aria-controls="invite-address-field" @click="addressEditing = !addressEditing">{{ addressEditing ? '收起' : '修改' }}</button></div>
        <div v-if="addressEditing || !origin" id="invite-address-field" class="invite-address-editor"><label class="invite-field-label">邀请链接的站点地址<input v-model="baseURL" type="url" required :disabled="busy" class="invite-input" placeholder="https://cinema.example.com" /></label></div>
        <p class="invite-muted invite-caption">外网分享请填写对方能访问的域名和端口。</p>
        <p v-if="!origin" role="alert" class="invite-message invite-message-error">请输入 HTTP(S) 站点地址，不含路径、参数或账号信息。</p>
      </div>
      <p v-if="formError" role="alert" class="invite-message invite-message-error">{{ formError }}</p>
      <div class="invite-form-actions"><span class="invite-muted invite-caption">完整邀请码仅本次生成后显示</span><button type="submit" :disabled="busy || !origin" class="invite-button invite-button-primary"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>{{ generating ? '生成中' : '生成邀请' }}</button></div>
    </form>

    <aside class="invite-settings"><slot name="settings" /></aside>

    <section v-if="generated.length" ref="resultSection" tabindex="-1" aria-labelledby="invite-results-heading" class="invite-card invite-results">
      <div class="invite-section-heading"><div><h2 id="invite-results-heading">请保存新生成的邀请</h2><p class="invite-muted invite-caption">刷新或隐藏后无法再次查看完整邀请码和链接。</p></div><button type="button" :disabled="copying" class="invite-button invite-button-text" @click="hideConfirm = !hideConfirm">隐藏</button></div>
      <div v-if="hideConfirm" class="invite-hide-confirm"><p class="invite-caption">确认已保存？隐藏后无法重新查看。</p><div><button type="button" class="invite-button invite-button-secondary" @click="hideConfirm = false">保留显示</button><button type="button" class="invite-button invite-button-danger" @click="hideGenerated">确认隐藏</button></div></div>
      <div v-if="generated.length > 1" class="invite-batch-action"><span class="invite-muted invite-caption">本页新生成 {{ generated.length }} 个，可分享 {{ usableGenerated.length }} 个</span><button type="button" :disabled="!origin || copying || !usableGenerated.length" class="invite-button invite-button-primary" @click="copy(allLinks, 'batch', 'all')">{{ copied('batch', 'all') ? '已复制全部链接' : '复制全部链接' }}</button></div>
      <article v-for="invite in generated" :key="invite.id" class="invite-generated-row">
        <div class="invite-generated-meta"><p>{{ invite.note || '注册邀请' }}</p><span v-if="status(invite) !== '未使用'" class="invite-status" :class="statusClass(invite)">{{ status(invite) }}</span><span class="invite-muted invite-caption">{{ date(invite.expiresAt) }} 到期</span></div>
        <p class="invite-muted invite-caption">{{ invite.playbackSource === 'assigned' ? '管理员共享小号池' : '自行绑定 115 后播放' }}</p>
        <textarea :value="link(invite.code)" readonly aria-label="邀请链接" rows="2" class="invite-input invite-link-value" @focus="($event.target as HTMLTextAreaElement).select()"></textarea>
        <div class="invite-generated-actions"><button type="button" :disabled="!origin || copying || status(invite) !== '未使用'" class="invite-button invite-button-primary" @click="copy(link(invite.code), invite.id, 'link')">{{ copied(invite.id, 'link') ? '链接已复制' : '复制邀请链接' }}</button><button type="button" :disabled="copying || status(invite) !== '未使用'" class="invite-button invite-button-text" @click="copy(invite.code, invite.id, 'code')">{{ copied(invite.id, 'code') ? '邀请码已复制' : '复制邀请码' }}</button></div>
        <div class="invite-code-value"><span class="invite-muted invite-caption">邀请码</span><code>{{ invite.code }}</code></div>
      </article>
      <div v-if="copyResult" class="invite-copy-feedback"><p role="status" class="invite-caption" :class="copyResult.success ? 'invite-success-text' : 'invite-warning-text'">{{ copyResult.success ? (copyResult.kind === 'all' ? '全部可分享链接已复制' : copyResult.kind === 'code' ? '邀请码已复制' : '邀请链接已复制') : '自动复制失败，已选中下方内容，请按 Ctrl+C 或长按复制。' }}</p><textarea v-if="!copyResult.success" ref="manualCopy" :value="copyResult.value" readonly aria-label="手动复制内容" rows="3" class="invite-input invite-link-value" @focus="($event.target as HTMLTextAreaElement).select()"></textarea></div>
    </section>

    <section class="invite-card invite-records" aria-labelledby="invite-records-heading" :aria-busy="loading">
      <div class="invite-section-heading"><div><h2 id="invite-records-heading" class="invite-title-with-icon"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10a9 9 0 1 1 2 8M3 5v5h5M12 7v5l3 2"/></svg>邀请记录</h2><p class="invite-muted invite-caption">查看使用状态，管理尚未使用的邀请。</p></div><button type="button" :disabled="busy || loading" class="invite-button invite-button-text" @click="load()">{{ loading ? '刷新中' : '刷新' }}</button></div>
      <p v-if="recordError" role="alert" class="invite-message invite-message-error">{{ recordError }}</p>
      <p v-if="recordNotice" role="status" class="invite-message invite-message-success">{{ recordNotice }}</p>
      <div v-if="!invites.length" class="invite-empty"><p>{{ loading ? '正在读取邀请记录' : recordError ? '记录读取失败，请重试' : '暂无邀请记录' }}</p><p class="invite-muted invite-caption">{{ loading || recordError ? '已生成的完整链接仍保留在上方' : '生成邀请后，可在这里查看到期和使用情况。' }}</p></div>
      <div v-else class="invite-record-list">
        <div class="invite-record-columns" aria-hidden="true"><span>邀请 / 状态</span><span>播放方式</span><span>使用 / 到期</span><span>操作</span></div>
        <article v-for="invite in invites" :key="invite.id" class="invite-record">
          <div class="invite-record-primary"><div class="invite-record-title"><h3>{{ invite.note || '注册邀请' }}</h3><span class="invite-status" :class="statusClass(invite)">{{ status(invite) }}</span></div><p class="invite-muted invite-caption">编号 {{ invite.id.slice(0, 8) }}</p><p class="invite-muted invite-caption">{{ date(invite.createdAt) }} 创建</p><p class="invite-muted invite-caption">创建人 {{ invite.createdBy }}</p></div>
          <p class="invite-record-source">{{ invite.playbackSource === 'assigned' ? '管理员共享小号池' : '自行绑定 115' }}</p>
          <div class="invite-record-time"><p>{{ invite.usedAt ? invite.username || invite.usedBy : '到期时间' }}</p><p class="invite-muted invite-caption">{{ date(invite.usedAt || invite.expiresAt) }}</p></div>
          <div class="invite-record-action"><button v-if="status(invite) === '未使用'" type="button" :disabled="busy" class="invite-button invite-button-text invite-danger-text" :aria-expanded="confirmRevoke === invite.id" @click="confirmRevoke = confirmRevoke === invite.id ? '' : invite.id">撤销</button><span v-else class="invite-muted invite-caption">无需操作</span></div>
          <div v-if="confirmRevoke === invite.id" class="invite-revoke-confirm"><p class="invite-caption">撤销后此邀请立即失效，无法恢复。</p><div><button type="button" :disabled="busy" class="invite-button invite-button-secondary" @click="confirmRevoke = ''">取消</button><button type="button" :disabled="busy" class="invite-button invite-button-danger" @click="revoke(invite)">{{ revokingId === invite.id ? '撤销中' : '确认撤销' }}</button></div></div>
        </article>
      </div>
      <div v-if="offset || hasMore" class="invite-pagination"><span class="invite-muted invite-caption">第 {{ offset / 50 + 1 }} 页</span><div><button type="button" :disabled="!offset || loading || busy" class="invite-button invite-button-secondary" @click="load(offset - 50)">上一页</button><button type="button" :disabled="!hasMore || loading || busy" class="invite-button invite-button-secondary" @click="load(offset + 50)">下一页</button></div></div>
    </section>
  </div>
</template>
