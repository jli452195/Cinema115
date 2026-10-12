const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    const page = await context.newPage()
    const errors = [], writes = []
    const settings = { libraryIds: ['library-0', 'library-1', 'library-2'], enabled: true }
    const names = ['电视剧', '电影', '动漫', '动漫电影', '豆瓣top250', '国漫', '华语电影', '纪录片', '美剧', '欧美', '日韩剧', '泰剧', '综艺', '有声书', '播放列表', '合集', '4K Ultra HD Remux Collection 名称较长的媒体库']
    const libraries = Array.from({ length: 24 }, (_, index) => ({ ItemId: `library-${index}`, Name: names[index] || `测试媒体库 ${index}`, CollectionType: index % 2 ? 'movies' : 'tvshows' }))
    let records = [], sequence = 0, failSave = false, failGenerate = false, failList = false, failRevoke = false, failSettings = false, failLibraries = false
    page.on('pageerror', error => errors.push(error.message))
    await page.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock')
      localStorage.setItem('cinema115-theme', 'light')
      window.__copied = ''
      window.__failCopy = false
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false })
      document.execCommand = command => {
        if (command !== 'copy') return false
        window.__copied = document.activeElement?.value || ''
        return !window.__failCopy
      }
    })
    // All API traffic is intercepted. This script never creates or revokes real invitations.
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      let data = {}, status = 200
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (p.endsWith('/member-playback/settings')) {
        if (failSettings) { status = 503; data = { error: '设置暂时读取失败' } }
        else data = { settings }
      } else if (p.endsWith('/member-playback/libraries')) {
        if (failLibraries) { status = 503; data = { error: '媒体库暂时读取失败' } }
        else data = { libraries }
      } else if (p.endsWith('/member-playback/registration-settings')) {
        writes.push({ path: p, body: req.postDataJSON() })
        if (failSave) { failSave = false; status = 400; data = { error: '保存失败，请重试' } }
        else { settings.libraryIds = req.postDataJSON().libraryIds; data = { settings } }
      } else if (p === '/api/admin/invites') {
        if (req.method() === 'POST') {
          const body = req.postDataJSON()
          writes.push({ path: p, body })
          if (failGenerate) { failGenerate = false; status = 400; data = { error: '生成失败，请重试' } }
          else {
            const now = Math.floor(Date.now() / 1000)
            const generated = Array.from({ length: body.count }, () => {
              sequence++
              return { id: `mock-invite-${sequence}`, note: body.note, playbackSource: body.playbackSource, code: `mock-code-${sequence}`, createdBy: 'admin', createdAt: now, expiresAt: now + body.days * 86400, usedAt: 0, revokedAt: 0 }
            })
            records = [...generated.map(({ code, ...invite }) => invite), ...records]
            data = { invites: generated }
          }
        } else if (failList) { failList = false; status = 503; data = { error: '邀请记录暂不可用' } }
        else {
          const offset = Number(url.searchParams.get('offset') || 0)
          data = { invites: records.slice(offset, offset + 50), hasMore: records.length > offset + 50 }
        }
      } else if (p.endsWith('/revoke')) {
        writes.push({ path: p, body: req.postDataJSON() })
        if (failRevoke) { failRevoke = false; status = 400; data = { error: '撤销失败，请重试' } }
        else { records.find(invite => invite.id === p.split('/').at(-2)).revokedAt = Math.floor(Date.now() / 1000) }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })

    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const summary = page.getByRole('region', { name: '默认媒体库', exact: true })
    const dialog = page.getByRole('dialog', { name: '新账号的默认媒体库', exact: true })
    const results = page.getByRole('region', { name: '请保存新生成的邀请', exact: true })
    const recordsPanel = page.getByRole('region', { name: '邀请记录', exact: true })
    const openEditor = () => summary.getByRole('button', { name: '编辑', exact: true }).click()
    const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await page.goto(base + '/users')
    await summary.getByText('已选 3 个媒体库', { exact: true }).waitFor()
    const initialLayout = await page.locator('.invite-workspace').evaluate(element => {
      const form = element.querySelector('.invite-generate-form').getBoundingClientRect()
      const settings = element.querySelector('.invite-settings').getBoundingClientRect()
      const records = element.querySelector('.invite-records').getBoundingClientRect()
      return { sideBySide: settings.left >= form.right, sameTop: Math.abs(settings.top - form.top) < 1, gap: records.top - form.bottom }
    })
    assert.deepEqual(initialLayout, { sideBySide: true, sameTop: true, gap: 20 })
    await openEditor()
    assert.equal(await dialog.getByRole('button', { name: '关闭媒体库设置' }).evaluate(el => el === document.activeElement), true)
    assert.equal(await dialog.locator('.invite-library-type').first().textContent(), 'tvshows')
    assert.equal(await dialog.getByRole('button', { name: '保存更改' }).isDisabled(), true)
    await dialog.getByLabel('搜索媒体库', { exact: true }).fill('电影')
    assert.equal(await dialog.locator('.invite-library-option').count(), 3)
    await dialog.locator('.invite-library-option').filter({ hasText: '动漫电影' }).click()
    await dialog.getByLabel('搜索媒体库', { exact: true }).fill('没有匹配名称')
    await dialog.getByText('没有匹配的媒体库，试试其他关键词。').waitFor()
    await dialog.getByRole('button', { name: '关闭媒体库设置' }).focus()
    await page.keyboard.press('Escape')
    await dialog.getByText('有未保存的修改，是否放弃？').waitFor()
    assert.equal(await dialog.getByRole('button', { name: '继续编辑' }).evaluate(el => el === document.activeElement), true)
    await page.keyboard.press('Escape')
    assert.equal(await dialog.getByRole('button', { name: '继续编辑' }).count(), 0)
    await dialog.getByRole('button', { name: '关闭媒体库设置' }).click()
    await dialog.getByRole('button', { name: '放弃并关闭' }).click()
    assert.equal(writes.length, 0)
    assert.equal(await summary.getByRole('button', { name: '编辑' }).evaluate(el => el === document.activeElement), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')

    await openEditor()
    await dialog.locator('.invite-library-option').filter({ hasText: '电视剧' }).click()
    failSave = true
    await dialog.getByRole('button', { name: '保存更改' }).click()
    await dialog.getByRole('alert').filter({ hasText: '保存失败' }).waitFor()
    assert.deepEqual(settings.libraryIds, ['library-0', 'library-1', 'library-2'])
    await dialog.getByRole('button', { name: '保存更改' }).click()
    await summary.getByText('已选 2 个媒体库', { exact: true }).waitFor()
    await summary.getByRole('status').waitFor()
    assert.deepEqual(writes.at(-1).body, { libraryIds: ['library-1', 'library-2'] })
    await openEditor()
    await dialog.getByRole('button', { name: '清空', exact: true }).click()
    await dialog.getByRole('button', { name: '保存并暂停注册' }).click()
    await page.getByText('尚未选择默认媒体库，受邀人暂时无法注册。').waitFor()
    await page.getByRole('button', { name: '设置媒体库', exact: true }).click()
    await dialog.getByRole('button', { name: '选择当前全部' }).click()
    await dialog.getByRole('button', { name: '保存更改' }).click()
    await summary.getByText('已选 24 个媒体库', { exact: true }).waitFor()

    await page.getByRole('button', { name: '修改', exact: true }).click()
    await page.getByLabel('邀请链接的站点地址', { exact: true }).fill('https://cinema.example.com/path')
    assert.equal(await page.getByRole('button', { name: '生成邀请', exact: true }).isDisabled(), true)
    await page.getByLabel('邀请链接的站点地址', { exact: true }).fill('https://cinema.example.com')
    await page.getByRole('button', { name: '收起', exact: true }).click()
    await page.locator('.invite-source-choice').filter({ hasText: '管理员共享小号池' }).click()
    assert.equal(await page.getByRole('radio', { name: /管理员共享小号池/ }).isChecked(), true)
    await page.getByLabel('数量', { exact: true }).fill('2')
    await page.getByRole('combobox', { name: /有效期/ }).selectOption('30')
    await page.getByLabel('备注选填', { exact: true }).fill('批量测试')
    failGenerate = true
    await page.getByRole('button', { name: '生成邀请', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '生成失败' }).waitFor()
    failList = true
    await page.getByRole('button', { name: '生成邀请', exact: true }).click()
    await results.waitFor()
    await recordsPanel.getByRole('alert').filter({ hasText: '邀请记录暂不可用' }).waitFor()
    assert.deepEqual(writes.at(-1).body, { count: 2, days: 30, note: '批量测试', playbackSource: 'assigned' })
    assert.equal(await results.locator('.invite-generated-row').count(), 2)
    await results.getByRole('button', { name: '复制全部链接', exact: true }).click()
    assert.equal(await page.evaluate(() => window.__copied.split('\n').length), 2)
    assert.equal(await page.evaluate(() => window.__copied.startsWith('https://cinema.example.com/register#invite=')), true)
    await results.getByRole('button', { name: '复制邀请码', exact: true }).first().click()
    assert.equal(await page.evaluate(() => window.__copied.startsWith('mock-code-')), true)
    await page.evaluate(() => { window.__failCopy = true })
    await results.getByRole('button', { name: '复制邀请链接', exact: true }).first().click()
    const manual = page.getByLabel('手动复制内容', { exact: true })
    await manual.waitFor()
    assert.equal(await manual.evaluate(el => el === document.activeElement && el.selectionEnd === el.value.length), true)
    const manualBox = await manual.boundingBox()
    assert.ok(manualBox.y >= 0 && manualBox.y + manualBox.height <= 1000)
    await page.evaluate(() => { window.__failCopy = false })
    assert.equal(await page.evaluate(() => JSON.stringify({ ...localStorage }).includes('mock-code-')), false)
    await recordsPanel.getByRole('button', { name: '刷新', exact: true }).click()
    const row = recordsPanel.locator('.invite-record').first()
    await row.getByRole('button', { name: '撤销', exact: true }).click()
    const beforeRevoke = writes.length
    await row.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(writes.length, beforeRevoke)
    await row.getByRole('button', { name: '撤销', exact: true }).click()
    failRevoke = true
    await row.getByRole('button', { name: '确认撤销', exact: true }).click()
    await recordsPanel.getByRole('alert').filter({ hasText: '撤销失败' }).waitFor()
    await row.getByRole('button', { name: '确认撤销', exact: true }).click()
    await row.getByText('已撤销', { exact: true }).waitFor()
    assert.equal(await results.locator('.invite-generated-row').count(), 1)
    await results.getByRole('button', { name: '隐藏', exact: true }).click()
    await results.getByRole('button', { name: '保留显示' }).click()
    assert.equal(await results.isVisible(), true)
    await results.getByRole('button', { name: '隐藏', exact: true }).click()
    await results.getByRole('button', { name: '确认隐藏' }).click()
    assert.equal(await results.count(), 0)
    await page.reload()
    await summary.getByText('已选 24 个媒体库', { exact: true }).waitFor()
    assert.equal(await results.count(), 0)

    // Desktop, mobile, narrow screens, short landscapes and both themes.
    for (const width of [1440, 768, 390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      for (const dark of [false, true]) {
        await page.evaluate(value => { document.documentElement.classList.toggle('dark', value); scrollTo(0, 0) }, dark)
        await page.waitForTimeout(220)
        await noOverflow()
        await page.screenshot({ path: path.join(os.tmpdir(), `cinema-invites-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true })
        await openEditor()
        await dialog.evaluate(el => Promise.all(el.getAnimations().map(animation => animation.finished)))
        const bounds = await dialog.boundingBox(), footer = await dialog.locator('footer').boundingBox()
        assert.ok(bounds.width <= width && bounds.height < 844)
        assert.ok(footer.y >= 0 && footer.y + footer.height <= 844)
        await noOverflow()
        assert.equal(await dialog.locator('.invite-library-type').first().textContent(), 'tvshows')
        await page.screenshot({ path: path.join(os.tmpdir(), `cinema-invites-editor-${width}-${dark ? 'dark' : 'light'}.png`) })
        await page.keyboard.press('Escape')
      }
    }
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    await page.waitForTimeout(220)
    await page.evaluate(() => scrollTo(0, 0))
    const generateBounds = await page.getByRole('button', { name: '生成邀请', exact: true }).boundingBox()
    assert.ok(generateBounds.y + generateBounds.height <= 844, 'Generate button should fit the 390px mobile first screen')
    await openEditor()
    await page.setViewportSize({ width: 640, height: 360 })
    const landscapeFooter = await dialog.locator('footer').boundingBox()
    assert.ok(landscapeFooter.y >= 0 && landscapeFooter.y + landscapeFooter.height <= 360)
    await page.keyboard.press('Escape')
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await openEditor()
    assert.equal(await dialog.evaluate(el => getComputedStyle(el).animationName), 'none')
    await page.keyboard.press('Escape')

    // Load failure, failed library fetch and stale selections all remain recoverable.
    failSettings = true
    await page.reload()
    await page.getByText('默认媒体库设置暂未确认，请检查后再分享邀请。').waitFor()
    await page.getByRole('button', { name: '检查设置' }).click()
    failSettings = false
    await dialog.getByRole('button', { name: '重试', exact: true }).click()
    await dialog.getByText('已选 24 / 24', { exact: true }).waitFor()
    await page.keyboard.press('Escape')
    failLibraries = true
    await page.reload()
    await summary.getByRole('alert').waitFor()
    await openEditor()
    assert.equal(await dialog.getByRole('button', { name: '保存更改' }).isDisabled(), true)
    failLibraries = false
    await dialog.getByRole('button', { name: '重试', exact: true }).click()
    await dialog.getByText('已选 24 / 24', { exact: true }).waitFor()
    await page.keyboard.press('Escape')
    settings.libraryIds = ['missing-library']
    await page.reload()
    await summary.getByText('有 1 个已选媒体库失效，请编辑检查。').waitFor()
    await openEditor()
    assert.equal(await dialog.getByRole('button', { name: '保存更改' }).isDisabled(), true)
    await dialog.getByRole('button', { name: '移除失效选择' }).click()
    await dialog.getByRole('button', { name: '保存并暂停注册' }).waitFor()
    await dialog.getByRole('button', { name: '关闭媒体库设置' }).click()
    await dialog.getByRole('button', { name: '放弃并关闭' }).click()
    assert.deepEqual(settings.libraryIds, ['missing-library'])
    assert.deepEqual(errors, [])
    console.log('Invitations UI passed: responsive layouts, original library types, search, focus/discard, save recovery, empty-template warning, source payloads, one-time results, batch copy/fallback, revoke confirmation, themes and load recovery')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
