// 使用真实字幕弹窗与内存夹具，验证版本选择、取消保护和响应式布局，不发起整理任务。
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { selectOption } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = new URL(process.env.CINEMA_URL || 'http://127.0.0.1:5173').origin
const continuousVersion = {id:'coalgirls',title:'轮回的拉格朗日',source:'字幕库夹具',name:'CoalGirls 01–24',url:'https://example.invalid/subtitles'}
const continuousFiles = Array.from({length:24}, (_, index) => {
  const episode = String(index + 1).padStart(2, '0')
  const name = `[Coalgirls]_Rinne_no_Lagrange_${episode}_(1920x1080_Blu-Ray_FLAC)_[108C51C3].DHR.tc.ass`
  return {name,path:'字幕目录/'+name,sha:'c'.repeat(40),revision:'fixture',size:128}
})
const harness = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/src/style.css"></head><body><div id="app"></div><script type="module">
import { createApp, h, nextTick } from '/node_modules/.vite/deps/vue.js';
import Picker from '/src/components/SubtitlePickerDialog.vue';
let app;
window.mountDesign = async (options = {}) => {
  app?.unmount(); window.applied = null; window.fixtureCloseCount = 0;
  const season = options.continuous && !options.multiSeason ? 2 : 1;
  const title = options.continuous ? '轮回的拉格朗日' : '青春CUP';
  const release = options.continuous ? 'Rinne.no.Lagrange' : 'Chu-Bra!!';
  const videos = Array.from({length:12}, (_,i)=>({id:'v'+(i+1),name:release+'.S0'+season+'E'+String(i+1).padStart(2,'0')+'.1080p.H264.AAC.mkv',season,episode:i+1}));
  if (options.multiSeason) videos.push(...videos.map(video=>({...video,id:'s2-'+video.id,name:video.name.replace('S01E','S02E'),season:2})));
  const library = options.library || videos.flatMap((video,i)=>['HKG','KTXP'].filter(hint=>!(options.missing && hint==='KTXP' && i===4)).map(hint=>{
    const name='ちゅーぶら！！ 第'+String(i+1).padStart(2,'0')+'話 「おとなパンツ！の女の子」 (BD 1920x1080 x264 AAC).'+hint+'.ass';
    return {versionId:'chu-bra',path:'字幕目录/'+name,file:{path:'字幕目录/'+name,name,sha:'a'.repeat(40),revision:'fixture',size:128},videoId:video.id,language:'',version:''};
  }));
  if (options.extra) library.push({versionId:'another-source',path:'Other.en.srt',file:{name:'Other.en.srt',path:'Other.en.srt',sha:'b'.repeat(40),revision:'fixture',size:20},videoId:'v1',language:'en',version:''});
  window.fixture = {videos, library};
  const targets = Object.fromEntries(videos.map(v=>[v.id,{name:v.name,stem:v.name.slice(0,-4)}]));
  const seasons = options.continuous ? [{number:1,episodeCount:12},{number:2,episodeCount:12}] : undefined;
  const version = options.continuous ? undefined : {id:'chu-bra',title,name:'BD 字幕合集',url:'https://example.invalid/subtitles',description:'HKG / KTXP 文件名标记，未指定语言。'};
  app = createApp({render:()=>h(Picker,{token:'fixture',kind:'tv',animationLibrary:true,title,videos,seasons,targets,matching:options.matching,library:options.empty?[]:library,local:[],version,onSelect:(library,local,version,matching)=>window.applied={library,local,matching},onClose:()=>window.fixtureCloseCount++})});
  app.mount('#app'); await nextTick(); await new Promise(requestAnimationFrame);
}; window.designReady=true;
</script></body></html>`

;(async () => {
  const artifacts = process.env.SUBTITLE_DESIGN_ARTIFACT_DIR || await fs.mkdtemp(path.join(os.tmpdir(), 'cinema-subtitle-design-'))
  const browser = await chromium.launch({executablePath:process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
  const errors = [], passed = []
  try {
    const page = await browser.newPage({viewport:{width:980,height:660},serviceWorkers:'block',reducedMotion:'reduce'})
    page.setDefaultTimeout(10000); page.on('pageerror',error=>errors.push(error.message))
    await page.route('**/*', async route => {
      const request=route.request(),url=new URL(request.url())
      if(url.origin===origin && url.pathname==='/__subtitle-design-test') return route.fulfill({contentType:'text/html; charset=utf-8',body:harness})
      if(url.origin===origin && request.method()==='GET' && /\/api\/admin\/subtitles\/assrt\/search/.test(url.pathname)) return route.fulfill({json:{configured:false,items:[],hasMore:false}})
      if(url.origin===origin && request.method()==='GET' && url.pathname==='/api/admin/subtitles/search') return route.fulfill({json:{versions:url.searchParams.get('q')===continuousVersion.title?[continuousVersion]:[]}})
      if(url.origin===origin && request.method()==='GET' && url.pathname==='/api/admin/subtitles/versions/coalgirls/files') return route.fulfill({json:{files:continuousFiles}})
      if(url.origin===origin && request.method()==='GET' && /^\/(?:src\/|node_modules\/|@vite\/|@id\/|@fs\/)/.test(url.pathname)) return route.continue()
      return route.abort('blockedbyclient')
    })
    await page.goto(origin+'/__subtitle-design-test'); await page.waitForFunction(()=>window.designReady)
    const button=name=>page.getByRole('button',{name,exact:true})
    const mount=options=>page.evaluate(options=>window.mountDesign(options),options || {})
    const selected=()=>page.locator('.match-files input[type=checkbox]:checked').count()
    const searchInput=page.getByRole('textbox',{name:'字幕作品搜索词',exact:true})
    async function setVersion(name, value) {
      await button('编辑字幕 '+name).click()
      await page.getByRole('textbox',{name:name+' 版本',exact:true}).fill(value)
    }
    async function layout(label) {
      const result=await page.evaluate(()=>{
        const dialog=document.querySelector('dialog[open]'),body=dialog.querySelector('.floating-dialog-body'),footer=dialog.querySelector('.floating-dialog-footer'),primary=dialog.querySelectorAll('.picker-apply');
        const scrolls=[...dialog.querySelectorAll('*')].filter(el=>['auto','scroll'].includes(getComputedStyle(el).overflowY) && el.scrollHeight>el.clientHeight+2 && !el.closest('.select-menu-panel'));
        return {width:body.scrollWidth-body.clientWidth,document:document.documentElement.scrollWidth-innerWidth,primary:primary.length,bottom:primary[0]?.getBoundingClientRect().bottom,height:innerHeight,scrolls:scrolls.map(el=>el.className),fixed:footer.getBoundingClientRect().height+dialog.querySelector('header').getBoundingClientRect().height};
      })
      assert(result.width<=1, label+' 正文横向溢出 '+JSON.stringify(result)); assert(result.document<=1,label+' 页面横向溢出')
      assert.equal(result.primary,1,label+' 主按钮唯一'); assert(result.bottom<=result.height,label+' 底部操作可见')
      assert(result.scrolls.length<=1 && result.scrolls.every(x=>x.includes('floating-dialog-body')),label+' 仅正文滚动 '+JSON.stringify(result))
      if((await page.viewportSize()).width<=390) assert(result.fixed<=185,label+' 固定区域过高 '+result.fixed)
      await page.screenshot({path:path.join(artifacts,label+'.png')})
    }
    await mount({extra:true})
    console.log('截图目录：'+artifacts)
    await layout('initial-select')
    assert.equal(await page.getByRole('textbox',{name:'字幕作品搜索词'}).count(),0,'重新打开直接展示列表')
    assert.equal(await page.locator('.match-editor').count(),0,'默认收起编辑器')
    assert.deepEqual(await page.locator('.match-filters > button').allTextContents(),['全选已匹配','取消全选'])
    assert.equal(await button('使用字幕').isDisabled(),true)
    assert.equal(await page.locator('.match-conflicts').count(),1)
    await page.getByRole('button',{name:/^仅选 HKG /}).click()
    assert.equal(await selected(),13,'仅选版本保留其他来源')
    await button('使用字幕').click()
    const applied=await page.evaluate(()=>window.applied)
    assert.equal(applied.library.length,13);assert(applied.library.every(row=>row.version==='' && (row.versionId==='another-source' || /HKG/.test(row.file.name))))
    await button('撤销').click(); assert.equal(await selected(),25)
    passed.push('整版选择、来源隔离、撤销')

    const firstEpisode=await page.evaluate(()=>window.fixture.library.filter(row=>row.videoId==='v1'))
    await mount({library:firstEpisode})
    assert.equal(await button('使用字幕').isDisabled(),true)
    const ktxp=firstEpisode.find(row=>row.file.name.endsWith('.KTXP.ass'))
    await setVersion(ktxp.file.name,'KTXP')
    const editedRow=page.locator('.match-files>li').filter({has:button('编辑字幕 '+ktxp.file.name)})
    await editedRow.getByText('查看输出与来源',{exact:true}).click()
    assert.match(await editedRow.locator('.match-target').innerText(),/\.KTXP\.ass$/)
    assert.equal(await button('使用字幕').isEnabled(),true,'逐条版本标记解决重名')
    await button('使用字幕').click()
    const edited=await page.evaluate(()=>window.applied.library)
    assert.equal(edited.length,3)
    assert.equal(edited.find(row=>row.path===ktxp.path).version,'KTXP')
    assert.equal(edited.find(row=>row.versionId==='another-source').version,'')
    await mount({library:edited}); assert.equal(await button('使用字幕').isEnabled(),true)
    passed.push('逐条版本编辑、输出命名、提交及重新打开')
    await mount({missing:true});assert.match(await page.locator('.variant-missing').innerText(),/KTXP.*S01E05/)
    await page.getByRole('button',{name:/^仅选 KTXP /}).click();assert.equal(await selected(),11)
    await button('撤销').click();assert.equal(await selected(),23)
    passed.push('不完整版本覆盖与缺集提示')
    await mount()
    const paired = await page.evaluate(() => window.fixture.library.slice(0, 2).flatMap(item => ['idx', 'sub'].map(ext => {
      const path = item.path.replace(/\.ass$/, '.' + ext).replace(ext === 'sub' ? /\.(HKG|KTXP)\.sub$/ : /$^/, (_, hint) => '.' + hint.toLowerCase() + '.sub')
      return { ...item, path, file: { ...item.file, path, name: path.split('/').pop() } }
    })))
    await mount({library:paired})
    await page.getByRole('button',{name:/^仅选 HKG /}).click();assert.equal(await selected(),2,'大小写不同的配对一并选取')
    await button('使用字幕').click();assert.equal((await page.evaluate(()=>window.applied.library)).length,2)
    await button('撤销').click()
    await setVersion(paired.find(row=>/\.HKG\.idx$/.test(row.path)).file.name,'HKG')
    await setVersion(paired.find(row=>/\.KTXP\.idx$/.test(row.path)).file.name,'KTXP')
    await button('使用字幕').click()
    const pairResult=await page.evaluate(()=>window.applied.library)
    assert.deepEqual(pairResult.map(row=>row.version).sort(),['HKG','HKG','KTXP','KTXP'])
    await mount({library:pairResult.map(row=>({...row,language:'zh-CN'}))})
    const pairName=pairResult[0].file.name
    const pairLanguage=page.getByRole('combobox',{name:pairName+' 语言',exact:true})
    await button('取消全选').click()
    await button('编辑字幕 '+pairName).click();await selectOption(pairLanguage,'zh-CN')
    await button('撤销').click();assert.equal(await selected(),4,'重复选择当前语言后仍可撤销')
    await button('取消全选').click();await selectOption(page.getByRole('combobox',{name:'筛选字幕语言',exact:true}),'zh-CN')
    await button('编辑字幕 '+pairName).click();await selectOption(pairLanguage,'zh-TW')
    await button('全选已匹配').click();await button('使用字幕').click()
    assert.equal((await page.evaluate(()=>window.applied.library)).length,4,'语言筛选后全选仍带上隐藏的配对文件')
    passed.push('IDX/SUB 大小写版本标记、成对写入、筛选后整对选择')
    const directoryFixture = Array.from({length:1000},(_,i) => {
      const hint=i%2?'KTXP':'HKG',name='Show.S01E01.'+hint+'.ass',path='directory-'+Math.floor(i/2)+'/'+name
      return {versionId:'many-directories',path,file:{path,name,sha:'a'.repeat(40),revision:'fixture',size:128},videoId:'v1',language:'',version:''}
    })
    const directoryStart=Date.now();await mount({library:directoryFixture})
    assert.equal(await page.locator('.match-files>li').count(),25)
    assert.equal(await page.locator('.match-variants').count(),1,'多来源仅显示当前选中的候选组')
    assert(Date.now()-directoryStart<2500,'1000 字幕与 500 个目录渲染保持有界')
    passed.push('1000 字幕、500 个双版本目录的分页与候选组渲染有界')
    await mount()
    const firstName=await page.evaluate(()=>window.fixture.library[0].file.name)
    const editor=page.locator('.match-editor')
    const editButton=button('编辑字幕 '+firstName)
    await editButton.click()
    const language=page.getByRole('combobox',{name:firstName+' 语言',exact:true})
    await language.click()
    const menu=page.locator('.select-menu-panel:popover-open')
    await menu.waitFor()
    await page.keyboard.press('Escape')
    await menu.waitFor({state:'hidden'})
    await editor.waitFor()
    await language.and(page.locator(':focus')).waitFor()
    await page.keyboard.press('Escape')
    await editor.waitFor({state:'detached'})
    await editButton.and(page.locator(':focus')).waitFor()
    const searchButton=button('更换字幕')
    await searchButton.click()
    await searchInput.and(page.locator(':focus')).waitFor()
    // 弹层处理返回后会拦截 300 毫秒内的连续关闭请求。
    await page.waitForTimeout(300)
    await page.keyboard.press('Escape')
    await searchInput.waitFor({state:'detached'})
    await searchButton.and(page.locator(':focus')).waitFor()
    assert.equal(await page.evaluate(()=>window.fixtureCloseCount),0)
    passed.push('Escape 逐级返回，保留列表')

    await mount({empty:true,multiSeason:true})
    await page.getByLabel('选择本地字幕或压缩包',{exact:true}).setInputFiles(['01.ass','02.ass'].map(name=>({name,mimeType:'text/plain',buffer:Buffer.from('[Script Info]')})))
    await button('编辑字幕 01.ass').click()
    const video = page.getByRole('combobox',{name:'01.ass 对应视频',exact:true})
    await selectOption(video,'v1')
    await selectOption(video,'')
    await selectOption(page.getByRole('combobox',{name:'字幕所属季',exact:true}),'2')
    const rowFor = name => page.locator('.match-files > li').filter({has:button('编辑字幕 '+name)})
    assert.match(await rowFor('01.ass').innerText(),/未匹配/)
    assert.match(await rowFor('02.ass').innerText(),/S02 E02/)
    passed.push('选择所属季时保留手动清空的对应关系')

    const chooseContinuousSource = () => page.locator('.picker-versions').getByRole('button',{name:/CoalGirls 01–24/}).click()
    await mount({empty:true,continuous:true})
    await chooseContinuousSource()
    const continuousRow = episode => rowFor(continuousFiles[episode-1].name)
    const continuousCheckbox = episode => continuousRow(episode).getByRole('checkbox')
    const continuousMatch = episode => continuousRow(episode).locator('.match-episode').innerText()
    const manualName = continuousFiles[1].name
    const continuousButton = button('按连续集号匹配')
    await continuousButton.waitFor()
    assert.equal(await continuousMatch(1),'S02 E01')
    assert.equal(await continuousMatch(13),'未匹配')
    await continuousCheckbox(1).check()
    await button('编辑字幕 '+manualName).click()
    await selectOption(page.getByRole('combobox',{name:manualName+' 对应视频',exact:true}),'v3')
    await button('编辑字幕 '+manualName).click()
    await continuousButton.click()
    for (let episode=1;episode<=12;episode++) {
      assert.equal(await continuousMatch(episode),episode===2?'S02 E03':'未匹配',`字幕 ${episode}`)
      assert.equal(await continuousMatch(episode+12),`S02 E${String(episode).padStart(2,'0')}`,`字幕 ${episode+12}`)
    }
    assert.equal(await continuousCheckbox(1).isChecked(),false,'原先勾选的过季字幕应取消勾选')
    assert.equal(await selected(),0,'繁体字幕等待用户选择')
    await button('全选已匹配').click()
    assert.equal(await continuousCheckbox(13).isChecked(),true)
    assert.equal(await continuousCheckbox(24).isChecked(),true)
    await continuousCheckbox(2).uncheck()
    await button('使用字幕').click()
    const continuousResult = await page.evaluate(()=>window.applied)
    assert.deepEqual(continuousResult.library.map(row=>[row.file.name,row.videoId,row.language]),continuousFiles.slice(12).map((file,index)=>[file.name,`v${index+1}`,'zh-TW']))
    await button('恢复按季集号匹配').click()
    assert.equal(await continuousMatch(1),'S02 E01')
    assert.equal(await continuousMatch(2),'S02 E03','手动选择的视频在恢复后保留')
    assert.equal(await continuousMatch(13),'未匹配')
    assert.equal(await continuousMatch(24),'未匹配')
    passed.push('字幕库连续集号换算、手动关联保护、繁体全选与恢复')

    await mount({continuous:true,...continuousResult})
    await button('更换字幕').click()
    await chooseContinuousSource()
    await button('恢复按季集号匹配').waitFor()
    assert.equal(await continuousMatch(1),'未匹配')
    assert.equal(await continuousMatch(13),'S02 E01')
    assert.equal(await selected(),12)
    passed.push('重新打开并重新载入字幕时保留已确认的连续匹配')

    await mount({empty:true,continuous:true,multiSeason:true})
    await chooseContinuousSource()
    await continuousButton.waitFor()
    assert.equal(await continuousButton.count(),1,'两季只需一个连续编号按钮')
    assert.equal(await page.getByRole('combobox',{name:'连续集号字幕来源',exact:true}).count(),0,'单一来源无需额外选择')
    assert.equal(await continuousMatch(1),'未匹配')
    await continuousButton.click()
    for (const [episode, expected] of [[1,'S01 E01'],[12,'S01 E12'],[13,'S02 E01'],[24,'S02 E12']]) {
      assert.equal(await continuousMatch(episode),expected,`字幕 ${episode}`)
    }
    await button('全选已匹配').click()
    await button('使用字幕').click()
    const allSeasonResult = await page.evaluate(()=>window.applied.library)
    assert.deepEqual(allSeasonResult.map(row=>[row.path,row.videoId]),continuousFiles.map((file,index)=>[file.path,index<12?`v${index+1}`:`s2-v${index-11}`]))
    await button('恢复按季集号匹配').click()
    assert.equal(await continuousMatch(1),'未匹配')
    assert.equal(await continuousMatch(24),'未匹配')
    assert.equal(await selected(),0)
    passed.push('全部所选字幕一次匹配两季并可恢复按季匹配')

    const file = continuousFiles[1]
    const saved = {versionId:continuousVersion.id,path:file.path,file,videoId:'s2-v2',language:'zh-TW',version:''}
    await mount({continuous:true,multiSeason:true,library:[saved,{...saved,videoId:'v2'}]})
    await button('更换字幕').click()
    await chooseContinuousSource()
    await continuousButton.click()
    assert.deepEqual(await continuousRow(2).locator('.match-episode').allTextContents(),['S01 E02','S02 E02'],'保留同一字幕文件已确认的两个关联')
    assert.equal(await continuousRow(2).getByRole('checkbox',{checked:true}).count(),2)
    await continuousRow(2).getByText('连续编号建议第 1 季第 2 集，已保留当前关联。',{exact:true}).waitFor()
    assert.equal(await continuousMatch(13),'S02 E01')
    passed.push('跨季连续匹配保留已保存的对应关系')

    for(const width of [980,390,320]) for(const dark of [false,true]) {
      await page.setViewportSize({width,height:660});await page.evaluate(dark=>document.documentElement.classList.toggle('dark',dark),dark);await mount()
      const label=width+'-'+(dark?'dark':'light')
      await layout(label+'-select')
      await editButton.click();await layout(label+'-edit')
      await editButton.click()
      await searchButton.click();await layout(label+'-search')
    }
    passed.push('980/390/320 × 深浅主题：列表、逐条编辑、搜索布局')
    assert.deepEqual(errors,[],'浏览器运行错误')
    console.log(JSON.stringify({passed,artifacts},null,2))
  } finally { await browser.close() }
})().catch(error=>{console.error(error);process.exitCode=1})
