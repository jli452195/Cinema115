// Isolated UI acceptance: all API requests use synthetic fixtures; no NAS/115 writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'fixture'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(), calls = [], errors = [], passed = []
    page.setDefaultTimeout(8000); page.on('pageerror', error => errors.push(error.message))
    let monitor = { id: 'one', accountId: 'main', sourceCID: '10', sourcePath: '/监控目录', stage: 'waiting', running: false, queued: false, hasBatch: false, lastError: '', enabled: false, intervalSeconds: 600, recentRuns: [], endedBatches: [] }
    let settings = { accountId: 'main', botId: 'fixture-bot', telegramEnabled: true, fixedSharePassword: 'test', reshareEnabled: true, reviewRetryEnabled: true, reviewRetryMinutes: 30, reviewRetryMax: 6, tgBatchWaitSeconds: 5, generation: { baseUrl: 'http://cinema.example:8080', outputDir: '.', videoExtensions: 'mkv,mp4', metadataExtensions: 'srt,ass', minSizeMB: 0, recursive: true, sharePath: '/电影' } }
    let failSchedule = false, failGeneration = false, nextSchedule
    function holdNextSchedule() {
      let release, start
      const response = new Promise(resolve => { release = resolve }), started = new Promise(resolve => { start = resolve })
      nextSchedule = { response, start }
      return { release, started }
    }
    const endpoint = '/api/admin/p115/folder-monitors', automation = '/api/admin/p115/share/automation-config'
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method(), body = request.postDataJSON()
      calls.push({ path: url.pathname, method, body })
      let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'fixture', role: 'admin' } }
      else if (url.pathname.includes('/notifications')) data = { items: [], unread: 0 }
      else if (url.pathname === '/api/admin/p115/accounts') data = { accounts: [{ id: 'main', primary: true, configured: true }] }
      else if (url.pathname === endpoint && method === 'GET') data = { monitors: monitor ? [monitor] : [] }
      else if (url.pathname === automation) {
        if (method === 'POST') {
          if (failGeneration) { failGeneration = false; status = 409; data = { error: '模拟生成设置保存失败' } }
          else { settings = { ...settings, generation: { ...settings.generation, ...body.generation } }; data = { settings } }
        } else data = { settings }
      } else if (url.pathname === endpoint + '/one/schedule') {
        const held = nextSchedule; nextSchedule = undefined
        if (held) { held.start(); await held.response }
        if (failSchedule) { failSchedule = false; status = 409; data = { error: '模拟监控设置保存失败' } }
        else { Object.assign(monitor, { enabled: body.enabled, intervalSeconds: body.enabled ? body.intervalSeconds : 0, schedulePaused: '', scheduleMessage: '' }); data = monitor }
      } else if (url.pathname === endpoint + '/one/share') {
        Object.assign(monitor, { stage: 'generating', running: true, queued: false, hasBatch: true, logTaskID: 'synthetic-run', batchCount: 2, batchMoved: 2, batchDirectoryName: 'synthetic-batch', shareUrl: 'https://115.com/s/synthetic' })
        data = { accepted: true, taskId: 'synthetic-run' }
      } else if (url.pathname === endpoint + '/one/recover') {
        Object.assign(monitor, { recoveryMessage: '模拟核对完成，未完成文件已保留。' }); data = monitor
      } else if (url.pathname === endpoint + '/one/end') {
        Object.assign(monitor, { stage: 'ended', running: false, queued: false, hasBatch: false, lastError: '', enabled: false, intervalSeconds: 0, batchDirectoryName: '', recoveryMessage: '任务已结束，文件保留', recentRuns: [{ taskId: 'synthetic-run', status: 'ended', trigger: 'manual', message: '任务已结束，原文件位置见批次详情', time: '2026-10-30T10:00:00Z' }], endedBatches: [{ logTaskId: 'synthetic-run', accountId: 'main', directoryId: '99', directoryName: 'synthetic-retained-files', cleaned: false, endedAt: '2026-10-30T10:00:00Z' }] }); data = monitor
      } else if (url.pathname === '/api/admin/p115/share/output-directories') {
        const current = url.searchParams.get('path') || ''
        data = { path: current, displayPath: current ? `/strm/${current}` : '/strm', directories: current ? [] : [{ name: '影视', path: '影视', displayPath: '/strm/影视' }], truncated: false }
      }
      else if (url.pathname === '/api/admin/p115/cloud-download/paths') {
        assert.equal(url.searchParams.get('accountId'), 'main')
        data = { directories: url.searchParams.get('cid') === '0' ? [{ id: '20', name: '新目录' }] : [], hasMore: false }
      } else if (url.pathname === endpoint + '/one' && method === 'PUT') { Object.assign(monitor, body, { enabled: false, intervalSeconds: 0 }); data = monitor }
      else if (url.pathname === endpoint && method === 'POST') { monitor = { ...body, id: 'one', stage: 'waiting', running: false, queued: false, hasBatch: false, lastError: '', enabled: false }; data = monitor }
      else if (url.pathname === endpoint + '/one' && method === 'DELETE') { monitor = undefined; status = 204 }
      await route.fulfill({ status, contentType: 'application/json', body: status === 204 ? undefined : JSON.stringify(data) }).catch(() => {})
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173', location = origin + '/115/folder-monitor'
    const toggle = () => page.getByRole('switch', { name: '自动监控', exact: true })
    const interval = () => page.getByRole('combobox', { name: '检查间隔', exact: true })
    const current = () => page.getByRole('heading', { name: '当前任务', exact: true })
    const dialog = () => page.getByRole('dialog', { name: '分享 STRM 生成设置', exact: true })
    const openSettings = () => page.getByRole('button', { name: '生成设置', exact: true }).click()
    const scheduleCalls = () => calls.filter(call => call.path.endsWith('/one/schedule'))
    const settingSaves = () => calls.filter(call => call.path === automation && call.method === 'POST')
    const reload = () => page.goto(location, { waitUntil: 'networkidle' })
    const flip = () => toggle().locator('..').click()
    await fs.mkdir('test-results/folder-monitor', { recursive: true })
    await reload()
    const initialOverflow = await page.evaluate(() => document.body.style.overflow)
    await page.getByRole('heading', { name: '监控分享', exact: true }).waitFor()
    assert.equal(await current().count(), 0, 'idle monitoring must not show a completed task panel')
    assert.equal(await toggle().isChecked(), false)
    assert.equal(await interval().isDisabled(), true)
    assert.equal(await page.getByRole('heading', { name: '最近记录', exact: true }).count(), 0)
    assert.match(await page.locator('main').innerText(), /本批源文件.*115 回收站/)
    assert.match(await page.locator('main').innerText(), /监控目录本身保留/)
    assert.equal(calls.some(call => call.method !== 'GET'), false, 'opening the page must not start a job or save settings')
    passed.push('idle state, scope and source-file cleanup explanation')

    failSchedule = true; await flip()
    await page.getByRole('alert').filter({ hasText: '模拟监控设置保存失败' }).waitFor()
    assert.equal(await toggle().isChecked(), false, 'failed enabling must restore the saved switch state')
    const heldSchedule = holdNextSchedule(); await flip(); await heldSchedule.started
    assert.equal(await toggle().isDisabled(), true, 'pending schedule saves must lock the switch')
    assert.equal(await interval().isDisabled(), true, 'pending schedule saves must lock the interval')
    heldSchedule.release()
    await page.waitForFunction(() => { const el = document.querySelector('[role="switch"][aria-label="自动监控"]'); return el.checked && !el.disabled })
    assert.equal(scheduleCalls().at(-1).body.enabled, true)
    await Promise.all([page.waitForResponse(response => response.url().endsWith('/one/schedule') && response.request().postDataJSON().intervalSeconds === 1800), interval().selectOption('1800')])
    assert.equal(scheduleCalls().at(-1).body.intervalSeconds, 1800)
    await reload(); assert.equal(await toggle().isChecked(), true); assert.equal(await interval().inputValue(), '1800')
    failSchedule = true; await flip()
    await page.getByRole('alert').filter({ hasText: '模拟监控设置保存失败' }).waitFor()
    assert.equal(await toggle().isChecked(), true, 'failed disabling must keep the saved switch enabled')
    failSchedule = true
    await Promise.all([page.waitForResponse(response => response.url().endsWith('/one/schedule') && response.status() === 409), interval().selectOption('600')])
    await page.waitForFunction(() => { const el = document.querySelector('select'); return el.value === '1800' && !el.disabled })
    passed.push('schedule save failure rollback, switch and interval persistence')

    await openSettings(); await dialog().getByLabel('STRM 播放地址', { exact: true }).fill('http://cancelled.example:8080')
    await dialog().getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(settingSaves().length, 0); assert.equal(settings.generation.baseUrl, 'http://cinema.example:8080')
    assert.equal(await page.evaluate(() => document.body.style.overflow), initialOverflow, 'closing generation settings must release its scroll lock')
    await openSettings(); await dialog().getByLabel('STRM 播放地址', { exact: true }).fill('http://updated.example:8080')
    await dialog().getByRole('button', { name: /选择目录/ }).click()
    const output = page.getByRole('dialog', { name: '选择 STRM 输出目录', exact: true })
    await output.getByRole('button', { name: /^影视/ }).click()
    await output.getByRole('button', { name: '选择此目录', exact: true }).click(); await output.waitFor({ state: 'hidden' })
    assert.equal(await dialog().getByLabel('STRM 播放地址', { exact: true }).inputValue(), 'http://updated.example:8080', 'nested directory selection must preserve the generation draft')
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden', 'closing the nested picker must retain the parent dialog scroll lock')
    await dialog().screenshot({ path: 'test-results/folder-monitor/settings-desktop.png', animations: 'disabled' })
    settings.botId = 'changed-elsewhere'; settings.reviewRetryMax = 9
    failGeneration = true; await dialog().getByRole('button', { name: '保存设置', exact: true }).click()
    await dialog().getByRole('alert').filter({ hasText: '模拟生成设置保存失败' }).waitFor()
    assert.equal(await dialog().getByLabel('STRM 播放地址', { exact: true }).inputValue(), 'http://updated.example:8080')
    await dialog().getByRole('button', { name: '保存设置', exact: true }).click(); await dialog().waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), initialOverflow, 'closing the parent after nested selection must release all scroll locks')
    assert.equal(settings.generation.baseUrl, 'http://updated.example:8080')
    assert.equal(settings.generation.outputDir, '影视')
    assert.equal(settings.botId, 'changed-elsewhere'); assert.equal(settings.reviewRetryMax, 9)
    assert.equal(settings.fixedSharePassword, 'test'); assert.equal(settings.telegramEnabled, true)
    assert.equal(settings.generation.sharePath, '/电影', 'editing generation options must retain the unrelated share path')
    assert.deepEqual(Object.keys(settingSaves().at(-1).body), ['generation'])
    assert.deepEqual(Object.keys(settingSaves().at(-1).body.generation).sort(), ['baseUrl', 'metadataExtensions', 'minSizeMB', 'outputDir', 'videoExtensions'])
    passed.push('generation dialog cancellation, failure draft retention and preservation of latest shared settings')

    await page.getByRole('button', { name: '立即运行', exact: true }).click(); await current().waitFor()
    assert.equal(await interval().isDisabled(), true)
    assert.equal(await toggle().isDisabled(), false, 'running jobs must still allow disabling future monitoring')
    await flip(); await page.waitForFunction(() => !document.querySelector('[role="switch"][aria-label="自动监控"]').checked)
    assert.equal(monitor.running, true, 'disabling monitoring must not cancel the current job')
    assert.equal(await current().count(), 1)
    passed.push('manual execution, active task visibility and disabling future monitoring without cancelling the job')

    Object.assign(monitor, { running: false, queued: false, stage: 'error', lastError: '模拟 STRM 写入失败', enabled: true, intervalSeconds: 600 })
    await reload(); await current().waitFor()
    await page.getByRole('button', { name: '核对残留', exact: true }).click()
    await page.getByText('模拟核对完成，未完成文件已保留。', { exact: true }).waitFor()
    page.once('dialog', d => d.accept())
    await page.getByRole('button', { name: '结束本次任务', exact: true }).click()
    await page.getByRole('heading', { name: '最近记录', exact: true }).waitFor()
    assert.equal(await current().count(), 0, 'ended jobs belong in history rather than the current task')
    assert.equal(await toggle().isChecked(), false, 'ending a failed batch must reflect the backend disabling monitoring')
    const retained = page.locator('details').filter({ hasText: 'synthetic-retained-files' }).last()
    await retained.locator('summary').click(); await page.getByText('synthetic-retained-files', { exact: true }).waitFor()
    assert.equal(calls.some(call => /delete|cancel/i.test(call.path)), false, 'recovery and ending must not delete files or cancel shares')
    passed.push('failed task recovery, ended task history and retained file visibility')

    await page.screenshot({ path: 'test-results/folder-monitor/desktop.png', fullPage: true, animations: 'disabled' })
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `page overflows at ${width}px`)
      await openSettings(); await dialog().getByLabel('STRM 播放地址', { exact: true }).waitFor()
      assert.equal(await page.evaluate(() => [...document.querySelectorAll('dialog[open]')].some(el => el.getBoundingClientRect().right > innerWidth || el.scrollWidth > el.clientWidth + 1)), false, `generation dialog overflows at ${width}px`)
      if (width === 390) await dialog().screenshot({ path: 'test-results/folder-monitor/settings-mobile.png', animations: 'disabled' })
      await dialog().getByRole('button', { name: '取消', exact: true }).click()
    }
    await page.setViewportSize({ width: 390, height: 844 }); await page.screenshot({ path: 'test-results/folder-monitor/mobile.png', fullPage: true, animations: 'disabled' })
    await page.evaluate(() => document.documentElement.classList.add('dark')); await page.screenshot({ path: 'test-results/folder-monitor/mobile-dark.png', fullPage: true, animations: 'disabled' })
    passed.push('desktop, 390px, 320px and dark mode layout')

    monitor = undefined; await reload()
    assert.equal(await page.getByRole('button', { name: '立即运行', exact: true }).isDisabled(), true)
    assert.equal(await toggle().isDisabled(), true)
    await openSettings(); await dialog().getByLabel('STRM 播放地址', { exact: true }).waitFor()
    await dialog().getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '选择目录', exact: true }).click()
    const source = page.getByRole('dialog')
    await source.getByRole('button', { name: /新目录/ }).click()
    await source.getByRole('button', { name: '选择此目录', exact: true }).click()
    await source.waitFor({ state: 'hidden' })
    assert.equal(monitor.sourceCID, '20'); assert.equal(monitor.enabled, false)
    passed.push('first-time setup reuses directory selection and permits generation settings before choosing a source')

    await openSettings(); await dialog().getByLabel('STRM 播放地址', { exact: true }).waitFor()
    await dialog().getByRole('button', { name: /选择目录/ }).click(); await output.waitFor()
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await page.evaluate(() => { history.pushState({}, '', '/115'); dispatchEvent(new PopStateEvent('popstate')) })
    await page.getByRole('heading', { name: '监控分享', exact: true }).waitFor({ state: 'hidden' })
    await page.getByRole('heading', { name: '115 网盘', exact: true }).waitFor()
    assert.equal(await page.locator('dialog[open]').count(), 0, 'SPA navigation must unmount both dialogs')
    assert.equal(await page.evaluate(() => document.body.style.overflow), initialOverflow, 'SPA navigation with nested dialogs must restore the original body overflow')
    passed.push('nested picker preserves the parent scroll lock, and SPA navigation releases both locks')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, requests: calls.length, screenshots: 'test-results/folder-monitor' }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
