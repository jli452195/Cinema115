// 使用独立页面检查主题转场，不访问真实业务接口。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = []
  try {
    const page = await browser.newPage({ viewport: { width: 1000, height: 700 }, serviceWorkers: 'block', colorScheme: 'light', hasTouch: true })
    page.setDefaultTimeout(8000)
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', route => route.abort())
    await page.route('**/__theme-transition-test', route => route.fulfill({ contentType: 'text/html', body: '<html><head><meta name="theme-color"><meta name="theme-color"></head><body><div id="fixture"></div></body></html>' }))
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/__theme-transition-test')
    await page.evaluate(async () => {
      localStorage.setItem('cinema115-theme', 'system')
      const { createApp, h } = await import('/node_modules/.vite/deps/vue.js')
      const { default: ThemeSwitcher } = await import('/src/components/ThemeSwitcher.vue')
      // 使用组件实际引用的模块地址，避免热更新时间戳产生第二份主题状态。
      const componentCode = await (await fetch('/src/components/ThemeSwitcher.vue')).text()
      const themeModule = componentCode.match(/["']([^"']*\/composables\/useTheme\.ts[^"']*)["']/)[1]
      const { useTheme } = await import(themeModule)
      await import('/src/style.css')
      window.theme = useTheme()
      window.records = []
      window.nativeTransition = document.startViewTransition.bind(document)
      document.startViewTransition = callback => {
        const record = { origin: document.documentElement.style.getPropertyValue('--theme-reveal-origin'), radius: parseFloat(document.documentElement.style.getPropertyValue('--theme-reveal-radius')) }
        window.records.push(record)
        const transition = window.nativeTransition(callback)
        window.latestTransition = transition
        transition.ready.then(() => {
          const root = document.documentElement
          const oldStyle = getComputedStyle(root, '::view-transition-old(root)')
          const newStyle = getComputedStyle(root, '::view-transition-new(root)')
          Object.assign(record, {
            ready: true, direction: root.dataset.themeReveal,
            oldZ: oldStyle.zIndex, newZ: newStyle.zIndex,
            oldAnimation: oldStyle.animationName, newAnimation: newStyle.animationName,
            oldDuration: oldStyle.animationDuration, newDuration: newStyle.animationDuration,
            maskMode: root.dataset.themeRevealMask, oldMask: oldStyle.maskImage, newMask: newStyle.maskImage,
          })
          if (window.pauseNextReveal) {
            window.pauseNextReveal = false
            const pseudo = record.direction === 'expand' ? '::view-transition-new(root)' : '::view-transition-old(root)'
            window.revealAnimation = document.getAnimations().find(animation => animation.effect?.pseudoElement === pseudo)
            window.revealAnimation.pause()
            window.revealAnimation.currentTime = 0
            record.paused = true
          }
        }).catch(() => {})
        return transition
      }
      createApp({ setup: () => () => h('main', { style: 'padding:80px;min-height:100vh' }, [h(ThemeSwitcher), h('p', { style: 'margin-top:24px' }, '主题切换测试')]) }).mount('#fixture')
    })
    const toggle = page.locator('.theme-toggle'), system = page.getByRole('button', { name: '跟随系统', exact: true })
    const settled = () => page.waitForFunction(() => !document.documentElement.classList.contains('theme-reveal'))
    const latest = () => page.evaluate(() => records.at(-1))

    assert.equal(await system.getAttribute('aria-pressed'), 'true')
    assert.equal(await toggle.getAttribute('aria-label'), '切换为深色')
    const box = await toggle.boundingBox()
    await toggle.click({ position: { x: 8, y: 9 } })
    await page.waitForFunction(() => records.at(-1)?.ready)
    const expand = await latest()
    assert.equal(expand.direction, 'expand')
    assert(+expand.newZ > +expand.oldZ)
    assert.equal(expand.oldAnimation, 'none')
    assert.equal(expand.newDuration, '0.4s')
    assert.equal(expand.maskMode, 'soft')
    assert.equal(expand.oldMask, 'none')
    assert(expand.newMask.includes('60%') && expand.newMask.includes('100%'))
    const [x, y] = expand.origin.split(' ').map(parseFloat)
    assert(Math.abs(x - box.x - 8) < 1 && Math.abs(y - box.y - 9) < 1, '鼠标点击偏离按钮中心时，圆心应跟随实际点击位置')
    assert(expand.radius * 0.6 >= Math.hypot(Math.max(x, 1000 - x), Math.max(y, 700 - y)))
    await settled()
    assert.equal(await page.evaluate(() => localStorage.getItem('cinema115-theme')), 'dark')
    const darkPalette = await page.evaluate(() => {
      const style = getComputedStyle(document.documentElement)
      return { background: style.backgroundColor, expected: `rgb(${style.getPropertyValue('--color-slate-950').trim().split(/\s+/).join(', ')})` }
    })
    assert.equal(darkPalette.background, darkPalette.expected, '按钮样式不能覆盖页面根背景')
    assert.equal(await system.getAttribute('aria-pressed'), 'false')
    passed.push('浅转深从鼠标实际点击位置展开比例软边，末帧实色覆盖四角')

    await toggle.focus()
    const center = await toggle.boundingBox()
    await toggle.press('Enter')
    await page.waitForFunction(() => records.at(-1)?.ready && records.at(-1).direction === 'contract')
    const contract = await latest()
    assert(+contract.oldZ > +contract.newZ)
    assert.equal(contract.newAnimation, 'none')
    assert.equal(contract.oldDuration, '0.4s')
    assert.equal(contract.newMask, 'none')
    assert(contract.oldMask.includes('60%') && contract.oldMask.includes('100%'))
    const [cx, cy] = contract.origin.split(' ').map(parseFloat)
    assert(Math.abs(cx - center.x - center.width / 2) < 1 && Math.abs(cy - center.y - center.height / 2) < 1)
    await settled()
    assert.equal(await page.evaluate(() => document.documentElement.classList.contains('dark')), false)
    assert(await toggle.evaluate(element => element === document.activeElement))
    passed.push('深转浅收缩旧深色快照，键盘使用按钮中心并保留焦点')

    for (const offset of [{ x: 5, y: 6 }, { x: 34, y: 33 }]) {
      const bounds = await toggle.boundingBox()
      const count = await page.evaluate(() => records.length)
      const point = { x: bounds.x + offset.x, y: bounds.y + offset.y }
      await page.touchscreen.tap(point.x, point.y)
      await page.waitForFunction(count => records.length > count && records.at(-1)?.ready, count)
      const [x, y] = (await latest()).origin.split(' ').map(parseFloat)
      assert(Math.abs(x - point.x) < 1 && Math.abs(y - point.y) < 1, '触摸按钮不同位置时圆心应跟随手指')
      await settled()
    }
    passed.push('真实触摸按钮不同位置，双向动画均从触摸坐标开始')

    await page.evaluate(() => theme.setTheme('dark'))
    const systemBounds = await system.boundingBox()
    const systemCount = await page.evaluate(() => records.length)
    await system.click({ position: { x: 4, y: 5 } })
    await page.waitForFunction(count => records.length > count && records.at(-1)?.ready, systemCount)
    const [sx, sy] = (await latest()).origin.split(' ').map(parseFloat)
    assert(Math.abs(sx - systemBounds.x - 4) < 1 && Math.abs(sy - systemBounds.y - 5) < 1, '跟随系统按钮也应使用实际点击坐标')
    await settled()
    passed.push('跟随系统入口同样使用真实点击位置')

    await system.click()
    assert.equal(await system.getAttribute('aria-pressed'), 'true')
    const count = await page.evaluate(() => records.length)
    await page.emulateMedia({ colorScheme: 'dark' })
    await page.waitForFunction(() => document.documentElement.classList.contains('dark'))
    assert.equal(await toggle.getAttribute('aria-label'), '切换为浅色')
    assert.equal(await page.evaluate(() => records.length), count)
    await toggle.click()
    await settled()
    await page.emulateMedia({ colorScheme: 'light' })
    await page.emulateMedia({ colorScheme: 'dark' })
    assert.equal(await page.evaluate(() => document.documentElement.classList.contains('dark')), false)
    passed.push('跟随系统即时同步图标，手动选择后不再跟随')

    await page.evaluate(async () => {
      const first = theme.setTheme('dark', { x: 20, y: 20 })
      await new Promise(resolve => requestAnimationFrame(resolve))
      await Promise.all([first, theme.setTheme('light'), theme.setTheme('dark'), theme.setTheme('light')])
    })
    assert.equal(await page.evaluate(() => theme.resolvedTheme.value), 'light')
    assert.equal(await page.evaluate(() => document.documentElement.classList.contains('theme-reveal')), false)
    assert.equal(await page.evaluate(() => document.documentElement.dataset.themeReveal), undefined)
    assert.equal(await page.evaluate(() => document.documentElement.dataset.themeRevealMask), undefined)
    assert.equal(await page.locator('[style*="view-transition-name: theme-reveal-anchor"]').count(), 0)
    await page.evaluate(async () => {
      let release
      const waiting = new Promise(resolve => { release = resolve })
      const old = theme.setTheme('dark', undefined, () => waiting)
      await theme.setTheme('light')
      release()
      await old
    })
    assert.equal(await page.evaluate(() => theme.resolvedTheme.value), 'light')
    passed.push('连续请求与延迟关闭菜单采用最后一次选择，转场标记完整清理')

    // 暂停真实快照动画，检查渐变像素与四角，避免只验证样式字符串。
    const lightRGB = await page.evaluate(() => getComputedStyle(document.body).backgroundColor.match(/\d+/g).map(Number))
    const darkRGB = darkPalette.background.match(/\d+/g).map(Number)
    const samples = [{ x: 258, y: 98 }, { x: 418, y: 98 }, { x: 578, y: 98 },
      { x: 5, y: 5 }, { x: 995, y: 5 }, { x: 5, y: 695 }, { x: 995, y: 695 },
      { x: 698, y: 298 }, { x: 698, y: 220 }]
    const pixels = async () => {
      const png = (await page.screenshot()).toString('base64')
      return page.evaluate(async ({ png, samples }) => {
        const img = new Image()
        img.src = `data:image/png;base64,${png}`
        await img.decode()
        const canvas = document.createElement('canvas')
        canvas.width = img.width; canvas.height = img.height
        const ctx = canvas.getContext('2d')
        ctx.drawImage(img, 0, 0)
        return samples.map(({ x, y }) => [...ctx.getImageData(x, y, 1, 1).data].slice(0, 3))
      }, { png, samples })
    }
    const near = (actual, expected) => actual.forEach((channel, i) => assert(Math.abs(channel - expected[i]) <= 4, `像素 ${actual} 应接近 ${expected}`))
    for (const target of ['dark', 'light']) {
      await page.evaluate(target => {
        window.pauseNextReveal = true
        window.pendingReveal = theme.setTheme(target, { x: 98, y: 98 })
      }, target)
      await page.waitForFunction(() => records.at(-1)?.paused)
      const start = await pixels()
      start.forEach(rgb => near(rgb, target === 'dark' ? lightRGB : darkRGB))
      await page.evaluate(() => {
        const root = document.documentElement
        const pseudo = root.dataset.themeReveal === 'expand' ? '::view-transition-new(root)' : '::view-transition-old(root)'
        let low = 0, high = 400
        for (let i = 0; i < 18; i++) {
          const time = (low + high) / 2
          revealAnimation.currentTime = time
          const radius = parseFloat(getComputedStyle(root, pseudo).getPropertyValue('--theme-reveal-mask-radius'))
          if ((radius < 400) === (root.dataset.themeReveal === 'expand')) low = time
          else high = time
        }
        revealAnimation.currentTime = (low + high) / 2
      })
      const middle = await pixels()
      near(middle[0], darkRGB)
      near(middle[1], lightRGB.map((channel, i) => (channel + darkRGB[i]) / 2))
      near(middle[2], lightRGB)
      await page.screenshot({ path: path.join(__dirname, `../test-results/theme-soft-${target}-mid.png`) })
      if (target === 'light') {
        // 模拟真实结束帧的浮点误差，旧深色快照必须继续保持透明。
        const mask = await page.evaluate(() => {
          window.savedRevealFrames = revealAnimation.effect.getKeyframes()
          const frame = { '--theme-reveal-mask-radius': '-0.000000001px' }
          revealAnimation.effect.setKeyframes([frame, frame])
          revealAnimation.currentTime = 200
          return getComputedStyle(document.documentElement, '::view-transition-old(root)').maskImage
        })
        assert.notEqual(mask, 'none', '微小负半径不能让遮罩失效')
        const tinyRadius = await pixels()
        tinyRadius.forEach(rgb => near(rgb, lightRGB))
        await page.evaluate(() => { revealAnimation.effect.setKeyframes(savedRevealFrames) })
      }
      await page.evaluate(() => { revealAnimation.currentTime = 399.99 })
      const end = await pixels()
      end.forEach(rgb => near(rgb, target === 'dark' ? darkRGB : lightRGB))
      await page.evaluate(async () => { revealAnimation.play(); await pendingReveal })
    }
    passed.push('双向渐变按比例混合，末帧微小负半径不闪黑，四角完整覆盖')

    // 模拟快照平面因浏览器栏而相对视口偏移；直接验证圆心像素，不能只检查鼠标坐标。
    await page.addStyleTag({ content: 'html.theme-reveal::view-transition-group(root) { transform: translate(-12px, -56px) !important; width: 1024px !important; height: 812px !important; }' }).then(handle => page.evaluate(element => { element.id = 'snapshot-offset-test' }, handle))
    for (const target of ['dark', 'light']) {
      await page.evaluate(target => {
        window.pauseNextReveal = true
        window.pendingReveal = theme.setTheme(target, { x: 698, y: 298 })
      }, target)
      await page.waitForFunction(() => records.at(-1)?.paused)
      const geometry = await page.evaluate(() => {
        const root = document.documentElement
        const point = root.style.getPropertyValue('--theme-reveal-origin').split(' ').map(parseFloat)
        const radius = parseFloat(root.style.getPropertyValue('--theme-reveal-radius'))
        const frame = { '--theme-reveal-mask-radius': '60px' }
        revealAnimation.effect.setKeyframes([frame, frame])
        revealAnimation.currentTime = 200
        return { point, radius }
      })
      assert.deepEqual(geometry.point, [710, 354], '圆心必须转换到真实快照局部坐标')
      assert(geometry.radius * 0.6 >= Math.hypot(710, 812 - 354), '半径必须覆盖实际快照尺寸')
      const corrected = await pixels()
      near(corrected[7], darkRGB)
      near(corrected[8], lightRGB)
      await page.evaluate(async () => { latestTransition.skipTransition(); await pendingReveal })
      assert.equal(await page.locator('[style*="view-transition-name: theme-reveal-anchor"]').count(), 0)
    }
    await page.evaluate(() => document.getElementById('snapshot-offset-test').remove())
    passed.push('浏览器栏引起的快照偏移下双向圆心仍对齐按钮，半径按快照尺寸计算，跳过后锚点清理')

    await page.evaluate(async () => {
      const start = document.startViewTransition
      document.startViewTransition = callback => {
        const transition = start(callback)
        transition.skipTransition()
        return transition
      }
      try { await theme.setTheme('dark') }
      finally { document.startViewTransition = start }
    })
    assert.equal(await page.evaluate(() => theme.resolvedTheme.value), 'dark')
    assert.equal(await page.locator('[style*="view-transition-name: theme-reveal-anchor"]').count(), 0)
    assert.equal(await page.evaluate(() => document.documentElement.classList.contains('theme-reveal')), false)
    await page.evaluate(() => theme.setTheme('light'))
    passed.push('ready 拒绝时仍完成主题更新并清理临时锚点')

    const beforeFallback = await page.evaluate(() => records.length)
    await page.evaluate(async () => {
      const supports = CSS.supports.bind(CSS)
      CSS.supports = (...args) => args[0] === 'mask-image' ? false : supports(...args)
      await theme.setTheme('dark')
      CSS.supports = supports
    })
    const fallback = await latest()
    assert.equal(await page.evaluate(() => records.length), beforeFallback + 1)
    assert.equal(fallback.maskMode, 'clip')
    assert.equal(fallback.newMask, 'none')
    await page.evaluate(() => theme.setTheme('light'))
    passed.push('缺少渐变遮罩支持时保持圆形裁剪降级')

    await page.emulateMedia({ reducedMotion: 'reduce' })
    const beforeReduce = await page.evaluate(() => records.length)
    await toggle.click()
    await page.waitForFunction(() => theme.resolvedTheme.value === 'dark')
    assert.equal(await page.evaluate(() => theme.resolvedTheme.value), 'dark')
    assert.equal(await page.evaluate(() => records.length), beforeReduce)
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await page.evaluate(() => { document.startViewTransition = undefined })
    await toggle.click()
    await page.waitForFunction(() => theme.resolvedTheme.value === 'light')
    assert.equal(await page.evaluate(() => theme.resolvedTheme.value), 'light')
    assert.equal(await page.evaluate(() => records.length), beforeReduce)
    const metas = await page.locator('meta[name="theme-color"]').evaluateAll(elements => elements.map(element => element.content))
    assert(metas.every(value => value === metas[0] && value.startsWith('rgb(')))
    passed.push('减少动态效果及无 API 时直接切换，浏览器主题色同步')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }, null, 2))
  } finally {
    await browser.close()
  }
})().catch(error => { console.error(error); process.exitCode = 1 })
