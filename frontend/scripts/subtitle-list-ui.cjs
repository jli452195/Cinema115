// 独立挂载真实字幕列表，仅使用内存测试数据，不访问网盘或提交整理任务。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { zipSync, strToU8 } = require('fflate')
const { selectOption } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], timings = []
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    page.setDefaultTimeout(15000)
    page.on('pageerror', error => errors.push(error.message))
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
        app = createApp({render:()=>h(SubtitleMatchList, {rows, videos, targetNames:{}})});
        app.mount('#app'); await nextTick(); await new Promise(requestAnimationFrame);
        return {count, mountMs:Math.round(performance.now()-start), renderedRows:document.querySelectorAll('.match-files>li').length, options:document.querySelectorAll('option').length};
      };
      window.mountPicker = async (videoCount) => {
        app?.unmount(); window.applied = null;
        const videos = Array.from({length:videoCount}, (_,i)=>({id:String(i+1), name:'YuYuHakusho.S01E'+String(i+1).padStart(3,'0')+'.mkv', season:1, episode:i+1}));
        app = createApp({render:()=>h(SubtitlePickerDialog, {token:'fixture', kind:'tv', animationLibrary:true, title:'幽游白书', videos, targetNames:{}, library:[], local:[], onSelect:(library, local)=>{ window.applied = {library, local:local.map(({file,videoId,language})=>({name:file.name,videoId,language,bytes:file.data.length}))}; }})});
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
      timings.push(result)
      await page.getByRole('button', {name:'选择当前语言全部已匹配项', exact:true}).click()
      assert.equal(await page.evaluate(() => window.fixture.rows.filter(row=>row.selected).length), count, '选择覆盖全部分页')
      await page.getByRole('button', {name:'清空选择', exact:true}).click()
      assert.equal(await page.evaluate(() => window.fixture.rows.filter(row=>row.selected).length), 0)
    }

    const version = page.getByRole('textbox', {name:'Show.S01E001.chs.ass 版本', exact:true})
    await version.fill('保留编辑')
    const start = Date.now()
    await page.getByRole('button', {name:'下一页', exact:true}).click()
    await page.getByRole('checkbox', {name:'选择字幕 Show.S01E013.cht.ass', exact:true}).waitFor()
    timings.push({changePageMs:Date.now()-start})
    await page.getByRole('button', {name:'上一页', exact:true}).click()
    assert.equal(await version.inputValue(), '保留编辑')
    await selectOption(page.getByRole('combobox', {name:'筛选字幕语言', exact:true}), 'zh-TW')
    await page.getByRole('button', {name:'选择当前语言全部已匹配项', exact:true}).click()
    assert.equal(await page.evaluate(() => window.fixture.rows.filter(row=>row.selected).length), 500)
    assert(await page.evaluate(() => window.fixture.rows.every(row=>row.selected === (row.language === 'zh-TW'))))
    assert.equal(await page.getByRole('textbox', {name:'字幕列表分页，当前页码，共 20 页，回车跳转', exact:true}).inputValue(), '1')

    // IDX 与 SUB 分处不同页，编辑和勾选仍须同步。
    await page.evaluate(async () => {
      await window.mountList(30, 30)
      const rows = window.fixture.rows
      rows[0].file.name='pair.idx'; rows[0].pairKey='pair'
      rows[29].file.name='pair.sub'; rows[29].pairKey='pair'
      await window.fixture.nextTick()
    })
    await page.getByRole('checkbox', {name:'选择字幕 pair.idx', exact:true}).check()
    await page.getByRole('textbox', {name:'pair.idx 版本', exact:true}).fill('配对')
    await page.getByRole('button', {name:'下一页', exact:true}).click()
    assert(await page.getByRole('checkbox', {name:'选择字幕 pair.sub', exact:true}).isChecked())
    assert.equal(await page.getByRole('textbox', {name:'pair.sub 版本', exact:true}).inputValue(), '配对')
    // 筛选及数据缩减后页码应收敛，不能停在空白页。
    await page.evaluate(async () => { window.fixture.rows.splice(1); await window.fixture.nextTick() })
    assert.equal(await page.locator('.match-files>li').count(), 1)
    assert.equal(await page.getByRole('navigation', {name:'字幕列表分页'}).count(), 0)

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
      await page.locator('.picker-footer').getByText(new RegExp(`已选 ${count} 个字幕`)).waitFor()
      assert.equal(await page.getByRole('alert').count(),0)
      assert.equal(await page.locator('.match-files>li').count(),25)
      await page.getByText(new RegExp(`共 ${count} 个字幕，0 个未匹配`)).waitFor()
      timings.push({zipSubtitleCount:count,importMatchRenderMs:Date.now()-started})
      const apply = page.getByRole('button', {name:'使用所选字幕',exact:true})
      assert(await apply.isEnabled())
      await apply.click()
      const applied = await page.evaluate(()=>window.applied)
      assert.deepEqual(applied.library,[])
      assert.deepEqual(applied.local,fixture.expected,'所有字幕的集号、语言及文件内容均应正确传出')
    }
    const oversized = archive(1001)
    await page.getByLabel('选择本地字幕或压缩包', {exact:true}).setInputFiles({name:'超过上限.zip',mimeType:'application/zip',buffer:oversized.buffer})
    await page.getByRole('alert').filter({hasText:'字幕数量超过 1000 个'}).waitFor()
    assert.equal(await page.locator('.match-files>li').count(),25)
    await page.locator('.picker-footer').getByText(/已选 1000 个字幕/).waitFor()
    await page.getByRole('button', {name:'使用所选字幕',exact:true}).click()
    assert.deepEqual(await page.evaluate(()=>window.applied.local),archive(1000).expected,'超限导入须完整保留此前选择')
    // 复现截图中的真实 ZIP 导入，再在完整选择器中批量设置语言并确认最终输出。
    await page.evaluate(()=>window.mountPicker(24))
    await page.getByText('没有找到匹配字幕',{exact:true}).waitFor()
    const screenshotFiles = {}
    for (let episode=1;episode<=24;episode++) screenshotFiles['Shangri-La 1-24 DVDRip Sub/[ReinForce] Shangri-La '+String(episode).padStart(2,'0')+' (DVDRip 852x480 x264 FLAC).ass'] = strToU8('[Script Info]\nTitle: Shangri-La\n')
    await page.getByLabel('选择本地字幕或压缩包',{exact:true}).setInputFiles({name:'Shangri-La.zip',mimeType:'application/zip',buffer:Buffer.from(zipSync(screenshotFiles))})
    await page.getByText(/共 24 个字幕，0 个未匹配/).waitFor()
    await page.getByRole('button',{name:'批量匹配 / 设置语言',exact:true}).click()
    await selectOption(page.getByRole('combobox',{name:'批量设置字幕语言',exact:true}),'zh-CN')
    await page.getByRole('button',{name:'应用批量设置',exact:true}).click()
    await page.getByRole('button',{name:'使用所选字幕',exact:true}).click()
    const screenshotApplied = await page.evaluate(()=>window.applied.local)
    assert.equal(screenshotApplied.length,24)
    assert(screenshotApplied.every((item,i)=>item.videoId===String(i+1) && item.language==='zh-CN'),'截图格式 24 集须自动识别并批量设置语言')

    // 在真实选择器中应用千文件顺序匹配，验证手动映射监听不会逐字段重算整组预览。
    await page.evaluate(()=>window.mountPicker(1000))
    await page.getByText('没有找到匹配字幕',{exact:true}).waitFor()
    const unknownFiles = {}
    for(let i=1;i<=1000;i++) unknownFiles['opaqueasset'+i+'tail.ass']=strToU8('[Script Info]\n')
    await page.getByLabel('选择本地字幕或压缩包',{exact:true}).setInputFiles({name:'unknown-1000.zip',mimeType:'application/zip',buffer:Buffer.from(zipSync(unknownFiles))})
    await page.getByText(/共 1000 个字幕，1000 个未匹配/).waitFor()
    await page.getByRole('button',{name:'批量匹配 / 设置语言',exact:true}).click()
    await page.getByRole('button',{name:'按顺序匹配',exact:true}).click()
    await selectOption(page.getByRole('combobox',{name:'批量匹配起始视频',exact:true}),'1')
    await page.getByRole('checkbox',{name:'我已核对对应关系，确认按当前预览应用',exact:true}).check()
    const batchStart=Date.now()
    await page.getByRole('button',{name:'应用批量设置',exact:true}).click()
    await page.getByText(/共 1000 个字幕，0 个未匹配/).waitFor()
    timings.push({batchSubtitleCount:1000,applyBatchMs:Date.now()-batchStart})
    await page.getByRole('button',{name:'撤销本次批量操作',exact:true}).click()
    await page.getByText(/共 1000 个字幕，1000 个未匹配/).waitFor()
    await page.getByRole('button',{name:'批量匹配 / 设置语言',exact:true}).click()
    await page.getByRole('button',{name:'按顺序匹配',exact:true}).click()
    await selectOption(page.getByRole('combobox',{name:'批量匹配起始视频',exact:true}),'1')
    await page.getByRole('checkbox',{name:'我已核对对应关系，确认按当前预览应用',exact:true}).check()
    await page.getByRole('button',{name:'应用批量设置',exact:true}).click()
    await page.getByText(/共 1000 个字幕，0 个未匹配/).waitFor()
    await page.getByRole('button',{name:'使用所选字幕',exact:true}).click()
    const batchApplied=await page.evaluate(()=>window.applied.local)
    assert.equal(batchApplied.length,1000)
    assert(batchApplied.every(item=>item.videoId===item.name.match(/opaqueasset(\d+)tail/)[1]),'千文件批量匹配须自然排序并完整传出')
    assert.equal(requests.length,10,'仅有五次挂载时的两个只读搜索请求')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({passed:['224/1000 字幕有界渲染','跨页全选与清空','编辑保留','语言筛选跨页选择','IDX/SUB 跨页联动','列表缩减页码修正','112/224/1000 真实 ZIP 经 Worker 导入匹配并逐项确认','1001 个 ZIP 拒绝且保留此前列表','截图格式 24 集真实导入自动匹配并统一语言','完整选择器 1000 个未知字幕批量顺序匹配、撤销重配和最终传出'], timings}, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
