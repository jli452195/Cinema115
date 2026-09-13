// 每类外部数据使用独立队列，等待中的请求可取消，避免占用其他服务的名额。
export function createRequestQueue(limit: number) {
  interface Job { start: () => void }
  const queue: Job[] = []
  let active = 0
  function drain() { while (active < limit && queue.length) queue.shift()!.start() }
  return function queueRequest<T>(signal: AbortSignal, run: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      if (signal.aborted) { reject(new DOMException('请求已取消', 'AbortError')); return }
      const abort = () => { const index = queue.indexOf(job); if (index >= 0) queue.splice(index, 1); reject(new DOMException('请求已取消', 'AbortError')) }
      const job: Job = { start: () => {
        signal.removeEventListener('abort', abort); active++
        Promise.resolve().then(run).then(resolve, reject).finally(() => { active--; drain() })
      } }
      signal.addEventListener('abort', abort, { once: true }); queue.push(job); drain()
    })
  }
}
