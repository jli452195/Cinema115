// 隔离验证公共通知和原生模态层级，所有接口均模拟。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1100, height: 800 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.removeItem('admin_token'); localStorage.setItem('cinema115-theme', 'light') })
    await context.route('**/api/**', route => route.fulfill({ json: {} }))
    const page = await context.newPage()
    page.setDefaultTimeout(7000)
    page.on('pageerror', error => errors.push(error.stack || error.message))
    await page.goto(origin + '/admin/login')
    await page.locator('.toast-host:popover-open').waitFor()
    await page.evaluate(async () => {
      window.toastFixture = (await import('/src/composables/useToast.ts')).useToast()
      const trigger = document.createElement('button')
      trigger.id = 'toast-fixture-trigger'; trigger.textContent = '通知测试触发器'
      trigger.style.cssText = 'position:fixed;left:20px;bottom:20px'
      document.body.append(trigger)
    })
    const toasts = page.locator('[data-sonner-toast]')
    const clear = async () => { await page.evaluate(() => window.toastFixture.clear()); await page.waitForFunction(() => document.querySelectorAll('[data-sonner-toast]').length === 0) }

    await page.evaluate(() => {
      for (const type of ['success', 'error', 'warning', 'info']) window.toastFixture[type](type + ' 通知内容', { duration: 0 })
      window.toastFixture.info('info 通知内容', { duration: 0 })
    })
    await page.waitForFunction(() => document.querySelectorAll('[data-sonner-toast]:not([data-removed="true"])').length === 4)
    await page.waitForFunction(() => document.querySelectorAll('[data-sonner-toast]').length === 4)
    assert.equal(await toasts.locator('svg').count() >= 4, true)
    assert.equal(await toasts.filter({ hasText: 'info 通知内容' }).count(), 1)
    assert.equal(await page.locator('[data-sonner-toast][data-visible="true"]').count(), 3)
    assert.equal(await page.locator('[data-sonner-toast][data-visible="false"]').evaluateAll(elements => elements.every(el => getComputedStyle(el).pointerEvents === 'none')), true)
    assert.equal(await page.getByRole('region', { name: /全局通知/ }).count(), 1)
    await clear()
    passed.push('四种语义通知、重复内容去重、三个可见通知及中文无障碍标签')

    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.evaluate(() => window.toastFixture.success('鼠标悬停期间保持显示', 600))
    await toasts.hover()
    await page.waitForTimeout(850)
    assert.equal(await toasts.getAttribute('data-removed'), 'false')
    await page.mouse.move(0, 0)
    await toasts.waitFor({ state: 'detached' })
    await page.evaluate(() => window.toastFixture.info('键盘阅读期间保持显示', 600))
    await toasts.getByRole('button', { name: '关闭提示' }).focus()
    await page.mouse.move(1000, 750)
    await page.waitForTimeout(850)
    assert.equal(await toasts.getAttribute('data-removed'), 'false')
    await page.locator('#toast-fixture-trigger').focus()
    await toasts.waitFor({ state: 'detached' })
    await page.evaluate(() => window.toastFixture.info('重复通知重置阅读时长', 500))
    await page.waitForTimeout(150)
    await page.evaluate(() => window.toastFixture.info('重复通知重置阅读时长', 1600))
    await page.waitForTimeout(650)
    assert.equal(await toasts.count(), 1)
    assert.equal(await toasts.getAttribute('data-removed'), 'false')
    await toasts.waitFor({ state: 'detached' })
    await page.evaluate(() => window.toastFixture.info('持续提示改为定时关闭', { duration: 0 }))
    await page.evaluate(() => window.toastFixture.info('持续提示改为定时关闭', 300))
    await page.waitForFunction(() => document.querySelectorAll('[data-sonner-toast]').length === 0)
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    passed.push('悬停和焦点暂停、离开后关闭、重复提示重新计时及持续转定时')

    await page.evaluate(() => window.toastFixture.promise(new Promise(resolve => { window.finishHeightTest = resolve }), {
      loading: '准备任务', duration: 10000,
      success: () => ({ message: '正在整理', description: '羽球青春 · 24 项资源 · 24 个视频 · 下载 → 整理 → STRM · 资源 24/24 已就绪 · 0/24 已整理 · STRM 已写入 0 个', classes: { description: 'task-resource-summary' } }),
    }))
    await toasts.getByText('准备任务', { exact: true }).waitFor()
    const sameToast = await toasts.elementHandle()
    await page.evaluate(() => window.finishHeightTest())
    await toasts.getByText('正在整理', { exact: true }).waitFor()
    for (const [width, dark] of [[1100, false], [320, true]]) {
      await page.setViewportSize({ width, height: 800 })
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      await page.mouse.move(0, 700)
      await page.waitForTimeout(450)
      const initialHeight = await toasts.evaluate(el => el.getBoundingClientRect().height)
      await toasts.hover()
      await page.waitForTimeout(450)
      const layout = await toasts.evaluate(el => {
        const box = el.getBoundingClientRect(), content = el.querySelector('[data-content]').getBoundingClientRect()
        return { height: box.height, containsContent: content.top >= box.top && content.bottom <= box.bottom, expanded: el.dataset.expanded }
      })
      assert.equal(layout.expanded, 'true')
      assert.equal(layout.containsContent, true, 'hover must keep the updated text inside the toast background')
      assert.ok(Math.abs(layout.height - initialHeight) <= 1, 'hover must not collapse the updated toast')
      assert.equal(await sameToast.evaluate(el => el.isConnected), true)
    }
    await clear()
    await page.setViewportSize({ width: 1100, height: 800 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    passed.push('同一通知从短内容更新为多行后，桌面浅色及手机深色悬停均不压缩背景或溢出文字')

    await page.evaluate(() => {
      for (const id of ['toast-parent', 'toast-child']) {
        const dialog = document.createElement('dialog')
        dialog.id = id
        dialog.style.cssText = 'width:300px;height:160px;overflow:hidden;border:1px solid #888'
        const button = document.createElement('button')
        button.textContent = '关闭测试弹窗'; button.onclick = () => dialog.close()
        dialog.append(button); document.body.append(dialog)
      }
      document.querySelector('#toast-parent').showModal()
      window.toastFixture.success('弹窗里的通知可以关闭', { duration: 0 })
    })
    await page.waitForFunction(() => document.querySelector('.toast-host:popover-open')?.closest('dialog')?.id === 'toast-parent')
    await toasts.getByRole('button', { name: '关闭提示' }).hover()
    await page.waitForFunction(() => {
      const button = document.querySelector('[data-sonner-toast] [data-close-button]')
      if (!button) return false
      const box = button.getBoundingClientRect()
      return button.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
    })
    await page.keyboard.press('Alt+t')
    assert.equal(await page.locator('[data-sonner-toaster]').evaluate(el => el === document.activeElement), true)
    await toasts.getByRole('button', { name: '关闭提示' }).click()
    await toasts.waitFor({ state: 'detached' })
    await page.evaluate(() => {
      document.querySelector('#toast-child').showModal()
      window.toastFixture.warning('嵌套弹窗共享同一个通知宿主', { duration: 0 })
    })
    await page.waitForFunction(() => document.querySelector('.toast-host:popover-open')?.closest('dialog')?.id === 'toast-child')
    await toasts.getByRole('button', { name: '关闭提示' }).focus()
    assert.equal(await toasts.getByRole('button', { name: '关闭提示' }).evaluate(el => el === document.activeElement), true)
    await page.evaluate(() => document.querySelector('#toast-child').remove())
    await page.waitForFunction(() => document.querySelector('.toast-host:popover-open')?.closest('dialog')?.id === 'toast-parent')
    await page.evaluate(() => document.querySelector('#toast-parent').remove())
    await page.waitForFunction(() => document.querySelector('.toast-host:popover-open')?.parentElement === document.body)
    assert.equal(await toasts.count(), 1)
    assert.equal(await page.locator('.toast-host').count(), 1)
    await clear()
    await page.evaluate(() => {
      const first = document.createElement('dialog'), second = document.createElement('dialog')
      first.id = 'toast-first'; second.id = 'toast-second'
      document.body.append(first, second)
      second.showModal(); first.showModal()
      window.toastFixture.info('同一轮反向开启时跟随实际顶层', { duration: 0 })
    })
    await page.waitForFunction(() => document.querySelector('.toast-host:popover-open')?.closest('dialog')?.id === 'toast-first')
    await toasts.getByRole('button', { name: '关闭提示' }).focus()
    assert.equal(await toasts.getByRole('button', { name: '关闭提示' }).evaluate(el => el === document.activeElement), true)
    await toasts.getByRole('button', { name: '关闭提示' }).click()
    await toasts.waitFor({ state: 'detached' })
    await page.evaluate(() => { document.querySelector('#toast-first').remove(); document.querySelector('#toast-second').remove() })
    await page.waitForFunction(() => document.querySelector('.toast-host:popover-open')?.parentElement === document.body)
    passed.push('模态上方真实点击、快捷键聚焦、嵌套和批量反向开启、突然卸载均保持单一宿主')

    await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true })
    for (const width of [1100, 320]) {
      await page.setViewportSize({ width, height: 800 })
      await page.evaluate(() => window.toastFixture.success('转存成功，共 1 个；文件已保存到所选目录', { title: '转存完成', duration: 0 }))
      for (const theme of ['light', 'dark']) {
        await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
        await page.waitForFunction(theme => document.querySelector('[data-sonner-toaster]')?.dataset.sonnerTheme === theme, theme)
        const layout = await toasts.evaluate(el => {
          const box = el.getBoundingClientRect(), style = getComputedStyle(el)
          return { left: box.left, right: box.right, width: box.width, actual: style.backgroundColor, richColors: el.dataset.richColors, overflow: document.documentElement.scrollWidth > innerWidth }
        })
        assert.equal(layout.actual, theme === 'dark' ? 'rgb(0, 0, 0)' : 'rgb(255, 255, 255)')
        assert.notEqual(layout.richColors, 'true')
        assert(layout.left >= 0 && layout.right <= width && layout.width > 0)
        assert.equal(layout.overflow, false)
        await page.screenshot({ path: path.join(__dirname, `../test-results/toast-${width}-${theme}.png`), animations: 'disabled' })
      }
      await clear()
    }
    await page.evaluate(() => window.toastFixture.error('x'.repeat(600), { duration: 0 }))
    assert.equal(await toasts.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    assert.equal(await toasts.evaluate(el => getComputedStyle(el).transitionDuration), '0s')
    await clear()
    passed.push('桌面与320px手机、已有通知随页面切换原生深浅主题、长文本及减少动态效果')

    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await page.evaluate(() => window.toastFixture.info('向右滑动关闭', { duration: 0 }))
    await page.waitForTimeout(400)
    const box = await toasts.boundingBox()
    await page.mouse.move(box.x + 30, box.y + box.height / 2)
    await page.mouse.down()
    await page.mouse.move(box.x + 220, box.y + box.height / 2, { steps: 10 })
    await page.mouse.up()
    await toasts.waitFor({ state: 'detached' })
    assert.deepEqual(errors, [])
    passed.push('原生滑动关闭且浏览器无异常')
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
