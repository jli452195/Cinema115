// Isolated fixture: real Vue components, mocked directory APIs, no account or filesystem writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  const page = await context.newPage(), errors = [], calls = [], signatures = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const special = '/media/片源 & #? + 中文'
  let releaseInitial, enterInitial, holdInitial = true, failCloudChild = true, strmRoot = '.'
  const initialEntered = new Promise(resolve => { enterInitial = resolve }), initialReleased = new Promise(resolve => { releaseInitial = resolve })
  await page.route('**/__directory-picker-fixture', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><html lang="zh-CN"><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/src/style.css"><link rel="stylesheet" href="/src/styles/floatingSurface.css"></head><body><div id="directory-fixture"></div></body></html>' }))
  await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
    calls.push({ endpoint, method: request.method(), query: Object.fromEntries(url.searchParams) })
    assert.equal(request.method(), 'GET', 'directory fixture must never write real records')
    let data, status = 200
    if (endpoint === '/api/admin/media-organize/directories') {
      const current = url.searchParams.get('path') || '/'
      if (holdInitial) { holdInitial = false; enterInitial(); await initialReleased }
      const listings = {
        '/': { parentPath: null, directories: [{ name: 'media', path: '/media' }] },
        '/media': { parentPath: '/', directories: [{ name: 'inbox', path: '/media/inbox' }, { name: '片源 & #? + 中文', path: special }] },
        '/media/inbox': { parentPath: '/media', directories: [] },
        [special]: { parentPath: '/media', directories: [] },
        '/many': { parentPath: '/', directories: Array.from({ length: 100 }, (_, i) => ({ name: '影片目录 ' + i, path: '/many/' + i })), truncated: true },
      }
      if (listings[current]) data = { path: current, truncated: false, ...listings[current] }
      else { status = 400; data = { error: '模拟目录不可读取' } }
    } else if (endpoint === '/api/admin/p115/cloud-download/paths') {
      assert.equal(url.searchParams.get('accountId'), 'fixture-account', 'the adapter must keep the requested account')
      const cid = url.searchParams.get('cid'), offset = Number(url.searchParams.get('offset'))
      if (cid === '0') data = offset === 0 ? { directories: [{ id: '10', name: '影片' }], hasMore: true, nextOffset: 100 } : { directories: [{ id: '20', name: '剧集' }], hasMore: false }
      else if (cid === '10') data = { files: [{ id: '30', name: '字幕', directory: true }, { id: 'not-directory', name: 'movie.mkv', directory: false }], count: 2 }
      else if (cid === '20' && failCloudChild) { failCloudChild = false; status = 503; data = { error: '模拟115子目录读取失败' } }
      else data = { directories: [], hasMore: false }
    } else if (endpoint === '/api/admin/p115/share/output-directories') {
      const requested = url.searchParams.get('path') || '', current = requested === '.' ? '' : requested
      assert.ok(current === '' || current === 'movies', 'STRM browsing must stay in its relative output namespace')
      data = { path: current || strmRoot, displayPath: '/strm' + (current ? '/' + current : ''), directories: current ? [] : [{ name: '电影', path: 'movies', displayPath: '/strm/movies' }], truncated: false }
    } else { status = 404; data = { error: 'Unexpected fixture endpoint: ' + endpoint } }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const dialog = () => page.locator('dialog.directory-picker-dialog[open]')
  const choose = () => dialog().getByRole('button', { name: /^(选择此目录|选择 STRM 根目录)$/ })
  const idle = async () => { await dialog().locator('.dp-content[aria-busy="false"]').waitFor() }
  const open = async kind => { await page.getByRole('button', { name: '浏览' + kind + '目录', exact: true }).click(); await dialog().waitFor() }
  const signature = async () => signatures.push(await dialog().evaluate(el => {
    const box = el.getBoundingClientRect(), row = el.querySelector('.dp-entry'), icon = row?.querySelector('svg'), style = getComputedStyle(el)
    return { width: Math.round(box.width), height: Math.round(box.height), radius: style.borderRadius, rowHeight: row ? Math.round(row.getBoundingClientRect().height) : 0, folderColor: icon ? getComputedStyle(icon).color : '' }
  }))
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/__directory-picker-fixture', { waitUntil: 'networkidle' })
    await page.evaluate(async () => {
      const { createApp, h, reactive, Fragment, nextTick } = await import('/node_modules/.vite/deps/vue.js')
      const [local, cloud, strm, floating] = await Promise.all([
        import('/src/components/MediaLocalDirectoryDialog.vue'), import('/src/components/P115DirectoryDialog.vue'),
        import('/src/components/STRMDirectoryDialog.vue'), import('/src/components/AnimeFloatingDialog.vue'),
      ])
      const state = reactive({ kind: '', parentOpen: true, saving: false, saveError: '', selected: [], created: [] })
      const adapters = { '本地': local.default, '115': cloud.default, STRM: strm.default }
      const app = createApp({ render() {
        return h(Fragment, [state.parentOpen ? h(floating.default, { title: '目录选择宿主', closeLabel: '关闭测试宿主', onClose: () => { state.parentOpen = false } }, { default: () => Object.keys(adapters).map(kind => h('button', { type: 'button', onClick: () => { state.kind = kind } }, '浏览' + kind + '目录')) }) : null,
          state.kind ? h(adapters[state.kind], {
            key: state.kind, token: 'fixture-token', title: state.kind + '目录',
            ...(state.kind === '本地' ? { initialPath: '/media/inbox' } : {}),
            ...(state.kind === '115' ? { accountId: 'fixture-account', allowRoot: false, allowCreate: true, saving: state.saving, saveError: state.saveError } : {}),
            onClose: () => { state.kind = '' },
            onSelect: value => { state.selected.push({ kind: state.kind, value }); state.kind = '' },
            onCreate: value => { state.created.push(value); state.saving = true },
          }) : null])
      } })
      window.directoryFixture = { state, app, nextTick }
      app.mount('#directory-fixture')
    })
    const parent = page.getByRole('dialog', { name: '目录选择宿主', exact: true })
    await parent.waitFor()
    await open('本地')
    await initialEntered
    assert.equal(await choose().isDisabled(), true, 'loading must block confirming an unverified directory')
    releaseInitial(); await idle()
    await dialog().getByRole('button', { name: '返回上级', exact: true }).click()
    await dialog().getByRole('button', { name: '打开片源 & #? + 中文', exact: true }).waitFor()
    await signature()
    await page.screenshot({ path: 'test-results/directory-picker-local.png' })
    await dialog().getByRole('textbox', { name: '目录路径', exact: true }).fill('/missing')
    assert.equal(await choose().isDisabled(), true, 'a draft path must not select the previous directory')
    await dialog().getByRole('button', { name: '前往', exact: true }).click()
    await dialog().getByRole('alert').filter({ hasText: '模拟目录不可读取' }).waitFor()
    assert.equal(await choose().isDisabled(), true)
    await dialog().getByRole('button', { name: '根目录', exact: true }).click(); await idle()
    assert.equal(await dialog().getByRole('button', { name: '返回上级', exact: true }).isDisabled(), true)
    await dialog().getByRole('button', { name: '打开media', exact: true }).click()
    await dialog().getByRole('button', { name: '打开片源 & #? + 中文', exact: true }).click(); await idle()
    const readsBeforeRefresh = calls.length
    await dialog().getByRole('button', { name: '刷新目录', exact: true }).click(); await idle()
    assert.equal(calls[readsBeforeRefresh].query.path, special)
    await choose().click(); await dialog().waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => window.directoryFixture.state.selected.at(-1).value), special)
    assert.equal(calls.some(call => call.endpoint.includes('/p115/')), false, 'local browsing must not depend on 115')
    assert.equal(await parent.isVisible(), true)
    assert.equal(await parent.getByRole('button', { name: '浏览本地目录', exact: true }).evaluate(el => document.activeElement === el), true)

    await open('115'); await idle()
    await dialog().getByRole('button', { name: '影片', exact: true }).waitFor()
    await dialog().getByRole('button', { name: '剧集', exact: true }).waitFor()
    assert.equal(await dialog().getByRole('textbox', { name: '目录路径', exact: true }).evaluate(el => el.readOnly), true)
    assert.equal(await choose().isDisabled(), true, 'a disallowed root must remain unselectable')
    assert.ok(calls.some(call => call.query.cid === '0' && call.query.offset === '100'), 'cloud directory pagination was skipped')
    await signature()
    await page.screenshot({ path: 'test-results/directory-picker-115.png' })
    await dialog().getByRole('button', { name: '在此新建 Cinema115 并使用', exact: true }).click()
    assert.deepEqual(await page.evaluate(() => JSON.parse(JSON.stringify(window.directoryFixture.state.created))), [{ id: '0', path: '/', name: '115 根目录' }])
    await dialog().getByRole('button', { name: '保存中…', exact: true }).waitFor()
    for (const name of ['取消', '返回上级', '根目录', '刷新目录', '影片', '在此新建 Cinema115 并使用']) assert.equal(await dialog().getByRole('button', { name, exact: true }).isDisabled(), true, 'saving must disable ' + name)
    await page.keyboard.press('Escape')
    assert.equal(await dialog().count(), 1, 'saving must prevent accidental dismissal')
    await page.evaluate(async () => { window.directoryFixture.state.saving = false; window.directoryFixture.state.saveError = '模拟目录保存失败'; await window.directoryFixture.nextTick() })
    await dialog().getByRole('alert').filter({ hasText: '模拟目录保存失败' }).waitFor()
    await page.setViewportSize({ width: 320, height: 740 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.equal(await dialog().evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    for (const name of ['取消', '选择此目录', '在此新建 Cinema115 并使用']) {
      const button = await dialog().getByRole('button', { name, exact: true }).boundingBox()
      assert.ok(button.x >= 0 && button.y >= 0 && button.x + button.width <= 320 && button.y + button.height <= 740, 'mobile creation/save-error footer must keep ' + name + ' visible')
    }
    await page.screenshot({ path: 'test-results/directory-picker-115-mobile-error.png' })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    await dialog().getByRole('button', { name: '影片', exact: true }).click(); await idle()
    assert.equal(await dialog().getByRole('button', { name: 'movie.mkv', exact: true }).count(), 0, 'files must not become directory entries')
    await dialog().getByRole('button', { name: '字幕', exact: true }).waitFor()
    await dialog().getByRole('button', { name: '返回上级', exact: true }).click(); await idle()
    await dialog().getByRole('button', { name: '剧集', exact: true }).click()
    await dialog().getByRole('alert').filter({ hasText: '模拟115子目录读取失败' }).waitFor()
    const beforeRetry = calls.length
    await dialog().getByRole('button', { name: '重新读取', exact: true }).click(); await idle()
    assert.equal(calls[beforeRetry].query.cid, '20', 'retry must load the failed child rather than the previous root')
    await choose().click(); await dialog().waitFor({ state: 'hidden' })
    assert.deepEqual(await page.evaluate(() => JSON.parse(JSON.stringify(window.directoryFixture.state.selected.at(-1).value))), { id: '20', path: '/剧集', name: '剧集' })

    await open('STRM'); await idle()
    assert.equal(await dialog().getByRole('textbox', { name: '目录路径', exact: true }).inputValue(), '/strm')
    assert.equal(await dialog().getByRole('textbox', { name: '目录路径', exact: true }).evaluate(el => el.readOnly), true)
    assert.equal(await dialog().getByRole('button', { name: '返回上级', exact: true }).isDisabled(), true)
    await signature()
    await page.screenshot({ path: 'test-results/directory-picker-strm.png' })
    await dialog().getByRole('button', { name: '电影', exact: true }).click(); await idle()
    assert.equal(await dialog().getByRole('textbox', { name: '目录路径', exact: true }).inputValue(), '/strm/movies')
    await dialog().getByRole('button', { name: '返回上级', exact: true }).click(); await idle()
    const beforeRootRefresh = calls.length
    await dialog().getByRole('button', { name: '刷新目录', exact: true }).click(); await idle()
    assert.equal(calls[beforeRootRefresh].query.path, '.', 'refresh must retain the explicit STRM root sentinel')
    await choose().click(); await dialog().waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => window.directoryFixture.state.selected.at(-1).value), '.', 'STRM root must remain an explicit relative selection')
    assert.deepEqual(signatures[0], signatures[1], 'local and 115 pickers must share the same visual rules')
    assert.deepEqual(signatures[1], signatures[2], '115 and STRM pickers must share the same visual rules')
    const color = signatures[0].folderColor.match(/\d+/g).map(Number)
    assert.ok(color[2] > color[0] && color[2] > color[1], 'folder icons must use the shared blue emphasis')
    strmRoot = ''
    await open('STRM'); await idle()
    assert.equal(await dialog().getByRole('button', { name: '返回上级', exact: true }).isDisabled(), true)
    await choose().click(); await dialog().waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => window.directoryFixture.state.selected.at(-1).value), '.', 'an empty API root must normalize to the same explicit selection')

    await open('本地'); await idle()
    await dialog().getByRole('textbox', { name: '目录路径', exact: true }).fill('/many')
    await dialog().getByRole('button', { name: '前往', exact: true }).click(); await idle()
    const scroller = dialog().locator('.anime-floating-body')
    assert.equal(await scroller.evaluate(el => el.scrollHeight > el.clientHeight), true)
    const beforeScroll = { path: await dialog().getByRole('textbox', { name: '目录路径', exact: true }).boundingBox(), confirm: await choose().boundingBox() }
    await scroller.evaluate(el => { el.scrollTop = el.scrollHeight })
    assert.equal((await dialog().getByRole('textbox', { name: '目录路径', exact: true }).boundingBox()).y, beforeScroll.path.y, 'navigation must stay fixed while the directory list scrolls')
    assert.equal((await choose().boundingBox()).y, beforeScroll.confirm.y, 'confirmation must stay fixed while the directory list scrolls')
    await page.setViewportSize({ width: 320, height: 740 })
    for (const dark of [false, true]) {
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false)
      assert.equal(await dialog().evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
      const bounds = await dialog().boundingBox(), confirmation = await choose().boundingBox()
      assert.ok(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= 321 && bounds.y + bounds.height <= 741)
      assert.ok(confirmation.y + confirmation.height <= 740, 'mobile confirmation must remain visible')
      await page.screenshot({ path: `test-results/directory-picker-mobile-${dark ? 'dark' : 'light'}.png` })
    }
    await page.keyboard.press('Escape'); await dialog().waitFor({ state: 'hidden' })
    assert.equal(await parent.isVisible(), true, 'Escape must close only the nested picker')
    assert.equal(await parent.getByRole('button', { name: '浏览本地目录', exact: true }).evaluate(el => document.activeElement === el), true)
    assert.deepEqual(errors, [])
    console.log('PASS: shared local/115/STRM appearance, paths and paging, root/create rules, loading/errors/retry, saving lock, STRM root semantics, internal scroll, nested focus, narrow light/dark layout')
  } finally { releaseInitial(); await page.evaluate(() => window.directoryFixture?.app.unmount()).catch(() => {}); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
