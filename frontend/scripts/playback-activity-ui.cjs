// All API traffic is mocked. No real accounts, playback sessions or settings.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', reducedMotion: 'no-preference' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const start = Date.parse('2026-10-17T10:00:00Z'), errors = [], reads = [], writes = []
  // The browser clock is deliberately five seconds behind the backend.
  await page.clock.install({ time: new Date(start - 5000) })
  page.on('pageerror', e => errors.push(e.message))
  const member = (userId, extra = {}) => ({ userId, embyId: userId.padEnd(32, 'a'), embyName: userId, source: 'assigned', embyState: 'active', disabled: false, childLimit: 0, childUsed: 0, stage: 'ready', driveStatus: { state: 'ready', message: 'mock' }, ...extra })
  const members = ['Playing', 'Paused', 'Issued', 'Preparing', 'OldEvidence', 'Mixed', 'SecondPlaying', 'FutureEvidence', ...Array.from({ length: 25 }, (_, i) => `ZOffscreen${i}`)].map(id => member(id))
  members.push(member('Review', { embyState: 'missing' }))
  const session = (userId, extra = {}) => ({ key: userId, userId, item: 'movie', generation: 7, issued: true, issuedAt: start - 5000, firstIssuedAt: start - 5000, updatedAt: start, expiresAt: start + 3600000, playbackState: 'playing', playbackStateAt: start, target: { accountId: 'config', accountName: 'Mock drive', source: 'assigned', issuedAt: start }, ...extra })
  let sessions = members.map(m => session(m.userId))
  sessions = sessions.map(s => s.userId === 'Paused' ? { ...s, playbackState: 'paused' } : s.userId === 'Issued' ? { ...s, playbackState: undefined, playbackStateAt: undefined } : s.userId === 'Preparing' ? { ...s, issued: false } : s.userId === 'OldEvidence' ? { ...s, firstIssuedAt: start - 120000, issuedAt: start - 120000, playbackStateAt: start - 91000 } : s.userId === 'FutureEvidence' ? { ...s, playbackStateAt: start + 1000000 } : s)
  sessions.push(session('Mixed', { key: 'Mixed-paused', playbackState: 'paused' }))
  let failRead = false
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (req.method() !== 'GET') { writes.push(p); return route.fulfill({ status: 405, json: {} }) }
   reads.push(p)
   if (p.endsWith('/playback-sessions')) return failRead ? route.fulfill({ status: 503, json: { error: 'mock read failed' } }) : route.fulfill({ json: { sessions, serverTime: await page.evaluate(() => Date.now() + 5000), devicePolicy: { mode: 'parallel', silenceSeconds: 60 } } })
   if (p === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', role: 'admin', username: 'admin' } } })
   return route.fulfill({ json: { members, settings: { enabled: true, libraryIds: [], allowDownload: false }, proxy: { listening: true }, accounts: [], pending: [], jobs: [], leases: [] } })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/users/list')
  const card = id => page.locator(`article.user-card[aria-label="${id}"]`)
  const state = id => card(id).getAttribute('data-playback-activity')
  const layers = id => card(id).locator('.member-activity-light')
  const geometry = id => card(id).evaluate(el => { const r = el.getBoundingClientRect(); return [r.width, r.height, el.querySelector('footer').getBoundingClientRect().top - r.top] })
  const styles = id => layers(id).evaluate(el => ['::before', '::after'].map(p => {
   const s = getComputedStyle(el, p); return { name: s.animationName, state: s.animationPlayState, opacity: s.opacity, duration: s.animationDuration, delay: s.animationDelay, background: s.backgroundImage }
  }))
  await layers('Playing').waitFor()
  await page.waitForFunction(() => document.querySelector('[aria-label="Playing"].user-card')?.hasAttribute('data-ambient-running'))
  assert.equal(await state('Playing'), 'playing')
  assert.equal(await state('Paused'), 'quiet')
  assert.equal(await state('Issued'), 'idle')
  assert.equal(await state('Preparing'), 'idle')
  assert.equal(await state('OldEvidence'), 'quiet')
  assert.equal(await state('FutureEvidence'), 'idle')
  assert.equal(await state('Mixed'), 'playing', 'One playing session wins over another paused session')
  assert.equal(reads.filter(p => p.endsWith('/playback-sessions')).length, 1, 'One shared session read')
  const initialGeometry = await geometry('Playing')
  const before = await styles('Playing')
  assert.deepEqual(before.map(s => s.name), ['playback-glow-a', 'playback-glow-b'])
  assert.deepEqual(before.map(s => s.duration), ['7s', '11s'])
  assert.ok(before.every(s => s.state === 'running'))
  assert.ok((await styles('Paused')).every(s => s.state === 'paused'))
  assert.notDeepEqual(before.map(s => s.delay), (await styles('SecondPlaying')).map(s => s.delay), 'Stable per-user phase offsets')
  assert.ok((await styles('ZOffscreen24')).every(s => s.state === 'paused'), 'Offscreen cards suspend both layers')
  const wave = await layers('Playing').evaluate(el => {
   const animations = el.getAnimations({ subtree: true }).filter(a => a.animationName === 'playback-glow-a')
   const a = animations[0], original = a.currentTime
   a.pause(); a.currentTime = 0
   const first = getComputedStyle(el, '::before').opacity
   a.currentTime = 3100
   const second = getComputedStyle(el, '::before').opacity
   a.currentTime = original; a.play()
   return [Number(first), Number(second)]
  })
  assert.ok(Math.abs(wave[0] - wave[1]) > .1, 'The wave visibly changes, rather than merely declaring keyframes')
  await card('Playing').hover()
  assert.equal(await card('Playing').evaluate(el => getComputedStyle(el, '::before').animationName), 'none', 'Green activity suppresses the blue hover layers')
  assert.deepEqual(await geometry('Playing'), initialGeometry, 'Hover preserves dimensions and content positions')
  await page.mouse.move(0, 0)
  await page.evaluate(async () => { await Promise.all(document.getAnimations().filter(a => Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a => a.finished.catch(() => {}))) })
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Performance.enable')
  const metrics = async () => Object.fromEntries((await cdp.send('Performance.getMetrics')).metrics.map(m => [m.name, m.value]))
  const baseline = await metrics()
  await new Promise(resolve => setTimeout(resolve, 1200))
  const activeMetrics = await metrics()
  assert.equal(activeMetrics.LayoutCount - baseline.LayoutCount, 0, 'Steady waves cause no layout work')
  const animationCounts = await page.evaluate(() => {
   const animations = document.getAnimations().filter(a => a.animationName?.startsWith('playback-glow-'))
   return { running: animations.filter(a => a.playState === 'running').length, paused: animations.filter(a => a.playState === 'paused').length }
  })
  assert.ok(animationCounts.running > 0 && animationCounts.running <= 8 && animationCounts.paused > 40, 'Only visible playing cards animate in the 34-card fixture')
  console.log('Glow performance:', { ...animationCounts, layoutCount: activeMetrics.LayoutCount - baseline.LayoutCount, scriptMs: Math.round((activeMetrics.ScriptDuration - baseline.ScriptDuration) * 1000) })
  await cdp.detach()
  const setHidden = async hidden => page.evaluate(hidden => { Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden }); document.dispatchEvent(new Event('visibilitychange')) }, hidden)
  await setHidden(true)
  assert.ok((await styles('Playing')).every(s => s.state === 'paused'))
  await setHidden(false)
  await page.waitForFunction(() => document.querySelector('[aria-label="Playing"].user-card')?.hasAttribute('data-ambient-running'))
  assert.ok((await styles('Playing')).every(s => s.state === 'running'))
  // Refresh through the shared dialog; no per-card request or state mutation.
  const dialog = page.locator('dialog.playback-dialog[open]')
  const refresh = async () => {
   await page.getByRole('button', { name: '\u67e5\u770b\u4f1a\u8bdd', exact: true }).click()
   await dialog.waitFor()
   await page.waitForFunction(() => document.querySelector('.playback-dialog-footer button')?.getAttribute('aria-disabled') === 'false')
   const response = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'))
   await dialog.getByRole('button', { name: '\u91cd\u65b0\u8bfb\u53d6', exact: true }).click(); await response
   await page.waitForFunction(() => document.querySelector('.playback-dialog-footer button')?.getAttribute('aria-disabled') === 'false')
   await dialog.getByRole('button', { name: '\u5173\u95ed\u64ad\u653e\u4f1a\u8bdd', exact: true }).click()
  }
  failRead = true; await refresh()
  assert.equal(await state('Playing'), 'quiet')
  assert.ok((await styles('Playing')).every(s => s.state === 'paused'))
  failRead = false; await refresh()
  // Refreshing a link in the same session must retain its recent observation.
  sessions = sessions.map(s => s.userId === 'Playing' ? { ...s, issuedAt: start + 1000 } : s)
  await refresh(); assert.equal(await state('Playing'), 'playing')
  await layers('Playing').evaluate(el => { window.activityAnimation = el.getAnimations({ subtree: true }).find(a => a.animationName === 'playback-glow-a') })
  sessions = sessions.map(s => s.userId === 'Playing' ? { ...s, playbackState: 'paused' } : s)
  await refresh(); assert.equal(await state('Playing'), 'quiet')
  assert.equal(await layers('Playing').evaluate(el => el.getAnimations({ subtree: true }).includes(window.activityAnimation)), true, 'Pause retains the existing animation phase')
  assert.ok((await styles('Playing')).every(s => s.state === 'paused'))
  assert.deepEqual(await geometry('Playing'), initialGeometry)
  sessions = sessions.filter(s => s.userId !== 'Playing')
  await refresh(); assert.equal(await state('Playing'), 'idle')
  await page.clock.runFor(800)
  await layers('Playing').waitFor({ state: 'detached' })
  assert.deepEqual(await geometry('Playing'), initialGeometry, 'Stop removes light without resizing')
  sessions.push(session('Playing')); await refresh()
  await page.emulateMedia({ reducedMotion: 'reduce' })
  assert.ok((await styles('Playing')).every(s => s.name === 'none'), 'Reduced motion retains a static tint')
  assert.equal(await layers('Playing').evaluate(el => getComputedStyle(el).transitionDuration), '0s')
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  const output = path.join(__dirname, '../test-results/playback-activity'); fs.mkdirSync(output, { recursive: true })
  for (const [width, height] of [[1440,1000], [390,844], [320,568]]) {
   await page.setViewportSize({ width, height }); await card('Playing').scrollIntoViewIfNeeded()
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await page.evaluate(async () => { await Promise.all(document.getAnimations().filter(a => Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a => a.finished.catch(() => {}))) })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.ok((await styles('Playing')).every(s => s.name.startsWith('playback-glow-')))
    await page.screenshot({ path: path.join(output, `cards-${width}-${dark ? 'dark' : 'light'}.png`) })
   }
  }
  await page.clock.runFor(95000)
  assert.equal(await state('Playing'), 'quiet', 'A successful poll cannot make old playback evidence fresh')
  assert.deepEqual(writes, [])
  assert.deepEqual(errors, [])
  console.log('Playback activity UI passed: confirmed/paused/preparing/issued/stale/multiple sessions, actual wave, phase offsets, hover, stop geometry, visibility/offscreen pause, reduced motion, 3 viewports, light/dark and shared reads')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
