<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import MemberPlaybackAdmin from '../components/MemberPlaybackAdmin.vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import InstantUploadPanel from '../components/InstantUploadPanel.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
import { embyRequest } from '../types/emby'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
interface Config { enabled: boolean; debug: boolean; prewarm: boolean; prepareFiles: boolean; directLinkCacheMinutes: number; port: number; url: string; accountId: string; embyPathPrefix: string }
interface Status { config: Config; listening: boolean; error: string }
const config = ref<Config>({ enabled: false, debug: false, prewarm: false, prepareFiles: true, directLinkCacheMinutes: 9, port: 8097, url: '', accountId: '', embyPathPrefix: '' })
const status = ref<Status>()
const mediaSettings = ref<InstanceType<typeof MemberPlaybackAdmin>>()
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
    if (proxy.status === 'fulfilled') { status.value = proxy.value; config.value = { directLinkCacheMinutes: 9, debug: false, prewarm: false, prepareFiles: true, ...proxy.value.config } }
    if (accountList.status === 'fulfilled') accounts.value = accountList.value.accounts
    const failed = results.find(result => result.status === 'rejected')
    if (failed?.status === 'rejected') throw failed.reason
    loaded.value = true
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '加载反代配置失败' }
  finally { busy.value = false }
}
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  let mediaSaved = false
  try {
    if (config.value.enabled && !await mediaSettings.value?.save()) { error.value = '媒体访问设置尚未保存，请检查下方提示；反代配置未更改。'; return }
    mediaSaved = config.value.enabled
    const result: Status = await embyRequest(props.token, 'proxy', lifecycle.signal, { ...config.value })
    status.value = result; config.value = { ...result.config }
    notice.value = result.listening ? '配置已保存，302 已接管播放，用户权限正在自动同步。请通过反代入口连接 Emby。' : '302 已关闭，停止接管新请求；现有 Emby 权限保留。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = (mediaSaved ? '媒体访问设置已保存，反代连接未更新：' : '') + (reason instanceof Error ? reason.message : '保存反代配置失败') }
  finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="Emby 302 与媒体访问" description="开启 302 接管原文件播放，有可用网盘的用户自动开通；下载与邀请注册单独管理。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions>
      <RouterLink to="/emby/cache" class="shrink-0 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-4 py-2.5 text-sm font-medium text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950">缓存管理</RouterLink>
      <RouterLink :to="{ path: '/logs', query: { module: 'Emby 302', level: 'ALL' } }" class="shrink-0 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-4 py-2.5 text-sm font-medium text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950">查看日志</RouterLink>
      <button :disabled="busy || !loaded" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40" @click="save">{{ busy ? '正在处理…' : '保存配置' }}</button>
    </template>
    <div class="space-y-5">
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <button v-if="!loaded && !busy" class="rounded-lg border px-4 py-2 text-sm" @click="load">重新加载</button>
      <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-800">{{ notice }}</p>
      <form class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm md:p-7" @submit.prevent="save">
        <fieldset :disabled="busy || !loaded" class="space-y-6 disabled:opacity-50">
          <P115PrimaryStatus v-if="loaded" :accounts="accounts" />
          <SettingSwitch v-model="config.enabled" label="启用 302 并接管播放" description="开启后自动为有可用网盘账号的用户同步播放权限；绑定失效或管理员停用时拒绝访问。关闭后停止反代入口和常规权限同步，不恢复历史 Emby 权限。" />
          <SettingSwitch v-model="config.debug" label="反代调试日志" description="保存后生效。开启后记录播放及预热的步骤时间轴、Emby 查询、等待预热、秒传分段耗时和 HTTP 连接计时，并可展开查看完整 CDN 直链与请求响应头。关闭后保留常规播放结果、错误和总耗时。图片、进度和普通透传每 30 秒汇总，异常单独记录。Cookie、登录令牌及未知请求头的值隐藏，不记录请求正文。" />
          <SettingSwitch v-model="config.prepareFiles" label="提前准备秒传文件" description="默认开启，保存后生效。收到播放信息后，为唯一或明确选中的 redirect115 媒体源准备绑定账号的文件，已有记录直接复用；正式播放时按播放器 UA 取链。主账号直取和分享直取不提前创建文件。打开详情但未播放也可能产生网盘文件，需手动删除；能否提前触发取决于播放器。关闭后，真正播放时仍会按需秒传。" />
          <SettingSwitch v-model="config.prewarm" label="提前获取播放直链" description="默认关闭，独立于文件准备设置。开启后提前执行完整取链流程，必要时也会秒传。相同 UA 可复用有效直链，不同 UA 需重新取链；为减少 115 请求，通常保持关闭即可。" />
          <div class="grid gap-5 sm:grid-cols-2">
            <label class="text-sm text-slate-700 dark:text-slate-300">直链缓存时间（分钟）<input v-model.number="config.directLinkCacheMinutes" type="number" min="0" max="30" step="1" required class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400 dark:text-slate-500">默认 9 分钟，0 为关闭。命中不延长到期时间；修改后保存生效。缓存不能延长 115 直链本身的有效期。</span></label>
            <label class="text-sm text-slate-700 dark:text-slate-300">Emby 原服务器地址<input v-model.trim="config.url" type="url" :required="config.enabled" placeholder="http://192.168.0.3:8096" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400 dark:text-slate-500">填写实际 Emby 地址，不能填写本反代入口。Docker 中 localhost 指向 Cinema115 容器。</span></label>
            <label class="text-sm text-slate-700 dark:text-slate-300">反代监听端口<input v-model.number="config.port" type="number" min="1024" max="65535" required class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400 dark:text-slate-500">建议 8097。不能与管理服务或其他程序使用相同端口；更换端口后需更新播放器连接地址。</span></label>
            <label class="text-sm text-slate-700 dark:text-slate-300">Emby 中对应 STRM 根目录的路径（按需填写）<input v-model.trim="config.embyPathPrefix" placeholder="例如 /media，对应 Cinema115 容器内的 /strm" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500" /><span class="mt-2 block text-xs leading-5 text-slate-400 dark:text-slate-500">Emby 返回本地 .strm 路径时使用。该路径下的相对目录必须与 Cinema115 的 STRM 根目录一致；如果返回的是 STRM 内的 HTTP 链接，可留空。</span></label>
          </div>
          <div class="space-y-3 border-t border-slate-100 dark:border-slate-800 pt-5">
            <h2 class="font-semibold text-slate-900 dark:text-slate-100">网盘 pickcode STRM</h2>
            <p class="text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">支持 /api/redirect115/{pickcode}，后面的文件名可省略。不限制来源主机，仅提取 pickcode，由主账号读取文件；不会访问原工具地址。</p>
            <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">Emby 用户绑定小号后，秒传到绑定账号的播放缓存目录并用该账号取链，已缓存的文件会复用；分享 STRM 优先尝试小号直接读取分享。未绑定用户默认使用主账号，绑定账号失效时暂停播放并保留绑定，恢复后新播放请求自动生效。</p>
          </div>
        </fieldset>
      </form>
      <MemberPlaybackAdmin ref="mediaSettings" :token="token" :listening="status?.listening" />
      <section class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm md:p-7">
        <div class="flex items-center justify-between"><h2 class="font-semibold text-slate-900 dark:text-slate-100">运行状态</h2><span class="rounded-full px-3 py-1 text-xs" :class="status?.listening ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 dark:text-slate-500'">{{ status?.listening ? '正在监听' : '未监听' }}</span></div>
        <p v-if="status?.error" role="alert" class="mt-3 text-sm text-red-600">{{ status.error }}</p>
        <p class="mt-4 break-all text-sm text-slate-700 dark:text-slate-300">Emby 客户端入口示例：<code>{{ entry }}</code></p>
        <p class="mt-2 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">使用原 Emby 用户登录。若有独立域名、HTTPS 网关或不同的宿主机端口，请使用实际对外地址。监听成功只表示服务已启动，仍需实际验证客户端播放与拖动。</p>
		<p class="mt-2 text-sm leading-6 text-amber-700">首次使用或升级后，请在播放器中通过反代地址退出并重新登录一次，以识别播放用户。正常重启无需重复登录；提示“尚未记录此 Emby 登录身份”时需重新登录。</p>
      </section>
      <div class="flex flex-wrap gap-4 text-sm"><RouterLink to="/emby/users" class="text-blue-600">Emby 用户与 115 账号绑定</RouterLink><RouterLink to="/emby/cache" class="text-blue-600">直链与秒传文件缓存管理</RouterLink></div>
      <InstantUploadPanel :token="token" :accounts="accounts" :source="status?.config" />
      <section class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm md:p-7">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">NAS / Docker 端口</h2>
        <p class="mt-3 text-sm leading-7 text-slate-600 dark:text-slate-400 dark:text-slate-500">桥接网络需要在容器设置中添加 TCP 端口映射 <code class="rounded bg-slate-100 dark:bg-slate-800 px-2 py-1">{{ mapping }}</code>（宿主机端口 : 容器端口）。项目 compose 使用 host 网络，无需映射；仅切换为桥接网络时需要配置映射。网页保存不会自动修改 Docker。</p>
        <p class="mt-3 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">使用 host 网络时通常无需映射，但端口不能与 NAS 上的其他服务冲突。直放成功时视频流量由客户端直接访问 115；转码与封装转换已关闭，不兼容原文件格式时请更换播放器；字幕、海报和播放进度继续由 Emby 处理。</p>
      </section>
    </div>
  </ManagementLayout>
</template>
