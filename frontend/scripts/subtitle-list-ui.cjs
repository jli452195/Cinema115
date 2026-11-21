// 独立挂载真实字幕列表，仅使用内存测试数据，不访问网盘或提交整理任务。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { zipSync, strToU8 } = require('fflate')
const { selectOption } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = new URL(process.env.CINEMA_URL || 'http://127.0.0.1:5173').origin

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true, args: ['--disable-features=LocalNetworkAccessChecks'] })
  const errors = [], consoleErrors = [], blockedRequests = [], timings = []
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    page.setDefaultTimeout(15000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
    // 仅放行 Vite 静态资源；下方夹具和只读搜索路由优先处理，其他请求一律阻断。
    await page.route('**/*', route => {
      const request = route.request(), url = new URL(request.url())
      if (url.origin === origin && request.method() === 'GET' && /^\/(?:src\/|node_modules\/|@vite\/|@id\/|@fs\/)/.test(url.pathname)) return route.continue()
      blockedRequests.push({method:request.method(),url:request.url()})
      return route.abort('blockedbyclient')
    })
    const button = name => page.getByRole('button', {name, exact:true})
    async function edit(name) {
      const trigger = button(`编辑字幕 ${name}`)
      if (await trigger.getAttribute('aria-expanded') !== 'true') await trigger.click()
      assert.equal(await page.locator('.match-editor').count(),1,'列表同时只展开一行编辑器')
    }
    const waitMatched = (matched, total = matched) => page.locator('.picker-stats > span').filter({hasText:new RegExp(`已匹配\\s*${matched}\\s*/\\s*${total}$`)}).waitFor()
    async function waitSelected(count) {
      const selection = page.locator('.picker-selection > span').filter({hasText:new RegExp(`已选\\s*${count}\\s*个`)})
      try { await selection.waitFor() }
      catch (error) {
        const pickerState = await page.locator('.picker-stats, .picker-footer, .picker-error, .picker-note').allTextContents()
        throw new Error(`未确认已选 ${count} 个字幕：${JSON.stringify({pickerState, errors, consoleErrors, blockedRequests})}`, {cause:error})
      }
    }
    await page.route('**/__subtitle-list-test', route => route.fulfill({ contentType: 'text/html', body: `<!doctype html><html lang="zh-CN"><head><link rel="stylesheet" href="/src/style.css"></head><body><div id="app"></div><script type="module">
      import { createApp, h, reactive, nextTick } from '/node_modules/.vite/deps/vue.js';
      import SubtitleMatchList from '/src/components/SubtitleMatchList.vue';
      import SubtitlePickerDialog from '/src/components/SubtitlePickerDialog.vue';
      let app;
      window.mountList = async (count, videoCount = 1000) => {
        app?.unmount();
        const videos = Array.from({length: videoCount}, (_, i) => ({id:String(i+1), name:'Show.S01E'+String(i+1).padStart(3,'0')+'.mkv'}));
        const rows = reactive(Array.from({length:count}, (_,i) => ({key:String(i), file:{name:'Show.S01E'+String(Math.floor(i/2)+1).padStart(3,'0')+(i%2?'.cht':'.chs')+'.ass'}, videoId:String(Math.floor(i/2)+1), language:i%2?'zh-TW':'zh-CN', version:'', selected:false, reason:''})));
        window.fixture = { rows, videos, nextTick };
        const start = performance.now();
        const stem = 'Show.S01E001.1080p.[zh-CN+zh-TW].softsub';
        app = createApp({render:()=>h(SubtitleMatchList, {rows, videos, targets:{'1':{name:stem+'.mkv',stem}}})});
        app.mount('#app'); await nextTick(); await new Promise(requestAnimationFrame);
        return {count, mountMs:Math.round(performance.now()-start), renderedRows:document.querySelectorAll('.match-files>li').length, options:document.querySelectorAll('option').length};
      };
      window.mountPicker = async (videoCount) => {
        app?.unmount(); window.applied = null;
        const videos = Array.from({length:videoCount}, (_,i)=>({id:String(i+1), name:'YuYuHakusho.S01E'+String(i+1).padStart(3,'0')+'.mkv', season:1, episode:i+1}));
        app = createApp({render:()=>h(SubtitlePickerDialog, {token:'fixture', kind:'tv', animationLibrary:true, title:'幽游白书', videos, targets:{}, library:[], local:[], onSelect:(library, local)=>{ window.applied = {library, local:local.map(({file,videoId,language})=>({name:file.name,videoId,language,bytes:file.data.length}))}; }})});
        app.mount('#app'); await nextTick();
      };
      window.ready = true;
    </script></body></html>` }))
    await page.goto(`${origin}/__subtitle-list-test`)
    await page.waitForFunction(() => window.ready)

    for (const count of [224, 1000]) {
      const result = await page.evaluate(count => window.mountList(count), count)
      assert.equal(result.renderedRows, 25)
      assert(result.options < 26000, '每页最多渲染 25 组视频选项')
      assert.equal(await page.locator('.match-editor').count(),0,'默认紧凑行不挂载编辑控件')
      assert.equal(await page.getByRole('combobox').count(),1,'默认只展示语言筛选，避免为每行创建视频选项')
      assert.equal(await page.getByText('整理后：Show.S01E001.1080p.[zh-CN+zh-TW].softsub.zh-CN.ass', {exact:true}).count(), 1)
      timings.push(result)
      await button('全选已匹配').click()
      assert.equal(await page.evaluate(() => window.fixture.rows.filter(row=>row.selected).length), count, '选择覆盖全部分页')
      await button('取消全选').click()
      assert.equal(await page.evaluate(() => window.fixture.rows.filter(row=>row.selected).length), 0)
    }

    const version = page.getByRole('textbox', {name:'Show.S01E001.chs.ass 版本', exact:true})
    await edit('Show.S01E001.chs.ass')
    await version.fill('保留编辑')
    const start = Date.now()
    await page.getByRole('button', {name:'下一页', exact:true}).click()
    await page.getByRole('checkbox', {name:'选择字幕 Show.S01E013.cht.ass', exact:true}).waitFor()
    timings.push({changePageMs:Date.now()-start})
    await page.getByRole('button', {name:'上一页', exact:true}).click()
    assert.equal(await version.count(),0,'分页返回后编辑器保持收起')
    await edit('Show.S01E001.chs.ass')
    assert.equal(await version.inputValue(), '保留编辑')
    await selectOption(page.getByRole('combobox', {name:'筛选字幕语言', exact:true}), 'zh-TW')
    await button('全选已匹配').click()
    assert.equal(await page.evaluate(() => window.fixture.rows.filter(row=>row.selected).length), 500)
    assert(await page.evaluate(() => window.fixture.rows.every(row=>row.selected === (row.language === 'zh-TW'))))
    assert.equal(await page.getByRole('textbox', {name:'字幕列表分页，当前页码，共 20 页，回车跳转', exact:true}).inputValue(), '1')

    // IDX 与 SUB 分处不同页，编辑和勾选仍须同步。
    await page.evaluate(async () => {
      await window.mountList(30, 30)
      const rows = window.fixture.rows
      // 同一视频内把配套文件放在第 25/26 个位置，联动排序后仍横跨两页。
      rows.forEach((row,i)=>{ row.videoId='1'; row.language='zh-CN'; row.file.name=(i<24?'AA-':'ZZ-')+String(i).padStart(2,'0')+'.ass' })
      rows[24].file.name='pair.idx'; rows[24].pairKey='pair'
      rows[25].file.name='pair.sub'; rows[25].pairKey='pair'
      await window.fixture.nextTick()
    })
    await page.getByRole('checkbox', {name:'选择字幕 pair.idx', exact:true}).check()
    await edit('pair.idx')
    await page.getByRole('textbox', {name:'pair.idx 版本', exact:true}).fill('配对')
    await page.getByRole('button', {name:'下一页', exact:true}).click()
    assert(await page.getByRole('checkbox', {name:'选择字幕 pair.sub', exact:true}).isChecked())
    await edit('pair.sub')
    assert.equal(await page.getByRole('textbox', {name:'pair.sub 版本', exact:true}).inputValue(), '配对')
    // 筛选及数据缩减后页码应收敛，不能停在空白页。
    await page.evaluate(async () => { window.fixture.rows.splice(1); await window.fixture.nextTick() })
    assert.equal(await page.locator('.match-files>li').count(), 1)
    assert.equal(await page.getByRole('navigation', {name:'字幕列表分页'}).count(), 0)
    console.log('PASS 224/1000 条紧凑列表、跨页编辑和选择、IDX/SUB 联动及页码收敛')

    // 完整选择器使用真实 Worker 解压及匹配，仅搜索接口返回空结果。
    const requests = []
    await page.route('**/api/**', async route => {
      const request = route.request(), pathname = new URL(request.url()).pathname
      requests.push({method:request.method(), pathname})
      assert.equal(request.method(), 'GET')
      assert(['/api/admin/subtitles/search', '/api/admin/subtitles/assrt/search'].includes(pathname))
      await route.fulfill({json:pathname.endsWith('/assrt/search') ? {configured:false,items:[],hasMore:false} : {versions:[]}})
    })
    const archive = (count, bilingual = false) => {
      const files = {}, expected = []
      for (let i=0; i<count; i++) {
        const episode = bilingual ? Math.floor(i/2)+1 : i+1, tag = bilingual && i%2 ? 'cht' : 'chs'
        const name = `YuYuHakusho.S01E${String(episode).padStart(3,'0')}.${tag}.ass`
        files[name] = strToU8(`[Script Info]\nTitle: 幽游白书 第 ${episode} 集\n`)
        expected.push({name,videoId:String(episode),language:tag==='cht'?'zh-TW':'zh-CN',bytes:files[name].length})
      }
      return {buffer:Buffer.from(zipSync(files)), expected}
    }
    for (const [count,bilingual] of [[112,false],[224,true],[1000,false]]) {
      await page.evaluate(count => window.mountPicker(count), bilingual ? count/2 : count)
      await page.getByText('没有找到匹配字幕', {exact:true}).waitFor()
      const fixture = archive(count,bilingual), started = Date.now()
      await page.getByLabel('选择本地字幕或压缩包', {exact:true}).setInputFiles({name:`幽游白书-${count}.zip`,mimeType:'application/zip',buffer:fixture.buffer})
      await waitSelected(count)
      assert.equal(await page.getByRole('alert').count(),0)
      assert.equal(await page.locator('.match-files>li').count(),25)
      await waitMatched(count)
      timings.push({zipSubtitleCount:count,importMatchRenderMs:Date.now()-started})
      const apply = button('使用字幕')
      assert(await apply.isEnabled())
      await apply.click()
      const applied = await page.evaluate(()=>window.applied)
      assert.deepEqual(applied.library,[])
      assert.deepEqual(applied.local,fixture.expected,'所有字幕的集号、语言及文件内容均应正确传出')
      console.log(`PASS ${count} 个字幕经真实 Worker ZIP 导入、匹配和完整传出`)
    }
    const oversized = archive(1001)
    await page.getByLabel('选择本地字幕或压缩包', {exact:true}).setInputFiles({name:'超过上限.zip',mimeType:'application/zip',buffer:oversized.buffer})
    await page.getByRole('alert').filter({hasText:'字幕数量超过 1000 个'}).waitFor()
    assert.equal(await page.locator('.match-files>li').count(),25)
    await waitSelected(1000)
    await button('使用字幕').click()
    assert.deepEqual(await page.evaluate(()=>window.applied.local),archive(1000).expected,'超限导入须完整保留此前选择')
    console.log('PASS 1001 个字幕拒绝导入并保留原有 1000 个字幕')
    // 复现截图中的真实 ZIP 导入，检查裸集号匹配及提交的字幕。
    await page.evaluate(()=>window.mountPicker(24))
    await page.getByText('没有找到匹配字幕',{exact:true}).waitFor()
    const screenshotFiles = {}
    for (let episode=1;episode<=24;episode++) screenshotFiles['Shangri-La 1-24 DVDRip Sub/[ReinForce] Shangri-La '+String(episode).padStart(2,'0')+' (DVDRip 852x480 x264 FLAC).ass'] = strToU8('[Script Info]\nTitle: Shangri-La\n')
    await page.getByLabel('选择本地字幕或压缩包',{exact:true}).setInputFiles({name:'Shangri-La.zip',mimeType:'application/zip',buffer:Buffer.from(zipSync(screenshotFiles))})
    await waitMatched(24)
    await button('使用字幕').click()
    const screenshotApplied = (await page.evaluate(()=>window.applied.local)).sort((a, b) => Number(a.videoId) - Number(b.videoId))
    assert.equal(screenshotApplied.length,24)
    assert(screenshotApplied.every((item,i)=>item.videoId===String(i+1) && item.language===''),'截图格式 24 集须自动识别，语言保持未指定')
    console.log('PASS 截图格式 24 集经 ZIP 导入、自动匹配和完整传出')
    assert.equal(requests.length,8,'每次挂载仅发出两个只读搜索请求')
    assert.deepEqual(errors, [])
    assert.deepEqual(consoleErrors, [],'浏览器控制台不能有错误')
    assert.deepEqual(blockedRequests, [],'组件不能尝试业务写请求或外部网络请求')
    console.log(JSON.stringify({passed:['224/1000 字幕有界渲染','跨页全选与清空','编辑保留','语言筛选跨页选择','IDX/SUB 跨页联动','列表缩减页码修正','112/224/1000 真实 ZIP 经 Worker 导入匹配并逐项确认','1001 个 ZIP 拒绝且保留此前列表','截图格式 24 集真实导入自动匹配'], timings}, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
