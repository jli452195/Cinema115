// 挂载真实公共弹窗并模拟所有 API；手势使用可信鼠标及 CDP 触摸，不读取真实账号或提交任务。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], requests = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 390, height: 860 }, hasTouch: true, reducedMotion: 'no-preference', serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.removeItem('admin_token'); localStorage.setItem('cinema115-theme', 'light') })
    await context.route('**/api/**', async route => {
      requests.push({ method: route.request().method(), url: new URL(route.request().url()).pathname })
      await route.fulfill({ json: { configured: false, accounts: [], items: [], unread: 0 } })
    })
    const page = await context.newPage(), cdp = await context.newCDPSession(page)
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    await page.goto(origin + '/admin/login')
    await page.evaluate(async () => {
      const source = await (await fetch('/src/components/AnimeFloatingDialog.vue')).text()
      const vueURL = source.match(/from ["']([^"']*\/vue\.js[^"']*)["']/)[1]
      const { createApp, h, reactive, nextTick } = await import(vueURL)
      const Dialog = (await import('/src/components/AnimeFloatingDialog.vue')).default
      const state = reactive({ open: false, child: false, presentation: 'sheet', closeDisabled: false, closed: [], trusted: [], savedScroll: 0 })
      const previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'clip'
      document.querySelector('#app').style.display = 'none'
      const mount = document.createElement('div'); mount.id = 'mobile-sheet-fixture'; document.body.append(mount)
      const pointer = event => state.trusted.push({ trusted: event.isTrusted, type: event.pointerType })
      document.addEventListener('pointerdown', pointer)
      const app = createApp({ render: () => h('div', [
        h('button', { id: 'open-mobile-sheet', onClick: () => { state.presentation = 'sheet'; state.open = true } }, '打开移动弹窗'),
        h('button', { id: 'open-plain-dialog', onClick: () => { state.presentation = undefined; state.open = true } }, '打开普通弹窗'),
        state.open && h(Dialog, { class: 'sheet-fixture-parent', title: '移动弹窗验证', closeLabel: '关闭测试弹窗', size: 'config', mobilePresentation: state.presentation, closeDisabled: state.closeDisabled, onClose: () => { state.closed.push('parent'); state.open = false } }, {
          header: () => h('p', '仅用于验证公共弹窗的固定头部'),
          default: () => [h('input', { id: 'sheet-preserved-input', value: '未提交内容', 'aria-label': '保留输入' }), h('div', { style: 'position:sticky;top:0;z-index:1;background:rgb(var(--surface-card));padding:8px 0' }, [h('button', { id: 'open-sheet-child', onClick: () => { state.savedScroll = document.querySelector('.sheet-fixture-parent .anime-floating-body').scrollTop; state.child = true } }, '打开嵌套弹窗')]), h('ul', { style: 'margin:0;padding:0;list-style:none' }, Array.from({ length: 80 }, (_, index) => h('li', { style: 'height:52px;border-bottom:1px solid #94a3b8;padding:12px' }, `文件 ${index + 1}`)))],
          footer: () => h('p', '公共弹窗底部插槽'),
        }),
        state.child && h(Dialog, { class: 'sheet-fixture-child', title: '嵌套弹窗验证', closeLabel: '关闭嵌套弹窗', mobilePresentation: 'sheet', onClose: () => { state.closed.push('child'); state.child = false } }, () => h('button', { id: 'child-action' }, '嵌套内容')),
      ]) })
      app.mount(mount)
      window.mobileSheetFixture = { state, nextTick, dispose: () => { app.unmount(); mount.remove(); document.removeEventListener('pointerdown', pointer); document.body.style.overflow = previousOverflow; document.querySelector('#app').style.display = '' } }
    })
    const parent = page.locator('dialog.sheet-fixture-parent[open]'), child = page.locator('dialog.sheet-fixture-child[open]')
    const body = parent.locator('.anime-floating-body'), handle = parent.locator('.bottom-sheet-grabber-zone')
    const open = async (plain = false) => {
      await page.locator(plain ? '#open-plain-dialog' : '#open-mobile-sheet').click(); await parent.waitFor()
      const animations = await parent.evaluate(element => element.getAnimations().map(animation => ({ duration: Number(animation.effect.getTiming().duration), frames: animation.effect.getKeyframes().map(frame => Object.keys(frame).filter(key => !['offset', 'computedOffset', 'easing', 'composite'].includes(key))) })))
      await stationary(parent)
      return animations
    }
    const stationary = async dialog => {
      await dialog.evaluate(async element => { const animations = element.getAnimations().filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity); await Promise.all(animations.map(animation => animation.finished.catch(() => {}))) })
      await page.waitForFunction(selector => { const element = document.querySelector(selector); return element && getComputedStyle(element).transform === 'none' && !element.getAnimations().some(animation => animation.playState === 'running') }, await dialog.evaluate(element => `dialog.${element.classList.contains('sheet-fixture-child') ? 'sheet-fixture-child' : 'sheet-fixture-parent'}[open]`))
    }
    const expanded = async expected => { await page.waitForFunction(expected => (document.querySelector('dialog.sheet-fixture-parent[open]')?.getAttribute('data-sheet-expanded') === 'true') === expected, expected); await stationary(parent) }
    const geometry = dialog => dialog.evaluate(element => { const box = element.getBoundingClientRect(), content = element.querySelector('.anime-floating-body').getBoundingClientRect(); return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width, height: box.height, viewportWidth: innerWidth, viewportHeight: innerHeight, bodyHeight: content.height, transform: getComputedStyle(element).transform } })
    const checkEdges = async full => {
      const box = await geometry(parent)
      assert.ok(Math.abs(box.left) <= 1 && Math.abs(box.right - box.viewportWidth) <= 1 && Math.abs(box.bottom - box.viewportHeight) <= 1, JSON.stringify(box))
      assert.ok(full ? Math.abs(box.top) <= 1 : box.top >= 23 && box.top <= 52, JSON.stringify(box))
      assert.equal(box.transform, 'none', '静止展开态和默认态均不保留transform')
      return box
    }
    const point = async locator => { const box = await locator.boundingBox(); assert.ok(box); return { x: box.x + box.width / 2, y: box.y + box.height / 2 } }
    const mouseDrag = async (locator, distance, { steps = 6, delay = 35, settle = 100 } = {}) => {
      const start = await point(locator)
      await page.mouse.move(start.x, start.y); await page.mouse.down()
      for (let index = 1; index <= steps; index++) { await page.mouse.move(start.x, start.y + distance * index / steps); if (delay) await page.waitForTimeout(delay) }
      if (settle) await page.waitForTimeout(settle)
      await page.mouse.up()
    }
    const touchStart = async locator => {
      const start = await point(locator)
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...start, id: 1, radiusX: 1, radiusY: 1, force: 1 }] })
      return start
    }
    const touchMove = (start, distance) => cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: start.x, y: start.y + distance, id: 1, radiusX: 1, radiusY: 1, force: 1 }] })
    const touchEnd = cancel => cdp.send('Input.dispatchTouchEvent', { type: cancel ? 'touchCancel' : 'touchEnd', touchPoints: [] })
    const touchDrag = async (locator, distance, { cancel = false, steps = 6, delay = 35, settle = 100 } = {}) => {
      const start = await touchStart(locator)
      for (let index = 1; index <= steps; index++) { await touchMove(start, distance * index / steps); if (delay) await page.waitForTimeout(delay) }
      if (settle) await page.waitForTimeout(settle)
      await touchEnd(cancel)
    }
    const closed = async (trigger = '#open-mobile-sheet') => {
      await parent.waitFor({ state: 'detached' })
      assert.equal(await page.evaluate(() => document.body.style.overflow), 'clip', '最后一层关闭后恢复原始滚动状态')
      assert.equal(await page.locator(trigger).evaluate(element => document.activeElement === element), true, '退出动画完成后恢复入口焦点')
    }

    const entryAnimations = await open()
    assert.ok(entryAnimations.some(animation => animation.duration >= 150 && animation.duration <= 300 && animation.frames.every(keys => keys.length === 1 && keys[0] === 'transform')), '进入动画仅移动表面，不逐帧改变高度')
    const collapsed = await checkEdges(false)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    assert.equal(await handle.isVisible(), true)
    const expandButton = parent.getByRole('button', { name: '展开弹窗', exact: true })
    assert.ok((await expandButton.boundingBox()).height >= 44)
    await expandButton.click(); await expanded(true)
    const full = await checkEdges(true)
    assert.ok(full.bodyHeight > collapsed.bodyHeight, '全屏展开增加可滚动正文空间')
    await parent.getByRole('button', { name: '恢复弹窗高度', exact: true }).click(); await expanded(false)
    await mouseDrag(handle, 12); await expanded(false); await checkEdges(false)
    await mouseDrag(parent.locator('.anime-floating-toolbar'), -28); await expanded(true); await checkEdges(true)
    await mouseDrag(handle, 48); await expanded(false); await checkEdges(false)
    passed.push('手机左右及底部贴边、默认留顶、44px展开按钮、上拖展开、全屏下拖恢复及短拖回弹')

    await touchDrag(handle, 70, { cancel: true }); await expanded(false); await checkEdges(false)
    const resizeTouch = await touchStart(handle); await touchMove(resizeTouch, 50)
    await page.setViewportSize({ width: 320, height: 740 }); await touchEnd(true)
    await stationary(parent); await checkEdges(false)
    assert.equal(await parent.evaluate(element => element.style.getPropertyValue('--sheet-viewport-height')), '740px', 'visualViewport尺寸变化更新正文可用高度')
    assert.equal(await parent.evaluate(element => element.style.getPropertyValue('--sheet-viewport-bottom')), '0px')
    await page.setViewportSize({ width: 390, height: 860 }); await stationary(parent)
    const scrollPoint = await body.boundingBox(), scrollBefore = await body.evaluate(element => element.scrollTop)
    const start = { x: scrollPoint.x + scrollPoint.width / 2, y: scrollPoint.y + scrollPoint.height - 80 }
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...start, id: 1, radiusX: 1, radiusY: 1, force: 1 }] })
    for (let index = 1; index <= 8; index++) { await touchMove(start, -index * 35); await page.waitForTimeout(30) }
    await touchEnd(false)
    await page.waitForFunction(before => document.querySelector('.sheet-fixture-parent .anime-floating-body').scrollTop > before + 100, scrollBefore)
    await expanded(false)
    assert.equal(await page.evaluate(() => window.scrollY), 0, '列表原生触摸滚动不滚动底层页面')
    passed.push('可信触摸取消、拖动中resize恢复，以及正文原生滚动不触发展开或关闭')

    await page.evaluate(() => { window.mobileSheetFixture.state.closeDisabled = true })
    assert.equal(await parent.getByRole('button', { name: '关闭测试弹窗', exact: true }).isDisabled(), true)
    const disabledClosed = await page.evaluate(() => window.mobileSheetFixture.state.closed.length)
    await page.keyboard.press('Escape'); await touchDrag(handle, 160); await stationary(parent)
    assert.equal(await page.evaluate(() => window.mobileSheetFixture.state.closed.length), disabledClosed, 'closeDisabled同时阻止Esc和下拖关闭')
    assert.equal(await parent.count(), 1)
    await page.evaluate(() => { window.mobileSheetFixture.state.closeDisabled = false })
    await parent.getByRole('button', { name: '关闭测试弹窗', exact: true }).click()
    const leaving = await page.evaluate(() => { const dialog = document.querySelector('.sheet-fixture-parent[open]'); return { open: !!dialog, locked: document.body.style.overflow, focusInside: !!dialog?.contains(document.activeElement) } })
    assert.deepEqual(leaving, { open: true, locked: 'hidden', focusInside: true }, '退出动画期间原生模态栈、滚动锁与焦点保留')
    await closed()
    passed.push('closeDisabled与退出动画期间持锁、最终焦点恢复')

    await open(); await touchDrag(handle, 150); await closed()
    await open(); await touchDrag(handle, 70, { steps: 2, delay: 5, settle: 0 }); await closed()
    const trusted = await page.evaluate(() => window.mobileSheetFixture.state.trusted)
    assert.ok(trusted.some(event => event.trusted && event.type === 'mouse') && trusted.some(event => event.trusted && event.type === 'touch'), '手势来自可信鼠标与触摸事件')
    passed.push('超过110px下拖及近期短距离快甩关闭')

    await open()
    await body.evaluate(element => { element.scrollTop = 180 })
    await parent.getByRole('button', { name: '打开嵌套弹窗', exact: true }).click()
    await child.waitFor(); await stationary(child)
    assert.equal(await parent.evaluate(element => getComputedStyle(element).visibility), 'hidden', '嵌套期间父壳退让')
    const savedScroll = await page.evaluate(() => window.mobileSheetFixture.state.savedScroll)
    await child.getByRole('button', { name: '关闭嵌套弹窗', exact: true }).click()
    assert.equal(await parent.evaluate(element => getComputedStyle(element).visibility), 'hidden', '子层退出动画完成前父层保持退让')
    await child.waitFor({ state: 'detached' })
    assert.equal(await parent.evaluate(element => getComputedStyle(element).visibility), 'visible')
    assert.equal(await body.evaluate(element => element.scrollTop), savedScroll)
    assert.equal(await parent.getByRole('textbox', { name: '保留输入', exact: true }).inputValue(), '未提交内容')
    assert.equal(await parent.locator('#open-sheet-child').evaluate(element => document.activeElement === element), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await parent.getByRole('button', { name: '关闭测试弹窗', exact: true }).click(); await closed()
    passed.push('嵌套退出前继续覆盖父层，退出后恢复父层输入、滚动及焦点')

    await page.emulateMedia({ reducedMotion: 'reduce' }); await open()
    assert.equal(await parent.evaluate(element => element.getAnimations().some(animation => animation.playState === 'running')), false)
    await parent.getByRole('button', { name: '展开弹窗', exact: true }).click(); await expanded(true)
    await parent.getByRole('button', { name: '关闭测试弹窗', exact: true }).click()
    assert.equal(await parent.count(), 0, 'reduced-motion关闭立即完成，不等待退出动画')
    await closed()
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await open(true)
    const plain = await geometry(parent)
    assert.ok(plain.left > 0 && plain.right < plain.viewportWidth && plain.top > 0, '未opt-in手机弹窗保留原居中边距')
    assert.equal(await parent.getByRole('button', { name: '展开弹窗', exact: true }).count(), 0)
    assert.equal(await parent.locator('.bottom-sheet-grabber-zone').isVisible().catch(() => false), false)
    await parent.getByRole('button', { name: '关闭测试弹窗', exact: true }).click(); await closed('#open-plain-dialog')
    await page.setViewportSize({ width: 1100, height: 900 }); await open()
    const desktop = await geometry(parent)
    assert.ok(desktop.left > 0 && desktop.right < desktop.viewportWidth && desktop.top > 0, '桌面opt-in弹窗保留原居中布局')
    assert.equal(await parent.getByRole('button', { name: '展开弹窗', exact: true }).count(), 0)
    assert.equal(await parent.locator('.bottom-sheet-grabber-zone').isVisible().catch(() => false), false)
    await parent.getByRole('button', { name: '关闭测试弹窗', exact: true }).click(); await closed()
    passed.push('reduced-motion即时状态、非opt-in手机与桌面布局隔离')

    await page.setViewportSize({ width: 390, height: 860 }); await open()
    await fs.mkdir(path.resolve(__dirname, '../../tests/browser'), { recursive: true })
    await page.screenshot({ path: path.resolve(__dirname, '../../tests/browser/mobile-sheet-default.png'), animations: 'disabled' })
    await parent.getByRole('button', { name: '展开弹窗', exact: true }).click(); await expanded(true)
    await page.screenshot({ path: path.resolve(__dirname, '../../tests/browser/mobile-sheet-expanded.png'), animations: 'disabled' })
    await page.evaluate(() => { window.mobileSheetFixture.dispose() })
    assert.deepEqual(errors, [])
    assert.equal(requests.some(request => request.method !== 'GET'), false)
    console.log(JSON.stringify({ passed, geometry: { collapsed, full }, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
