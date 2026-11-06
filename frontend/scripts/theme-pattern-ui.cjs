// 验证登录页整页快照对照方案，不访问业务接口。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = []
  try {
    const mount = async page => {
      page.on('pageerror', error => errors.push(error.message))
      await page.route('**/api/**', route => route.abort())
      await page.route('**/__theme-pattern-test', route => route.fulfill({ contentType: 'text/html', body: '<html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="fixture"></div></body></html>' }))
      await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/__theme-pattern-test')
      await page.evaluate(async () => {
        localStorage.setItem('cinema115-theme', 'light')
        const { createApp, h } = await import('/node_modules/.vite/deps/vue.js')
        const { default: LoginLayout } = await import('/src/components/LoginLayout.vue')
        const code = await (await fetch('/src/components/ThemeSwitcher.vue')).text()
        const { useTheme } = await import(code.match(/["']([^"']*\/composables\/useTheme\.ts[^"']*)["']/)[1])
        await import('/src/style.css')
        window.theme = useTheme()
        createApp({ render: () => h(LoginLayout, { subtitle: '主题背景测试' }, { default: () => h('input', { class: 'auth-input', placeholder: '输入框遮挡测试' }) }) }).mount('#fixture')
        const native = document.startViewTransition.bind(document)
        document.startViewTransition = callback => {
          const transition = native(callback)
          window.transition = transition
          transition.ready.then(() => queueMicrotask(() => {
            if (!window.pausePattern) return
            window.pseudoAnimations = document.getAnimations().filter(animation => animation.effect?.pseudoElement)
            window.drift = document.querySelector('.pattern-bg-canvas').getAnimations()[0]
            window.drift.pause()
            pseudoAnimations.forEach(animation => { animation.pause(); animation.currentTime = 0 })
            window.driftStart = drift.currentTime
            // 固定暂停生效的时刻，避免截图前多推进一个刷新帧。
            window.drift.currentTime = driftStart
            window.patternReady = true
          })).catch(() => {})
          return transition
        }
      })
    }
    const page = await browser.newPage({ viewport: { width: 1000, height: 700 }, serviceWorkers: 'block' })
    await mount(page)

    // 在无表单遮挡的点阵列读取周期相位，验证的是可见圆点而非底层动画时钟。
    const inspect = async width => {
      const png = (await page.screenshot()).toString('base64')
      return page.evaluate(async ({ png, width }) => {
        const image = new Image()
        image.src = `data:image/png;base64,${png}`
        await image.decode()
        const canvas = document.createElement('canvas')
        canvas.width = image.width; canvas.height = image.height
        const ctx = canvas.getContext('2d')
        ctx.drawImage(image, 0, 0)
        const x = width === 1000 ? 260 : 60
        let sin = 0, cos = 0, total = 0
        const pixels = []
        for (let y = 260; y < 380; y++) {
          const dot = ctx.getImageData(x, y, 1, 1).data
          const base = ctx.getImageData(x + 8, y, 1, 1).data
          const weight = Math.max(0, Math.abs(dot[0] - base[0]) - 1)
          sin += Math.sin((y + 0.5) * Math.PI / 20) * weight
          cos += Math.cos((y + 0.5) * Math.PI / 20) * weight
          total += weight
          pixels.push(...dot.slice(0, 3))
        }
        return { phase: (Math.atan2(sin, cos) * 20 / Math.PI + 40) % 40, total, pixels }
      }, { png, width })
    }
    for (const width of [1000, 390]) {
      await page.setViewportSize({ width, height: 700 })
      for (const target of ['dark', 'light']) {
        await page.evaluate(target => {
          window.pausePattern = true; window.patternReady = false
          window.pending = theme.setTheme(target, { x: innerWidth - 50, y: 40 })
        }, target)
        await page.waitForFunction(() => window.patternReady)
        const before = await inspect(width)
        await page.screenshot({ path: `test-results/pattern-${width}-${target}-before.png` })
        await page.evaluate(() => {
          drift.currentTime = driftStart + 200
        })
        const middle = await inspect(width)
        await page.screenshot({ path: `test-results/pattern-${width}-${target}-middle.png` })
        const movement = (middle.phase - before.phase + 40) % 40
        assert(before.total > 15 && middle.total > 15, '截图中必须检测到可见点阵')
        assert(Math.min(movement, 40 - movement) < 0.5, `整页旧快照应保持捕获时的点阵，不能再独立重绘，实际相位变化${movement}`)
        await page.evaluate(() => {
          pseudoAnimations.forEach(animation => { animation.currentTime = 399.99 })
          drift.currentTime = driftStart + 399.99
        })
        const end = await inspect(width)
        await page.evaluate(async () => { transition.skipTransition(); await pending })
        const live = await inspect(width)
        const maxDifference = Math.max(...live.pixels.map((value, index) => Math.abs(value - end.pixels[index])))
        assert(maxDifference <= 2, `新主题快照与实际页面交接须保持一致，最大像素差${maxDifference}`)
        assert.equal(await page.evaluate(() => document.documentElement.classList.contains('theme-reveal-pattern')), false)
        await page.evaluate(() => drift.play())
      }
    }
    passed.push('桌面与手机双向切换：旧点阵随整页快照定格，新主题交接像素一致')

    await page.evaluate(async () => {
      window.pausePattern = false
      drift.play()
      window.snapshotSamples = []
      const pending = theme.setTheme('dark')
      await new Promise(resolve => requestAnimationFrame(resolve))
      await transition.ready
      while (document.documentElement.classList.contains('theme-reveal')) {
        const root = document.documentElement
        snapshotSamples.push({
          split: root.classList.contains('theme-reveal-pattern'),
          position: root.style.getPropertyValue('--theme-pattern-position'),
          names: ['[data-theme-pattern]', '[data-theme-foreground]'].map(selector => getComputedStyle(document.querySelector(selector)).viewTransitionName),
          pseudos: document.getAnimations().map(animation => animation.effect?.pseudoElement).filter(Boolean),
        })
        await new Promise(resolve => requestAnimationFrame(resolve))
      }
      await pending
    })
    const samples = await page.evaluate(() => snapshotSamples)
    assert(samples.length > 5)
    assert(samples.every(sample => !sample.split && sample.position === '' && sample.names.every(name => name === 'none') && sample.pseudos.every(name => !name.includes('auth-'))), '登录页不能再启用点阵、表单命名快照或逐帧位置同步')
    passed.push('完整转场仅使用整页内容快照，没有登录页分层或点阵位置同步')

    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.evaluate(() => theme.setTheme('light'))
    assert.equal(await page.locator('.pattern-bg-canvas').evaluate(element => element.getAnimations().length), 0)
    passed.push('减少动态效果时直接切换主题，点阵保持静止')

    // 录制高 DPR 手机视口的完整生命周期，捕获动画中及交接后的大幅亮度回跳。
    const mobile = await browser.newPage({ viewport: { width: 375, height: 734 }, deviceScaleFactor: 3.25, isMobile: true, hasTouch: true, serviceWorkers: 'block' })
    await mount(mobile)
    const cdp = await mobile.context().newCDPSession(mobile)
    const frames = []
    cdp.on('Page.screencastFrame', event => {
      frames.push({ time: event.metadata.timestamp, png: event.data })
      void cdp.send('Page.screencastFrameAck', { sessionId: event.sessionId })
    })
    await cdp.send('Page.startScreencast', { format: 'png', maxWidth: 375, maxHeight: 734, everyNthFrame: 1 })
    const cycles = await mobile.evaluate(async () => {
      const cycles = []
      window.compositeSamples = []
      for (const target of ['dark', 'light', 'dark', 'light', 'dark', 'light']) {
        const start = Date.now() / 1000
        const pending = theme.setTheme(target, { x: 293, y: 32 })
        await new Promise(resolve => requestAnimationFrame(resolve))
        await transition.ready
        while (document.documentElement.classList.contains('theme-reveal')) {
          const root = document.documentElement
          const source = document.querySelector('.auth-content').getBoundingClientRect()
          const oldRoot = getComputedStyle(root, '::view-transition-old(root)')
          const newRoot = getComputedStyle(root, '::view-transition-new(root)')
          compositeSamples.push({
            rect: [source.x, source.y, source.width, source.height],
            baseMask: target === 'dark' ? oldRoot.maskImage : newRoot.maskImage,
            baseOpacity: target === 'dark' ? oldRoot.opacity : newRoot.opacity,
            blends: [oldRoot.mixBlendMode, newRoot.mixBlendMode],
            split: root.classList.contains('theme-reveal-pattern'),
            names: ['[data-theme-pattern]', '[data-theme-foreground]'].map(selector => getComputedStyle(document.querySelector(selector)).viewTransitionName),
          })
          await new Promise(resolve => requestAnimationFrame(resolve))
        }
        await pending
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
        cycles.push({ target, start, end: Date.now() / 1000 })
      }
      return cycles
    })
    await cdp.send('Page.stopScreencast')
    const decoded = await mobile.evaluate(async frames => {
      const canvas = document.createElement('canvas'), ctx = canvas.getContext('2d')
      const result = []
      for (const frame of frames) {
        const image = new Image()
        image.src = `data:image/png;base64,${frame.png}`
        await image.decode()
        canvas.width = image.width; canvas.height = image.height
        ctx.drawImage(image, 0, 0)
        const values = [[5, 100], [108, 100], [5, 710]].map(([x, y]) => {
          const pixel = ctx.getImageData(Math.floor(x * image.width / 375), Math.floor(y * image.height / 734), 1, 1).data
          return (pixel[0] + pixel[1] + pixel[2]) / 3
        })
        result.push({ time: frame.time, values })
      }
      return result
    }, frames)
    for (const cycle of cycles) {
      const during = decoded.filter(frame => frame.time > cycle.start + 0.025 && frame.time < cycle.end - 0.025)
      assert(during.length >= 8, `每次手机转场须捕获足够帧，实际${during.length}`)
      for (let point = 0; point < 3; point++) {
        let extreme = during[0].values[point]
        for (const frame of during) {
          const value = frame.values[point]
          assert(cycle.target === 'dark' ? value <= extreme + 15 : value >= extreme - 15, `手机${cycle.target}转场采样点${point}亮度反向跳变：${extreme} -> ${value}`)
          extreme = cycle.target === 'dark' ? Math.min(extreme, value) : Math.max(extreme, value)
        }
      }
    }
    const composite = await mobile.evaluate(() => compositeSamples)
    assert(composite.length > 60)
    assert(composite.every(sample => sample.baseMask === 'none' && sample.baseOpacity === '1' && sample.blends.every(blend => blend === 'normal')), '完整生命周期须保留不透明底层，禁用加亮混合')
    assert(composite.every(sample => !sample.split && sample.names.every(name => name === 'none')), '手机连续切换也须始终使用整页快照')
    assert(composite.every(sample => sample.rect.every((value, index) => Math.abs(value - composite[0].rect[index]) < 0.1)), '手机连续切换时表单几何位置不能抖动')
    assert.equal(await mobile.locator('[style*="view-transition-name: theme-reveal-anchor"]').count(), 0)
    passed.push(`高 DPR 手机视口连续切换6次：${decoded.length}帧无大幅亮度回跳，表单位置稳定，底层始终不透明`)
    await mobile.close()
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
