<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
const props = defineProps<{ token: string }>()
interface Child { requestId: string; userId: string; name: string; completed: boolean; paused: boolean; disabled: boolean; revoked: boolean; blocked: boolean; revision: number; stage: string; message: string }
interface Children { limit: number; used: number; items: Child[] }
const data = ref<Children>(), busy = ref(false), error = ref(''), notice = ref('')
const name = ref(''), password = ref(''), requestId = ref(''), retryName = ref(''), consent = ref(false)
const revokeID = ref(''), passwordID = ref(''), newPassword = ref('')
const lifecycle = new AbortController()
async function request(path = '', body?: object): Promise<Children> {
 const response = await fetch('/api/me/playback/children' + path, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000)]) })
 const value = await response.json().catch(() => null)
 if (!response.ok) throw new Error(value?.error || '子账号操作失败')
 return value
}
async function load() { try { data.value = await request() } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '状态读取失败' } }
function id() { const bytes = crypto.getRandomValues(new Uint8Array(16)); return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('') }
async function create() {
 if (busy.value || !consent.value) return
 if (!requestId.value) { requestId.value = id(); retryName.value = name.value.trim() }
 busy.value = true; error.value = ''; notice.value = ''
 try { data.value = await request('', { requestId: requestId.value, name: retryName.value, password: password.value }); requestId.value = ''; retryName.value = ''; name.value = ''; notice.value = '子账号已创建，请使用 Emby 登录。' }
 catch (e) { if (!lifecycle.signal.aborted) { error.value = e instanceof Error ? e.message : '创建结果未确认'; try { data.value = await request(); if (!data.value.items.some(child => child.requestId === requestId.value)) { requestId.value = ''; retryName.value = '' } } catch { /* Preserve the request ID until its result can be read. */ } } }
 finally { password.value = ''; busy.value = false }
}
function retry(child: Child) { requestId.value = child.requestId; retryName.value = child.name; name.value = child.name; password.value = ''; notice.value = '请重新输入密码，继续原创建任务。' }
async function action(child: Child, action: string) {
 if (busy.value) return
 busy.value = true; error.value = ''; notice.value = ''
 try { data.value = await request('/' + encodeURIComponent(child.userId), { action, revision: child.revision, ...(action === 'password' ? { password: newPassword.value } : {}) }); revokeID.value = ''; passwordID.value = ''; notice.value = action === 'password' ? '密码已更新；已登录设备不保证立即退出。' : '子账号状态已更新。' }
 catch (e) { if (!lifecycle.signal.aborted) { error.value = e instanceof Error ? e.message : '操作结果未确认'; await load() } }
 finally { newPassword.value = ''; busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
 <section v-if="data && (data.limit > 0 || data.items.length) || error" class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
  <div class="flex items-center justify-between gap-3"><h2 class="font-semibold">播放子账号</h2><button :disabled="busy" class="text-sm text-blue-600" @click="load">刷新子账号</button></div>
  <p v-if="data" class="text-sm">已使用 {{ data.used }} / {{ data.limit }} 个名额，不包含本人。创建中和停用账号仍占名额。</p>
  <p class="text-xs leading-6 text-slate-500">子账号只能登录 Emby，不能登录网站。共享你已绑定的个人网盘和秒传目录；你切换自己的播放来源不会改变共享网盘。媒体权限由管理员设置。</p>
  <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
  <form v-if="data && (requestId || data.used < data.limit)" class="space-y-3 rounded-xl border p-4 dark:border-slate-700" @submit.prevent="create">
   <h3 class="text-sm font-semibold">{{ requestId ? '继续创建子账号' : '创建播放子账号' }}</h3>
   <label class="block text-sm">子账号用户名<input v-model="name" :disabled="busy || !!requestId" required minlength="3" maxlength="32" autocomplete="off" class="mt-1 block w-full rounded-lg border bg-transparent p-2" /></label>
   <label class="block text-sm">子账号初始密码<input v-model="password" :disabled="busy" required type="password" minlength="8" maxlength="128" autocomplete="new-password" class="mt-1 block w-full rounded-lg border bg-transparent p-2" /></label>
   <label class="flex items-start gap-2 text-sm"><input v-model="consent" type="checkbox" class="mt-1" />我授权这些子账号使用我的个人网盘播放，并承担秒传文件占用的空间。</label>
   <button :disabled="busy || !consent" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40">{{ busy ? '处理中…' : requestId ? '继续创建' : '创建子账号' }}</button>
  </form>
  <article v-for="child in data?.items || []" :key="child.requestId" class="space-y-3 rounded-xl border p-4 dark:border-slate-700">
   <h3 class="break-all font-medium">{{ child.name }} <span class="text-xs text-slate-500">{{ child.revoked ? '已撤销' : !child.completed ? '创建待完成' : child.disabled ? '已停用' : child.paused ? '播放已暂停' : child.stage === 'ready' ? '播放已开通' : '播放待恢复' }}</span></h3>
   <p class="text-xs text-slate-500">{{ child.message }}</p>
   <button v-if="!child.completed && !child.revoked" :disabled="busy" class="text-sm text-blue-600" @click="retry(child)">继续此创建任务</button>
   <div v-if="child.completed && !child.revoked" class="flex flex-wrap gap-3 text-sm">
    <button :disabled="busy || child.blocked" class="text-blue-600 disabled:opacity-40" @click="action(child, child.paused ? 'resume' : 'pause')">{{ child.paused ? '恢复播放' : '暂停播放' }}</button>
    <button :disabled="busy || child.blocked" class="text-blue-600 disabled:opacity-40" @click="action(child, child.disabled ? 'enable' : 'disable')">{{ child.disabled ? '启用登录' : '停用登录' }}</button>
    <button :disabled="busy || child.blocked" class="text-blue-600 disabled:opacity-40" @click="passwordID = child.userId; newPassword = ''">重置密码</button>
    <button :disabled="busy || child.blocked" class="text-red-600 disabled:opacity-40" @click="revokeID = child.userId">移除子账号</button>
   </div>
   <form v-if="passwordID === child.userId && !child.revoked" class="space-y-2" @submit.prevent="action(child, 'password')"><label class="block text-sm">子账号新密码<input v-model="newPassword" required minlength="8" maxlength="128" type="password" autocomplete="new-password" class="mt-1 block w-full rounded-lg border bg-transparent p-2" /></label><p class="text-xs text-slate-500">更新密码不保证已登录设备立即退出；需要阻止使用时请停用账号。</p><button :disabled="busy" class="text-sm text-blue-600">保存新密码</button></form>
   <div v-if="revokeID === child.userId && !child.revoked" class="space-y-2 text-sm"><p>移除后释放名额且不能恢复共享，保留 Emby 账号及观看记录。后续播放请求将被拒绝，已发出的直链可能仍有效。</p><button :disabled="busy" class="mr-4 text-red-600" @click="action(child, 'revoke')">确认移除</button><button :disabled="busy" @click="revokeID = ''">取消</button></div>
  </article>
 </section>
</template>
