import { computed, onMounted, ref } from 'vue'
import { loadCloudDownloadProfiles, type CloudDownloadTarget, type CloudDownloadTargetSnapshot } from '../lib/cloud-download'

type StoredCloudDownloadTarget = Omit<CloudDownloadTargetSnapshot, 'targetPath'>

const storageKey = 'cinema115.cloud-download-target'

function readStored(): StoredCloudDownloadTarget | undefined {
  try {
    const raw = localStorage.getItem(storageKey)
    if (!raw) return undefined
    const parsed = JSON.parse(raw) as Partial<StoredCloudDownloadTarget> | null
    if (!parsed || typeof parsed.accountId !== 'string' || typeof parsed.profileId !== 'string' || typeof parsed.targetCID !== 'string') return undefined
    return { accountId: parsed.accountId, profileId: parsed.profileId, targetCID: parsed.targetCID }
  } catch {
    return undefined
  }
}

function writeStored(value: StoredCloudDownloadTarget) {
  try {
    localStorage.setItem(storageKey, JSON.stringify(value))
  } catch {
    /* 禁用浏览器存储时仍可在当前页面内正常选择。 */
  }
}

// 网页手动提交与各资源搜索弹窗共用云下载目录选择与失效再确认逻辑。
export function useCloudDownloadTarget(token: string, signal: AbortSignal, options: { autoLoad?: boolean } = {}) {
  const allProfiles = ref<CloudDownloadTarget[]>([])
  const accountID = ref('')
  const selectedID = ref('')
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')
  const reconfirmMessage = ref('')
  let preference = readStored()
  let loadGeneration = 0

  const targets = computed(() => allProfiles.value.filter(item => item.ready && item.accountId === accountID.value))
  const target = computed(() => targets.value.find(item => item.id === selectedID.value))
  const canSubmit = computed(() => !!target.value && !loading.value && !error.value)
  const selectOptions = computed(() => targets.value.map(item => ({ value: item.id, label: item.name })))

  function select(id: string) {
    const matched = targets.value.find(item => item.id === id)
    if (!matched) {
      selectedID.value = ''
      return
    }
    selectedID.value = matched.id
    reconfirmMessage.value = ''
    preference = { accountId: matched.accountId, profileId: matched.id, targetCID: matched.targetCID }
    writeStored(preference)
  }

  function snapshot(): CloudDownloadTargetSnapshot | undefined {
    const current = target.value
    if (!current || !canSubmit.value) return undefined
    return {
      profileId: current.id,
      accountId: current.accountId,
      targetCID: current.targetCID,
      targetPath: current.targetPath,
    }
  }

  function missingTargetMessage(): string {
    if (reconfirmMessage.value) return reconfirmMessage.value
    if (loading.value && !loaded.value) return '正在读取云下载目录，请稍候再试。'
    if (error.value) return error.value
    if (targets.value.length) return '请先选择云下载目录，再提交下载。'
    return '请先在“115 云转存”新增磁力/ED2K 转存目录'
  }

  async function load() {
    if (!token || signal.aborted) return
    const generation = ++loadGeneration
    loading.value = true
    error.value = ''
    try {
      const data = await loadCloudDownloadProfiles(token, signal)
      if (signal.aborted || generation !== loadGeneration) return
      accountID.value = data.accountId || ''
      allProfiles.value = data.profiles || []
      loaded.value = true

      const ready = targets.value
      if (reconfirmMessage.value) return
      const stored = preference
      if (stored) {
        const matched = ready.find(item =>
          item.id === stored.profileId
          && item.accountId === stored.accountId
          && item.targetCID === stored.targetCID,
        )
        if (matched) {
          selectedID.value = matched.id
          reconfirmMessage.value = ''
        } else {
          // 历史选择已删除、同 ID 改 CID 或主账号已切换时，即使唯一有效也要求再次选择。
          selectedID.value = ''
          reconfirmMessage.value = '上次使用的下载目录已变更、删除或主账号已切换，请重新确认下载目录。'
        }
        return
      }

      if (ready.length === 1) {
        select(ready[0]!.id)
      } else {
        selectedID.value = ''
        reconfirmMessage.value = ''
      }
    } catch (reason) {
      if (!signal.aborted && generation === loadGeneration) {
        error.value = reason instanceof Error ? reason.message : '读取云下载目录失败'
      }
    } finally {
      if (!signal.aborted && generation === loadGeneration) loading.value = false
    }
  }

  async function handleTargetChanged(submitted: CloudDownloadTargetSnapshot, message?: string) {
    const current = target.value
    // 其他请求的迟到回执可以刷新列表，但不能清掉用户刚选好的另一个目标。
    if (current && (current.id !== submitted.profileId || current.accountId !== submitted.accountId || current.targetCID !== submitted.targetCID)) {
      await load()
      return
    }
    selectedID.value = ''
    reconfirmMessage.value = message || '所选下载目录已变更或删除，本次未提交，请重新确认目录。'
    await load()
  }

  onMounted(() => {
    if (options.autoLoad !== false && token) void load()
  })

  return {
    allProfiles,
    accountID,
    targets,
    selectedID,
    target,
    canSubmit,
    options: selectOptions,
    loading,
    loaded,
    error,
    reconfirmMessage,
    select,
    snapshot,
    missingTargetMessage,
    load,
    handleTargetChanged,
  }
}

export type CloudDownloadTargetController = ReturnType<typeof useCloudDownloadTarget>
