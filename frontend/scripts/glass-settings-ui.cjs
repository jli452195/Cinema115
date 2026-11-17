// 所有接口使用模拟数据；验证浏览器外观偏好，不读取真实凭据或提交业务任务。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const storageKey = 'cinema115-glass-settings'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], requests = [], passed = []
  let role = 'admin'
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'light', serviceWorkers: 'block', permissions: ['clipboard-read', 'clipboard-write'] })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-glass-token'); if (!localStorage.getItem('cinema115-theme')) localStorage.setItem('cinema115-theme', 'light') })
    await context.route('**/api/**', async route => {
      const endpoint = new URL(route.request().url()).pathname
      requests.push({ endpoint, method: route.request().method() })
      let data = { configured: true, accounts: [], items: [], total: 0, totalPages: 1, hasMore: false }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock-glass', username: '玻璃外观验证', role } }
      else if (endpoint.endsWith('/telegram-user/config')) data = { configured: true, hasSession: true, botConfigured: true }
      else if (endpoint.endsWith('/telegram-user/channels')) data = { account: { hasSession: true }, channels: [{ id: 'mock-channel', title: '模拟频道' }] }
      else if (endpoint.endsWith('/resources/search-settings')) data = { order: ['re0', 'bot', 'channels', 'gying', 'mikan', 'garden', 'dmhy', 'woniu'] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 300146, kind: 'tv', title: '嫁金钗', originalTitle: 'Where the Mask Ends', overview: '玻璃设置验证。', posterUrl: '', backdropUrl: '', date: '2026-01-01', rating: 8, votes: 10, status: 'Released', runtime: 45, episodeCount: 30, genres: [], countries: [], cast: [], seasons: [], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '' }
      else if (endpoint.includes('/emby/media/')) data = { media: { state: 'absent', items: [] } }
      else if (endpoint.includes('recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, request: null, libraryState: 'absent', items: [], hasMore: false }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      await route.fulfill({ json: data })
    })
    const page = await context.newPage(), cdp = await context.newCDPSession(page)
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    const trigger = () => page.locator('button[aria-controls="header-management"]')
    const dialog = () => page.getByRole('dialog', { name: '外观设置', exact: true })
    const detailPills = () => page.locator('.media-detail .detail-search-buttons .search-pill')
    const open = async () => {
      await trigger().click()
      await page.getByRole('navigation', { name: '账户菜单' }).getByRole('button', { name: '外观设置', exact: true }).click()
      await dialog().waitFor()
      await dialog().evaluate(async element => { await Promise.all(element.getAnimations().map(animation => animation.finished.catch(() => {}))) })
      assert.equal(await page.locator('#header-management').count(), 0, '菜单退场完成后再打开外观弹窗')
      assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    }
    const close = async () => {
      await dialog().getByRole('button', { name: '关闭外观设置', exact: true }).click()
      await dialog().waitFor({ state: 'detached' })
      assert.equal(await trigger().evaluate(element => document.activeElement === element), true, '关闭返回稳定的账户入口')
    }
    const values = () => dialog().getByRole('slider').evaluateAll(elements => elements.map(element => Number(element.value)))
    const change = (name, value) => dialog().getByRole('slider', { name, exact: true }).evaluate((element, value) => { element.value = String(value); element.dispatchEvent(new Event('input', { bubbles: true })) }, value)
    const pillStyle = locator => locator.first().evaluate(element => { const style = getComputedStyle(element, '::before'); return { filter: style.backdropFilter, background: style.backgroundColor, shadow: style.boxShadow, radius: style.borderRadius, textFilter: getComputedStyle(element).filter, height: element.getBoundingClientRect().height } })
    const screenshot = async name => { await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true }); await page.screenshot({ path: path.join(__dirname, '../test-results', name + '.png') }) }
    const useTheme = theme => page.evaluate(async theme => { const { useTheme } = await import('/src/composables/useTheme.ts'); await useTheme().setTheme(theme) }, theme)

    await page.goto(origin + '/tv/300146')
    await detailPills().last().waitFor()
    assert.equal(await detailPills().count(), 6)
    await open()
    assert.deepEqual(await values(), [22, 175, 42, 80])
    const defaultStyle = await pillStyle(detailPills())
    assert.match(defaultStyle.filter, /blur\(22px\).*saturate\(1\.75\)/)
    assert.match(defaultStyle.background, /0\.42\)/)
    assert.equal(defaultStyle.textFilter, 'none')
    assert.ok(defaultStyle.height >= 44)
    await dialog().getByRole('button', { name: '传统毛玻璃', exact: true }).click()
    assert.deepEqual(await values(), [24, 110, 80, 35])
    await change('背景模糊', 13); await change('色彩饱和度', 160); await change('底色浓度', 65); await change('边缘高光', 20)
    assert.deepEqual(await values(), [13, 160, 65, 20])
    assert.equal(await dialog().getByText('已自定义', { exact: true }).count(), 1)
    assert.equal(await dialog().locator('.segmented-control-option[aria-pressed=true]').count(), 0)
    await page.waitForFunction(() => getComputedStyle(document.querySelector('.media-detail .detail-search-buttons .search-pill'), '::before').backgroundColor.endsWith('0.65)'))
    const editedStyle = await pillStyle(detailPills())
    assert.match(editedStyle.filter, /blur\(13px\).*saturate\(1\.6\)/)
    assert.match(editedStyle.background, /0\.65\)/)
    assert.notEqual(editedStyle.shadow, defaultStyle.shadow)
    await page.waitForFunction(key => JSON.parse(localStorage.getItem(key))?.blur === 13, storageKey)
    const apiBefore = requests.length
    await dialog().getByRole('button', { name: '复制 CSS', exact: true }).click()
    await dialog().getByRole('button', { name: '已复制', exact: true }).waitFor()
    const copied = await page.evaluate(() => navigator.clipboard.readText())
    for (const expected of ['--user-glass-blur: 13px', '--user-glass-opacity: 0.65']) assert.ok(copied.includes(expected))
    assert.deepEqual(requests.slice(apiBefore).filter(request => request.method !== 'GET'), [])
    await screenshot('glass-settings-desktop-light')
    await close()
    await page.reload(); await detailPills().last().waitFor(); await open()
    assert.deepEqual(await values(), [13, 160, 65, 20])
    passed.push('默认参数、真实入口/焦点、预设/自定义、即时材质、复制 CSS 与刷新保存')

    const other = await context.newPage()
    await other.goto(origin + '/tv/300146')
    await other.waitForFunction(() => document.documentElement.style.getPropertyValue('--user-glass-blur') === '13px')
    await change('背景模糊', 17)
    await other.waitForFunction(() => document.documentElement.style.getPropertyValue('--user-glass-blur') === '17px')
    await dialog().getByRole('button', { name: '恢复默认', exact: true }).click()
    assert.deepEqual(await values(), [22, 175, 42, 80])
    await other.waitForFunction(() => document.documentElement.style.getPropertyValue('--user-glass-blur') === '22px')
    await other.close()
    passed.push('多标签同步与恢复默认')

    const refraction = dialog().getByRole('switch', { name: '边缘微折射（实验性）', exact: true })
    assert.equal(await refraction.isDisabled(), false)
    await dialog().getByText('边缘微折射（实验性）', { exact: true }).click()
    assert.equal(await refraction.isChecked(), true)
    assert.match((await pillStyle(detailPills())).filter, /cinema-glass-refraction/)
    assert.match((await pillStyle(dialog().locator('.search-pill'))).filter, /cinema-glass-refraction/)
    assert.equal(await dialog().evaluate(element => getComputedStyle(element).backdropFilter.includes('url(')), false, '大弹窗不采用 SVG 折射')
    await change('背景模糊', 0); await change('底色浓度', 25)
    const stage = dialog().locator('.preview-stage')
    const refractedPixels = await stage.screenshot({ animations: 'disabled', path: path.join(__dirname, '../test-results/glass-preview-refraction-on.png') })
    await dialog().getByText('边缘微折射（实验性）', { exact: true }).click()
    const nativePixels = await stage.screenshot({ animations: 'disabled', path: path.join(__dirname, '../test-results/glass-preview-refraction-off.png') })
    assert.equal(refractedPixels.equals(nativePixels), false, '开关实际改变预览像素，而不只是生成滤镜语法')
    assert.equal((await pillStyle(dialog().locator('.search-pill'))).textFilter, 'none', '折射始终与文字图标隔离')
    await dialog().getByText('边缘微折射（实验性）', { exact: true }).click()
    await dialog().getByRole('button', { name: 'TG 液态玻璃', exact: true }).click()
    assert.deepEqual(await values(), [22, 175, 42, 80])
    await useTheme('dark')
    assert.match((await pillStyle(detailPills())).background, /18, 22, 30/)
    await screenshot('glass-settings-desktop-dark')
    await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-transparency', value: 'reduce' }] })
    await page.waitForFunction(() => document.documentElement.dataset.glassRefraction === 'off')
    assert.equal((await pillStyle(detailPills())).filter, 'none')
    assert.equal(await refraction.isDisabled(), true)
    await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-contrast', value: 'more' }] })
    assert.equal((await pillStyle(detailPills())).filter, 'none')
    await cdp.send('Emulation.setEmulatedMedia', { features: [] })
    await page.waitForFunction(() => document.documentElement.dataset.glassRefraction === 'on')
    await close()
    passed.push('桌面微折射仅影响胶囊、深色主题与无透明/高对比降级')

    for (const width of [390, 320]) for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: 860 })
      await useTheme(theme)
      const boxes = await detailPills().evaluateAll(elements => elements.map(element => { const box = element.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height } }))
      assert.deepEqual(await detailPills().evaluateAll(elements => elements.map(element => element.dataset.searchSource)), ['re0', 'bot', 'channels', 'gying', 'dmhy', 'woniu'], '自然换行保留六个来源及原顺序')
      assert.deepEqual(await page.locator('.media-detail .detail-search-buttons').evaluate(element => ({ display: getComputedStyle(element).display, wrap: getComputedStyle(element).flexWrap })), { display: 'flex', wrap: 'wrap' })
      assert.ok(new Set(boxes.map(box => Math.round(box.width))).size > 1, '按钮按内容保持固有宽度')
      assert.ok(boxes.every(box => box.height >= 44 && box.x >= 0 && box.x + box.width <= width + 1))
      await screenshot(`glass-search-${width}-${theme}`)
      await open()
      const box = await dialog().boundingBox()
      assert.ok(Math.abs(box.x) <= 1 && Math.abs(box.x + box.width - width) <= 1 && Math.abs(box.y + box.height - 860) <= 1, `手机抽屉贴边：${JSON.stringify(box)}`)
      assert.equal(await dialog().getByRole('switch').isDisabled(), true)
      assert.equal((await pillStyle(detailPills())).filter.includes('url('), false)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
      await screenshot(`glass-settings-${width}-${theme}`)
      await close()
    }
    passed.push('390/320 深浅主题自然换行胶囊、来源顺序/固有宽度、44px 热区、实色贴边抽屉与折射回退')

    role = 'user'
    await page.reload(); await trigger().waitFor(); await open()
    assert.equal(await dialog().getByRole('slider').count(), 4)
    await page.evaluate(() => {
      window.glassCopyOriginal = { write: navigator.clipboard.writeText, exec: document.execCommand }
      navigator.clipboard.writeText = async () => { throw new Error('模拟复制权限被拒绝') }
      document.execCommand = command => command === 'copy' ? false : window.glassCopyOriginal.exec.call(document, command)
      window.glassStorageOriginal = Storage.prototype.setItem
      Storage.prototype.setItem = function(key, value) { if (key === 'cinema115-glass-settings') throw new Error('模拟存储不可用'); return window.glassStorageOriginal.call(this, key, value) }
    })
    await dialog().getByRole('button', { name: '复制 CSS', exact: true }).click()
    const manualCss = dialog().getByRole('textbox', { name: '复制未成功，可手动选择以下 CSS 参数。', exact: true })
    await manualCss.waitFor()
    assert.match(await manualCss.inputValue(), /--user-glass-blur: 22px/)
    await manualCss.focus()
    assert.equal(await manualCss.evaluate(element => element.selectionStart === 0 && element.selectionEnd === element.value.length), true)
    await change('背景模糊', 18)
    await dialog().getByText('当前浏览器无法保存设置，本次调整仍然生效。', { exact: true }).waitFor()
    assert.match((await pillStyle(dialog().locator('.search-pill'))).filter, /blur\(18px\)/)
    await page.evaluate(() => {
      navigator.clipboard.writeText = window.glassCopyOriginal.write
      document.execCommand = window.glassCopyOriginal.exec
      Storage.prototype.setItem = window.glassStorageOriginal
    })
    await dialog().getByRole('button', { name: '恢复默认', exact: true }).click()
    await close()
    await page.evaluate(key => localStorage.setItem(key, '{broken json'), storageKey)
    await page.reload(); await trigger().waitFor(); await open()
    assert.deepEqual(await values(), [22, 175, 42, 80], '损坏的浏览器配置回退默认参数')
    await close()
    assert.equal(requests.filter(request => !['GET', 'HEAD'].includes(request.method)).length, 0, '全部外观操作没有业务写请求')
    assert.deepEqual(errors, [])
    passed.push('普通用户可用、复制/保存失败降级、损坏配置恢复及无业务写入/浏览器错误')
    console.log(JSON.stringify({ passed, screenshots: 'test-results/glass-settings-*.png' }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
