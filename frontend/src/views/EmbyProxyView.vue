<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountSelect from '../components/P115AccountSelect.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
import { embyRequest } from '../types/emby'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
interface Config { enabled: boolean; port: number; url: string; accountId: string; strmBaseUrl: string; embyPathPrefix: string }
interface Status { config: Config; listening: boolean; error: string }
const config = ref<Config>({ enabled: false, port: 8097, url: '', accountId: '', strmBaseUrl: '', embyPathPrefix: '' })
const status = ref<Status>()
const accounts = ref<P115Account[]>([])
const busy = ref(true)
const loaded = ref(false)
const error = ref('')
const notice = ref('')
const lifecycle = new AbortController()
const entry = computed(() => `http://${window.location.hostname}:${status.value?.config.port || 8097}`)
const mapping = computed(() => `${config.value.port}:${config.value.port}`)

async function load() {
  busy.value = true; error.value = ''
  try {
    const results = await Promise.allSettled([
      embyRequest(props.token, 'proxy', lifecycle.signal),
      p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal }),
    ])
    const [proxy, accountList] = results
    if (proxy.status === 'fulfilled') { status.value = proxy.value; config.value = { ...proxy.value.config } }
    if (accountList.status === 'fulfilled') accounts.value = accountList.value.accounts
    const failed = results.find(result => result.status === 'rejected')
    if (failed?.status === 'rejected') throw failed.reason
    loaded.value = true
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '加载反代配置失败' }
  finally { busy.value = false }
}
async function save() {
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const result: Status = await embyRequest(props.token, 'proxy', lifecycle.signal, { ...config.value })
    status.value = result; config.value = { ...result.config }
    notice.value = result.listening ? '配置已保存，反代端口已开始监听。请使用反代入口连接 Emby。' : '配置已保存，反代已停用。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存反代配置失败' }
  finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="Emby 302 反代" description="通过 Emby 登录与播放权限校验，将分享 STRM 的直放请求跳转到 115。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions>
      <RouterLink :to="{ path: '/logs', query: { module: 'Emby 302', level: 'ALL' } }" class="shrink-0 rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm font-medium text-slate-700 hover:bg-slate-50">查看日志</RouterLink>
      <button :disabled="busy || !loaded" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40" @click="save">{{ busy ? '正在处理…' : '保存配置' }}</button>
    </template>
    <div class="space-y-5">
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <button v-if="!loaded && !busy" class="rounded-lg border px-4 py-2 text-sm" @click="load">重新加载</button>
      <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-800">{{ notice }}</p>
      <form class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7" @submit.prevent="save">
        <fieldset :disabled="busy || !loaded" class="space-y-6 disabled:opacity-50">
          <SettingSwitch v-model="config.enabled" label="启用 Emby 302 反代" description="保存后立即生效。播放器连接下面的反代端口，原有 STRM 无需重新生成。" />
          <div class="grid gap-5 sm:grid-cols-2">
            <label class="text-sm text-slate-700">Emby 原服务器地址<input v-model.trim="config.url" type="url" :required="config.enabled" placeholder="http://192.168.0.3:8096" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400">填写实际 Emby 地址，不能填写本反代入口。Docker 中 localhost 指向 Cinema115 容器。</span></label>
            <label class="text-sm text-slate-700">反代监听端口<input v-model.number="config.port" type="number" min="1024" max="65535" required class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400">建议 8097。不能与管理服务或其他程序使用相同端口；更换端口后需更新播放器连接地址。</span></label>
            <div><P115AccountSelect id="emby-proxy-account" v-model="config.accountId" :accounts="accounts" /><p class="mt-2 text-xs leading-5 text-slate-400">本功能独立选择取直链账号，账号失效时停止解析。<RouterLink to="/helper115" class="text-blue-600">管理账号</RouterLink></p></div>
            <label class="text-sm text-slate-700">STRM 中的 Cinema115 来源地址<input v-model.trim="config.strmBaseUrl" type="url" :required="config.enabled" placeholder="http://192.168.0.3:8115" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400">填写现有 STRM 第一行的协议、主机与端口，不含 /api/ 和查询参数。仅处理这个来源的分享链接。</span></label>
            <label class="text-sm text-slate-700 sm:col-span-2">Emby 中对应 STRM 根目录的路径（按需填写）<input v-model.trim="config.embyPathPrefix" placeholder="例如 /media，对应 Cinema115 容器内的 /strm" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400">Emby 返回本地 .strm 路径时使用。该路径下的相对目录必须与 Cinema115 的 STRM 根目录一致；如果返回的是 STRM 内的 HTTP 链接，可留空。</span></label>
          </div>
        </fieldset>
      </form>
      <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
        <div class="flex items-center justify-between"><h2 class="font-semibold text-slate-900">运行状态</h2><span class="rounded-full px-3 py-1 text-xs" :class="status?.listening ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'">{{ status?.listening ? '正在监听' : '未监听' }}</span></div>
        <p v-if="status?.error" role="alert" class="mt-3 text-sm text-red-600">{{ status.error }}</p>
        <p class="mt-4 break-all text-sm text-slate-700">Emby 客户端入口示例：<code>{{ entry }}</code></p>
        <p class="mt-2 text-xs leading-6 text-slate-500">使用原 Emby 用户登录。若有独立域名、HTTPS 网关或不同的宿主机端口，请使用实际对外地址。监听成功只表示服务已启动，仍需实际验证客户端播放与拖动。</p>
      </section>
      <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
        <h2 class="font-semibold text-slate-900">NAS / Docker 端口</h2>
        <p class="mt-3 text-sm leading-7 text-slate-600">桥接网络需要在容器设置中添加 TCP 端口映射 <code class="rounded bg-slate-100 px-2 py-1">{{ mapping }}</code>（宿主机端口 : 容器端口）。项目 compose 使用 host 网络，无需映射；仅切换为桥接网络时需要配置映射。网页保存不会自动修改 Docker。</p>
        <p class="mt-3 text-xs leading-6 text-slate-500">使用 host 网络时通常无需映射，但端口不能与 NAS 上的其他服务冲突。直放成功时视频流量由客户端直接访问 115；转码与封装转换已关闭，不兼容原文件格式时请更换播放器；字幕、海报和播放进度继续由 Emby 处理。本次不包含朋友账号秒传与播放缓存清理。</p>
      </section>
    </div>
  </ManagementLayout>
</template>
