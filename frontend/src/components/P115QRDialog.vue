<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import { p115Request } from '../lib/p115-api'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ close: []; connected: [] }>()
const dialog = ref<HTMLDialogElement>()
const clientType = ref('alipay-miniapp')
const qrImage = ref('')
const loading = ref(false)
const message = ref('选择客户端类型后获取二维码')
const failed = ref(false)
let timer: number | undefined
let controller: AbortController | undefined
let generation = 0
let previousFocus: HTMLElement | null = null

function stop() {
  generation++
  controller?.abort()
  window.clearTimeout(timer)
  loading.value = false
}

function reset() {
  stop()
  qrImage.value = ''
  failed.value = false
  message.value = '客户端已切换，请重新获取二维码'
}

function close() {
  stop()
  emit('close')
}

async function createQRCode() {
  stop()
  const current = generation
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true
  qrImage.value = ''
  failed.value = false
  message.value = '正在获取二维码…'
  try {
    const data = await p115Request<{ qrId: string; qrcode: string }>(props.token, 'qrcode', {
      method: 'POST', body: JSON.stringify({ clientType: clientType.value }), signal,
    })
    const image = await QRCode.toDataURL(data.qrcode, { width: 256, margin: 2 })
    if (current !== generation) return
    qrImage.value = image
    message.value = '请使用 115 App 扫码，并在手机上确认登录'
    timer = window.setTimeout(() => poll(data.qrId, current, signal), 1500)
  } catch (error) {
    if (current !== generation) return
    failed.value = true
    message.value = error instanceof Error ? error.message : '二维码获取失败'
  } finally {
    if (current === generation) loading.value = false
  }
}

async function poll(qrId: string, current: number, signal: AbortSignal) {
  try {
    const data = await p115Request<{ status: string; connected: boolean }>(props.token, `qrcode/status?qrId=${encodeURIComponent(qrId)}`, { signal })
    // 关闭、刷新或切换类型后，旧请求的结果不能覆盖当前扫码状态。
    if (current !== generation) return
    if (data.status === 'success' && data.connected) {
      stop()
      emit('connected')
      return
    }
    if (data.status === 'expired' || data.status === 'cancelled') {
      failed.value = true
      message.value = data.status === 'expired' ? '二维码已过期，请重新获取' : '已取消登录，请重新获取二维码'
      stop()
      return
    }
    message.value = data.status === 'scanned' ? '已扫码，请在手机上确认登录' : '等待扫码，请使用 115 App 扫描二维码'
    timer = window.setTimeout(() => poll(qrId, current, signal), 2000)
  } catch (error) {
    if (current !== generation) return
    failed.value = true
    message.value = error instanceof Error ? error.message : '二维码状态查询失败'
    stop()
  }
}

onMounted(() => {
  previousFocus = document.activeElement as HTMLElement | null
  dialog.value?.showModal()
})
onBeforeUnmount(() => {
  stop()
  dialog.value?.close()
  previousFocus?.focus()
})
</script>

<template>
  <dialog ref="dialog" class="qr-dialog" aria-labelledby="qr-title" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
    <section class="qr-panel">
      <header><div><h2 id="qr-title">扫码获取 Cookie</h2><p>选择登录设备，在手机确认后自动保存</p></div><button type="button" class="close" aria-label="关闭扫码弹窗" @click="close">×</button></header>
      <label for="qr-client">客户端类型</label>
      <select id="qr-client" v-model="clientType" @change="reset">
        <option value="alipay-miniapp">支付宝小程序</option><option value="wechat-miniapp">微信小程序</option>
        <option value="115-life-android">115生活 Android</option><option value="115-android">115 Android</option>
        <option value="115-life-ios">115生活 iOS</option><option value="115-ios">115 iOS</option>
      </select>
      <div class="qr-area" :aria-busy="loading"><img v-if="qrImage" :src="qrImage" alt="115 登录二维码" :class="{ faded: failed }" /><span v-else>{{ loading ? '正在生成二维码…' : '点击下方按钮获取二维码' }}</span></div>
      <p class="qr-message" :class="{ error: failed }" role="status">{{ message }}</p>
      <button type="button" class="generate" :disabled="loading" @click="createQRCode">{{ loading ? '获取中…' : qrImage ? '刷新二维码' : '获取二维码' }}</button>
      <p class="note">二维码入口相同，所选客户端类型在确认登录时生效。</p>
    </section>
  </dialog>
</template>

<style scoped>
.qr-dialog { width: min(440px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); padding: 0; border: 0; border-radius: 18px; color: #334155; box-shadow: 0 24px 80px #0f172a33; }
.qr-dialog::backdrop { background: #0f172a66; backdrop-filter: blur(3px); }
.qr-panel { padding: 26px; }
header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 24px; }
h2 { font-size: 19px; font-weight: 650; color: #1e293b; }
header p, .note { font-size: 12px; color: #94a3b8; margin-top: 7px; }
.close { font-size: 26px; line-height: 26px; padding: 0 6px; color: #94a3b8; }
label { display: block; font-size: 12px; margin-bottom: 8px; }
select { width: 100%; background: #f5f7fa; border: 1px solid #e5e9ef; border-radius: 8px; padding: 11px; font-size: 14px; }
.qr-area { margin: 24px auto 12px; width: 240px; height: 240px; max-width: 100%; display: grid; place-items: center; border: 1px dashed #dbe2ea; border-radius: 12px; font-size: 13px; color: #94a3b8; overflow: hidden; }
.qr-area img { width: 100%; height: 100%; object-fit: contain; }
.faded { opacity: .3; }
.qr-message { min-height: 40px; text-align: center; font-size: 13px; color: #64748b; }
.error { color: #dc2626; }
.generate { width: 100%; border-radius: 8px; background: #367bf5; color: white; padding: 11px; font-size: 14px; }
.generate:disabled { opacity: .6; cursor: wait; }
.note { text-align: center; line-height: 1.6; margin-top: 14px; }
button:focus-visible, select:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
</style>
