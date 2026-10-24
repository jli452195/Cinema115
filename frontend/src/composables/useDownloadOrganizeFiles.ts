import { computed, onBeforeUnmount, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import { magnetKey } from '../lib/cloud-download-batch'
import { organizeFileRow } from '../utils/organizeNaming'
import type { DownloadFiles, DownloadOrganizeResource, OrganizeFile, OrganizeRow, OrganizeSubtitle } from '../types/manualOrganize'

export function useDownloadOrganizeFiles(token: string, entries: DownloadOrganizeResource[], multiple: boolean, lifecycle: AbortSignal) {
  const states = ref(entries.map(entry => ({ ...entry, key: magnetKey(entry.resource.magnet), status: 'waiting', error: '' })))
  const rows = ref<OrganizeRow[]>([]), subtitles = ref<OrganizeSubtitle[]>([])
  const sources: Record<string, OrganizeFile> = {}
  const owners = new Map<string, { key: string; id: string }>()
  const parsing = ref(false)
  let controller: AbortController | undefined
  const active = computed(() => states.value.filter(state => state.status !== 'removed'))
  const unresolved = computed(() => active.value.filter(state => state.status !== 'ready'))
  const readyCount = computed(() => active.value.length - unresolved.value.length)
  function append(entry: typeof states.value[number], data: DownloadFiles) {
    const key = (id: string) => multiple ? `${entry.key}:${id}` : id
    for (const file of data.files) {
      const id = key(file.id)
      owners.set(id, { key: entry.key, id: file.id })
      sources[id] = file
      rows.value.push(organizeFileRow({ ...file, id }, multiple || !!file.titleOnly))
    }
    subtitles.value.push(...(data.subtitles || []).map(sub => ({ ...sub, file: { ...sub.file, id: key(sub.file.id) }, videoId: sub.videoId ? key(sub.videoId) : undefined })))
    entry.files = data
    entry.status = 'ready'
  }
  function remove(key: string) {
    const entry = states.value.find(state => state.key === key)
    if (!entry) return
    entry.status = 'removed'
    const removed = new Set(rows.value.filter(row => owners.get(row.id)?.key === key).map(row => row.id))
    rows.value = rows.value.filter(row => !removed.has(row.id))
    subtitles.value = subtitles.value.filter(sub => !sub.file.id.startsWith(`${key}:`))
    // Removing a resource does not submit it as an ordinary download.
  }
  function pause() { controller?.abort() }
  async function parse() {
    if (parsing.value || lifecycle.aborted) return
    parsing.value = true; controller = new AbortController()
    const queueSignal = AbortSignal.any([lifecycle, controller.signal])
    try {
      for (const entry of states.value) {
        if (queueSignal.aborted) break
        if (entry.status !== 'waiting') continue
        entry.status = 'loading'; entry.error = ''
        try {
          const data = entry.files || await p115Request<DownloadFiles>(token, 'download-organize/files', { method: 'POST', body: JSON.stringify(entry.source), signal: AbortSignal.any([queueSignal, AbortSignal.timeout(50000)]) })
          if (queueSignal.aborted) { if (entry.status !== 'removed') entry.status = 'waiting'; break }
          if (entry.status === 'removed') continue
          if (!data.files.length) throw new Error(data.reason || '没有可整理视频，可返回使用普通离线')
          if (rows.value.length + data.files.length > 1000) throw new Error('整批最多整理 1000 个视频，请移除部分资源后重试')
          append(entry, data)
        } catch (reason) {
          if (entry.status === 'removed') continue
          if (queueSignal.aborted) { entry.status = 'waiting'; break }
          entry.status = 'error'; entry.error = reason instanceof Error ? reason.message : '解析失败，可重试或返回普通离线'
        }
      }
    } finally { parsing.value = false; controller = undefined }
  }
  function retry(key: string) {
    if (parsing.value) return
    const entry = states.value.find(state => state.key === key)
    if (entry?.status === 'error') { entry.status = 'waiting'; entry.files = undefined; void parse() }
  }
  function resourceSelections() {
    return active.value.map(entry => ({ ...entry.source, manifestId: entry.files?.manifestId, files: rows.value.filter(row => row.selected && owners.get(row.id)?.key === entry.key).map(row => ({ id: owners.get(row.id)!.id, season: row.season, episode: row.episode })) })).filter(entry => entry.files.length)
  }
  const selectedKeys = computed(() => [...new Set(rows.value.filter(row => row.selected).map(row => owners.get(row.id)!.key))])
  for (const entry of states.value) if (entry.files) append(entry, entry.files)
  onBeforeUnmount(pause)
  return { states, rows, subtitles, sources, parsing, active, unresolved, readyCount, selectedKeys, owners, remove, pause, parse, retry, resourceSelections }
}
