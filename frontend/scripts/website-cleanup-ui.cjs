const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = [], records = []
    page.on('pageerror', e => errors.push(e.message))
    let fail = true
    const users = ['empty', 'created', 'adopted'].map(id => ({ id, username: id, disabled: false, role: 'user', createdAt: '2026-09-29T00:00:00Z' }))
    const preview = id => {
      const u = users.find(u => u.id === id)
      return { userId: id, username: id, embyId: id === 'empty' ? '' : 'emby-' + id, embyName: 'Emby ' + id, adopted: id === 'adopted', source: id === 'empty' ? '' : 'personal', drives: id === 'created' ? [{ id: 'drive', uid: '123', playbackParentCid: '456' }] : [], mode: id === 'empty' ? 'none' : id === 'adopted' ? 'keep' : 'delete', state: u?.cleanupState || '', blockedReason: '', fingerprint: u?.cleanupState ? 'retry' : 'initial' }
    }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (url.pathname === '/api/admin/users') data = { users, hasMore: false }
      else if (url.pathname === '/api/admin/member-playback/users') data = { members: [] }
      else if (url.pathname === '/api/admin/users/cleanup-records') data = { records }
      else if (/\/users\/[^/]+\/cleanup$/.test(url.pathname)) {
        const id = url.pathname.split('/').at(-2)
        if (req.method() === 'GET') data = preview(id)
        else {
          writes.push({ id, ...req.postDataJSON() })
          if (id === 'created' && fail) {
            Object.assign(users.find(u => u.id === id), { disabled: true, cleanupState: 'deleting' }); status = 400; data = { error: 'Emby 暂不可用，删除待完成，请重试' }
          } else {
            records.push({ ...preview(id), state: 'deleted' }); users.splice(users.findIndex(u => u.id === id), 1); data = { message: '已删除' }
          }
        }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/users')
    const row = name => page.getByRole('group', { name: name + ' 的网站账号', exact: true })
    const dialog = page.getByRole('dialog', { name: '删除网站用户' })
    await row('empty').getByRole('button', { name: '删除用户', exact: true }).click()
    await dialog.getByText('未关联 Emby 账号。', { exact: true }).waitFor()
    assert.equal(await dialog.getByLabel('Emby 处理方式').count(), 0)
    assert.equal(await dialog.getByRole('button', { name: '确认删除' }).isDisabled(), true)
    await dialog.getByLabel('输入网站用户名确认').fill('empty')
    await dialog.getByRole('button', { name: '确认删除' }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.deepEqual(writes.at(-1), { id: 'empty', username: 'empty', mode: 'none', fingerprint: 'initial' })
    await row('created').getByRole('button', { name: '删除用户', exact: true }).click()
    assert.equal(await dialog.getByLabel('Emby 处理方式').inputValue(), 'delete')
    await dialog.getByText('个人网盘 UID：123 · 目录：456', { exact: true }).waitFor()
    await dialog.getByLabel('输入网站用户名确认').fill('created')
    await dialog.getByRole('button', { name: '确认删除' }).click()
    await dialog.getByText('删除待完成，网站账号已锁定。重试会继续原处理方式。').waitFor()
    assert.equal(await dialog.getByLabel('Emby 处理方式').isDisabled(), true)
    fail = false
    await dialog.getByRole('button', { name: '重试删除', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(writes.at(-1).fingerprint, 'retry')
    await row('adopted').getByRole('button', { name: '删除用户', exact: true }).click()
    assert.equal(await dialog.getByLabel('Emby 处理方式').inputValue(), 'keep')
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await dialog.evaluate(el => el.scrollWidth > el.clientWidth), false)
    await dialog.getByLabel('输入网站用户名确认').fill('adopted')
    await dialog.getByRole('button', { name: '确认删除' }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(writes.at(-1).mode, 'keep')
    await page.getByText('用户清理记录（3）', { exact: true }).click()
    await page.getByText('Emby：Emby adopted · 保留且锁定', { exact: true }).waitFor()
    assert.equal(users.length, 0)
    assert.deepEqual(errors, [])
    console.log('Website cleanup UI passed: empty, created/adopted defaults, confirmation, pending retry, durable choice, history, dark mobile dialog')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
