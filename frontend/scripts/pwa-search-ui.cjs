// 使用模拟接口验证正式搜索路由，不访问真实账户或写入数据。
const assert = require('node:assert/strict')
const path = require('node:path')
const os = require('node:os')
const fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const media = (id, kind = 'movie', title = `测试影视 ${id}`) => ({ id, kind, title, posterUrl: '', date: '2023-01-01', rating: 8, votes: 100 })
const person = id => ({ id, name: `测试演员 ${id}`, profileUrl: '', knownFor: [] })
const results = make => ({ items: Array.from({ length: 20 }, (_, i) => make(i + 1)), total: 40, totalPages: 2 })

function contrast(a, b) {
  const luminance = value => value.match(/\d+/g).slice(0, 3).reduce((sum, c, i) => {
    const v = Number(c) / 255
    return sum + [0.2126, 0.7152, 0.0722][i] * (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4)
  }, 0)
  const values = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (values[0] + 0.05) / (values[1] + 0.05)
}

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    await fs.mkdir('test-results', { recursive: true })
    for (const [width, theme] of [[320, 'light'], [320, 'dark'], [390, 'light'], [390, 'dark'], [430, 'light'], [430, 'dark'], [1440, 'dark']]) {
      const mobile = width < 640
      const ctx = await browser.newContext({ viewport: { width, height: 820 }, isMobile: mobile, hasTouch: mobile, colorScheme: theme, serviceWorkers: 'block', reducedMotion: 'reduce' })
      const errors = [], searches = []
      let delay = false
      await ctx.addInitScript(theme => {
        localStorage.setItem('admin_token', 'mock-search-token')
        localStorage.setItem('cinema115-theme', theme)
      }, theme)
      await ctx.route('**/api/**', async route => {
        const { pathname, searchParams } = new URL(route.request().url())
        const q = searchParams.get('query')
        let data = { items: [] }
        if (pathname.endsWith('/auth/me')) data = { user: { id: 'mock', username: 'mock', role: 'admin' } }
        else if (pathname.endsWith('/tmdb/genres')) data = { genres: [{ id: 16, name: '动画' }] }
        else if (pathname.endsWith('/tmdb/browse') && !q) data = results(id => media(id, 'tv'))
        else if (pathname.endsWith('/tmdb/browse') || pathname.endsWith('/tmdb/people')) {
          searches.push(Object.fromEntries(searchParams))
          if (delay) await new Promise(resolve => setTimeout(resolve, 650))
          const kind = searchParams.get('kind')
          data = results(kind === 'person' ? person : id => media(id, kind === 'tv' ? 'tv' : 'movie'))
        } else if (pathname.includes('/tmdb/detail/')) {
          data = { ...media(Number(pathname.split('/').pop())), overview: '测试电影简介', backdropUrl: '', originalTitle: '', tagline: '', runtime: 120, genres: [], countries: [], cast: [], seasons: [] }
        } else if (pathname.includes('/tmdb/person/')) {
          data = { ...person(Number(pathname.split('/').pop())), biography: '测试简介', items: [] }
        }
        await route.fulfill({ json: data }).catch(() => {})
      })
      const page = await ctx.newPage()
      page.setDefaultTimeout(8500)
      page.on('pageerror', error => errors.push(error.message))
      const cards = page.locator('.search-grid > a')
      const input = page.getByRole('searchbox')
      const scopes = page.getByRole('navigation', { name: '搜索范围' })
      const search = page.getByRole('button', { name: '搜索', exact: true })
      const back = page.getByRole('button', { name: '返回上一页' })
      const screenshot = state => page.screenshot({ path: `test-results/pwa-search-${width}-${theme}-${state}.png` })
      const noOverflow = async () => assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
      const scrollY = () => page.evaluate(() => window.scrollY)
      const closeToScroll = top => page.waitForFunction(top => Math.abs(window.scrollY - top) <= 2, top)
      const params = () => new URL(page.url()).searchParams
      const isMainCase = width === 390 && theme === 'light'
      let homeScroll = 0
      if (isMainCase) {
        await page.goto(`${origin}/tv?genre=16`)
        await page.locator('.catalog-media-grid > a').last().waitFor()
        await page.evaluate(() => window.scrollTo(0, 1200))
        homeScroll = await scrollY()
        assert.ok(homeScroll > 690)
        await page.getByRole('link', { name: '搜索影视', exact: true }).click()
      } else await page.goto(`${origin}/search`)
      await input.waitFor()
      await noOverflow()
      if (!mobile) {
        assert.match(await scopes.getAttribute('class'), /segmented-control-neutral/)
        await scopes.getByRole('button', { name: '聚合' }).waitFor()
        await search.waitFor()
        await ctx.close()
        continue
      }
      assert.match(await scopes.getAttribute('class'), /segmented-control-pwa-pill/)
      assert.equal(await scopes.getByRole('button', { pressed: true }).innerText(), '影视')
      assert.equal(await search.count(), 0)
      assert.equal(await page.locator('.search-empty').innerText(), '输入电影或剧集名称开始搜索。')
      assert.equal(await page.getByRole('button', { name: '沙丘', exact: true }).count(), 0)
      assert.equal(await page.locator('.has-mobile-dock').count(), 0)
      const colors = await page.evaluate(() => {
        const style = selector => getComputedStyle(document.querySelector(selector))
        const form = style('.search-form'), header = style('.search-header'), attribution = style('.search-attribution')
        const indicator = getComputedStyle(document.querySelector('.segmented-control-indicator'), '::before')
        return {
          background: header.backgroundColor, page: style('.is-search-workspace').backgroundColor,
          image: style('.is-search-workspace').backgroundImage, surface: form.backgroundColor,
          formBorder: form.borderTopWidth, headerBorder: header.borderBottomWidth, headerShadow: header.boxShadow,
          backBackground: style('.mobile-search-back').backgroundColor, inputSize: style('.search-input').fontSize,
          placeholder: getComputedStyle(document.querySelector('.search-input'), '::placeholder').color,
          footer: attribution.color, scope: style('.segmented-control-option[aria-pressed=true]').color,
          indicator: indicator.backgroundColor, scopeShadow: style('.segmented-control').boxShadow,
          footerBottom: document.querySelector('.search-attribution').getBoundingClientRect().bottom,
        }
      })
      assert.equal(colors.background, theme === 'dark' ? 'rgb(24, 25, 28)' : 'rgb(248, 250, 252)')
      assert.equal(colors.page, colors.background)
      assert.equal(colors.image, 'none')
      assert.equal(colors.formBorder, '0px')
      assert.equal(colors.headerBorder, '0px')
      assert.equal(colors.headerShadow, 'none')
      assert.equal(colors.backBackground, 'rgba(0, 0, 0, 0)')
      assert.equal(colors.inputSize, '16px')
      assert.notEqual(colors.scopeShadow, 'none')
      assert.ok(colors.footerBottom > 750 && colors.footerBottom <= 820)
      assert.ok(contrast(colors.placeholder, colors.surface) >= 4.5)
      assert.ok(contrast(colors.footer, colors.page) >= 4.5)
      await screenshot('empty')
      await input.fill('待提交')
      await search.waitFor()
      assert.equal(searches.length, 0)
      assert.equal(params().has('q'), false)
      assert.ok(await search.evaluate(el => el.getBoundingClientRect().height >= 44))
      const inputWidth = await input.evaluate(el => el.getBoundingClientRect().width)
      assert.ok(inputWidth >= 108, `搜索输入宽度仅 ${inputWidth}px`)
      await noOverflow()
      if (isMainCase) await input.press('Enter')
      else await search.click()
      await cards.last().waitFor()
      assert.equal(params().get('q'), '待提交')
      assert.equal(searches.at(-1).kind, 'multi')
      await screenshot('results')
      if (isMainCase) {
        // 滚到骨架屏能达到的位置以下，再模拟详情返回时的慢请求。
        await page.evaluate(() => window.scrollTo(0, 2190))
        await page.locator('.search-header.is-scrolled').waitFor()
        await cards.last().evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }))
        const top = await scrollY()
        assert.ok(top > 2100)
        await cards.last().click()
        await page.waitForURL('**/movie/20')
        delay = true
        await page.goBack()
        await cards.last().waitFor()
        await closeToScroll(top)
        delay = false
        await page.getByRole('navigation', { name: '搜索分页' }).getByRole('button', { name: '下一页' }).click()
        await page.waitForURL('**&page=2')
        await cards.last().waitFor()
        await closeToScroll(0)
        await scopes.getByRole('button', { name: '演员' }).click()
        await page.getByRole('link', { name: '测试演员 20，查看演员' }).waitFor()
        assert.equal(params().get('scope'), 'person')
        assert.equal(params().has('page'), false)
        await page.getByRole('button', { name: '清空搜索' }).click()
        await page.getByText('输入演员姓名，查看代表作和参演影视。', { exact: true }).waitFor()
        await input.fill('刘亦菲')
        await input.press('Enter')
        await page.getByRole('link', { name: '测试演员 20，查看演员' }).waitFor()
        assert.equal(params().get('q'), '刘亦菲')
        await cards.last().evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }))
        const actorTop = await scrollY()
        await cards.last().click()
        await page.getByRole('heading', { name: '测试演员 20', exact: true }).waitFor()
        delay = true
        await page.locator('.mobile-nav-back').click()
        await cards.last().waitFor()
        await closeToScroll(actorTop)
        delay = false
        await back.click()
        await page.waitForURL('**/tv?genre=16')
        await page.locator('.catalog-media-grid > a').last().waitFor()
        await closeToScroll(homeScroll)
      }
      if (width === 430 && theme === 'dark') {
        await page.goto(`${origin}/search?q=沙丘&scope=movie&page=2`)
        await cards.last().waitFor()
        assert.equal(await input.inputValue(), '沙丘')
        assert.equal(await input.evaluate(el => document.activeElement === el), false)
        assert.equal(await scopes.getByRole('button', { pressed: true }).innerText(), '电影')
        assert.equal(searches.at(-1).page, '2')
        await back.click()
        // 有同源历史时应返回上一页；无历史入口另开标签页验证。
        const direct = await ctx.newPage()
        await direct.goto(`${origin}/search?q=沙丘`)
        await direct.getByRole('button', { name: '返回上一页' }).click()
        await direct.waitForURL(url => ['/tv', '/movie'].includes(url.pathname))
      }
      assert.deepEqual(errors, [])
      await ctx.close()
      console.log(`${width}px ${theme}: OK`)
    }
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
