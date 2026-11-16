// 独立挂载真实 Vue 字幕组件，只使用内存夹具；所有业务请求均在浏览器入口阻断。
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const os = require('node:os')
const path = require('node:path')
const { selectOption, inputValue, selectOptions } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

const origin = new URL(process.env.CINEMA_URL || 'http://127.0.0.1:5173').origin
const harnessPath = '/__subtitle-batch-test'
const harness = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/src/style.css"><style>
body { margin:0;background:rgb(var(--surface-page));color:rgb(var(--content-primary)); }
#app { max-width:1200px;margin:0 auto;padding:16px;min-width:0; }
</style></head><body><div id="app"></div><script type="module">
import { createApp, h, reactive, nextTick } from '/node_modules/.vite/deps/vue.js';
import SubtitleMatchList from '/src/components/SubtitleMatchList.vue';
import { episodeNumber } from '/src/utils/subtitleImport.ts';
let app;
window.mountBatch = async (fixture) => {
  app?.unmount();
  const rows = reactive(structuredClone(fixture.rows));
  const videos = structuredClone(fixture.videos);
  const protectedKeys = new Set(fixture.protectedKeys || []);
  window.fixture = { rows, videos, protectedKeys };
  app = createApp({render:()=>h(SubtitleMatchList, {
    rows, videos, targetNames:fixture.targetNames || {}, kind:fixture.kind || 'tv',
    protectedKeys, disabled:!!fixture.disabled,
  })});
  app.mount('#app');
  await nextTick(); await new Promise(requestAnimationFrame);
  return { unrecognized:rows.filter(row=>!episodeNumber(row.file.name)).length };
};
window.batchReady = true;
</script></body></html>`

function video(episode, season = 1, id = `v${episode}`) {
  // 原视频名故意不含集号，确保目标按显式季集信息排序。
  return { id, name:`opaque-video-${id}.mkv`, season, episode }
}
function row(key, name, extra = {}) {
  return { key, file:{name,sourceKey:'archive-a',sourceLabel:'压缩包 A'}, videoId:'', language:'', version:'', selected:false, reason:'未识别集号', ...extra }
}
function sequenceFixture() {
  // 35 条乱序输入可同时暴露字符串排序、只更新预览页、只更新列表页等错误。
  const rows = Array.from({length:35}, (_, i) => {
    const n = (i * 13) % 35 + 1
    return row(`r${n}`, `未知目录/opaqueasset${n}tail.ass`, n === 5 ? {videoId:'v5'} : {})
  })
  const videos = Array.from({length:35}, (_, i)=>video(35-i))
  return { rows, videos, protectedKeys:['r9'], targetNames:Object.fromEntries(videos.map(v=>[v.id,`Regression.S01E${String(v.episode).padStart(2,'0')}.mkv`])) }
}

;(async () => {
  const artifacts = process.env.SUBTITLE_BATCH_ARTIFACT_DIR || await fs.mkdtemp(path.join(os.tmpdir(), 'cinema-subtitle-batch-'))
  await fs.mkdir(artifacts, {recursive:true})
  // 仅在测试浏览器关闭本地网络访问检查，让 Vite HMR 正常连接；不忽略控制台错误。
  const browser = await chromium.launch({executablePath:process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true,args:['--disable-features=LocalNetworkAccessChecks']})
  const passed = [], failed = [], screenshots = [], pageErrors = [], consoleErrors = [], blockedRequests = []
  let page
  try {
    const context = await browser.newContext({viewport:{width:1280,height:960},serviceWorkers:'block',reducedMotion:'reduce'})
    page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', error=>pageErrors.push(error.message))
    page.on('console', message=>{if(message.type()==='error') consoleErrors.push(message.text())})
    await context.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url())
      if (url.origin === origin && url.pathname === harnessPath && request.method() === 'GET') {
        return route.fulfill({contentType:'text/html; charset=utf-8',body:harness})
      }
      // 仅放行 Vite 的静态模块与样式，严禁任何 API、真实网盘或整理写请求发出。
      if (url.origin === origin && request.method() === 'GET' && /^\/(?:src\/|node_modules\/|@vite\/|@id\/|@fs\/)/.test(url.pathname)) return route.continue()
      blockedRequests.push({method:request.method(),url:request.url()})
      return route.abort('blockedbyclient')
    })
    await page.goto(`${origin}${harnessPath}`)
    await page.waitForFunction(()=>window.batchReady, null, {timeout:90000})
    const panel = ()=>page.getByRole('region', {name:'字幕批量操作',exact:true})
    const button = name=>page.getByRole('button',{name,exact:true})
    const combo = name=>page.getByRole('combobox',{name,exact:true})
    const check = name=>page.getByRole('checkbox',{name,exact:true})
    const task = name=>panel().getByRole('navigation',{name:'字幕批量任务',exact:true}).getByRole('button',{name,exact:true})
    const apply = ()=>panel().getByRole('button',{name:'应用设置',exact:true})
    const snapshot = ()=>page.evaluate(()=>JSON.parse(JSON.stringify(window.fixture.rows)))
    async function mount(fixture) {
      await page.keyboard.press('Escape')
      return page.evaluate(f=>window.mountBatch(f),fixture)
    }
    async function open() {
      await button('批量处理').click()
      await panel().waitFor({state:'visible'})
      assert.equal(await page.locator('.match-files').count(),0,'批量视图替换列表，避免两套控件同时操作')
      assert.equal(await button('应用设置').count(),1,'独立面板只保留一个应用入口')
    }
    async function sequence(start = 'v1') {
      await task('匹配视频').click()
      await panel().getByRole('button',{name:'按顺序匹配',exact:true}).click()
      await selectOption(combo('批量匹配目标季'),'1')
      await selectOption(combo('批量匹配起始视频'),start)
    }
    async function assertDisabled(locator, message) { assert(await locator.isDisabled(),message) }
    async function assertEnabled(locator, message) { assert(await locator.isEnabled(),message) }
    async function run(name, test) {
      const started = Date.now()
      try {
        await test()
        passed.push({name,ms:Date.now()-started})
        console.log(`PASS ${name}`)
      } catch (error) {
        const item = {name,error:error.stack || String(error)}
        failed.push(item)
        console.error(`FAIL ${name}\n${item.error}`)
        try {
          const file = path.join(artifacts,`failure-${failed.length}.png`)
          await page.screenshot({path:file})
          item.screenshot = file
          item.panelText = await panel().innerText({timeout:1000}).catch(()=>null)
        } catch { /* 页面不可用时仍保留原始失败，不用截图错误覆盖断言。 */ }
      }
    }

    await run('取消、切换模式和跨页预览均无副作用；默认保留语言且顺序需确认', async()=>{
      const fixture = sequenceFixture()
      assert.equal((await mount(fixture)).unrecognized,35,'夹具名称必须均无法自动识别集号')
      const before = await snapshot()
      await open()
      assert.equal(await task('匹配视频').getAttribute('aria-pressed'),'true')
      assert.equal(await panel().getByRole('button',{name:'按集号匹配',exact:true}).getAttribute('aria-pressed'),'true','电视剧默认按集号匹配')
      assert.equal(await combo('批量设置字幕语言').count(),0,'匹配任务不暴露语言设置')
      await task('设置语言').click()
      assert.equal(await inputValue(combo('批量设置字幕语言')),'__keep')
      await selectOption(combo('批量设置字幕语言'),'zh-CN')
      assert(await check('仅填写未指定语言').isChecked())
      assert.equal(await combo('批量匹配目标季').count(),0)
      await task('匹配视频').click()
      assert(await check('同时勾选本组已匹配项').isChecked())
      await panel().getByRole('button',{name:'按顺序匹配',exact:true}).click()
      assert.equal(await inputValue(combo('批量匹配起始视频')),'','顺序模式不得自动猜选起始视频')
      await assertDisabled(apply(),'尚未指定起始视频不得应用')
      await sequence()
      await assertDisabled(apply(),'顺序匹配未经确认不得应用')
      assert.equal(await combo('批量设置字幕语言').count(),0)
      assert.equal(await panel().locator('.batch-preview-row').count(),20,'预览第一页仅渲染 20 条')
      const nav = panel().getByRole('navigation',{name:'批量预览分页',exact:true})
      await nav.getByRole('button',{name:'下一页',exact:true}).click()
      assert.equal(await panel().locator('.batch-preview-row').count(),15)
      await nav.getByRole('button',{name:'上一页',exact:true}).click()
      await check('我已核对对应关系，确认按当前预览应用').check()
      await assertEnabled(apply())
      await check('同时勾选本组已匹配项').uncheck()
      assert(!(await check('我已核对对应关系，确认按当前预览应用').isChecked()),'变更预览必须清除先前确认')
      assert.deepEqual(await snapshot(),before,'预览不能提前修改响应式行数据')
      await panel().getByRole('button',{name:'取消',exact:true}).click()
      assert.equal(await panel().count(),0)
      assert.deepEqual(await snapshot(),before,'取消不能修改任何行')
      assert.equal(await button('撤销').count(),0)
    })

    await run('35 条自然排序顺序匹配覆盖全部分页，保留已映射和手动清空项', async()=>{
      const fixture = sequenceFixture()
      await mount(fixture); await open(); await sequence()
      await check('我已核对对应关系，确认按当前预览应用').check()
      const preview = panel().locator('.batch-preview-row').filter({hasText:'opaqueasset1tail.ass'})
      assert.match(await preview.innerText(),/Regression\.S01E01\.ass/,'预览应使用 targetNames 命名')
      await apply().click()
      assert.equal(await panel().count(),0)
      const after = await snapshot()
      for (const r of after) {
        const n = Number(r.key.slice(1))
        assert.equal(r.videoId,n === 9 ? '' : `v${n}`,`${r.file.name} 应按数字自然排序配对`)
        assert.equal(r.selected,n !== 9,`${r.key} 应勾选已匹配项但保留手动清空`)
        assert.equal(r.language,'','默认不得设置语言')
      }
      assert.equal(after.filter(r=>r.videoId).length,34)
      await button('撤销').click()
      assert.deepEqual(await snapshot(),fixture.rows,'撤销应恢复应用前的完整组状态')
    })

    await run('缺失字幕集号阻止顺序，切回集号匹配后不发生错移', async()=>{
      const fixture = {rows:[row('one','Show.S01E01.ass'),row('three','Show.S01E03.ass')],videos:[video(1),video(2),video(3)]}
      await mount(fixture); await open(); await sequence()
      assert.match(await panel().getByRole('alert').allTextContents().then(a=>a.join('\n')),/改用按集号匹配/)
      await check('我已核对对应关系，确认按当前预览应用').check()
      await assertDisabled(apply(),'即使人工确认也不能绕过缺集阻止')
      assert.deepEqual(await snapshot(),fixture.rows)
      await panel().getByRole('button',{name:'按集号匹配',exact:true}).click()
      await assertEnabled(apply()); await apply().click()
      assert.deepEqual((await snapshot()).map(r=>r.videoId),['v1','v3'])
    })

    await run('目标视频缺集阻止顺序匹配，确认不能绕过', async()=>{
      const fixture = {rows:[row('one','opaqueasset1tail.ass'),row('two','opaqueasset2tail.ass')],videos:[video(1),video(3)]}
      await mount(fixture); await open(); await sequence()
      assert.match((await panel().getByRole('alert').allTextContents()).join('\n'),/目标视频存在缺集.*按集号/)
      await check('我已核对对应关系，确认按当前预览应用').check()
      await assertDisabled(apply()); assert.deepEqual(await snapshot(),fixture.rows)
    })

    await run('同集多个视频版本和已有映射顺序冲突均被阻止', async()=>{
      for (const fixture of [
        {rows:[row('one','opaqueasset1tail.ass')],videos:[video(1),video(1,1,'alternate')]},
        {rows:[row('one','opaqueasset1tail.ass',{videoId:'v2'}),row('two','opaqueasset2tail.ass')],videos:[video(1),video(2)]},
      ]) {
        await mount(fixture); await open(); await sequence()
        assert((await panel().getByRole('alert').count())>0,'冲突必须显示错误')
        await check('我已核对对应关系，确认按当前预览应用').check()
        await assertDisabled(apply()); assert.deepEqual(await snapshot(),fixture.rows)
      }
    })

    await run('已知重复字幕集号不可按顺序套用', async()=>{
      const fixture = {rows:[row('first','Alpha.S01E01.ass'),row('duplicate','Beta.S01E01.ass')],videos:[video(1),video(2)]}
      await mount(fixture); await open(); await sequence()
      assert.match((await panel().getByRole('alert').allTextContents()).join('\n'),/重复.*按集号/)
      await check('我已核对对应关系，确认按当前预览应用').check()
      await assertDisabled(apply()); assert.deepEqual(await snapshot(),fixture.rows)
    })

    await run('发布编号与目标编号歧义保留未匹配，不盲选目标集号', async()=>{
      const fixture = {rows:[row('ambiguous','27.sc.ass')],videos:[
        {id:'offset',name:'[Group] Show - 27 [1080p].mkv',season:2,episode:1},
        {id:'target',name:'[Group] Show - 53 [1080p].mkv',season:2,episode:27},
      ]}
      await mount(fixture); await open()
      await assertDisabled(apply(),'两种编号解释指向不同视频时不得直接匹配')
      assert.match((await panel().locator('.batch-preview-row').allInnerTexts()).join('\n'),/保持未匹配/)
      assert.deepEqual(await snapshot(),fixture.rows)
      await panel().getByRole('button',{name:'取消',exact:true}).click()
    })

    await run('不同来源的重复目标文件冲突被阻止', async()=>{
      const fixture = {rows:[row('new','Show.S01E01.ass'),row('existing','Show.S01E01.ass',{file:{name:'Show.S01E01.ass',sourceKey:'archive-b'},videoId:'v1',selected:true})],videos:[video(1)]}
      await mount(fixture); await open()
      const incoming = (await selectOptions(combo('批量操作字幕组'))).find(option=>option.label.includes('压缩包 A'))
      assert(incoming,'显式选择待匹配来源，不能依赖已匹配项排序后的默认组')
      await selectOption(combo('批量操作字幕组'),incoming.value)
      assert.match((await panel().getByRole('alert').allTextContents()).join('\n'),/重复.*目标文件/)
      await assertDisabled(apply()); assert.deepEqual(await snapshot(),fixture.rows)
    })

    await run('来源 sourceKey、目录、语言、版本分别分组，同名来源不串组', async()=>{
      const fixture = {rows:[
        row('a','same/Show.S01E01.ass'),
        row('b','same/Show.S01E01.ass',{file:{name:'same/Show.S01E01.ass',sourceKey:'archive-b',sourceLabel:'压缩包 A'}}),
        row('folder','other/Show.S01E01.ass'),
        row('language','same/Show.S01E01.ass',{language:'en'}),
        row('version','same/Show.S01E01.ass',{version:'导演版'}),
      ],videos:[video(1)]}
      await mount(fixture); await open()
      const options = await selectOptions(combo('批量操作字幕组'))
      assert.equal(options.length,5,'四种分组维度均须独立，不能只按来源显示名分组')
      assert.equal(new Set(options.map(o=>o.value)).size,5)
      for (const option of options) {
        await selectOption(combo('批量操作字幕组'),option.value)
        assert.equal(await panel().locator('.batch-preview-row').count(),1)
      }
      const sameUnknown = options.find(option=>/same.*未指定/.test(option.label) && !option.label.includes('导演版'))
      assert(sameUnknown,'选择同目录、未指定语言和版本的首个来源组')
      await selectOption(combo('批量操作字幕组'),sameUnknown.value)
      await apply().click()
      assert.equal((await snapshot())[0].videoId,'v1')
      await open()
      await task('设置语言').click()
      await selectOption(combo('批量操作字幕组'),sameUnknown.value)
      await selectOption(combo('批量设置字幕语言'),'zh-CN')
      await apply().click()
      const after = await snapshot()
      assert.equal(after[0].videoId,'v1'); assert.equal(after[0].language,'zh-CN'); assert.equal(after[0].selected,true)
      assert.deepEqual(after.slice(1),fixture.rows.slice(1),'应用只可影响所选来源组')
    })

    await run('仅设置语言可跨页应用，默认只填未指定并可覆盖选定已知语言组', async()=>{
      const fixture = sequenceFixture()
      fixture.rows.push(row('known','Known.S01E01.ass',{language:'en',videoId:'v1'}))
      await mount(fixture); await open()
      await task('设置语言').click()
      const unknown = (await selectOptions(combo('批量操作字幕组'))).find(option=>option.label.includes('未知目录') && option.label.includes('未指定'))
      assert(unknown,'显式选择覆盖全部 35 个字幕的未指定语言组')
      await selectOption(combo('批量操作字幕组'),unknown.value)
      await selectOption(combo('批量设置字幕语言'),'zh-CN')
      assert(await check('仅填写未指定语言').isChecked())
      assert.equal(await combo('批量匹配目标季').count(),0)
      assert.equal(await combo('批量匹配起始视频').count(),0)
      await check('同时勾选本组已匹配项').uncheck()
      await apply().click()
      const after = await snapshot()
      after.slice(0,35).forEach((r,i)=>{
        assert.equal(r.language,'zh-CN')
        assert.equal(r.videoId,fixture.rows[i].videoId,'仅语言模式不得新增或改变对应关系')
        assert.equal(r.selected,fixture.rows[i].selected,'关闭自动勾选须保留选择')
      })
      assert.deepEqual(after[35],fixture.rows[35],'默认不得修改其他语言组')
      await open(); await task('设置语言').click()
      const options = await selectOptions(combo('批量操作字幕组'))
      const known = options.find(o=>o.label.includes('英文'))
      assert(known,'应有独立英文组')
      await selectOption(combo('批量操作字幕组'),known.value)
      await selectOption(combo('批量设置字幕语言'),'zh-CN')
      await check('同时勾选本组已匹配项').uncheck()
      await assertDisabled(apply(),'只填未指定时，已有语言组不应产生变更')
      await check('仅填写未指定语言').uncheck()
      await assertEnabled(apply()); await apply().click()
      assert.equal((await snapshot())[35].language,'zh-CN')
    })

    await run('撤销批量操作不覆盖之后通过真实下拉控件手动编辑的语言', async()=>{
      const fixture = {rows:[row('one','Show.S01E01.ass'),row('two','Show.S01E02.ass'),row('three','Show.S01E03.ass')],videos:[video(1),video(2),video(3)]}
      await mount(fixture); await open()
      await apply().click()
      const beforeLanguage = await snapshot()
      await open(); await task('设置语言').click()
      await selectOption(combo('批量设置字幕语言'),'zh-CN')
      await apply().click()
      await button('编辑字幕 Show.S01E01.ass').click()
      await selectOption(combo('Show.S01E01.ass 语言'),'en')
      const edited = (await snapshot())[0]
      await button('撤销').click()
      const after = await snapshot()
      assert.deepEqual(after[0],edited,'后续手动语言编辑必须完整保留')
      assert.deepEqual(after.slice(1),beforeLanguage.slice(1),'其他未经手动编辑项应完整恢复到本次语言任务之前')
      assert.equal(await button('撤销').count(),0)
    })

    await run('多季必须明确选择目标季，按编辑后的季集匹配而非原名猜测', async()=>{
      const fixture = {rows:[row('one','Show.S02E01.ass')],videos:[video(1,1,'s1'),video(1,2,'s2')]}
      await mount(fixture); await open()
      assert.equal(await inputValue(combo('批量匹配目标季')),'')
      await assertDisabled(apply())
      await selectOption(combo('批量匹配目标季'),'2')
      await assertEnabled(apply()); await apply().click()
      assert.equal((await snapshot())[0].videoId,'s2')
    })

    await run('电影默认设置语言，不暴露集号或顺序匹配，也不改变视频映射', async()=>{
      const fixture = {kind:'movie',rows:[row('movie','Film.ass',{videoId:'film'}),row('extra','Extra.srt')],videos:[{id:'film',name:'Film.2026.mkv'}]}
      await mount(fixture); await open()
      for (const name of ['按集号匹配','按顺序匹配']) assert.equal(await panel().getByRole('button',{name,exact:true}).count(),0)
      for (const name of ['批量匹配目标季','批量匹配起始视频']) assert.equal(await combo(name).count(),0)
      await selectOption(combo('批量设置字幕语言'),'zh-CN')
      await apply().click()
      assert.deepEqual((await snapshot()).map(r=>({videoId:r.videoId,language:r.language})),[{videoId:'film',language:'zh-CN'},{videoId:'',language:'zh-CN'}])
      await button('撤销').click()
      assert.deepEqual(await snapshot(),fixture.rows)
    })

    await run('字幕库和本地版本任务校验路径符号，命名包含 HKG/KTXP 且可完整撤销', async()=>{
      for (const [source, version] of [['local','HKG'],['library','KTXP']]) {
        const fixture = {rows:[1,2].map(episode=>row(`r${episode}`,`Show.S01E0${episode}.ass`,{
          videoId:`v${episode}`,language:'zh-CN',selected:true,
          ...(source==='library'?{versionId:'library-version',path:`Show.S01E0${episode}.ass`}:{}),
        })),videos:[video(1),video(2)]}
        await mount(fixture); await open(); await task('版本标记').click()
        for (const name of ['批量匹配目标季','批量匹配起始视频','批量设置字幕语言']) assert.equal(await combo(name).count(),0,'版本任务只展示版本设置')
        const marker = panel().getByRole('textbox',{name:'批量设置版本标记',exact:true})
        await marker.fill('../HKG')
        assert.match((await panel().getByRole('alert').allTextContents()).join('\n'),/版本|路径/)
        await assertDisabled(apply(),'非法版本不得应用')
        assert.deepEqual(await snapshot(),fixture.rows)
        await marker.fill(version)
        assert.match((await panel().locator('.batch-preview-row').allInnerTexts()).join('\n'),new RegExp(`\\.${version}\\.zh-CN\\.ass`))
        await assertEnabled(apply()); await apply().click()
        const after = await snapshot()
        assert(after.every(item=>item.version===version))
        assert.deepEqual(after.map(item=>({...item,version:''})),fixture.rows,'版本任务不能改变视频、语言或选择')
        await button('撤销').click()
        assert.deepEqual(await snapshot(),fixture.rows)
      }
    })

    await run('移动端 320/390px 无横向溢出，桌面与移动端浅深主题截图', async()=>{
      const fixture = sequenceFixture()
      fixture.rows.forEach(r=>{r.file.name='非常长的目录与字幕来源用于验证移动端换行/'.repeat(2)+r.file.name})
      await mount(fixture); await open(); await sequence()
      for (const width of [320,390,1280]) {
        await page.setViewportSize({width,height:960})
        for (const theme of ['light','dark']) {
          await page.emulateMedia({colorScheme:theme,reducedMotion:'reduce'})
          await page.evaluate(async theme=>{
            document.documentElement.classList.toggle('dark',theme==='dark')
            await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame)
          },theme)
          await page.evaluate(()=>window.scrollTo(0,0))
          const dimensions = await page.evaluate(()=>({
            client:document.documentElement.clientWidth,scroll:document.documentElement.scrollWidth,
            body:document.body.scrollWidth,scheme:getComputedStyle(document.documentElement).colorScheme,
          }))
          assert(dimensions.scroll<=dimensions.client+1 && dimensions.body<=dimensions.client+1,`${width}px ${theme} 页面横向溢出：${JSON.stringify(dimensions)}`)
          assert.equal(dimensions.scheme,theme)
          // 同时打开真实下拉浮层验证定位，不用 overflow:hidden 掩盖页面溢出。
          const options = await selectOptions(combo('批量操作字幕组'))
          assert.equal(options.length,1)
          await combo('批量操作字幕组').click()
          const afterDropdown = await page.evaluate(()=>({client:document.documentElement.clientWidth,scroll:document.documentElement.scrollWidth}))
          assert(afterDropdown.scroll<=afterDropdown.client+1,'打开浮层不应撑宽页面')
          await combo('批量操作字幕组').press('Escape')
          await task('匹配视频').click()
          const file = path.join(artifacts,`batch-${width}-${theme}.png`)
          // 完整截取面板，移动端也能查看确认框与禁用的应用按钮，而非只截首屏。
          await panel().screenshot({path:file})
          screenshots.push({width,theme,file})
        }
      }
    })

    await run('无浏览器运行错误、无任何业务请求或外部网络请求', async()=>{
      assert.deepEqual(pageErrors,[],'真实组件运行时不能抛出异常')
      assert.deepEqual(consoleErrors,[],'浏览器控制台不能有错误')
      assert.deepEqual(blockedRequests,[],'即使已拦截，也不允许组件尝试发出业务请求')
    })
    const report = {passed,failed,screenshots,pageErrors,consoleErrors,blockedRequests,artifacts}
    await fs.writeFile(path.join(artifacts,'report.json'),JSON.stringify(report,null,2)+'\n','utf8')
    console.log(JSON.stringify(report,null,2))
    if (failed.length) process.exitCode = 1
  } finally { await browser.close() }
})().catch(error=>{console.error(error);process.exitCode=1})

