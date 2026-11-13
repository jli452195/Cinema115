import { p115Request } from './p115-api'
import type { DownloadPipeline } from '../types/organize'

export async function fetchDownloadPipelines(token: string, ids: string[], signal?: AbortSignal): Promise<DownloadPipeline[]> {
  const read = async (selected: string[]): Promise<DownloadPipeline[]> => {
    const query = new URLSearchParams({ ids: selected.join(',') })
    try {
      return (await p115Request<{ pipelines: DownloadPipeline[] }>(token, `download-organize/progress?${query}`, { signal })).pipelines
    } catch (error) {
      if ((error as { status?: number }).status !== 409) throw error
      if (selected.length === 1) return []
      // 已移出历史的记录单独核对，其余活跃批次继续合并查询。
      const middle = Math.ceil(selected.length / 2)
      return (await Promise.all([read(selected.slice(0, middle)), read(selected.slice(middle))])).flat()
    }
  }
  const selected = [...new Set(ids)]
  const pipelines: DownloadPipeline[] = []
  for (let offset = 0; offset < selected.length; offset += 100) {
    pipelines.push(...await read(selected.slice(offset, offset + 100)))
  }
  return pipelines
}

export async function retryDownloadPipelineShares(token: string, ids: string[], signal?: AbortSignal): Promise<void> {
  const selected = [...new Set(ids)]
  if (!selected.length) return
  const data = await p115Request<{ results: { id: string; accepted: boolean; reason?: string }[] }>(token, 'share/tasks/retry', {
    method: 'POST', body: JSON.stringify({ ids: selected }), signal,
  })
  const rejected = selected.filter(id => !data.results.some(result => result.id === id && result.accepted))
  if (rejected.length) throw Object.assign(new Error(rejected.map(id => data.results.find(result => result.id === id)?.reason || '未确认重试结果，请刷新任务记录').join('；')), { status: 409 })
}
