import { computed, onMounted, ref } from 'vue'
import { shareTransferTargets, transferShare, type ShareTransferLink, type ShareTransferTarget } from '../lib/share-transfer'
import { normalize115Share } from '../utils/shareLink'
import { sha256 } from 'js-sha256'
import { useToast } from './useToast'

interface TransferStatus { message: string; state: 'pending' | 'success' | 'uncertain' | 'failed' }
// 弹窗重新打开后仍保留本次会话的提交结果，避免对未知结果重复转存。
const statuses = ref<Record<string, TransferStatus>>({})
const storageKey = 'cinema115.share-transfer-target'

export function useShareTransfer(token: string, signal: AbortSignal) {
  const toast = useToast()
  const targets = ref<ShareTransferTarget[]>([])
  const selectedID = ref('')
  const loading = ref(true)
  const error = ref('')
  const accountID = ref('')
  const target = computed(() => targets.value.find(item => item.id === selectedID.value))
  const options = computed(() => targets.value.map(item => ({ value: item.id, label: item.name })))
  function select(id: string) {
    selectedID.value = id
    try { localStorage.setItem(storageKey, JSON.stringify({ accountId: accountID.value, id })) } catch { /* 禁用存储时仍可在当前弹窗选择。 */ }
  }
  async function load() {
    loading.value = true; error.value = ''
    targets.value = []; selectedID.value = ''
    try {
      const data = await shareTransferTargets(token, AbortSignal.any([signal, AbortSignal.timeout(15000)]))
      if (signal.aborted) return
      targets.value = data.profiles || []
      accountID.value = data.accountId
      let saved: { accountId?: string; id?: string } = {}
      try { saved = JSON.parse(localStorage.getItem(storageKey) || '{}') || {} } catch { /* 无效偏好重新选择。 */ }
      selectedID.value = targets.value.length === 1 ? targets.value[0]!.id : saved.accountId === accountID.value && targets.value.some(item => item.id === saved.id) ? saved.id! : ''
      if (selectedID.value) select(selectedID.value)
    } catch (reason) {
      if (!signal.aborted) error.value = reason instanceof Error ? reason.message : '读取转存目录失败'
    } finally { loading.value = false }
  }
  function linkID(link: ShareTransferLink) {
    const canonical = normalize115Share(link.url)
    if (!canonical) return sha256(link.url)
    const url = new URL(canonical)
    if (link.password?.trim()) url.searchParams.set('password', link.password.trim())
    return sha256(url.toString())
  }
  function key(id: string, snapshot = target.value) { return JSON.stringify([snapshot?.accountId, snapshot?.id, snapshot?.targetCID, id]) }
  function status(id: string, snapshot = target.value) { return statuses.value[key(id, snapshot)] }
  async function submit(link: ShareTransferLink, snapshot: ShareTransferTarget) {
    signal.throwIfAborted()
    const id = linkID(link)
    const identity = key(id, snapshot)
    if (statuses.value[identity] && statuses.value[identity]!.state !== 'failed') return
    const url = normalize115Share(link.url)
    if (!url) {
      statuses.value[identity] = { state: 'failed', message: '无效的 115 分享链接' }
      toast.error('无效的 115 分享链接', { title: '转存失败', key: identity })
      return
    }
    // 仅保留有界的会话结果，不将分享链接或回执写入浏览器持久存储。
    if (Object.keys(statuses.value).length >= 200) {
      const oldest = Object.keys(statuses.value).find(item => statuses.value[item]?.state !== 'pending')
      if (oldest) delete statuses.value[oldest]
    }
    statuses.value[identity] = { state: 'pending', message: `正在转存到：${snapshot.targetPath}` }
    try {
      const result = await transferShare(token, snapshot, { ...link, url }, AbortSignal.any([signal, AbortSignal.timeout(120000)]))
      statuses.value[identity] = { state: 'success', message: `已转存到：${result.receipt.targetPath}` }
      toast.success(statuses.value[identity].message, { title: '转存成功', key: identity })
    } catch (reason) {
      const info = reason as { status?: number; responseData?: { outcomeUncertain?: boolean } }
      const uncertain = info.responseData?.outcomeUncertain ?? (!info.status || info.status >= 500)
      statuses.value[identity] = { state: uncertain ? 'uncertain' : 'failed', message: uncertain ? `转存结果待核对，请检查：${snapshot.targetPath}。${reason instanceof Error ? reason.message : ''}` : reason instanceof Error ? reason.message : '转存失败' }
      toast[uncertain ? 'warning' : 'error'](statuses.value[identity].message, { title: uncertain ? '转存结果待核对' : '转存失败', key: identity, duration: uncertain ? 6000 : 5000 })
    }
  }
  onMounted(load)
  return { targets, selectedID, target, options, loading, error, select, load, linkID, status, submit }
}

export type ShareTransferController = ReturnType<typeof useShareTransfer>
