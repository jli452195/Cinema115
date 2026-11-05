// 所有业务接口均在浏览器中模拟，不创建真实下载任务。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const magnet = n => `magnet:?xt=urn:btih:${n.toString(16).padStart(40, '0')}`

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = []
  async function fixture(source = 'gying') {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block', reducedMotion: 'reduce' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light')
      window.copyAllowed = true
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { if (!window.copyAllowed) throw new Error('模拟复制被拒绝') } } })
      document.execCommand = () => window.copyAllowed
    })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', error => errors.push(error.message))
    const calls = [], held = new Map(), unknown = new Set(), rejected = new Set()
    const state = { configured: true, lostJob: false }
    const naming = require('./media-naming-fixture.cjs').mediaNamingFixture('百变之星', '2003')
    const files = [{ id: 'show/E001.mkv', name: 'E001.mkv', sourcePath: '/show/E001.mkv', directory: false, parentId: '', size: 1024, mediaType: 'video', season: 1, episode: 1 }]
    const links = Array.from({ length: 15 }, (_, i) => ({ category: 'torrent', type: 'magnet', typeLabel: '磁力', subTypeLabel: '1080P', workTitle: `测试合集 ${i + 1} [1080p]`, url: magnet(i + 1), hasTorrent: true }))
    links.push({ ...links[0], workTitle: '相同 BTIH 的别名', url: `${magnet(1)}&dn=alias` })
    links.push({ ...links[2], workTitle: '待核对 BTIH 的别名', url: `${magnet(3)}&dn=alias` })
    await context.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname
      const body = request.postDataJSON()
      calls.push({ endpoint, body, method: request.method() })
      let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin', disabled: false } }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 12501, kind: 'tv', title: '百变之星', originalTitle: 'カレイドスター', overview: '', posterUrl: '', backdropUrl: '', date: '2003-04-03', rating: 7, votes: 1, genres: [{ id: 16, name: '动画' }], countries: ['Japan'], countryCodes: ['JP'], cast: [], seasons: [{ number: 1, name: '第一季', episodeCount: 26 }], collection: null }
      else if (endpoint.endsWith(`/${source}/config`)) data = { configured: true, hasSession: true, resourceOrder: [] }
      else if (endpoint.endsWith(`/${source}/search`)) data = { query: 'カレイドスター', matchedBy: 'title', total: 1, cached: false, attempts: [], items: [{ id: 'tv:Show', title: '百变之星', year: 2003, mediaType: 'tv', links }] }
      else if (endpoint.endsWith('/download-organize/files')) data = body.magnet === magnet(2) ? { files: [], reason: '清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）' } : { files, subtitles: [] }
      else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: state.configured ? 'primary' : '', targetCID: state.configured ? '10' : '', targetPath: '/下载' }
      else if (endpoint.endsWith('/download-organize/config')) data = { accountId: 'primary', targetCID: '20', targetPath: '/动漫' }
      else if (endpoint.endsWith('/download-organize/share-config')) data = { ready: false, reason: '请先完善分享 STRM 设置' }
      else if (endpoint.endsWith('/naming/media-preview')) data = naming.preview(body, 3)
      else if (endpoint.endsWith('/download-organize/preview')) data = { id: 'one-plan', title: '百变之星', status: 'preview', ...naming.plan(body.files.map(file => files.find(candidate => candidate.id === file.id))) }
      else if (endpoint.endsWith('/download-organize/jobs')) {
        if (request.method() === 'GET') data = { jobs: [] }
        else if (state.lostJob) { state.lostJob = false; status = 502; data = { error: '模拟回执丢失' } }
        else {
          if (state.jobGate) await state.jobGate
          data = { id: 'one-plan', title: '百变之星', status: 'queued', items: [{ file: files[0] }] }
        }
      } else if (endpoint.endsWith('/cloud-download/tasks')) {
        const key = new URL(body.url).searchParams.get('xt')
        if (held.has(key)) await held.get(key).promise
        if (unknown.has(key)) { status = 504; data = { error: '处理结果待核对，请先查看云下载', outcomeUncertain: true } }
        else if (rejected.has(key)) { status = 400; data = { error: '模拟明确拒绝，请稍后重试' } }
        else data = { accepted: true }
      } else if (endpoint.endsWith('/accounts')) data = { accounts: [] }
      await route.fulfill({ status, json: data })
    })
    const count = (suffix, method) => calls.filter(call => call.endpoint.endsWith(suffix) && (!method || call.method === method)).length
    const search = page.locator('dialog.gy-dialog')
    const rows = search.locator('.gy-resource-list > li')
    const toasts = page.locator('[data-sonner-toast]:not([data-removed="true"])')
    async function open() {
      await page.goto(`${origin}/tv/12501`)
      await page.getByRole('button', { name: source === 'gying' ? /观影\s*搜索/ : /蜗牛\s*搜索/, exact: true }).click()
      await rows.first().waitFor()
      await page.evaluate(async () => { window.toastFixture = (await import('/src/composables/useToast.ts')).useToast() })
    }
    async function clear() {
      await page.evaluate(() => window.toastFixture.clear())
      await page.waitForFunction(() => !document.querySelector('[data-sonner-toast]'))
    }
    async function organize(index, loseReceipt = false) {
      await rows.nth(index).locator('[data-gy-action="organize"]').click()
      const child = page.locator('dialog.anime-floating-organize')
      await child.getByText('版本 3', { exact: true }).waitFor()
      await child.getByRole('checkbox', { name: '选择目录 show 及下级', exact: true }).check()
      state.lostJob = loseReceipt
      await child.getByRole('button', { name: '下载并自动整理', exact: true }).click()
      if (loseReceipt) {
        await child.getByRole('button', { name: '确认任务状态', exact: true }).waitFor()
        assert.equal(await child.isVisible(), true)
        assert.equal(await toasts.filter({ hasText: '任务已创建' }).count(), 0)
        await child.getByRole('button', { name: '确认任务状态', exact: true }).click()
      }
      await child.waitFor({ state: 'detached' })
      await toasts.filter({ hasText: '任务已创建' }).waitFor()
    }
    function hold(n) {
      let release
      const promise = new Promise(resolve => { release = resolve })
      held.set(`urn:btih:${n.toString(16).padStart(40, '0')}`, { promise, release })
      return release
    }
    function holdJob() {
      let release
      state.jobGate = new Promise(resolve => { release = resolve })
      return release
    }
    await open()
    return { context, page, calls, count, rows, search, toasts, state, unknown, rejected, clear, organize, hold, holdJob }
  }
  try {
    {
      const f = await fixture()
      try {
        await f.search.locator('.gy-item-card').filter({ hasText: '百变之星' }).click()
        await f.search.locator('.gy-source-strip').first().getByRole('button', { name: /^磁力/ }).click()
        await f.search.locator('.gy-subtypes').getByRole('button', { name: /^1080P/ }).click()
        const row = f.rows.nth(6), scrollBody = f.search.locator('.gy-body')
        await row.locator('[data-gy-action="organize"]').scrollIntoViewIfNeeded()
        const scrollTop = await scrollBody.evaluate(el => el.scrollTop)
        const before = await row.boundingBox()
        await f.organize(6, true)
        assert.equal(await f.search.isVisible(), true)
        assert.equal(await f.toasts.count(), 1)
        assert.equal(await f.search.locator('.gy-task-status.gy-success').count(), 0)
        assert.equal(await f.search.locator('.gy-subtypes button[aria-pressed="true"]').textContent(), '1080P17')
        assert(Math.abs(await scrollBody.evaluate(el => el.scrollTop) - scrollTop) <= 1)
        const submitted = row.locator('[data-gy-action="cloud-task"]')
        assert.equal(await submitted.getAttribute('href'), '/115/download-organize')
        assert.equal(await submitted.textContent().then(text => text.trim()), '已提交')
        assert.equal(await row.evaluate(el => el.contains(document.activeElement)), true)
        assert(Math.abs((await row.boundingBox()).height - before.height) <= 1)
        assert.deepEqual(f.calls.filter(call => call.endpoint.endsWith('/download-organize/jobs') && call.method === 'POST').map(call => call.body.previewId), ['one-plan', 'one-plan'])
        assert.equal(f.count('/download-organize/preview'), 1)
        assert.equal(f.count('/cloud-download/tasks'), 0)
        await f.toasts.getByRole('button', { name: '查看任务', exact: true }).click()
        await f.page.waitForURL('**/115/download-organize')
        await f.search.waitFor({ state: 'detached' })
        assert.equal(await f.page.evaluate(() => document.body.style.overflow), '')
        passed.push('整理回执丢失复用原任务；成功关闭子弹窗，只显示一条 Toast，保留筛选、滚动、焦点和卡片高度；Toast 操作可跳转')
      } finally { await f.context.close() }
    }
    {
      const f = await fixture()
      try {
        const row = f.rows.first(), alias = f.rows.nth(15), second = f.rows.nth(1)
        const before = await row.boundingBox(), releaseFirst = f.hold(1), releaseSecond = f.hold(2)
        await row.locator('[data-gy-action="cloud"]').click()
        await row.getByRole('button', { name: '提交中…', exact: true }).waitFor()
        assert.equal(await alias.locator('[data-gy-action="cloud"]').isDisabled(), true)
        assert.equal(await alias.locator('[data-gy-action="organize"]').isDisabled(), true)
        assert.equal(await f.search.getByRole('button', { name: '关闭观影搜索', exact: true }).isDisabled(), true)
        await f.page.keyboard.press('Escape')
        assert.equal(await f.search.isVisible(), true)
        await f.page.evaluate(async () => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/115/cloud-download') })
        assert.equal(new URL(f.page.url()).pathname, '/tv/12501')
        assert.equal(await f.search.isVisible(), true)
        await f.page.evaluate(async () => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/tv/12502') })
        assert.equal(new URL(f.page.url()).pathname, '/tv/12501')
        await f.search.getByRole('link', { name: '观影配置', exact: true }).click()
        assert.equal(new URL(f.page.url()).pathname, '/tv/12501')
        assert.equal(await f.search.isVisible(), true)
        assert.equal(await second.locator('[data-gy-action="cloud"]').isDisabled(), false)
        await second.locator('[data-gy-action="cloud"]').click()
        await f.page.waitForFunction(() => document.querySelectorAll('[data-gy-action="cloud"][disabled]').length >= 3)
        releaseFirst()
        await row.locator('[data-gy-action="cloud-task"]').waitFor()
        assert.equal(await f.search.getByRole('button', { name: '关闭观影搜索', exact: true }).isDisabled(), true)
        releaseSecond()
        await second.locator('[data-gy-action="cloud-task"]').waitFor()
        assert.equal(f.count('/cloud-download/tasks'), 2)
        assert.equal(await f.search.locator('.gy-task-status.gy-success').count(), 0)
        assert.equal(await alias.locator('[data-gy-action="cloud-task"]').getAttribute('href'), '/115/cloud-download')
        assert(Math.abs((await row.boundingBox()).height - before.height) <= 1)
        await f.clear()
        await row.locator('[data-gy-action="cloud-task"]').click()
        await f.page.waitForURL('**/115/cloud-download')
        await f.search.waitFor({ state: 'detached' })
        assert.equal(await f.page.evaluate(() => document.body.style.overflow), '')
        passed.push('普通云下载阻止关闭、Escape 和离页；同 BTIH 别名防重，不同磁力可并发；回执后卡片不增高且保留任务入口')
      } finally { await f.context.close() }
    }
    {
      const f = await fixture()
      try {
        const releaseCloud = f.hold(1), releaseJob = f.holdJob()
        await f.rows.first().locator('[data-gy-action="cloud"]').click()
        await f.rows.first().getByRole('button', { name: '提交中…', exact: true }).waitFor()
        await f.rows.nth(4).locator('[data-gy-action="organize"]').click()
        const child = f.page.locator('dialog.anime-floating-organize')
        await child.getByText('版本 3', { exact: true }).waitFor()
        await child.getByRole('checkbox', { name: '选择目录 show 及下级', exact: true }).check()
        await child.getByRole('button', { name: '下载并自动整理', exact: true }).click()
        await child.getByRole('button', { name: '正在确认并创建任务…', exact: true }).waitFor()
        releaseCloud()
        const cloudToast = f.toasts.filter({ hasText: '已提交云下载' })
        await cloudToast.getByRole('button', { name: '查看云下载', exact: true }).click()
        assert.equal(await cloudToast.getAttribute('data-removed'), 'false', '被创建中的任务阻止跳转时保留原 Toast 操作入口')
        assert.equal(new URL(f.page.url()).pathname, '/tv/12501')
        assert.equal(await child.isVisible(), true)
        assert.equal(await child.getByRole('button', { name: '关闭下载并整理', exact: true }).isDisabled(), true)
        await f.page.evaluate(async () => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/tv/12502') })
        assert.equal(new URL(f.page.url()).pathname, '/tv/12501')
        assert.equal(await child.isVisible(), true)
        assert.equal(f.count('/download-organize/jobs', 'POST'), 1)
        releaseJob()
        await child.waitFor({ state: 'detached' })
        await f.toasts.filter({ hasText: '任务已创建' }).getByRole('button', { name: '查看任务', exact: true }).waitFor()
        assert.equal(await f.rows.nth(4).locator('[data-gy-action="cloud-task"]').getAttribute('href'), '/115/download-organize')
        assert.equal(f.count('/download-organize/preview'), 1)
        assert.deepEqual(f.calls.filter(call => call.endpoint.endsWith('/download-organize/jobs') && call.method === 'POST').map(call => call.body.previewId), ['one-plan'])
        await cloudToast.getByRole('button', { name: '查看云下载', exact: true }).click()
        await f.page.waitForURL('**/115/cloud-download')
        await f.search.waitFor({ state: 'detached' })
        assert.equal(await f.page.evaluate(() => document.body.style.overflow), '')
        passed.push('云下载 Toast 操作不能打断另一整理任务创建且保留原入口；原任务只创建一次，完成后可自动关闭并使用原 Toast 跳转')
      } finally { await f.context.close() }
    }
    {
      const f = await fixture()
      try {
        const unknown = f.rows.nth(2)
        f.unknown.add(`urn:btih:${'3'.padStart(40, '0')}`)
        await unknown.locator('[data-gy-action="cloud"]').click()
        await unknown.getByRole('button', { name: '待核对', exact: true }).waitFor()
        assert.equal(await unknown.locator('[data-gy-action="cloud"]').isDisabled(), true)
        assert.equal(await f.rows.nth(16).locator('[data-gy-action="cloud"]').isDisabled(), true)
        assert.equal(await unknown.getByRole('link', { name: '查看云下载', exact: true }).getAttribute('href'), '/115/cloud-download')
        await f.clear()
        assert.equal(await unknown.locator('.gy-task-status').isVisible(), true)
        f.state.configured = false
        await f.rows.nth(3).locator('[data-gy-action="cloud"]').click()
        await f.rows.nth(3).getByRole('link', { name: '前往配置', exact: true }).waitFor()
        assert.equal(f.count('/cloud-download/tasks'), 1)
        f.state.configured = true
        const fallback = f.rows.nth(1)
        await fallback.locator('[data-gy-action="organize"]').click()
        await fallback.getByText(/清单中没有可整理的视频文件/).waitFor()
        await fallback.getByRole('button', { name: '普通离线', exact: true }).click()
        await fallback.locator('[data-gy-action="cloud-task"]').waitFor()
        await f.toasts.filter({ hasText: '已提交云下载' }).waitFor()
        assert.equal(await fallback.locator('.gy-notice').count(), 0)
        assert.equal(f.count('/cloud-download/tasks'), 2)
        await f.clear()
        const copyRow = f.rows.nth(4), before = await copyRow.boundingBox()
        await copyRow.locator('[data-gy-action="copy"]').click()
        await f.toasts.filter({ hasText: '已复制' }).waitFor()
        assert(Math.abs((await copyRow.boundingBox()).height - before.height) <= 1)
        await f.clear()
        await f.page.evaluate(() => { window.copyAllowed = false })
        await copyRow.locator('[data-gy-action="copy"]').click()
        const manual = copyRow.getByRole('textbox', { name: '手动复制资源链接', exact: true })
        await manual.waitFor()
        assert.equal(await manual.inputValue(), magnet(5))
        assert.equal(await manual.getAttribute('readonly'), '')
        const rejected = f.rows.nth(5), rejectedKey = `urn:btih:${'6'.padStart(40, '0')}`
        const rejectedHeight = (await rejected.boundingBox()).height
        f.rejected.add(rejectedKey)
        await rejected.locator('[data-gy-action="cloud"]').click()
        await f.toasts.filter({ hasText: '模拟明确拒绝，请稍后重试' }).waitFor()
        assert.equal(await rejected.locator('.gy-notice').count(), 0)
        assert.equal(await rejected.locator('[data-gy-action="cloud"]').isDisabled(), false)
        assert(Math.abs((await rejected.boundingBox()).height - rejectedHeight) <= 1)
        f.rejected.delete(rejectedKey)
        await rejected.locator('[data-gy-action="cloud"]').click()
        await rejected.locator('[data-gy-action="cloud-task"]').waitFor()
        assert.equal(f.count('/cloud-download/tasks'), 4)
        passed.push('未知回执与别名保持待核对并提供入口；缺配置不提交；无视频保留普通离线；复制失败可手动复制；明确拒绝只 Toast 且可重试')
      } finally { await f.context.close() }
    }
    {
      const f = await fixture('woniu')
      try {
        await f.page.setViewportSize({ width: 320, height: 800 })
        const row = f.rows.first(), before = await row.boundingBox()
        await row.locator('[data-gy-action="cloud"]').click()
        await row.locator('[data-gy-action="cloud-task"]').waitFor()
        await f.toasts.filter({ hasText: '已提交云下载' }).waitFor()
        assert.equal(await f.toasts.count(), 1)
        assert.equal(await row.locator('.gy-notice').count(), 0)
        assert(Math.abs((await row.boundingBox()).height - before.height) <= 1)
        await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true })
        for (const theme of ['light', 'dark']) {
          await f.page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
          const layout = await f.toasts.evaluate(el => {
            const box = el.getBoundingClientRect()
            const dialog = document.querySelector('dialog.gy-dialog')
            const content = el.querySelector('[data-content]').getBoundingClientRect()
            const action = el.querySelector('[data-button]').getBoundingClientRect()
            const close = el.querySelector('[data-close-button]').getBoundingClientRect()
            return {
              left: box.left, right: box.right, width: box.width,
              overflow: document.documentElement.scrollWidth > innerWidth, dialogOverflow: dialog.scrollWidth > dialog.clientWidth + 1,
              contentWidth: content.width, contentRight: content.right, contentBottom: content.bottom,
              actionHeight: action.height, actionLeft: action.left, actionRight: action.right, actionTop: action.top, actionBottom: action.bottom,
              contentLeft: content.left, toastBottom: box.bottom, closeBottom: close.bottom,
            }
          })
          assert(layout.left >= 0 && layout.right <= 320)
          assert.equal(layout.overflow, false); assert.equal(layout.dialogOverflow, false)
          assert(layout.actionHeight >= 44, '手机端 Toast 操作按钮至少高 44px')
          assert(layout.contentWidth >= layout.width - 105, '正文占满图标与关闭按钮之间的可用列')
          assert(layout.contentRight >= layout.right - 47)
          assert(layout.actionTop >= layout.contentBottom - 1, '操作按钮位于正文下方，不与正文重叠')
          assert(Math.abs(layout.actionLeft - layout.contentLeft) <= 1)
          assert(layout.actionRight <= layout.right && layout.actionBottom <= layout.toastBottom)
          assert(layout.actionTop >= layout.closeBottom, '操作按钮与右上角关闭按钮不重叠')
          await f.page.screenshot({ path: path.join(__dirname, `../test-results/magnet-toast-320-${theme}.png`), animations: 'disabled' })
        }
        await f.toasts.getByRole('button', { name: '关闭提示', exact: true }).click()
        await f.page.waitForFunction(() => !document.querySelector('[data-sonner-toast]'))
        await f.search.getByRole('button', { name: '关闭蜗牛搜索', exact: true }).click()
        await f.search.waitFor({ state: 'detached' })
        assert.equal(await f.page.evaluate(() => document.body.style.overflow), '')
        passed.push('蜗牛复用相同成功行为；320px 深浅主题无溢出，Toast 正文占满可用列，操作按钮另起一行且至少 44px，无重叠并可原生关闭')
      } finally { await f.context.close() }
    }
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
