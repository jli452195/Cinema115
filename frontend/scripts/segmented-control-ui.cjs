// 隔离验证公共分段控件及两处页面，所有 API 均模拟，不提交实际任务。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], requests = []
  try {
    const context = await browser.newContext({ viewport: { width: 1100, height: 800 }, serviceWorkers: 'block', reducedMotion: 'no-preference' })
    await context.addInitScript(() => { localStorage.setItem('cinema115-theme', 'light') })
    await context.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url())
      requests.push({ path: url.pathname, query: url.search, method: request.method() })
      let data = { items: [], total: 0, totalPages: 1, genres: [], unread: 0, hasMore: false, accounts: [], configured: false }
      if (url.pathname === '/api/auth/me') data = { user: { id: 'segmented-fixture', username: 'segmented-fixture', role: 'admin' } }
      else if (url.pathname === '/api/admin/follows/settings') data = { baseUrl: '', autoEnabled: false, telegramEnabled: false, notificationsConfigured: false }
      else if (url.pathname === '/api/admin/follows') data = { items: [], hasMore: false, counts: { pending: 1, processing: 0, caught_up: 3, closed: 0, completed: 4 } }
      else if (url.pathname.endsWith('/media-organize/history')) data = { items: [], page: Number(url.searchParams.get('page') || 1), pages: 3, total: 6148, counts: { all: 6148, running: 0, done: 6136, failed: 12 }, active: false, activity: [], cleanup: [] }
      await route.fulfill({ json: data }).catch(() => {})
    })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.stack || error.message))
    await page.goto(origin + '/admin/login')
    await page.evaluate(async () => {
      const source = await (await fetch('/src/components/SegmentedControl.vue')).text()
      const vueURL = source.match(/from ["']([^"']*\/vue\.js[^"']*)["']/)[1]
      const { createApp, h, reactive, nextTick } = await import(vueURL)
      const Component = (await import('/src/components/SegmentedControl.vue')).default
      const state = reactive({ value: 0, disabled: false, fieldsetDisabled: false, shown: true, density: 'default', appearance: undefined, references: false, options: [{ value: 0, label: '全部', count: 6148 }, { value: 1, label: '整理中', disabled: true }, { value: 2, label: '成功', count: 6136 }, { value: 3, label: '失败', count: 12 }] })
      const events = []
      const mount = document.createElement('div')
      mount.id = 'segmented-fixture'; mount.style.cssText = 'max-width:720px;padding:16px'
      document.querySelector('#app').style.display = 'none'
      document.body.append(mount)
      const referenceOptions = [{ value: 0, label: '文件', count: 7 }, { value: 2, label: '版本', count: 2 }]
      const app = createApp({ render: () => h('fieldset', { disabled: state.fieldsetDisabled, style: 'min-width:0' }, [
        state.shown && h(Component, { label: '分段控件测试', modelValue: state.value, options: state.options, disabled: state.disabled, density: state.density, appearance: state.appearance, 'onUpdate:modelValue': value => { state.value = value; events.push(value) } }),
        state.references && h('div', { style: 'display:flex;flex-wrap:wrap;gap:8px;margin-top:12px;min-width:0' }, [h(Component, { label: '默认外观对照', modelValue: 0, options: referenceOptions }), h(Component, { label: '中性外观对照', modelValue: 0, options: referenceOptions, appearance: 'neutral' })]),
      ]) })
      app.mount(mount)
      window.segmentedFixture = { state, events, nextTick, dispose: () => { app.unmount(); mount.remove(); document.querySelector('#app').style.display = '' } }
    })
    const control = page.getByRole('navigation', { name: '分段控件测试', exact: true })
    const aligned = () => page.waitForFunction(() => {
      const root = document.querySelector('#segmented-fixture .segmented-control'), active = root?.querySelector('[aria-pressed=true]'), indicator = root?.querySelector('.segmented-control-indicator')
      if (!active || !indicator) return false
      const a = active.getBoundingClientRect(), b = indicator.getBoundingClientRect()
      return Math.abs(a.left - b.left) < 1 && Math.abs(a.width - b.width) < 1 && Math.abs(a.top - b.top) < 1 && Math.abs(a.height - b.height) < 1
    })
    await aligned()
    assert.equal(await control.locator('.segmented-control-indicator').evaluate(el => el.style.transition), 'none')
    const animated = await page.evaluate(async () => {
      const root = document.querySelector('#segmented-fixture .segmented-control'), indicator = root.querySelector('.segmented-control-indicator')
      root.querySelector('button:last-child').click()
      await window.segmentedFixture.nextTick()
      await new Promise(requestAnimationFrame)
      return { transition: indicator.style.transition, animations: indicator.getAnimations().length }
    })
    assert.match(animated.transition, /left 300ms/); assert(animated.animations > 0)
    await aligned()
    assert.equal(await control.getByRole('button', { name: '失败', exact: true }).getAttribute('aria-pressed'), 'true')
    await control.getByRole('button', { name: '失败', exact: true }).click()
    assert.deepEqual(await page.evaluate(() => window.segmentedFixture.events), [3, 3])
    passed.push('首次直接定位、切换发生真实滑动、数字值及重选事件保留')

    await control.getByRole('button', { name: '全部', exact: true }).focus()
    await page.keyboard.press('ArrowRight')
    assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '成功')
    assert.deepEqual(await page.evaluate(() => window.segmentedFixture.events), [3, 3])
    await page.keyboard.press('Enter'); await aligned()
    assert.deepEqual(await page.evaluate(() => window.segmentedFixture.events), [3, 3, 2])
    await page.keyboard.press('Home')
    assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '全部')
    await page.keyboard.press('End')
    assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '失败')
    await page.evaluate(() => { window.segmentedFixture.state.disabled = true })
    assert.equal(await control.locator('button:enabled').count(), 0)
    await control.getByRole('button', { name: '全部', exact: true }).dispatchEvent('click')
    assert.deepEqual(await page.evaluate(() => window.segmentedFixture.events), [3, 3, 2])
    await page.evaluate(() => { window.segmentedFixture.state.disabled = false; window.segmentedFixture.state.fieldsetDisabled = true })
    assert.equal(await control.locator('button:enabled').count(), 0)
    passed.push('方向键跳过禁用项且不发筛选、Enter确认、Home/End及整个fieldset禁用')

    await page.evaluate(() => {
      const { state } = window.segmentedFixture
      state.fieldsetDisabled = false
      state.options = Array.from({ length: 10 }, (_, value) => ({ value, label: '分段选项' + value, count: value === state.value ? '123456789' : value }))
    })
    await aligned()
    await page.setViewportSize({ width: 320, height: 800 })
    await control.locator('button').first().focus(); await page.keyboard.press('End'); await page.keyboard.press('Enter'); await aligned()
    assert.equal(await control.evaluate(el => el.scrollLeft > 0), true)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.evaluate(() => { window.segmentedFixture.state.shown = false })
    await control.waitFor({ state: 'detached' })
    await page.evaluate(() => { window.segmentedFixture.state.shown = true })
    await aligned()
    assert.equal(await control.locator('.segmented-control-indicator').evaluate(el => el.style.transition), 'none')
    assert.equal(await control.evaluate(el => { const root = el.getBoundingClientRect(), active = el.querySelector('[aria-pressed=true]').getBoundingClientRect(); return active.left >= root.left && active.right <= root.right }), true)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await control.getByRole('button', { name: '分段选项0', exact: true }).click(); await aligned()
    assert.equal(await control.locator('.segmented-control-indicator').evaluate(el => el.style.transition), 'none')
    await page.evaluate(() => { window.segmentedFixture.state.value = 99 })
    await page.waitForFunction(() => document.querySelector('.segmented-control-indicator').style.opacity === '0')
    await page.evaluate(() => { window.segmentedFixture.state.value = 1 })
    await aligned()
    passed.push('计数与选项变化、手机横滚和布局、条件重挂载、无匹配值及减少动态效果')

    // 新外观与默认、中性对照同时挂载，验证样式作用域及两种层级的交互。
    await page.setViewportSize({ width: 1100, height: 800 })
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await page.evaluate(() => {
      const { state, events } = window.segmentedFixture
      state.references = true; state.density = 'compact'; state.value = 0; events.length = 0
      state.options = [{ value: 0, label: '文件', count: 6148 }, { value: 1, label: '不可用', disabled: true }, { value: 2, label: '版本', count: 2 }]
    })
    const defaultReference = page.getByRole('navigation', { name: '默认外观对照', exact: true })
    const neutralReference = page.getByRole('navigation', { name: '中性外观对照', exact: true })
    const skin = locator => locator.evaluate(element => {
      const root = getComputedStyle(element), indicator = getComputedStyle(element.querySelector('.segmented-control-indicator')), option = getComputedStyle(element.querySelector('button'))
      return { background: root.backgroundColor, radius: root.borderRadius, padding: root.padding, border: root.borderWidth, indicatorBackground: indicator.backgroundColor, indicatorRadius: indicator.borderRadius, indicatorShadow: indicator.boxShadow, optionHeight: option.minHeight, optionPadding: option.padding, optionColor: option.color }
    })
    const settledSkin = async locator => {
      let previous, stable = 0
      for (let attempt = 0; attempt < 20; attempt++) {
        const current = await skin(locator)
        stable = JSON.stringify(current) === JSON.stringify(previous) ? stable + 1 : 0
        if (stable >= 2) return current
        previous = current
        await page.waitForTimeout(100)
      }
      assert.fail('主题过渡后公共控件样式未稳定')
    }
    const appearanceRequests = requests.length
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      await page.waitForTimeout(350)
      const defaultSkin = await settledSkin(defaultReference), neutralSkin = await settledSkin(neutralReference)
      assert.equal(defaultSkin.radius, '12px'); assert.equal(defaultSkin.padding, '4px'); assert.equal(defaultSkin.border, '0px')
      assert.equal(neutralSkin.radius, '12px'); assert.equal(neutralSkin.padding, '4px'); assert.equal(neutralSkin.border, '1px')
      for (const appearance of ['underline', 'subtle']) {
        await page.evaluate(appearance => { const { state, events } = window.segmentedFixture; state.appearance = appearance; state.value = 0; events.length = 0 }, appearance)
        await aligned()
        await page.waitForTimeout(350)
        const visual = await control.evaluate(element => {
          const root = getComputedStyle(element), indicatorElement = element.querySelector('.segmented-control-indicator'), indicator = getComputedStyle(indicatorElement), line = getComputedStyle(indicatorElement, '::after'), count = getComputedStyle(element.querySelector('.segmented-control-count')), active = getComputedStyle(element.querySelector('[aria-pressed=true]'))
          return { background: root.backgroundColor, padding: root.padding, radius: root.borderRadius, height: element.getBoundingClientRect().height, optionHeight: element.querySelector('button').getBoundingClientRect().height, indicatorBackground: indicator.backgroundColor, indicatorShadow: indicator.boxShadow, lineHeight: line.height, lineBackground: line.backgroundColor, activeColor: active.color, countBackground: count.backgroundColor, countRadius: count.borderRadius }
        })
        if (appearance === 'underline') {
          assert.equal(visual.background, 'rgba(0, 0, 0, 0)'); assert.equal(visual.padding, '0px'); assert.equal(visual.radius, '0px')
          assert.equal(visual.indicatorBackground, 'rgba(0, 0, 0, 0)'); assert.equal(visual.indicatorShadow, 'none'); assert.equal(visual.lineHeight, '2px')
          assert.equal(visual.lineBackground, visual.activeColor, '底线与当前资源文字使用同一主题色')
          assert.notEqual(visual.countBackground, 'rgba(0, 0, 0, 0)'); assert.equal(visual.countRadius, '999px', '一级数量采用独立徽标')
        } else {
          assert.equal(visual.padding, '2px'); assert.equal(visual.radius, '8px'); assert.equal(visual.height, 36); assert.equal(visual.optionHeight, 32)
          assert.notEqual(visual.background, 'rgba(0, 0, 0, 0)', '二级展示方式保留低对比底槽')
        }
        assert.deepEqual(await skin(defaultReference), defaultSkin, `${appearance}不改变同时挂载的默认控件`)
        assert.deepEqual(await skin(neutralReference), neutralSkin, `${appearance}不改变同时挂载的中性控件`)
        await control.getByRole('button', { name: '文件', exact: true }).focus()
        await page.keyboard.press('ArrowRight')
        assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '版本', '新外观仍跳过禁用项')
        assert.deepEqual(await page.evaluate(() => window.segmentedFixture.events), [], '方向键不自动确认')
        await page.keyboard.press('Enter'); await aligned()
        await page.keyboard.press('Home')
        assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '文件')
        await page.keyboard.press('End')
        assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '版本')
        await page.keyboard.press('ArrowRight'); await page.keyboard.press('Enter'); await aligned()
        assert.deepEqual(await page.evaluate(() => window.segmentedFixture.events), [2, 0])
        await page.emulateMedia({ reducedMotion: 'reduce' })
        await control.getByRole('button', { name: '版本', exact: true }).click(); await aligned()
        assert.equal(await control.locator('.segmented-control-indicator').evaluate(element => element.style.transition), 'none', `${appearance}尊重减少动态效果`)
        await page.setViewportSize({ width: 320, height: 800 }); await aligned()
        const mobile = await control.evaluate(element => ({ padding: getComputedStyle(element).padding, height: element.getBoundingClientRect().height, options: [...element.querySelectorAll('button')].map(button => { const rect = button.getBoundingClientRect(); return { width: rect.width, height: rect.height } }) }))
        assert.equal(mobile.padding, '0px'); assert.equal(mobile.height, 44, `${appearance}手机整体高度保持44px`)
        assert.ok(mobile.options.every(option => option.width >= 44 && option.height >= 44), `${appearance}手机点击区至少44px`)
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
        await page.setViewportSize({ width: 1100, height: 800 }); await page.emulateMedia({ reducedMotion: 'no-preference' }); await aligned()
      }
    }
    assert.equal(requests.length, appearanceRequests, '新外观、键盘和主题切换不触发网络请求')
    await page.evaluate(() => { window.segmentedFixture.state.appearance = 'default' })
    assert.equal(await control.evaluate(element => element.classList.contains('segmented-control-underline') || element.classList.contains('segmented-control-subtle')), false)
    assert.equal((await skin(control)).padding, '4px', '恢复default后不残留新外观样式')
    passed.push('underline底线与计数徽标、subtle桌面36/手机44px、默认/neutral样式隔离、深浅主题/键盘/减少动态效果')
    await page.evaluate(() => { window.segmentedFixture.dispose(); localStorage.setItem('admin_token', 'mock-segmented') })

    await page.setViewportSize({ width: 1100, height: 800 })
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await page.goto(origin + '/follows/manage?status=processing&kind=tv&q=fixture')
    const statuses = page.getByRole('navigation', { name: '处理状态筛选', exact: true })
    await statuses.getByRole('button', { name: '处理中', exact: true }).waitFor()
    await statuses.getByRole('button', { name: '已完成', exact: true }).click()
    await page.waitForURL(url => url.searchParams.get('status') === 'completed')
    assert.equal(new URL(page.url()).searchParams.get('kind'), 'tv')
    assert.equal(new URL(page.url()).searchParams.get('q'), 'fixture')
    await page.getByRole('navigation', { name: '影视类型筛选', exact: true }).getByRole('button', { name: '电影', exact: true }).click()
    await page.waitForURL(url => url.searchParams.get('kind') === 'movie')
    passed.push('追更管理状态/类型筛选保留查询和页面路由')

    await page.goto(origin + '/media/organize/history?status=done&page=3&q=fixture')
    const records = page.getByRole('group', { name: '记录状态', exact: true })
    await records.getByRole('button', { name: '成功', exact: true }).waitFor()
    await records.getByRole('button', { name: '成功', exact: true }).click()
    await page.waitForURL(url => !url.searchParams.has('page'))
    assert.equal(new URL(page.url()).searchParams.get('q'), 'fixture')
    await records.getByRole('button', { name: '失败', exact: true }).click()
    await page.waitForURL(url => url.searchParams.get('status') === 'failed')
    await page.waitForFunction(() => document.querySelector('.mo-status-filter [aria-pressed=true]')?.textContent.includes('12'))
    passed.push('整理记录重选复位分页、保留关键词、切换状态与实时数量')
    await fs.mkdir('test-results', { recursive: true })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    for (const route of ['/follows/manage', '/media/organize/history']) {
      await page.goto(origin + route)
      await page.locator('.segmented-control button').first().waitFor()
      for (const width of [1100, 320]) {
        await page.setViewportSize({ width, height: 800 })
        // 手机管理页状态栏底色透明，类型筛选的底色由玻璃外壳承载。
        const mobileFollows = route === '/follows/manage' && width < 640
        const track = mobileFollows ? page.locator('.subscription-kind.glass-floating-pill') : page.locator('.segmented-control').first()
        for (const dark of [false, true]) {
          await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
          if (mobileFollows) {
            assert.equal(await statuses.evaluate(el => getComputedStyle(el).backgroundColor), 'rgba(0, 0, 0, 0)')
            const targets = await track.locator('.segmented-control-pwa-pill button').evaluateAll(options => options.map(option => { const box = option.getBoundingClientRect(); return { width: box.width, height: box.height } }))
            assert.equal(targets.length, 3)
            assert.ok(targets.every(target => target.width >= 44 && target.height >= 44))
          }
          const background = await track.evaluate(el => getComputedStyle(el).backgroundColor)
          const channel = Number(background.match(/[\d.]+/)[0])
          assert(dark ? channel < 100 : channel > 200, JSON.stringify({ dark, background }))
          const overflow = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth, elements: [...document.querySelectorAll('body *')].filter(el => { const r = el.getBoundingClientRect(); return r.right > innerWidth + 1 && getComputedStyle(el).position !== 'fixed' }).slice(0, 12).map(el => ({ tag: el.tagName, class: el.className, width: el.getBoundingClientRect().width })) }))
          assert(overflow.scroll <= overflow.width, JSON.stringify({ route, dark, ...overflow }))
          await page.screenshot({ path: `test-results/segmented-${route.includes('follows') ? 'follows' : 'history'}-${width}-${dark ? 'dark' : 'light'}.png`, animations: 'disabled' })
        }
      }
    }
    assert.deepEqual(errors, [])
    assert.equal(requests.some(request => request.method !== 'GET'), false)
    passed.push('两处页面桌面/320px深浅主题无横向溢出、无浏览器错误或写请求')
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
