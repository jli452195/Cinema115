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
      // DevTools 不支持模拟 display-mode，仅在隔离页面中同步切换查询与对应 CSS 分支。
      let standalone = false
      const standaloneQuery = new EventTarget()
      Object.defineProperty(standaloneQuery, 'matches', { get: () => standalone })
      const matchMedia = window.matchMedia.bind(window)
      window.matchMedia = query => query === '(display-mode: standalone)' ? standaloneQuery : matchMedia(query)
      const standaloneRules = new Map()
      window.setStandalone = value => {
        const collect = rules => {
          for (const rule of rules) {
            if (rule instanceof CSSMediaRule && rule.conditionText.includes('display-mode: standalone')) standaloneRules.set(rule, rule.media.mediaText)
            if (rule.cssRules) collect(rule.cssRules)
          }
        }
        for (const sheet of document.styleSheets) collect(sheet.cssRules)
        for (const [rule, original] of standaloneRules) rule.media.mediaText = value ? 'all' : original
        standalone = value
        standaloneQuery.dispatchEvent(new Event('change'))
      }
      window.scene = ref('header')
      window.headerProps = ref({ tonalSurface: true })
      window.headerBackdrop = ref(false)
      const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] })
      await router.push('/')
      createApp({ render: () => scene.value === 'header'
        ? h('div', { style: { '--management-sidebar-width': '64px', '--management-sidebar-progress': '0', background: headerBackdrop.value ? 'linear-gradient(90deg,#d946ef,#2563eb)' : undefined } }, [h(Header, headerProps.value), h('main', { class: 'workbench-content', style: 'min-height:1400px' }, [h('p', { id: 'content-start' }, '安全区布局测试')])])
        : h(LoginLayout, { subtitle: '安全区登录布局测试' }, { default: () => h('input', { class: 'auth-input', placeholder: '输入框' }) })
      }).use(router).mount('#fixture')
      const root = document.documentElement
      for (const [side, value] of Object.entries({ top: 28, left: 28, right: 12, bottom: 24 })) root.style.setProperty(`--app-safe-${side}`, `${value}px`)
      window.chromeWrites = []
      window.transitionSettled = true
      const descriptor = Object.getOwnPropertyDescriptor(HTMLMetaElement.prototype, 'content')
      Object.defineProperty(HTMLMetaElement.prototype, 'content', {
        ...descriptor,
        set(value) {
          if (this.name === 'theme-color') {
            // 在赋值时采样，验证颜色在动画开始前提交，且过程中不重复写入。
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
    const topPixels = async rows => {
      const png = (await page.screenshot()).toString('base64')
      return page.evaluate(async ({ png, rows }) => {
        const image = new Image()
        image.src = `data:image/png;base64,${png}`
        await image.decode()
        const canvas = document.createElement('canvas')
        canvas.width = image.width; canvas.height = image.height
        const ctx = canvas.getContext('2d')
        ctx.drawImage(image, 0, 0)
        return rows.flatMap(y => Array.from({ length: Math.floor((image.width - 16) / 13) }, (_, index) => [...ctx.getImageData(2 + index * 13, y, 1, 1).data].slice(0, 3)))
      }, { png, rows })
    }
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.evaluate(() => { setStandalone(true); headerBackdrop.value = true })
    for (const target of ['light', 'dark']) {
      await page.evaluate(target => theme.setTheme(target), target)
      for (const safeTop of [0, 28]) {
        await page.evaluate(safeTop => document.documentElement.style.setProperty('--app-safe-top', `${safeTop}px`), safeTop)
        for (const tonalSurface of [false, true]) {
          await page.evaluate(tonalSurface => {
            scene.value = 'header'
            headerProps.value = { tonalSurface, overBackdrop: true }
            scrollTo(0, 0)
          }, tonalSurface)
          await page.locator('.workbench-header').waitFor()
          for (const pixel of await topPixels([2, safeTop + 2])) assert(pixel.every((channel, index) => Math.abs(channel - colors[target][index]) <= 1), `PWA ${target} 顶部须与系统栏纯色一致，实际${pixel}`)
          const surface = await page.evaluate(() => {
            const header = document.querySelector('.workbench-header')
            return {
              immersive: header.classList.contains('immersive-surface') || header.classList.contains('over-backdrop'),
              opacity: getComputedStyle(header.querySelector('.header-surface, .header-background')).opacity,
              text: getComputedStyle(header.querySelector('.top-link')).color,
            }
          })
          assert.equal(surface.immersive, false)
          assert.equal(surface.opacity, '1')
          if (target === 'light') assert.notEqual(surface.text, 'rgb(255, 255, 255)', 'PWA 浅色实色顶栏不能保留海报上的白色文字')
          await page.evaluate(() => scrollTo(0, 200))
          await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)))
          for (const pixel of await topPixels([2])) assert.deepEqual(pixel, colors[target], 'PWA 滚动后顶部仍须保持同色')
        }
        await page.evaluate(() => { scene.value = 'login'; scrollTo(0, 0) })
        await page.locator('.auth-theme-toggle').waitFor()
        for (const pixel of await topPixels([2, safeTop + 2])) assert.deepEqual(pixel, colors[target], 'PWA 登录页顶部不能混入光晕或点阵颜色')
        if (safeTop === 28) await page.screenshot({ path: path.join(__dirname, `../test-results/pwa-login-unified-${target}.png`) })
      }
    }
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    assert.equal(await page.locator('.pattern-bg-canvas').evaluate(element => element.getAnimations()[0]?.playState), 'running', 'PWA 内容区点阵应继续播放')
    await page.evaluate(() => {
      setStandalone(false)
      scene.value = 'header'
      headerProps.value = { tonalSurface: true, overBackdrop: true }
      scrollTo(0, 0)
    })
    await page.locator('.workbench-header.immersive-surface').waitFor()
    assert.equal(await page.locator('.header-surface').evaluate(element => getComputedStyle(element).opacity), '0', '离开 PWA 模式后恢复浏览器海报透出效果')
    await page.evaluate(async () => {
      headerProps.value = { tonalSurface: true }
      headerBackdrop.value = false
      await theme.setTheme('light')
    })
    passed.push('PWA 深浅主题、零与非零安全区、两类海报顶栏：像素与系统栏底色一致；登录页下方保留动效，浏览器模式恢复原样')

    for (const standalone of [false, true]) {
      for (const scene of ['header', 'login']) {
      await page.evaluate(({ scene, standalone }) => { setStandalone(standalone); window.scene.value = scene }, { scene, standalone })
      await page.locator(scene === 'login' ? '.auth-theme-toggle' : '.workbench-header').waitFor()
      for (const target of ['dark', 'light']) {
        const targetColor = `rgb(${colors[target].join(' ')})`
        await page.evaluate(target => {
          window.pauseReveal = true; window.revealReady = false
          window.chromeWrites = []
          window.pending = theme.setTheme(target, { x: 340, y: 60 })
        }, target)
        await page.waitForFunction(() => window.revealReady)
        assert.equal(await meta(), targetColor, '圆形动画刚开始时，系统栏颜色应已跟随页面主题更新')
        const expectedWrites = [{ value: targetColor, reveal: true, settled: false }]
        assert.deepEqual(await page.evaluate(() => chromeWrites), expectedWrites)
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
        await page.screenshot({ path: path.join(__dirname, `../test-results/pwa-shell-${standalone ? 'installed' : 'browser'}-${scene}-${target}-middle.png`) })
        assert.equal(await meta(), targetColor, '转场中途保持已提交的目标颜色')
        await page.evaluate(() => { reveal.currentTime = 399.99 })
        await page.screenshot({ path: path.join(__dirname, `../test-results/pwa-shell-${standalone ? 'installed' : 'browser'}-${scene}-${target}-end.png`) })
        assert.equal(await meta(), targetColor)
        assert.deepEqual(await page.evaluate(() => chromeWrites), expectedWrites, '动画中途不逐帧重写颜色')
        await page.evaluate(async () => { transition.skipTransition(); await pending })
        assert.equal(await meta(), targetColor)
        assert.deepEqual(await page.evaluate(() => chromeWrites), expectedWrites, '动画结束和快照清理不重复写入颜色')
      }
      }
    }
    await page.evaluate(() => { setStandalone(false); window.scene.value = 'header' })
    passed.push('浏览器与 PWA、站内与登录页双向切换：动画开始前同步系统栏一次，中途和结束不重复写入')

    await page.evaluate(async () => {
      window.chromeWrites = []; window.revealReady = false
      window.pending = theme.setTheme('dark', { x: 340, y: 60 })
    })
    await page.waitForFunction(() => window.revealReady)
    await page.evaluate(async () => {
      transition.skipTransition()
      await transition.finished
      // 动画结束时立即撤回选择，旧请求清理不能覆盖新主题的颜色。
      const next = theme.setTheme('light')
      window.pauseReveal = false
      await Promise.all([pending, next])
    })
    assert.equal(await meta(), 'rgb(248 250 252)')
    assert.deepEqual(await page.evaluate(() => chromeWrites.map(write => write.value)), ['rgb(24 25 28)', 'rgb(248 250 252)'])
    passed.push('动画结束时撤回选择，系统栏立即跟随新主题，旧请求不覆盖新颜色')

    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.evaluate(async () => {
      const raf = window.requestAnimationFrame
      try {
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
        // 没有转场时，系统栏同步不能依赖后台会暂停的动画帧。
        window.requestAnimationFrame = () => { throw new Error('后台主题同步不应等待动画帧') }
        await theme.setTheme('dark')
        if (document.querySelector('meta[name="theme-color"]').content !== 'rgb(24 25 28)') throw new Error('后台深色未同步')
        await theme.setTheme('light')
      } finally {
        window.requestAnimationFrame = raf
        delete document.visibilityState
      }
    })
    assert.equal(await meta(), 'rgb(248 250 252)')
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    passed.push('后台无动画切换也立即同步颜色，不再依赖额外动画帧')

    // 相同主题请求会结束旧转场，但不能重复写入颜色。
    await page.evaluate(() => {
      window.pauseReveal = true; window.chromeWrites = []; window.revealReady = false
      window.pending = theme.setTheme('dark', { x: 340, y: 60 })
    })
    await page.waitForFunction(() => window.revealReady)
    assert.equal(await meta(), 'rgb(24 25 28)')
    await page.evaluate(async () => { await theme.setTheme('dark'); await pending })
    assert.deepEqual(await page.evaluate(() => chromeWrites), [{ value: 'rgb(24 25 28)', reveal: true, settled: false }])
    await page.evaluate(async () => { window.pauseReveal = false; await theme.setTheme('light') })

    await page.evaluate(() => {
      window.pauseReveal = true; window.revealReady = false; window.chromeWrites = []
      window.pending = theme.setTheme('dark')
    })
    await page.waitForFunction(() => window.revealReady)
    await page.evaluate(async () => { await Promise.all([pending, theme.setTheme('light'), theme.setTheme('dark')]) })
    assert.equal(await meta(), 'rgb(24 25 28)')
    assert.equal(await page.evaluate(() => chromeWrites.every(write => write.reveal && !write.settled)), true)
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
