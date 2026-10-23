// All API calls are mocked. No real library files or task records are changed.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], writes = []
  try {
    await fs.mkdir('test-results', { recursive: true })
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'history-ui-test'))
    const page = await context.newPage()
    page.setDefaultTimeout(12000)
    page.on('pageerror', error => errors.push(error.message))
    await page.clock.install()
    const config = { accountId: 'main', sourceCID: '10', sourcePath: '/来源/待整理', targetCID: '20', targetPath: '/影视库' }
    const file = i => ({ id: String(i), name: `閃靈二人組 - ${i}.rmvb`, sourcePath: `/来源/${'超长目录/'.repeat(20)}${i}.rmvb`, mediaType: i < 12 ? 'video' : 'subtitle', size: 100, directory: false })
    const makeJob = i => ({ id: `job-${i}`, title: i === 0 ? '部分完成作品' : i === 1 ? '转生贤者的异世界生活～获得第二职业并成为世界最强～'.repeat(3) : `已完成作品 ${i}`, config, status: i === 0 ? 'partial' : 'completed', createdAt: new Date(Date.UTC(2026, 9, 15, 12 - i)).toISOString(), items: Array.from({ length: 36 }, (_, n) => ({ file: file(n), targetPath: `/影视库/閃靈二人組/Season 01/${n}.rmvb`, status: 'done' })) })
    let jobs = Array.from({ length: 12 }, (_, i) => makeJob(i)), offline = true
    let resumeMode = 'hold', releaseResume
    jobs[0].items = jobs[0].items.slice(0, 6).map((item, i) => ({ ...item, status: ['done', 'uncertain', 'renamed', 'pending', 'blocked', 'skipped'][i], message: i === 1 ? '结果未确认，请核实实际文件' : '' }))
    const cleanup = [{ id: 'clean', accountId: 'main', mode: 'empty', parentPath: '/来源/待整理', status: 'completed', createdAt: '2026-10-15T00:00:00Z', items: [{ id: 'folder', name: '空目录', path: '/来源/空目录', status: 'done' }] }]
    const subtitles = [{ id: 'sub', accountId: 'main', jobId: 'job-2', status: 'completed', createdAt: '2026-10-15T01:00:00Z', items: [0, 1].map(i => ({ localId: String(i), originalName: `${i}.srt`, video: file(i), targetPath: `/影视库/${i}.srt`, status: i ? 'skipped' : 'done' })) }]
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname
      if (request.method() !== 'GET') writes.push(endpoint)
      let data = {}, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [{ id: 'main', name: '主账号', primary: true, configured: true }] }
      else if (endpoint.endsWith('/manual-organize/config')) data = config
      else if (endpoint.endsWith('/manual-organize/files')) data = { files: [], count: 0 }
      else if (endpoint.endsWith('/subtitles/videos')) data = { videos: [{ ...file(0), parentId: '20', path: '/影视库/0.rmvb' }] }
      else if (endpoint.endsWith('/garden-organize/resume')) {
        if (resumeMode === 'hold') await new Promise(resolve => { releaseResume = resolve })
        if (resumeMode === 'reject') { status = 409; data = { error: '账号配置已变化，请重新确认账号和目录' } }
        else if (resumeMode === 'unknown') { status = 502; data = { error: '模拟响应丢失' } }
        else {
          const job = jobs.find(job => job.id === 'resume')
          job.status = 'downloading'; job.resumable = false; job.message = '正在检查原 115 任务，确认后继续整理；不会重新提交离线'
          data = job
        }
      }
      else if (/manual-organize\/(jobs|cleanup|subtitles)$/.test(endpoint)) {
        if (offline) { status = 502; data = { error: '模拟读取失败' } }
        else data = { jobs: endpoint.endsWith('/jobs') ? jobs : endpoint.endsWith('/cleanup') ? cleanup : subtitles }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(origin + '/115/manual-organize', { waitUntil: 'networkidle' })
    const history = page.getByRole('region', { name: '任务记录', exact: true })
    await history.getByRole('alert').waitFor()
    assert.match(await history.innerText(), /任务记录加载失败/)
    offline = false
    await history.getByRole('button', { name: '刷新记录', exact: true }).click()
    await history.locator('li').first().waitFor()
    assert.equal(await history.locator('li').count(), 8)
    await history.getByRole('button', { name: '显示更多' }).click()
    assert.equal(await history.locator('li').count(), 12)
    await history.getByRole('button', { name: /^清理 / }).click()
    assert.equal(await history.locator('li').count(), 1)
    await history.getByRole('button', { name: /^整理 / }).click()
    assert.equal(await history.locator('li').count(), 12)
    passed.push('initial failure recovery, eight-row default, show more and independent category state')

    const detailButton = history.getByRole('button', { name: '查看部分完成作品的详情', exact: true })
    await detailButton.click()
    let dialog = page.getByRole('dialog', { name: '部分完成作品', exact: true })
    await dialog.waitFor()
    assert.equal(await dialog.locator('li').count(), 4)
    assert.match(await dialog.innerText(), /已改名，尚未移动/)
    await dialog.getByRole('button', { name: '全部 6', exact: true }).click()
    assert.equal(await dialog.locator('li').count(), 6)
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined }))
    await dialog.getByRole('button', { name: '复制来源目录', exact: true }).click()
    await dialog.getByText('已复制完整路径。', { exact: true }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '复制来源目录', exact: true }).evaluate(el => document.activeElement === el), true)
    const scroll = dialog.locator('.overscroll-contain')
    await scroll.evaluate(el => { el.scrollTop = 300 })
    const beforeScroll = await scroll.evaluate(el => el.scrollTop)
    jobs[0].message = '刷新后的任务摘要'
    await page.clock.fastForward(10000)
    await dialog.getByText('刷新后的任务摘要', { exact: true }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '全部 6' }).getAttribute('aria-pressed'), 'true')
    assert.equal(await scroll.evaluate(el => el.scrollTop), beforeScroll)
    passed.push('partial states and default attention filter; polling preserves drawer, selection and scroll')

    offline = true
    await page.clock.fastForward(10000)
    await dialog.getByRole('alert').waitFor()
    assert.equal(await dialog.getByRole('button', { name: '补充字幕', exact: true }).isDisabled(), true)
    assert.equal(await history.locator('li').count(), 12)
    offline = false
    await page.clock.fastForward(10000)
    await dialog.getByRole('button', { name: '补充字幕', exact: true }).waitFor({ state: 'visible' })
    await page.waitForFunction(() => !document.querySelector('dialog button[title="请刷新任务记录，确认最新状态后再执行操作。"]'))
    await dialog.getByRole('button', { name: '补充字幕', exact: true }).click()
    const subtitleDialog = page.getByRole('dialog', { name: '补充字幕 · 部分完成作品', exact: true })
    await subtitleDialog.waitFor()
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await subtitleDialog.getByRole('button', { name: '关闭字幕导入' }).click()
    assert.equal(await detailButton.evaluate(el => document.activeElement === el), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('stale data disables actions; drawer-to-subtitle transition restores focus and scroll lock')

    await history.getByRole('button', { name: /^字幕 / }).click()
    await history.getByRole('button', { name: /的详情$/ }).click()
    dialog = page.getByRole('dialog')
    assert.match(await dialog.innerText(), /已上传 1\/2 · 跳过 1/)
    assert.equal(await dialog.getByText('任务有未完成的条目', { exact: true }).count(), 0)
    await page.keyboard.press('Escape')
    await history.getByRole('button', { name: /^整理 / }).click()
    jobs.unshift({ ...makeJob(20), id: 'active', title: '正在整理的作品', status: 'running', createdAt: '2026-10-15T23:00:00Z' })
    await page.clock.fastForward(10000)
    await history.getByRole('progressbar').waitFor()
    assert.equal(await history.getByRole('button', { name: '查看正在整理的作品的详情' }).count(), 1)
    await history.getByRole('button', { name: /^清理 / }).click()
    jobs[0].status = 'completed'
    await page.clock.fastForward(2000)
    await history.getByRole('progressbar').waitFor({ state: 'detached' })
    assert.equal(await history.getByRole('button', { name: /^清理 / }).getAttribute('aria-pressed'), 'true')
    await history.getByRole('button', { name: '查看整理记录', exact: true }).waitFor()
    await history.getByRole('button', { name: '查看整理记录', exact: true }).click()
    assert.equal(await history.getByRole('button', { name: /^整理 / }).getAttribute('aria-pressed'), 'true')
    passed.push('skips are distinct from uploads; active tasks are not duplicated and completion keeps the selected category')

    await history.getByRole('button', { name: /^整理 / }).click()
    await history.screenshot({ path: 'test-results/organize-history-desktop.png' })
    await history.getByRole('button', { name: /查看转生贤者.*的详情/ }).click()
    dialog = page.getByRole('dialog')
    assert.equal(Math.round((await dialog.boundingBox()).width), 720)
    await dialog.screenshot({ path: 'test-results/organize-history-details.png' })
    await page.keyboard.press('Escape')
    for (const width of [1024, 390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, `page overflow at ${width}`)
      await history.getByRole('button', { name: /查看转生贤者.*的详情/ }).click()
      dialog = page.getByRole('dialog')
      assert.equal(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth), true, `drawer overflow at ${width}`)
      const bounds = await dialog.boundingBox()
      assert(bounds.x >= -1 && bounds.x + bounds.width <= width + 1, `drawer outside viewport at ${width}: ${JSON.stringify(bounds)}`)
      if (width < 640) assert.equal(Math.round(bounds.width), width)
      if (width === 390) await dialog.screenshot({ path: 'test-results/organize-history-mobile-details.png' })
      await page.keyboard.press('Escape')
      if (width === 390) await history.screenshot({ path: 'test-results/organize-history-mobile.png' })
    }
    await history.getByRole('button', { name: '查看部分完成作品的详情', exact: true }).click()
    dialog = page.getByRole('dialog', { name: '部分完成作品', exact: true })
    jobs = jobs.filter(job => job.id !== 'job-0')
    await page.clock.fastForward(10000)
    await dialog.getByText('此任务已不在最近记录中，当前显示打开时保留的快照。', { exact: true }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '补充字幕', exact: true }).isDisabled(), true)
    await page.keyboard.press('Escape')
    passed.push('desktop, 1024px, 390px and 320px layouts; long titles and paths do not overflow')
    passed.push('retention removes a task without closing its snapshot; further actions remain disabled')
    assert.deepEqual(errors, [])
    assert.deepEqual(writes, [])

    const resumeJob = { ...makeJob(30), id: 'resume', title: '可恢复的下载整理', status: 'interrupted', resumable: true, download: { directoryId: '999' } }
    resumeJob.items = resumeJob.items.slice(0, 3).map(item => ({ ...item, status: 'pending' }))
    jobs.unshift(resumeJob)
    await page.clock.fastForward(10000)
    await history.getByRole('button', { name: '查看可恢复的下载整理的详情' }).click()
    dialog = page.getByRole('dialog', { name: resumeJob.title, exact: true })
    let resumeButton = dialog.getByRole('button', { name: '检查并继续', exact: true })
    assert.equal(await resumeButton.isEnabled(), true)
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      const footer = dialog.locator('footer')
      assert.equal(await footer.evaluate(el => el.scrollWidth <= el.clientWidth), true, `320px recovery footer overflow, dark=${dark}`)
      const box = await resumeButton.boundingBox()
      assert(box.x >= 0 && box.x + box.width <= 321, 'recovery button outside mobile viewport')
    }
    await dialog.locator('.overscroll-contain').evaluate(el => { el.scrollTop = el.scrollHeight })
    await resumeButton.click()
    const checking = dialog.getByRole('button', { name: '检查中…', exact: true })
    await page.waitForFunction(() => !!document.querySelector('button[aria-busy="true"]'))
    assert.equal(await checking.isDisabled(), true)
    await checking.evaluate(el => el.click())
    await page.waitForTimeout(20)
    assert.equal(writes.length, 1)
    await page.keyboard.press('Escape')
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    await history.getByRole('button', { name: '查看可恢复的下载整理的详情' }).click()
    dialog = page.getByRole('dialog', { name: resumeJob.title, exact: true })
    assert.equal(await dialog.getByRole('button', { name: '检查中…', exact: true }).isDisabled(), true)
    resumeMode = 'reject'; releaseResume()
    await dialog.getByText('账号配置已变化，请重新确认账号和目录', { exact: true }).waitFor()
    const alertBounds = await dialog.getByText('账号配置已变化，请重新确认账号和目录', { exact: true }).boundingBox()
    assert(alertBounds.y >= 0 && alertBounds.y + alertBounds.height <= 845, 'recovery error not scrolled into view')
    resumeButton = dialog.getByRole('button', { name: '检查并继续', exact: true })
    await page.waitForFunction(() => !document.querySelector('button[aria-busy="true"]'))
    assert.equal(await resumeButton.isEnabled(), true)
    passed.push('recovery stays in details; 320px light/dark footer; duplicate clicks blocked; closing and reopening retains pending state; server refusal visible')

    offline = true; resumeMode = 'unknown'
    await resumeButton.click()
    await dialog.getByText(/恢复结果暂未确认/).waitFor()
    await page.waitForFunction(() => !document.querySelector('button[aria-busy="true"]'))
    assert.equal(await resumeButton.isDisabled(), true)
    const writesBeforeRefresh = writes.length
    await page.keyboard.press('Escape')
    offline = false
    await history.getByRole('button', { name: '刷新记录', exact: true }).click()
    await history.getByRole('alert').waitFor({ state: 'detached' })
    assert.equal(writes.length, writesBeforeRefresh, 'refresh replayed recovery')
    await history.getByRole('button', { name: '查看可恢复的下载整理的详情' }).click()
    dialog = page.getByRole('dialog', { name: resumeJob.title, exact: true })
    resumeMode = 'success'
    await dialog.getByRole('button', { name: '检查并继续', exact: true }).click()
    await dialog.getByText('离线中，完成后整理', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: /检查中|检查并继续/ }).waitFor({ state: 'detached' })
    assert.equal(await dialog.evaluate(el => el.contains(document.activeElement)), true, 'focus escaped after recovery button disappeared')
    resumeJob.status = 'completed'; resumeJob.items.forEach(item => { item.status = 'done' })
    await page.clock.fastForward(2000)
    await dialog.getByText('已完成', { exact: true }).first().waitFor()
    assert.equal(await dialog.getByRole('button', { name: '检查并继续', exact: true }).count(), 0)
    assert.deepEqual(writes, Array(3).fill('/api/admin/p115/garden-organize/resume'))
    assert.deepEqual(errors, [])
    passed.push('lost response is not automatically replayed; stale history blocks retry; refresh is read-only; recovery and completion update open details with active polling')
    console.log(JSON.stringify({ passed, errors, writes }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
