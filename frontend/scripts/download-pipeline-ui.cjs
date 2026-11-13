// 所有接口均为模拟，只验证批次通知、统一记录与阶段重试，不访问真实网盘。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const lostRetry = process.argv.includes('--lost-retry-response')
const failedRefresh = process.argv.includes('--failed-refresh')
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], writes = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(); page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    const createdAt = '2026-11-13T00:00:00Z'
    const jobs = [1, 2].map(number => ({
      id: `job-${number}`, title: `资源 ${number}`, status: 'completed', createdAt,
      config: { accountId: 'primary', sourceCID: '10', sourcePath: '/临时', targetCID: '20', targetPath: '/结果' },
      download: { hash: String(number).repeat(40), batchId: 'history-batch', share: { directory: `Cinema115-${number}`, handedOff: true, leaderId: `share-${number}` } },
      items: [{ file: { id: `video-${number}`, name: `Show.E0${number}.mkv`, mediaType: 'video', sourcePath: `/临时/E0${number}.mkv` }, name: `Show.S01E0${number}.mkv`, targetPath: `/结果/Show.S01E0${number}.mkv`, status: 'done' }],
    }))
    const pipeline = {
      id: 'history-batch', title: '批次作品', createdAt, jobIds: jobs.map(job => job.id), shareSTRM: true,
      status: 'partial', finished: true, message: '整理已完成，1 个 STRM 批次需要重试', total: 2, ready: 2, organized: 2, failed: 0, videoCount: 2, written: 1,
      shares: [
        { id: 'share-1', status: 'success', written: 1, total: 1, error: '', note: '', shareUrl: 'https://115.com/s/mock-success', canRetry: false, nextRetryAt: null },
        { id: 'share-2', status: 'failed', written: 0, total: 1, error: '模拟 STRM 写入失败', note: '', shareUrl: 'https://115.com/s/mock-failed', canRetry: true, nextRetryAt: null },
      ],
    }
    let stage = 'downloading'
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname
      let data = {}
      if (req.method() === 'POST') writes.push({ endpoint, body: req.postDataJSON() })
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin' } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [{ id: 'primary', name: '主账号', primary: true, configured: true }] }
      else if (endpoint.endsWith('/download-organize/jobs')) {
        if (failedRefresh && writes.length) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: '模拟读取失败' }) })
        data = { jobs, pipelines: [pipeline] }
      }
      else if (endpoint.endsWith('/download-organize/progress')) data = { pipelines: new URL(req.url()).searchParams.get('ids').split(',').map(id => id === pipeline.id ? pipeline : { ...pipeline, id, status: stage, finished: stage === 'success', message: `通知阶段 ${stage}` }) }
      else if (endpoint.endsWith('/share/tasks/retry')) {
        assert.deepEqual(req.postDataJSON(), { ids: ['share-2'] })
        Object.assign(pipeline.shares[1], { status: 'success', canRetry: false, written: 1, error: '' })
        Object.assign(pipeline, { status: 'success', written: 2, message: '2 个资源整理完成，已生成 2 个 STRM' })
        if (lostRetry) return route.abort('failed')
        data = { results: [{ id: 'share-2', accepted: true }] }
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto(`${origin}/115/download-organize`)
    await page.getByRole('button', { name: '查看批次作品的详情', exact: true }).waitFor()
    assert.equal(await page.locator('.task-card').count(), 1, 'two resource jobs form one batch card')

    await page.evaluate(async () => {
      const { trackDownloadPipeline } = await import('/src/composables/useDownloadPipelineToast.ts')
      trackDownloadPipeline('mock', 'toast-batch', '通知测试 · 2 项资源')
    })
    const toast = page.locator('[data-sonner-toast]')
    await toast.getByText('正在下载', { exact: true }).waitFor()
    const original = await toast.elementHandle()
    for (const [next, label] of [['organizing', '正在整理'], ['generating', '正在生成 STRM'], ['review', '分享等待审核'], ['success', '任务完成']]) {
      stage = next
      await toast.getByText(label, { exact: true }).waitFor()
      assert.equal(await toast.count(), 1)
      assert.equal(await original.evaluate(el => el.isConnected), true, 'all stages update the same toast element')
    }
    await page.mouse.move(0, 0)
    await toast.waitFor({ state: 'detached', timeout: 12000 })

    await page.getByRole('button', { name: '查看批次作品的详情', exact: true }).click()
    const details = page.getByRole('dialog', { name: '下载整理详情', exact: true })
    await details.getByRole('region', { name: '流程进度', exact: true }).waitFor()
    const shares = details.getByRole('region', { name: '分享 STRM 进度', exact: true })
    assert.equal(await shares.getByRole('button', { name: '重试 STRM', exact: true }).count(), 1)
    await shares.getByText('模拟 STRM 写入失败', { exact: true }).waitFor()
    assert.equal(await details.getByRole('link', { name: /日志/ }).count(), 0)
    for (const [width, dark] of [[1440, false], [320, true]]) {
      await page.setViewportSize({ width, height: 900 })
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      assert.equal(await details.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    }
    await shares.getByRole('button', { name: '重试 STRM', exact: true }).click()
    if (failedRefresh) {
      await details.getByText('重试结果暂未确认，请刷新记录核对；不会自动重复提交。', { exact: true }).waitFor()
      assert.equal(await toast.getByText('任务完成', { exact: true }).count(), 0, 'failed refresh cannot confirm a lost retry response')
    } else {
      await shares.getByRole('button', { name: '重试 STRM', exact: true }).waitFor({ state: 'detached' })
      await shares.getByText('已完成 · 已写入 1/1', { exact: true }).first().waitFor()
      await toast.getByText('任务完成', { exact: true }).waitFor()
    }
    assert.equal(await details.isVisible(), true, 'refresh preserves the open batch details')
    assert.deepEqual(writes, [{ endpoint: '/api/admin/p115/share/tasks/retry', body: { ids: ['share-2'] } }], 'retry only submits the failed STRM stage')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ lostRetry, failedRefresh, passed: ['one real Sonner element updates through download, organize, STRM, review and success, then auto-dismisses', 'one batch card for two resources with linked STRM results', 'light desktop and dark 320px details fit', 'retry only the failed STRM stage; details stay open; no duplicate download or organize request'], errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
