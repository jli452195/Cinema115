// 系统栏由浏览器绘制；这里验证首屏主题、颜色提示时序及非零安全区布局。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = []
  try {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 }, colorScheme: 'light', serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('cinema115-theme', 'dark'))
    const page = await context.newPage()
    page.setDefaultTimeout(8000)
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', route => route.fulfill({ contentType: 'application/json', body: '{"items":[],"unread":0}' }))
    await page.route('**/src/main.ts*', route => route.fulfill({ contentType: 'application/javascript', body: '' }))
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(origin)
    assert.equal(await page.evaluate(() => document.documentElement.classList.contains('dark')), true)
    assert.equal(await page.locator('meta[name="theme-color"]').count(), 1)
    assert.equal(await page.locator('meta[name="theme-color"]').evaluateAll(elements => elements.every(element => element.content === '#18191c')), true)
    assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).backgroundColor), 'rgb(24, 25, 28)')
    assert((await page.locator('meta[name="viewport"]').getAttribute('content')).includes('viewport-fit=cover'))
    passed.push('主应用尚未加载时，已保存的深色偏好覆盖浅色系统，首屏与系统栏颜色一致')

    await page.route('**/__pwa-shell-test', route => route.fulfill({ contentType: 'text/html', body: '<html><head><meta name="theme-color"></head><body><div id="fixture"></div></body></html>' }))
    await page.goto(origin + '/__pwa-shell-test')
    await page.evaluate(async () => {
      localStorage.setItem('cinema115-theme', 'light')
      const { createApp, h, ref } = await import('/node_modules/.vite/deps/vue.js')
      const headerCode = await (await fetch('/src/components/WorkbenchHeader.vue')).text()
      const routerModule = headerCode.match(/["']([^"']*\/deps\/vue-router\.js[^"']*)["']/)[1]
      const { createRouter, createMemoryHistory } = await import(routerModule)
      const { default: Header } = await import('/src/components/WorkbenchHeader.vue')
      const { default: LoginLayout } = await import('/src/components/LoginLayout.vue')
      const code = await (await fetch('/src/components/ThemeSwitcher.vue')).text()
      const { useTheme } = await import(code.match(/["']([^"']*\/composables\/useTheme\.ts[^"']*)["']/)[1])
      await import('/src/style.css')
      window.theme = useTheme()
      window.waitForThemePaint = (await import('/src/utils/browserTheme.ts')).waitForThemePaint
      window.scene = ref('header')
      const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] })
      await router.push('/')
      createApp({ render: () => scene.value === 'header'
        ? h('div', { style: '--management-sidebar-width:64px;--management-sidebar-progress:0' }, [h(Header, { tonalSurface: true }), h('main', { class: 'workbench-content', style: 'min-height:1400px' }, [h('p', { id: 'content-start' }, '安全区布局测试')])])
        : h(LoginLayout, { subtitle: '安全区登录布局测试' }, { default: () => h('input', { class: 'auth-input', placeholder: '输入框' }) })
      }).use(router).mount('#fixture')
      const root = document.documentElement
      for (const [side, value] of Object.entries({ top: 28, left: 28, right: 12, bottom: 24 })) root.style.setProperty(`--app-safe-${side}`, `${value}px`)
      window.chromeWrites = []
      window.handoffSamples = []
      window.transitionSettled = true
      const descriptor = Object.getOwnPropertyDescriptor(HTMLMetaElement.prototype, 'content')
      Object.defineProperty(HTMLMetaElement.prototype, 'content', {
        ...descriptor,
        set(value) {
          if (this.name === 'theme-color') {
            // 在赋值时采样，避免异步观察器把同一任务内的提前更新误判为清理后更新。
            chromeWrites.push({ value, reveal: root.classList.contains('theme-reveal'), settled: window.transitionSettled })
          }
          descriptor.set.call(this, value)
        },
      })
      const native = document.startViewTransition.bind(document)
      document.startViewTransition = callback => {
        window.transition = native(callback)
        window.transitionSettled = false
        transition.finished.then(() => {
          window.transitionSettled = true
          requestAnimationFrame(() => handoffSamples.push({ value: document.querySelector('meta[name="theme-color"]').content, reveal: root.classList.contains('theme-reveal') }))
        }).catch(() => { window.transitionSettled = true })
        transition.ready.then(() => {
          if (!window.pauseReveal) return
          window.reveal = document.getAnimations().find(animation => animation.effect?.pseudoElement === (root.dataset.themeReveal === 'expand' ? '::view-transition-new(root)' : '::view-transition-old(root)'))
          reveal.pause(); reveal.currentTime = 0
          window.revealReady = true
        }).catch(() => {})
        return transition
      }
    })
    for (const width of [390, 844, 1280]) {
      await page.setViewportSize({ width, height: 844 })
      const layout = await page.evaluate(() => {
        const header = document.querySelector('.workbench-header').getBoundingClientRect()
        const toolbar = document.querySelector('.header-inner').getBoundingClientRect()
        const surface = document.querySelector('.header-surface').getBoundingClientRect()
        const content = document.querySelector('#content-start').getBoundingClientRect()
        return { header: { top: header.top, bottom: header.bottom }, toolbar: { top: toolbar.top, height: toolbar.height }, surface: { top: surface.top, height: surface.height }, contentTop: content.top, padding: getComputedStyle(document.querySelector('.header-inner')).paddingLeft }
      })
      const toolbar = width >= 1024 ? 72 : 64
      assert.equal(layout.header.top, 0)
      assert.equal(layout.toolbar.top, 28)
      assert.equal(layout.toolbar.height, toolbar)
      assert.equal(layout.surface.top, 0)
      assert.equal(layout.surface.height, toolbar + 28)
      assert.equal(parseFloat(layout.padding), 28)
      assert(layout.contentTop >= layout.header.bottom + 15)
    }
    await page.setViewportSize({ width: 390, height: 844 })
    await page.getByRole('button', { name: '打开侧栏', exact: true }).click()
    assert.equal(await page.locator('.management-drawer-heading').evaluate(element => element.getBoundingClientRect().top), 28)
    assert.equal(await page.locator('.management-drawer-heading').evaluate(element => element.getBoundingClientRect().height), 64)
    await page.keyboard.press('Escape')
    passed.push('手机、横屏及桌面非零安全区：背景贯通、工具栏高度不变、内容偏移正确，侧栏不重复避让')

    const colors = { light: [248, 250, 252], dark: [24, 25, 28] }
    const meta = () => page.locator('meta[name="theme-color"]').first().getAttribute('content')
    for (const scene of ['header', 'login']) {
      await page.evaluate(scene => { window.scene.value = scene }, scene)
      await page.locator(scene === 'login' ? '.auth-theme-toggle' : '.workbench-header').waitFor()
      for (const target of ['dark', 'light']) {
        const old = await meta()
        await page.evaluate(target => {
          window.pauseReveal = true; window.revealReady = false
          window.chromeWrites = []; window.handoffSamples = []
          window.pending = theme.setTheme(target, { x: 340, y: 60 })
        }, target)
        await page.waitForFunction(() => window.revealReady)
        assert.equal(await meta(), old, '页面主题已更新、顶部尚未揭开时，系统栏须保持原色')
        await page.evaluate(() => {
          const pseudo = document.documentElement.dataset.themeReveal === 'expand' ? '::view-transition-new(root)' : '::view-transition-old(root)'
          let low = 0, high = 400
          for (let i = 0; i < 22; i++) {
            const time = (low + high) / 2
            reveal.currentTime = time
            const radius = parseFloat(getComputedStyle(document.documentElement, pseudo).getPropertyValue('--theme-reveal-mask-radius'))
            if ((radius < 180) === (document.documentElement.dataset.themeReveal === 'expand')) low = time
            else high = time
          }
          reveal.currentTime = (low + high) / 2
        })
        const targetColor = `rgb(${colors[target].join(' ')})`
        await page.screenshot({ path: path.join(__dirname, `../test-results/pwa-shell-${target}-middle.png`) })
        assert.equal(await meta(), old, '转场中途不能请求手机系统栏重绘')
        await page.evaluate(() => { reveal.currentTime = 399.99 })
        await page.screenshot({ path: path.join(__dirname, `../test-results/pwa-shell-${target}-end.png`) })
        assert.equal(await meta(), old, '快照销毁前保持系统栏稳定，不逐帧追色')
        assert.deepEqual(await page.evaluate(() => chromeWrites), [])
        await page.evaluate(async () => { transition.skipTransition(); await pending })
        assert.equal(await meta(), targetColor)
        assert.deepEqual(await page.evaluate(() => handoffSamples), [{ value: old, reveal: false }], '页面快照清理后须先获得一轮绘制机会，再更新系统栏')
        assert.deepEqual(await page.evaluate(() => chromeWrites), [{ value: targetColor, reveal: false, settled: true }])
      }
    }
    await page.evaluate(() => { window.scene.value = 'header' })
    passed.push('站内与登录页双向切换：先清理快照并等待绘制机会，再同步系统栏一次')

    await page.evaluate(async () => {
      window.chromeWrites = []; window.revealReady = false
      window.pending = theme.setTheme('dark', { x: 340, y: 60 })
      await new Promise(resolve => requestAnimationFrame(resolve))
      await transition.ready
      transition.skipTransition()
      await transition.finished
      // 在快照已清理、原生栏尚未更新的窗口撤回选择，不能再写入旧深色。
      const next = theme.setTheme('light')
      window.pauseReveal = false
      await Promise.all([pending, next])
    })
    assert.equal(await meta(), 'rgb(248 250 252)')
    assert.deepEqual(await page.evaluate(() => chromeWrites), [])
    passed.push('绘制等待期间收到新主题请求时，不写入已过时的系统栏颜色')

    await page.evaluate(async () => {
      const setVisibility = value => Object.defineProperty(document, 'visibilityState', { configurable: true, value })
      try {
        setVisibility('hidden')
        await waitForThemePaint()
        setVisibility('visible')
        const pending = waitForThemePaint()
        setVisibility('hidden')
        document.dispatchEvent(new Event('visibilitychange'))
        await pending
      } finally {
        delete document.visibilityState
      }
    })
    passed.push('页面已在后台或等待期间切到后台时，不会因动画帧暂停而挂起')

    // 新请求与已应用的页面主题相同时，也要补齐被旧转场推迟的系统栏同步。
    await page.evaluate(() => {
      window.pauseReveal = true; window.chromeWrites = []; window.revealReady = false
      window.pending = theme.setTheme('dark', { x: 340, y: 60 })
    })
    await page.waitForFunction(() => window.revealReady)
    assert.equal(await meta(), 'rgb(248 250 252)')
    await page.evaluate(async () => { await theme.setTheme('dark'); await pending })
    assert.deepEqual(await page.evaluate(() => chromeWrites), [{ value: 'rgb(24 25 28)', reveal: false, settled: true }])
    await page.evaluate(async () => { window.pauseReveal = false; await theme.setTheme('light') })

    await page.evaluate(async () => {
      window.pauseReveal = false
      window.chromeWrites = []
      const first = theme.setTheme('dark')
      await new Promise(resolve => requestAnimationFrame(resolve))
      await Promise.all([first, theme.setTheme('light'), theme.setTheme('dark')])
    })
    assert.equal(await meta(), 'rgb(24 25 28)')
    assert.equal(await page.evaluate(() => chromeWrites.every(write => !write.reveal && write.settled)), true)
    assert.equal(await page.evaluate(() => chromeWrites.length), 1, '快速请求不能写入过时的中间主题')
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.evaluate(() => theme.setTheme('light'))
    assert.equal(await meta(), 'rgb(248 250 252)')
    await page.evaluate(() => { scene.value = 'login' })
    await page.locator('.auth-theme-toggle').waitFor()
    assert.equal(await page.locator('.auth-theme-toggle').evaluate(element => element.getBoundingClientRect().top), 40)
    assert.equal(await page.locator('.auth-theme-toggle').evaluate(element => parseFloat(getComputedStyle(element).right)), 24)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    passed.push('连续点击与减少动画偏好保持颜色正确，登录入口避让安全区且无横向溢出')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
