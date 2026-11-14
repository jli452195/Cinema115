// 隔离运行真实词表 API；只使用临时 TXT，不接触项目运行数据或网盘。
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const { spawn } = require('node:child_process')
const { once } = require('node:events')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const backend = path.resolve('../backend')
const fixtureSource = `package main
import (
 "context"
 "fmt"
 "net"
 "net/http"
 "os"
 "time"
 "github.com/gin-gonic/gin"
 "strm115/backend/internal/httpapi"
 "strm115/backend/internal/service"
)
func main() {
 gin.SetMode(gin.ReleaseMode)
 settings, err := service.OpenRecognitionSettings(context.Background(), os.Args[1])
 if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
 r := gin.New()
 httpapi.RegisterRecognition(r.Group("/api/admin"), settings)
 listener, err := net.Listen("tcp", "127.0.0.1:0")
 if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
 fmt.Println("RECOGNITION_URL=http://"+listener.Addr().String())
 server := &http.Server{Handler:r, ReadHeaderTimeout:5*time.Second}
 if err := server.Serve(listener); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
`

async function main() {
  let fixture, browser
  try {
  await fs.mkdir(path.join(backend, 'tmp'), { recursive: true })
  const temporary = await fs.mkdtemp(path.join(backend, 'tmp', 'recognition-ui-'))
  const source = path.join(temporary, 'main.go'), executable = path.join(temporary, 'fixture.exe'), dataDirectory = path.join(temporary, 'data')
  await fs.writeFile(source, fixtureSource)
  const builder = spawn('go', ['build', '-o', executable, source], { cwd: backend, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  let buildOutput = ''; builder.stdout.on('data', data => { buildOutput += data }); builder.stderr.on('data', data => { buildOutput += data })
  assert.equal((await once(builder, 'exit'))[0], 0, buildOutput)
  fixture = spawn(executable, [dataDirectory], { cwd: backend, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  let fixtureOutput = ''; fixture.stderr.on('data', data => { fixtureOutput += data })
  const address = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('隔离 API 启动超时：'+fixtureOutput)), 15000)
    fixture.once('exit', code => { clearTimeout(timeout); reject(new Error('隔离 API 提前退出：'+code+' '+fixtureOutput)) })
    fixture.stdout.on('data', data => { const match = String(data).match(/RECOGNITION_URL=(http:\/\/[^\s]+)/); if (match) { clearTimeout(timeout); resolve(match[1]) } })
  })
  const api = async (suffix = '', init) => {
    const response = await fetch(address+'/api/admin/recognition'+suffix, init)
    const data = await response.json(); assert.equal(response.status, 200, JSON.stringify(data)); return data
  }
  const initial = await api(), passed = [], errors = [], calls = []
  const groupFile = path.join(dataDirectory, 'release-groups.txt'), platformFile = path.join(dataDirectory, 'platforms.txt')
  assert.equal(initial.record.groups, '')
  assert.equal(initial.record.platforms, '')
  assert.equal(initial.record.loadError, undefined)
  assert.equal('defaults' in initial, false)
  const assertMissingLists = async () => {
    await assert.rejects(fs.access(groupFile), { code: 'ENOENT' })
    await assert.rejects(fs.access(platformFile), { code: 'ENOENT' })
  }
  await assertMissingLists()
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', acceptDownloads: true })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'recognition-ui-fixture'); localStorage.setItem('cinema115-theme', 'light') })
  const state = { loseSave: false, externalEdit: false }
  let holdPreview, previewStarted, previewFinished
  await context.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
    calls.push({ endpoint, method: request.method() })
    if (endpoint.startsWith('/api/admin/recognition')) {
      if (request.method() === 'PUT' && state.externalEdit) {
        state.externalEdit = false
        await fs.appendFile(groupFile, '\n# 外部编辑\n')
      }
      const response = await route.fetch({ url: address+endpoint+url.search })
      if (endpoint.endsWith('/preview') && holdPreview) {
        const waiting = holdPreview
        holdPreview = undefined; previewStarted()
        await waiting
        await route.fulfill({ response }).catch(() => {})
        previewFinished()
        return
      }
      if (request.method() === 'PUT' && state.loseSave) { state.loseSave = false; await response.dispose(); return route.abort() }
      return route.fulfill({ response })
    }
    const data = endpoint === '/api/auth/me' ? { user: { id: 'admin', username: 'admin', role: 'admin' } } : endpoint.includes('notifications') ? { items: [], unread: 0 } : {}
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  const page = await context.newPage(); page.setDefaultTimeout(15000); page.on('pageerror', error => errors.push(error.message))
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(origin+'/media/organize/recognition')
    await page.locator('.cm-content').waitFor()
    assert.equal(await page.getByRole('navigation', { name: '整理功能', exact: true }).getByRole('link', { name: '识别词表' }).getAttribute('aria-current'), 'page')
    await page.getByText('当前词表为空，此类信息不会被识别。', { exact: true }).waitFor()
    assert.equal(await page.locator('input[type="url"]').count(), 0)
    await page.locator('.rs-references summary').click()
    assert.equal(await page.getByRole('link', { name: 'MoviePilot 发布组规则', exact: true }).getAttribute('href'), 'https://github.com/jxxghp/MoviePilot/blob/HEAD/app/domain/meta/releasegroup.py')
    assert.equal(await page.getByRole('link', { name: 'TRaSH-Guides 公开规则', exact: true }).getAttribute('rel'), 'noopener noreferrer')
    await page.locator('.rs-references summary').click()
    await assertMissingLists()
    passed.push('首次缺失 TXT 读取为空，不报错且不自动创建文件')
    const editor = () => page.locator('.cm-content')
    const replaceEditor = async value => { await editor().click(); await page.keyboard.press('ControlOrMeta+a'); await page.keyboard.insertText(value) }
    const save = () => page.getByRole('button', { name: '保存并生效', exact: true })
    const groups = page.getByRole('button', { name: '字幕组与发布组', exact: true })
    const platforms = page.getByRole('button', { name: '来源平台', exact: true })
    const samples = page.getByRole('textbox', { name: '测试文件名', exact: true })
    const groupText = '喵萌奶茶屋\nLoliHouse\n(?:桜|樱)都字幕[组組]\n桜都字幕组 ~= (?:桜|樱)都字幕[组組]\n'
    const platformText = 'NF ~= NF|Netflix\nIQ ~= IQ|iQIYI|爱奇艺|愛奇藝\n'
    await replaceEditor(groupText)
    await platforms.click(); await replaceEditor(platformText)
    await groups.click(); assert.equal((await editor().innerText()).trimEnd(), groupText.trimEnd())
    await samples.fill('[喵萌奶茶屋&LoliHouse] 某动画 - 01 [NF WEB-DL 1080p].mkv\n[樱都字幕組] 某动画 - 02 [iQIYI WEB-DL].mkv')
    await page.locator('.rs-result').first().waitFor()
    assert.deepEqual(await page.locator('.rs-result').first().locator('.rs-group').allTextContents(), ['喵萌奶茶屋', 'LoliHouse'])
    // 直接读取真实接口的结果，避免仅靠展示文案判断识别是否正确。
    const previewResponse = await api('/preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ groups: groupText, platforms: 'NF ~= NF|Netflix\nIQ ~= IQ|iQIYI|爱奇艺|愛奇藝\n', items: ['[喵萌奶茶屋&LoliHouse] 某动画 - 01 [NF WEB-DL 1080p].mkv', '[樱都字幕組] 某动画 - 02 [iQIYI WEB-DL].mkv'] }) })
    assert.deepEqual(previewResponse.items[0].releaseGroups, ['喵萌奶茶屋', 'LoliHouse'])
    assert.deepEqual(previewResponse.items[1].releaseGroups, ['桜都字幕组'])
    assert.deepEqual(previewResponse.items[1].platforms, ['IQ'])
    assert.equal((await api()).record.revision, initial.record.revision)
    await assertMissingLists()
    passed.push('两份草稿切换保留，真实接口识别联合组/可选映射/爱奇艺，测试不保存')
    await page.locator('.rs-match-details').first().locator('summary').click()
    await page.getByRole('button', { name: '发布组第 2 行', exact: true }).first().click()
    assert.equal(await editor().evaluate(element => element.contains(document.activeElement) || element === document.activeElement), true)
    passed.push('命中原文和词表行号可展开，点击行号聚焦编辑器')
    let releasePreview
    holdPreview = new Promise(resolve => { releasePreview = resolve })
    const delayedStarted = new Promise(resolve => { previewStarted = resolve })
    const delayedFinished = new Promise(resolve => { previewFinished = resolve })
    await samples.fill('[LoliHouse] 旧测试 [NF WEB-DL].mkv')
    await delayedStarted
    await samples.fill('[喵萌奶茶屋] 新测试 [IQ WEB-DL].mkv')
    await page.locator('.rs-result-name').filter({ hasText: '新测试' }).waitFor()
    releasePreview(); await delayedFinished
    assert.equal(await page.locator('.rs-result-name').count(), 1)
    assert.ok((await page.locator('.rs-result-name').innerText()).includes('新测试'))
    passed.push('旧测试请求延迟返回也不会覆盖新草稿结果')
    await samples.fill('[喵萌奶茶屋&LoliHouse] 某动画 - 01 [NF WEB-DL 1080p].mkv\n[樱都字幕組] 某动画 - 02 [iQIYI WEB-DL].mkv')
    await page.locator('.rs-result').nth(1).waitFor()
    await page.screenshot({ path: 'test-results/recognition-desktop-light.png', fullPage: true })

    await save().click(); await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '保存并生效' && button.disabled))
    assert.equal((await api()).record.groups, groupText)
    assert.equal(await fs.readFile(groupFile, 'utf8'), groupText)
    assert.equal(await fs.readFile(platformFile, 'utf8'), platformText)
    passed.push('首次从空词表保存创建两份 UTF-8 TXT 并激活')

    await replaceEditor(groupText+'坏正则 ~= (\n')
    await save().click()
    await page.getByRole('alert').filter({ hasText: '正则' }).first().waitFor()
    assert.equal((await api()).record.groups, groupText)
    assert.ok((await editor().innerText()).includes('坏正则'))
    await replaceEditor(groupText)
    passed.push('无效正则返回行诊断且不覆盖已保存配置')

    await replaceEditor(groupText+'新组\n')
    state.loseSave = true; await save().click()
    await page.getByRole('alert').filter({ hasText: '草稿已保留' }).first().waitFor()
    assert.ok((await editor().innerText()).includes('新组'))
    assert.equal(await editor().getAttribute('contenteditable'), 'true')
    assert.equal(await save().isEnabled(), true)
    await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '保存并生效' && button.disabled))
    assert.equal((await api()).record.groups, groupText+'新组\n')
    passed.push('保存响应丢失保留可编辑草稿，直接重试保存成功')

    await replaceEditor(groupText+'覆盖草稿\n')
    state.externalEdit = true; await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '保存并生效' && button.disabled))
    assert.equal((await api()).record.groups, groupText+'覆盖草稿\n')
    assert.equal(await fs.readFile(groupFile, 'utf8'), groupText+'覆盖草稿\n')
    passed.push('外部 TXT 修改后保存直接覆盖为当前草稿')
    await replaceEditor(groupText+'未保存草稿\n')

    await page.getByRole('navigation', { name: '整理功能', exact: true }).getByRole('link', { name: '命名模板', exact: true }).click()
    await page.getByRole('dialog').waitFor()
    await page.getByRole('dialog').getByRole('button', { name: '继续编辑', exact: true }).click()
    assert.equal(new URL(page.url()).pathname, '/media/organize/recognition')
    passed.push('离开未保存词表时提示，取消保留编辑')

    const downloaded = page.waitForEvent('download')
    await page.getByRole('button', { name: '导出 TXT', exact: true }).click()
    const download = await downloaded
    assert.equal(download.suggestedFilename(), 'release-groups.txt')
    assert.equal(await fs.readFile(await download.path(), 'utf8'), groupText+'未保存草稿\n')
    await page.getByLabel('导入识别词表 TXT', { exact: true }).setInputFiles({ name: 'groups.txt', mimeType: 'text/plain', buffer: Buffer.from('\ufeff'+groupText) })
    await page.getByRole('dialog').getByRole('button', { name: '载入草稿', exact: true }).click()
    assert.equal((await editor().innerText()).trimEnd(), groupText.trimEnd())
    assert.equal((await api()).record.groups, groupText+'覆盖草稿\n')
    await platforms.click(); assert.ok((await editor().innerText()).includes('iQIYI'))
    await groups.click()
    passed.push('UTF-8 TXT 导出精确保留草稿，带 BOM 导入仅替换当前草稿且不保存')

    // 截图保留可解释的正常测试结果，异常交互已经在上面断言。
    await page.getByRole('button', { name: '重新读取', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '重新读取', exact: true }).click()
    await page.locator('.rs-result').first().waitFor()
    await page.locator('.rs-match-details').first().locator('summary').click()
    await page.screenshot({ path: 'test-results/recognition-desktop-light.png', fullPage: true })

    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.screenshot({ path: 'test-results/recognition-desktop-dark.png', fullPage: true })
    await page.setViewportSize({ width: 390, height: 844 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth+1))
    const selectedTab = page.getByRole('navigation', { name: '整理功能', exact: true }).getByRole('link', { name: '识别词表' })
    assert.ok((await selectedTab.boundingBox()).x + (await selectedTab.boundingBox()).width <= 390)
    await page.getByRole('button', { name: '发布组第 1 行', exact: true }).first().click()
    assert.ok((await editor().boundingBox()).y >= 0 && (await editor().boundingBox()).y < 844)
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }))
    await page.screenshot({ path: 'test-results/recognition-mobile-dark.png', fullPage: true })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    await page.screenshot({ path: 'test-results/recognition-mobile-light.png', fullPage: true })
    passed.push('桌面与390px手机深浅主题，无页面横向溢出')

    await page.setViewportSize({ width: 1440, height: 1000 })
    await groups.click()
    await page.getByRole('button', { name: '清空词表', exact: true }).click()
    await page.getByRole('dialog').getByText('仅清空当前', { exact: false }).waitFor()
    await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
    assert.ok((await editor().innerText()).includes('LoliHouse'))
    await page.getByRole('button', { name: '清空词表', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '清空词表', exact: true }).click()
    assert.equal((await editor().innerText()).trim(), '')
    await page.getByText('当前词表为空，此类信息不会被识别。', { exact: true }).waitFor()
    assert.ok((await api()).record.groups.includes('LoliHouse'))
    await platforms.click(); assert.equal((await editor().innerText()).trimEnd(), platformText.trimEnd())
    await groups.click(); await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '保存并生效' && button.disabled))
    const cleared = (await api()).record
    assert.equal(cleared.groups, '')
    assert.equal(cleared.platforms, platformText)
    const clearedPreview = await api('/preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ groups: cleared.groups, platforms: cleared.platforms, items: ['[LoliHouse] 动画 [NF].mkv'] }) })
    assert.deepEqual(clearedPreview.items[0].releaseGroups, [])
    assert.deepEqual(clearedPreview.items[0].platforms, ['NF'])
    assert.equal(await fs.readFile(groupFile, 'utf8'), '')
    passed.push('清空只修改当前草稿，保存后该类不识别且另一词表保持')

    await replaceEditor(groupText); await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '保存并生效' && button.disabled))
    await fs.unlink(platformFile)
    await page.getByRole('button', { name: '重新读取', exact: true }).click()
    await page.getByRole('button', { name: '应用文件修改', exact: true }).waitFor()
    await platforms.click()
    assert.equal((await editor().innerText()).trim(), '')
    await page.getByRole('button', { name: '应用文件修改', exact: true }).click()
    await page.getByRole('button', { name: '应用文件修改', exact: true }).waitFor({ state: 'detached' })
    const deleted = (await api()).record
    assert.equal(deleted.groups, groupText)
    assert.equal(deleted.platforms, '')
    assert.equal(deleted.activeRevision, deleted.revision)
    assert.equal(deleted.loadError, undefined)
    await assert.rejects(fs.access(platformFile), { code: 'ENOENT' })
    const deletedPreview = await api('/preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ groups: deleted.groups, platforms: deleted.platforms, items: ['[LoliHouse] 动画 [NF].mkv'] }) })
    assert.deepEqual(deletedPreview.items[0].releaseGroups, ['LoliHouse'])
    assert.deepEqual(deletedPreview.items[0].platforms, [])
    passed.push('删除 TXT 后重新读取并应用，该类不识别且不会重建文件')

    assert.deepEqual(errors, [])
    assert.equal(calls.some(call => /\/recognition\/(sources|pull)$/.test(call.endpoint)), false)
    await fs.writeFile('test-results/recognition-ui.json', JSON.stringify({ passed, calls: calls.length, errors }, null, 2))
    console.log(JSON.stringify({ passed, calls: calls.length, errors }, null, 2))
  } finally {
    await browser?.close(); fixture?.kill()
  }
}
main().catch(error => { console.error(error); process.exitCode = 1 })
