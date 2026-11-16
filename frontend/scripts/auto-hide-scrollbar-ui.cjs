// 独立空白页面只加载真实滚动条模块和样式，不加载应用、认证或业务接口。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

const origin = new URL(process.env.CINEMA_URL || 'http://127.0.0.1:5173').origin
const fixturePath = '/__auto-hide-scrollbar-test'
const passed = [], errors = [], blocked = []
let phase = '初始化'

const fixture = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>滚动条回归</title>
<style>
  :root { color-scheme: light; scrollbar-gutter: stable; }
  :root.dark { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; background: #f8fafc; color: #0f172a; font: 14px/1.5 sans-serif; }
  .dark body { background: #18191c; color: #e2e8f0; }
  main { position: relative; min-height: 2400px; padding: 20px; }
  #before { position: absolute; left: 40px; top: 20px; }
  .box { position: absolute; overflow: auto; border: 1px solid #94a3b8; background: rgb(148 163 184 / .08); overscroll-behavior: contain; }
  .tall { height: 1300px; padding: 16px; background: repeating-linear-gradient(transparent 0 39px, rgb(100 116 139 / .15) 39px 40px); }
  #vertical { left: 40px; top: 70px; width: 360px; height: 180px; }
  #outer { left: 460px; top: 70px; width: 420px; height: 260px; }
  #inner { position: relative; margin: 20px; width: 240px; height: 120px; }
  #outer-tail { height: 950px; padding: 16px; }
  #horizontal { left: 40px; top: 290px; width: 360px; height: 90px; overflow-y: hidden; }
  #horizontal > div { width: 1600px; height: 50px; padding: 12px; white-space: nowrap; }
  #textarea { left: 40px; top: 430px; width: 330px; height: 130px; resize: none; }
  #select { left: 400px; top: 430px; width: 190px; height: 130px; }
  #cm { position: absolute; left: 620px; top: 430px; width: 240px; }
  #cm-scroll { position: relative; height: 130px; width: 240px; }
  #monaco { position: absolute; left: 900px; top: 430px; width: 240px; }
  #monaco-scroll { position: relative; height: 130px; width: 240px; }
  #hidden-tabs { left: 40px; top: 610px; width: 360px; height: 60px; scrollbar-width: none; overflow-y: hidden; }
  #hidden-tabs::-webkit-scrollbar { display: none; }
  #hidden-tabs > div { width: 1200px; padding: 12px; }
  #dynamic-host { position: absolute; left: 460px; top: 610px; }
</style></head><body><main>
<button id="before">键盘起点</button>
<div id="vertical" class="box" data-auto-scrollbar tabindex="0" aria-label="普通纵向列表"><div class="tall">普通列表</div></div>
<div id="outer" class="box" data-auto-scrollbar tabindex="0" aria-label="父滚动区">
  <div id="inner" class="box" data-auto-scrollbar tabindex="0" aria-label="嵌套滚动区"><div class="tall">嵌套列表</div></div><div id="outer-tail">父区剩余内容</div>
</div>
<div id="horizontal" class="box" data-auto-scrollbar tabindex="0" aria-label="横向列表"><div>可以横向滚动的内容</div></div>
<textarea id="textarea" class="box" data-auto-scrollbar aria-label="误标记多行输入框"></textarea>
<select id="select" class="box" data-auto-scrollbar size="5" aria-label="误标记原生选择器"></select>
<div id="cm" class="cm-editor" data-auto-scrollbar><div id="cm-scroll" class="cm-scroller box" data-auto-scrollbar><div class="cm-content tall" contenteditable="true" aria-label="误标记代码编辑器">编辑器内容</div></div></div>
<div id="monaco" class="monaco-editor" data-auto-scrollbar data-scrollbar="native"><div id="monaco-scroll" class="monaco-scrollable-element box" data-auto-scrollbar><div class="tall" contenteditable="true" aria-label="显式豁免原生编辑区">显式原生编辑区</div></div></div>
<div id="hidden-tabs" class="box" aria-label="刻意隐藏滚动条的标签页"><div>保留原样的标签条</div></div>
<div id="dynamic-host"></div>
</main></body></html>`

async function mount(browser, options = {}) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block', ...options })
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === origin && url.pathname === fixturePath && request.isNavigationRequest()) {
      return route.fulfill({ contentType: 'text/html; charset=utf-8', body: fixture })
    }
    // 白名单只放行 Vite 源码和依赖，意外业务或外部请求一律拦截并使回归失败。
    const source = /^\/(?:src\/|node_modules\/|@vite\/|@id\/|@fs\/)/.test(url.pathname)
    if (url.origin === origin && source && request.method() === 'GET') {
      return route.continue()
    }
    blocked.push({ method: request.method(), path: url.pathname })
    return route.abort('blockedbyclient')
  })
  const page = await context.newPage()
  page.setDefaultTimeout(6000)
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(origin + fixturePath)
  await page.mouse.move(12, 12)
  await page.evaluate(async () => {
    document.querySelector('#textarea').value = Array.from({ length: 80 }, (_, i) => `输入行 ${i + 1}`).join('\n')
    document.querySelector('#select').innerHTML = Array.from({ length: 50 }, (_, i) => `<option value="${i}">选项 ${i + 1}</option>`).join('')
    const canvas = document.createElement('canvas'), ctx = canvas.getContext('2d')
    canvas.width = canvas.height = 1
    const alpha = color => {
      ctx.clearRect(0, 0, 1, 1)
      ctx.fillStyle = 'transparent'; ctx.fillStyle = color
      ctx.fillRect(0, 0, 1, 1)
      return ctx.getImageData(0, 0, 1, 1).data[3] / 255
    }
    const firstColor = pair => {
      let depth = 0
      for (let i = 0; i < pair.length; i++) {
        if (pair[i] === '(') depth++
        else if (pair[i] === ')') depth--
        else if (/\s/.test(pair[i]) && depth === 0) return pair.slice(0, i)
      }
      return pair
    }
    const read = selector => {
      const el = document.querySelector(selector), style = getComputedStyle(el)
      const bar = getComputedStyle(el, '::-webkit-scrollbar'), thumb = getComputedStyle(el, '::-webkit-scrollbar-thumb')
      const rect = el.getBoundingClientRect()
      // Chrome 中非 auto 的标准属性优先于 WebKit 伪元素，不能只检查伪元素颜色。
      const native = style.scrollbarColor === 'auto' && bar.width === 'auto' && bar.display !== 'none'
      const thumbAlpha = alpha(style.scrollbarColor === 'auto' ? thumb.backgroundColor : firstColor(style.scrollbarColor))
      return {
        state: el.getAttribute('data-scrollbar-state'),
        visible: style.scrollbarWidth !== 'none' && bar.display !== 'none' && (native || thumbAlpha > .01),
        native, thumbColor: thumb.backgroundColor,
        scrollbarWidth: style.scrollbarWidth, scrollbarColor: style.scrollbarColor,
        barWidth: bar.width, barHeight: bar.height, barDisplay: bar.display,
        gutterY: el.offsetWidth - el.clientWidth - parseFloat(style.borderLeftWidth || 0) - parseFloat(style.borderRightWidth || 0),
        scrollTop: el.scrollTop, scrollLeft: el.scrollLeft,
        rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      }
    }
    window.scrollbarTest = { read }
    // 加载真实主题变量，避免夹具补造颜色掩盖实际主题或辅助功能问题。
    await import('/src/style.css')
    const exempt = ['#textarea', '#select', '#cm', '#cm-scroll', '#monaco', '#monaco-scroll']
    exempt.forEach(selector => document.querySelector(selector).removeAttribute('data-auto-scrollbar'))
    await import('/src/styles/scrollbars.css')
    window.scrollbarTest.exemptBaseline = Object.fromEntries(['#textarea', '#select', '#cm-scroll', '#monaco-scroll', '#hidden-tabs'].map(selector => [selector, read(selector)]))
    exempt.forEach(selector => document.querySelector(selector).setAttribute('data-auto-scrollbar', ''))
    const { installAutoHideScrollbars } = await import('/src/lib/auto-hide-scrollbar.ts')
    window.scrollbarTest.install = installAutoHideScrollbars
    window.scrollbarTest.dispose = installAutoHideScrollbars()
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
  })
  return { context, page }
}

const read = (page, selector) => page.evaluate(selector => window.scrollbarTest.read(selector), selector)
const state = (page, selector, expected, timeout = 1800) => page.waitForFunction(({ selector, expected }) => document.querySelector(selector).getAttribute('data-scrollbar-state') === expected, { selector, expected }, { timeout })
const visibility = (page, selector, expected, timeout = 1800) => page.waitForFunction(({ selector, expected }) => window.scrollbarTest.read(selector).visible === expected, { selector, expected }, { timeout })
const idle = async (page, selector, timeout = 1800) => { await state(page, selector, null, timeout); await visibility(page, selector, false, timeout) }
const park = page => page.mouse.move(12, 12)
const appearance = value => ({ scrollbarWidth: value.scrollbarWidth, scrollbarColor: value.scrollbarColor, barWidth: value.barWidth, barHeight: value.barHeight, barDisplay: value.barDisplay, thumbColor: value.thumbColor })
async function moveIn(page, selector, x = .4, y = .4) {
  const { rect } = await read(page, selector)
  await page.mouse.move(rect.x + rect.width * x, rect.y + rect.height * y)
}
async function wheel(page, selector, deltaX, deltaY) {
  const before = await read(page, selector)
  await moveIn(page, selector)
  await page.mouse.wheel(deltaX, deltaY)
  await page.waitForFunction(({ selector, before, horizontal }) => {
    const el = document.querySelector(selector)
    return horizontal ? el.scrollLeft !== before.scrollLeft : el.scrollTop !== before.scrollTop
  }, { selector, before, horizontal: deltaX !== 0 })
  await state(page, selector, 'active')
}
async function noState(page, selectors) {
  const states = await page.evaluate(selectors => selectors.map(selector => ({ selector, value: document.querySelector(selector).getAttribute('data-scrollbar-state') })), selectors)
  assert(states.every(value => value.value === null), `不相关容器不得被激活：${JSON.stringify(states)}`)
}
async function beginDrag(page) {
  await page.locator('#vertical').evaluate(el => { el.scrollTop = 0 })
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  await park(page); await idle(page, '#vertical')
  const before = await read(page, '#vertical')
  assert(before.gutterY > 0, `原生拖动需要实际槽位：${JSON.stringify(before)}`)
  const x = before.rect.x + before.rect.width - before.gutterY / 2 - 1
  const y = before.rect.y + 9
  await page.mouse.move(x, y)
  await state(page, '#vertical', 'edge')
  await page.mouse.down()
  await state(page, '#vertical', 'dragging')
  return { x, y }
}

;(async () => {
  // Playwright 无头模式默认隐藏原生滚动条；排除此参数才能验证真实拖动。
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true, ignoreDefaultArgs: ['--hide-scrollbars'] })
  try {
    const { context, page } = await mount(browser)
    phase = '桌面默认隐藏'
    assert(await page.evaluate(() => matchMedia('(hover: hover) and (pointer: fine)').matches), '桌面上下文必须是真实精细指针环境')
    for (const selector of ['html', '#vertical', '#outer', '#inner', '#horizontal']) {
      await idle(page, selector)
    }
    passed.push('桌面根页面及独立容器初始隐藏')

    phase = '滚轮激活、刷新计时和内容区静止隐藏'
    await wheel(page, '#vertical', 0, 180)
    await visibility(page, '#vertical', true)
    await page.waitForTimeout(550)
    await wheel(page, '#vertical', 0, 140)
    await page.waitForTimeout(650)
    assert.equal((await read(page, '#vertical')).state, 'active', '继续滚动必须重置计时，旧计时不能提前隐藏')
    await idle(page, '#vertical', 1000)
    await moveIn(page, '#vertical', .5, .5)
    await page.waitForTimeout(1000)
    assert.equal((await read(page, '#vertical')).visible, false, '鼠标停在内容区不能让滑块常亮')
    passed.push('真实滚轮激活并刷新隐藏计时，静止内容悬停不常亮')

    phase = '边缘唤出和离开隐藏'
    let { rect } = await read(page, '#vertical')
    await page.mouse.move(rect.x + rect.width - 10, rect.y + 90)
    await state(page, '#vertical', 'edge'); await visibility(page, '#vertical', true)
    await page.waitForTimeout(550)
    assert.equal((await read(page, '#vertical')).state, 'edge', '指针仍停在边缘时应保持可发现')
    await moveIn(page, '#vertical', .5, .5)
    await page.waitForTimeout(180)
    assert.equal((await read(page, '#vertical')).state, 'edge', '离开边缘后短暂保留拖动容错')
    await idle(page, '#vertical', 900)
    passed.push('边缘唤出、停留保持，离开后延迟隐藏，内容区不误触')

    phase = '原生滑块拖动、跨出容器与释放'
    try {
      const drag = await beginDrag(page)
      await page.mouse.move(drag.x, drag.y + 52, { steps: 6 })
      await page.waitForFunction(() => document.querySelector('#vertical').scrollTop > 0)
      await park(page)
      await page.waitForTimeout(1100)
      assert.equal((await read(page, '#vertical')).state, 'dragging', '按住并跨出容器不能被 idle 计时清除')
      assert.equal((await read(page, '#vertical')).visible, true)
    } finally { await page.mouse.up() }
    await idle(page, '#vertical')
    passed.push('真实原生滑块可拖动，按住跨区超过 idle 仍显示，释放恢复隐藏')

    phase = '失焦清理拖动锁'
    try {
      await beginDrag(page)
      // 主动发送生命周期事件，避免依赖 headless 多窗口焦点实现差异。
      await page.evaluate(() => window.dispatchEvent(new Event('blur')))
      await idle(page, '#vertical', 700)
    } finally { await page.mouse.up() }
    await park(page)
    passed.push('窗口 blur 清理未收到 pointerup 的拖动锁，不遗留常亮')

    phase = '键盘焦点与操作提示'
    await page.locator('#before').click()
    await page.keyboard.press('Tab')
    assert.equal(await page.evaluate(() => document.activeElement.id), 'vertical', 'Tab 必须真实进入滚动区')
    await visibility(page, '#vertical', true)
    await page.waitForTimeout(1350)
    assert.equal((await read(page, '#vertical')).visible, true, '持续键盘焦点不能因计时到期失去滚动指示')
    await page.keyboard.press('ArrowLeft')
    await state(page, '#vertical', 'key')
    await page.evaluate(() => document.activeElement.blur())
    await park(page)
    await idle(page, '#vertical')
    passed.push('真实 Tab 焦点保持指示，键盘操作提示在失焦后正常归隐')

    phase = '嵌套滚动只激活实际目标'
    const outerBefore = await read(page, '#outer')
    await wheel(page, '#inner', 0, 100)
    await visibility(page, '#inner', true)
    await noState(page, ['#outer', 'html', 'body'])
    assert.equal((await read(page, '#outer')).scrollTop, outerBefore.scrollTop, '内层滚动不能带动父层')
    await park(page); await idle(page, '#inner')
    await page.mouse.move(outerBefore.rect.x + 330, outerBefore.rect.y + 210)
    await page.mouse.wheel(0, 120)
    await state(page, '#outer', 'active')
    assert((await read(page, '#outer')).scrollTop > outerBefore.scrollTop)
    await noState(page, ['#inner', 'html', 'body'])
    await park(page); await idle(page, '#outer')
    passed.push('嵌套和父区的真实滚轮各自激活实际容器，html/body 不跟亮')

    phase = 'document 根滚动事件映射 html'
    await page.mouse.move(1180, 800)
    await page.mouse.wheel(0, 250)
    await state(page, 'html', 'active')
    await visibility(page, 'html', true)
    assert(await page.evaluate(() => scrollY > 0))
    await noState(page, ['body', '#outer', '#inner', '#vertical'])
    await park(page); await idle(page, 'html')
    await page.evaluate(() => window.scrollTo(0, 0))
    await page.waitForFunction(() => scrollY === 0)
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    await idle(page, 'html')
    passed.push('真实 document scroll target 正确映射 html，body 和内部容器不跟亮')

    phase = '横向滚动与底边发现'
    const horizontalBefore = await read(page, '#horizontal')
    await wheel(page, '#horizontal', 180, 0)
    await visibility(page, '#horizontal', true)
    await park(page); await idle(page, '#horizontal')
    rect = horizontalBefore.rect
    await page.mouse.move(rect.x + rect.width / 2, rect.y + rect.height - 10)
    await state(page, '#horizontal', 'edge'); await visibility(page, '#horizontal', true)
    await park(page); await idle(page, '#horizontal')
    passed.push('横向滚动激活、底边唤出及离开隐藏')

    phase = '误标记的输入控件和编辑器豁免'
    for (const selector of ['#textarea', '#select', '#cm-scroll', '#monaco-scroll']) {
      const baseline = await page.evaluate(selector => window.scrollbarTest.exemptBaseline[selector], selector)
      assert.deepEqual(appearance(await read(page, selector)), appearance(baseline), `${selector} 误标记也必须保留原生或编辑器滚动条样式`)
      await page.locator(selector).evaluate(el => { el.scrollTop = 70 })
      await page.waitForFunction(selector => document.querySelector(selector).scrollTop > 0, selector)
      await moveIn(page, selector, .98, .5)
      await page.waitForTimeout(100)
      await noState(page, [selector, '#cm', '#monaco', 'html', 'body'])
    }
    await page.locator('#textarea').press('End')
    await noState(page, ['#textarea', 'html', 'body'])
    await page.evaluate(() => document.activeElement.blur())
    await park(page)
    const hiddenBefore = await page.evaluate(() => window.scrollbarTest.exemptBaseline['#hidden-tabs'])
    await page.locator('#hidden-tabs').evaluate(el => { el.scrollLeft = 150 })
    await page.waitForTimeout(100)
    const hiddenAfter = await read(page, '#hidden-tabs')
    assert.equal(hiddenAfter.state, null)
    assert.equal(hiddenAfter.visible, false)
    assert.deepEqual(appearance(hiddenAfter), appearance(hiddenBefore), '未标记的隐藏标签条不能被全局规则改写')
    passed.push('textarea/select/CodeMirror/显式原生编辑区误标记仍豁免；未标记隐藏横向条保持原样')

    phase = '安装后动态新增容器'
    await page.locator('#dynamic-host').evaluate(host => {
      host.innerHTML = '<div id="dynamic" class="box" data-auto-scrollbar style="position:relative;width:260px;height:100px"><div class="tall">动态列表</div></div>'
    })
    await wheel(page, '#dynamic', 0, 80)
    await visibility(page, '#dynamic', true)
    await park(page); await idle(page, '#dynamic')
    passed.push('安装后新增的显式标记容器无需重新安装即可工作')

    phase = '深色主题透明与活动状态'
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await idle(page, '#vertical')
    await wheel(page, '#vertical', 0, 100)
    await visibility(page, '#vertical', true)
    await park(page); await idle(page, '#vertical')
    passed.push('深色主题静止隐藏、滚动可见')

    phase = '高对比度与强制颜色降级'
    for (const mode of [{ contrast: 'more', forcedColors: 'none' }, { contrast: 'no-preference', forcedColors: 'active' }]) {
      await page.emulateMedia(mode)
      assert(await page.evaluate(mode => matchMedia(mode.forcedColors === 'active' ? '(forced-colors: active)' : '(prefers-contrast: more)').matches, mode), '必须真实启用所测媒体条件')
      await visibility(page, '#vertical', true)
      await page.waitForTimeout(1400)
      assert.equal((await read(page, '#vertical')).visible, true, '辅助功能模式不能因 idle 隐藏')
    }
    await page.emulateMedia({ contrast: 'no-preference', forcedColors: 'none' })
    await idle(page, '#vertical')
    passed.push('高对比度及强制颜色保持可见，退出后恢复隐藏')

    phase = '清理函数移除监听、状态和拖动锁'
    await wheel(page, '#vertical', 0, 80)
    await park(page)
    await page.evaluate(() => { window.scrollbarTest.dispose(); window.scrollbarTest.dispose() })
    await noState(page, ['html', 'body', '#vertical', '#outer', '#inner', '#horizontal', '#dynamic'])
    await page.locator('#vertical').evaluate(el => { el.scrollTop += 40 })
    await moveIn(page, '#vertical', .98, .5)
    await page.waitForTimeout(150)
    await noState(page, ['html', 'body', '#vertical'])
    await page.evaluate(() => { window.scrollbarTest.dispose = window.scrollbarTest.install() })
    await wheel(page, '#vertical', 0, 80)
    await park(page); await idle(page, '#vertical')
    passed.push('重复清理安全，清理后不再响应事件，重新安装可恢复')
    await context.close()

    phase = '触屏 coarse 指针使用系统滚动条'
    const touch = await mount(browser, { hasTouch: true, isMobile: true })
    assert(await touch.page.evaluate(() => matchMedia('(pointer: coarse)').matches && matchMedia('(hover: none)').matches), '独立触屏上下文必须实际匹配 coarse/无悬停')
    let touchValue = await read(touch.page, '#vertical')
    assert.equal(touchValue.native, true, `触屏静止时应保留系统绘制：${JSON.stringify(touchValue)}`)
    assert.notEqual(touchValue.scrollbarWidth, 'none')
    await touch.page.locator('#vertical').evaluate(el => { el.scrollTop = 100 })
    await touch.page.waitForTimeout(1400)
    touchValue = await read(touch.page, '#vertical')
    assert.equal(touchValue.native, true, '触屏滚动后不应套用透明细滑块')
    await touch.page.evaluate(() => window.scrollbarTest.dispose())
    await touch.context.close()
    passed.push('真实 coarse/无悬停媒体环境保留系统滚动条，idle 不改成透明细条')

    assert.deepEqual(blocked, [], '夹具不应产生任何业务接口、外部请求或写请求')
    assert.deepEqual(errors, [], '浏览器不能有未捕获异常')
    console.log(JSON.stringify({ passed, businessRequests: blocked, errors }, null, 2))
  } catch (error) {
    console.error(`失败阶段：${phase}`)
    throw error
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
