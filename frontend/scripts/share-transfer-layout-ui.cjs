// 所有接口均模拟，验证转存、分享 STRM 与通知期间的布局，不访问真实网盘。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 1336, height: 800 }, serviceWorkers: 'block' })
    const compiled = await (await page.request.get(origin + '/src/components/ShareTransferDirectory.vue')).text()
    const vueURL = compiled.match(/from "([^"]*\/vue\.js\?[^"]+)"/)[1]
    const routerURL = compiled.match(/from "([^"]*\/vue-router\.js\?[^"]+)"/)[1]
    page.setDefaultTimeout(10000)
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    const source = { text: '测试资源', items: [{ id: 'fixture', kind: 'resource', title: '测试作品 (2022) 3.73G', size: '3.73G' }], links: [], remaining: 917, empty: false, allLoaded: true, cached: true }
    const target = { id: 'one', name: '动画', accountId: 'main', targetCID: '88', targetPath: '/资源/动画' }
    let resolveLinks, resolveTransfer, linkCalls = 0, writes = 0
    let resolveSTRM, strmRequested, strmWrites = 0, runID = '', finished = false, failed = false
    await page.route('**/api/**', async route => {
      const endpoint = new URL(route.request().url()).pathname
      if (endpoint.endsWith('/telegram-user/config')) return route.fulfill({ json: { botConfigured: true, hasSession: true, bot: 'fixture_bot' } })
      if (endpoint.endsWith('/telegram-user/search-stream')) return route.fulfill({ contentType: 'text/event-stream', body: `event: result\ndata: ${JSON.stringify({ complete: true, result: source })}\n\n` })
      if (endpoint.endsWith('/share-transfer/targets')) return route.fulfill({ json: { accountId: 'main', profiles: [target] } })
      if (endpoint.endsWith('/share/automation-config')) return route.fulfill({ json: { settings: { accountId: 'main', generation: { outputDir: '/strm', baseUrl: 'http://fixture' } } } })
      if (endpoint.includes('/share/generation-runs')) {
        if (route.request().method() === 'POST') {
          strmWrites++; finished = false; runID = route.request().postDataJSON().id
          await new Promise(resolve => { resolveSTRM = resolve; strmRequested() })
        }
        const status = finished ? failed ? 'failed' : 'success' : 'queued'
        return route.fulfill({ json: { id: runID, status, selectedTaskIds: [runID], items: [{ taskId: runID, status, error: finished && failed ? '模拟生成失败' : undefined, result: finished && !failed ? { count: 2, directory: '/strm' } : undefined }] } })
      }
      if (endpoint.endsWith('/telegram-user/select')) {
        linkCalls++
        await new Promise(resolve => { resolveLinks = resolve })
        return route.fulfill({ json: { ...source, links: ['https://115cdn.com/s/fixture?password=abcd'] } })
      }
      if (endpoint.endsWith('/share-transfer')) {
        writes++
        assert.deepEqual(route.request().postDataJSON().target, target)
        await new Promise(resolve => { resolveTransfer = resolve })
        return route.fulfill({ json: { receipt: { targetPath: target.targetPath, itemCount: 1, acceptedItems: 1 } } })
      }
      throw new Error(`未模拟接口：${endpoint}`)
    })
    await page.route('**/transfer-layout-fixture', route => route.fulfill({ contentType: 'text/html', body: `<!doctype html><html><body><div id="app"></div><script type="module">
      import { createApp, h, ref } from '${vueURL}';
      import { createRouter, createMemoryHistory } from '${routerURL}';
      import BotSearchDialog from '/src/components/BotSearchDialog.vue';
      import GlobalToast from '/src/components/GlobalToast.vue';
      import '/src/style.css'; import '/src/styles/floatingSurface.css'; import '/src/styles/mediaSearchDialog.css';
      const router = createRouter({ history: createMemoryHistory(), routes: [{path:'/', component:{render:()=>null}}] });
      createApp({setup(){ const open = ref(true); return ()=>[open.value && h(BotSearchDialog,{token:'mock',kind:'tv',tmdbId:'1',title:'测试作品',onClose:()=>{open.value=false}}),h(GlobalToast)] }}).use(router).mount('#app');
      </script></body></html>` }))
    const measure = () => page.evaluate(() => {
      const box = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { y: r.y, h: r.height, w: r.width } }
      return { dialog: box('.bot-dialog'), card: box('.bot-resource'), button: box('.resource-action-button'), strm: box('[data-bot-action="strm"]'), scroll: document.querySelector('.bot-body').scrollTop }
    })
    for (const width of [1336, 390]) {
      failed = width === 390
      await page.setViewportSize({ width, height: 800 })
      await page.goto(origin + '/transfer-layout-fixture')
      const transfer = page.locator('.resource-action-button')
      try { await transfer.waitFor() } catch (error) { console.error(errors, await page.locator('body').innerText()); throw error }
      await page.waitForFunction(() => !document.querySelector('.resource-action-button').disabled)
      const before = await measure()
      await transfer.click()
      await page.waitForFunction(() => document.querySelector('.resource-action-button')?.textContent.includes('获取链接'))
      const loading = await measure()
      console.log(JSON.stringify({ width, before, loading }))
      for (const item of ['dialog', 'card', 'button']) {
        assert.ok(Math.abs(loading[item].y - before[item].y) <= 1, `${width}px ${item} 取链时上下跳动`)
        assert.ok(Math.abs(loading[item].h - before[item].h) <= 1, `${width}px ${item} 取链时高度变化`)
      }
      resolveLinks()
      await page.waitForFunction(() => document.querySelector('.resource-action-button')?.textContent.includes('转存中'))
      const pending = await measure()
      resolveTransfer()
      await page.waitForFunction(() => document.querySelector('.resource-action-button')?.textContent.includes('已转存'))
      await page.locator('[data-sonner-toast]').waitFor()
      const complete = await measure()
      for (const state of [pending, complete]) {
        for (const item of ['dialog', 'card', 'button']) {
          assert.ok(Math.abs(state[item].y - before[item].y) <= 1, `${width}px ${item} 转存时上下跳动`)
          assert.ok(Math.abs(state[item].h - before[item].h) <= 1, `${width}px ${item} 转存时高度变化`)
        }
        assert.ok(Math.abs(state.button.w - before.button.w) <= 1, '转存按钮随文案改变宽度')
        assert.equal(state.scroll, before.scroll)
      }
      const strmBefore = await measure()
      const strmRequest = new Promise(resolve => { strmRequested = resolve })
      await page.locator('[data-bot-action="strm"]').click()
      const loadingToast = page.locator('[data-sonner-toast][data-type="loading"]')
      await loadingToast.waitFor()
      const summary = await loadingToast.locator('[data-description]').innerText()
      assert.equal(summary, 'Bot · 测试作品 (2022) 3.73G')
      assert.equal(await loadingToast.locator('[data-description]').evaluate(element => getComputedStyle(element).webkitLineClamp), '2')
      const toastElement = await loadingToast.elementHandle()
      const strmLoading = await measure()
      await page.waitForFunction(() => document.querySelector('[data-bot-action="strm"]').textContent.includes('处理中'))
      await strmRequest
      assert.equal(typeof resolveSTRM, 'function')
      resolveSTRM()
      await page.waitForFunction(() => !document.querySelector('[title="停止本窗口已提交的 STRM 批次"]').disabled)
      assert.equal(await loadingToast.count(), 1, '后台接收后原通知仍应保持加载状态')
      assert.equal(await page.getByText('已提交后台', { exact: true }).count(), 0)
      assert.equal(await loadingToast.locator('[data-button]').count(), 0)
      const strmSubmitted = await measure()
      for (const state of [strmLoading, strmSubmitted]) {
        for (const item of ['dialog', 'card', 'strm']) {
          assert.ok(Math.abs(state[item].y - strmBefore[item].y) <= 1, `${width}px ${item} 分享 STRM 时上下跳动`)
          assert.ok(Math.abs(state[item].h - strmBefore[item].h) <= 1, `${width}px ${item} 分享 STRM 时高度变化`)
        }
        assert.ok(Math.abs(state.strm.w - strmBefore.strm.w) <= 1, '分享 STRM 按钮随文案改变宽度')
        assert.equal(state.scroll, strmBefore.scroll)
      }
      if (failed) {
        await page.getByRole('button', { name: '关闭资源搜索' }).click()
        await page.locator('.bot-dialog').waitFor({ state: 'detached' })
        await loadingToast.waitFor()
      }
      finished = true
      const resultToast = page.locator('[data-sonner-toast]').filter({ hasText: failed ? '模拟生成失败' : 'STRM 生成成功' })
      await resultToast.waitFor()
      assert.equal(await resultToast.getAttribute('data-type'), failed ? 'error' : 'success')
      assert.ok(await resultToast.evaluate((element, original) => element === original, toastElement), '任务结果应在原通知上更新')
      assert.equal(await resultToast.locator('[data-button]').count(), 0)
      assert.equal(await resultToast.locator('[data-description]').innerText(), summary)
      if (!failed) {
        await page.waitForFunction(() => document.querySelector('[data-bot-action="strm"]').textContent.includes('已生成'))
        const after = await measure()
        assert.deepEqual(after, strmBefore, '完成通知不应改变弹窗布局')
      }
    }
    assert.equal(linkCalls, 2)
    assert.equal(writes, 2)
    assert.equal(strmWrites, 2)
    assert.deepEqual(errors, [])
    console.log('通过：桌面与手机转存、分享 STRM 和 Promise Toast 更新期间布局保持稳定')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
