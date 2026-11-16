// 使用真实字幕弹窗与内存夹具，验证版本选择、取消保护和响应式布局，不发起整理任务。
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { selectOption } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = new URL(process.env.CINEMA_URL || 'http://127.0.0.1:5173').origin
const harness = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/src/style.css"></head><body><div id="app"></div><script type="module">
import { createApp, h, nextTick } from '/node_modules/.vite/deps/vue.js';
import Picker from '/src/components/SubtitlePickerDialog.vue';
let app;
window.mountDesign = async (options = {}) => {
  app?.unmount(); window.applied = null; window.fixtureCloseCount = 0;
  const videos = Array.from({length:12}, (_,i)=>({id:'v'+(i+1),name:'Chu-Bra!!.S01E'+String(i+1).padStart(2,'0')+'.1080p.H264.AAC.mkv',season:1,episode:i+1}));
  const library = options.library || videos.flatMap((video,i)=>['HKG','KTXP'].filter(hint=>!(options.missing && hint==='KTXP' && i===4)).map(hint=>{
    const name='ちゅーぶら！！ 第'+String(i+1).padStart(2,'0')+'話 「おとなパンツ！の女の子」 (BD 1920x1080 x264 AAC).'+hint+'.ass';
    return {versionId:'chu-bra',path:'字幕目录/'+name,file:{path:'字幕目录/'+name,name,sha:'a'.repeat(40),revision:'fixture',size:128},videoId:video.id,language:'',version:''};
  }));
  if (options.extra) library.push({versionId:'another-source',path:'Other.en.srt',file:{name:'Other.en.srt',path:'Other.en.srt',sha:'b'.repeat(40),revision:'fixture',size:20},videoId:'v1',language:'en',version:''});
  window.fixture = {videos, library};
  app = createApp({render:()=>h(Picker,{token:'fixture',kind:'tv',animationLibrary:true,title:'青春CUP',videos,targetNames:Object.fromEntries(videos.map(v=>[v.id,v.name])),library:options.empty?[]:library,local:[],version:{id:'chu-bra',title:'青春CUP',name:'BD 字幕合集',url:'https://example.invalid/subtitles',description:'HKG / KTXP 文件名标记，未指定语言。'},onSelect:(library,local)=>window.applied={library,local},onClose:()=>window.fixtureCloseCount++})});
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
      if(url.origin===origin && request.method()==='GET' && /\/api\/admin\/subtitles\/search/.test(url.pathname)) return route.fulfill({json:{versions:[]}})
      if(url.origin===origin && request.method()==='GET' && /^\/(?:src\/|node_modules\/|@vite\/|@id\/|@fs\/)/.test(url.pathname)) return route.continue()
      return route.abort('blockedbyclient')
    })
    await page.goto(origin+'/__subtitle-design-test'); await page.waitForFunction(()=>window.designReady)
    const button=name=>page.getByRole('button',{name,exact:true})
    const mount=options=>page.evaluate(options=>window.mountDesign(options),options || {})
    const selected=()=>page.locator('.match-files input[type=checkbox]:checked').count()
    async function layout(label) {
      const result=await page.evaluate(()=>{
        const dialog=document.querySelector('dialog[open]'),body=dialog.querySelector('.anime-floating-body'),footer=dialog.querySelector('.anime-floating-footer'),primary=dialog.querySelectorAll('.picker-apply');
        const scrolls=[...dialog.querySelectorAll('*')].filter(el=>['auto','scroll'].includes(getComputedStyle(el).overflowY) && el.scrollHeight>el.clientHeight+2 && !el.closest('.select-menu-popover'));
        return {width:body.scrollWidth-body.clientWidth,document:document.documentElement.scrollWidth-innerWidth,primary:primary.length,bottom:primary[0]?.getBoundingClientRect().bottom,height:innerHeight,scrolls:scrolls.map(el=>el.className),fixed:footer.getBoundingClientRect().height+dialog.querySelector('header').getBoundingClientRect().height};
      })
      assert(result.width<=1, label+' 正文横向溢出 '+JSON.stringify(result)); assert(result.document<=1,label+' 页面横向溢出')
      assert.equal(result.primary,1,label+' 主按钮唯一'); assert(result.bottom<=result.height,label+' 底部操作可见')
      assert(result.scrolls.length<=1 && result.scrolls.every(x=>x.includes('anime-floating-body')),label+' 仅正文滚动 '+JSON.stringify(result))
      if((await page.viewportSize()).width<=390) assert(result.fixed<=185,label+' 固定区域过高 '+result.fixed)
      await page.screenshot({path:path.join(artifacts,label+'.png')})
    }
    await mount({extra:true})
    console.log('截图目录：'+artifacts)
    await layout('initial-select')
    assert.equal(await page.getByRole('textbox',{name:'字幕作品搜索词'}).count(),0,'重新打开直接展示列表')
    assert.equal(await page.locator('.match-editor').count(),0,'默认收起编辑器')
    assert.equal(await button('使用字幕').isDisabled(),true)
    assert.equal(await page.locator('.match-conflicts').count(),1)
    await page.getByRole('button',{name:/^仅选 HKG /}).click()
    assert.equal(await selected(),13,'仅选版本保留其他来源')
    await button('使用字幕').click()
    let applied=await page.evaluate(()=>window.applied)
    assert.equal(applied.library.length,13);assert(applied.library.every(row=>row.version==='' && (row.versionId==='another-source' || /HKG/.test(row.file.name))))
    await button('撤销').click(); assert.equal(await selected(),25)
    await button('保留多版').click()
    assert.equal(await page.locator('.match-files').count(),0,'批量替代列表')
    assert.equal(await button('使用字幕').count(),0)
    await button('取消').click(); assert.equal(await selected(),25)
    await button('保留多版').click(); await button('应用设置').click()
    assert.equal(await button('使用字幕').isEnabled(),true,'版本标记解决重名')
    await button('使用字幕').click();applied=await page.evaluate(()=>window.applied)
    assert.equal(applied.library.filter(row=>row.version==='HKG').length,12);assert.equal(applied.library.filter(row=>row.version==='KTXP').length,12)
    assert.equal(applied.library.find(row=>row.versionId==='another-source').version,'')
    await mount({library:applied.library}); assert.equal(await button('使用字幕').isEnabled(),true)
    passed.push('整版选择、来源隔离、撤销、保留多版预览确认、版本提交及重新打开')
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
    await button('保留多版').click();await button('应用设置').click();await button('使用字幕').click()
    const pairResult=await page.evaluate(()=>window.applied.library)
    assert.deepEqual(pairResult.map(row=>row.version).sort(),['HKG','HKG','KTXP','KTXP'])
    await mount({library:pairResult.map(row=>({...row,language:'zh-CN'}))})
    await button('取消全选').click();await selectOption(page.getByRole('combobox',{name:'筛选字幕语言',exact:true}),'zh-CN')
    const pairName=pairResult[0].file.name
    await button('编辑字幕 '+pairName).click();await selectOption(page.getByRole('combobox',{name:pairName+' 语言',exact:true}),'zh-TW')
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
    await button('编辑字幕 '+firstName).click(); await page.keyboard.press('Escape')
    assert.equal(await page.locator('.match-editor').count(),0);assert.equal(await page.evaluate(()=>window.fixtureCloseCount),0)
    await button('更换字幕').click();await page.keyboard.press('Escape');assert.equal(await page.getByRole('textbox',{name:'字幕作品搜索词'}).count(),0)
    await button('批量处理').click();await page.keyboard.press('Escape');assert.equal(await page.locator('.subtitle-batch').count(),0);assert.equal(await page.evaluate(()=>window.fixtureCloseCount),0)
    assert.equal(await button('批量处理').evaluate(el=>el===document.activeElement),true,'取消批量后恢复触发按钮焦点')
    const savedScroll=await button('保留多版').evaluate(el=>{el.scrollIntoView({block:'center'});return el.closest('.anime-floating-body').scrollTop})
    await button('保留多版').click();await button('取消').click()
    assert.equal(await button('保留多版').evaluate(el=>el===document.activeElement),true,'恢复具体版本操作的焦点')
    assert.equal(await page.locator('.anime-floating-body').evaluate(el=>el.scrollTop),savedScroll,'取消批量后恢复正文滚动位置')
    await button('批量处理').click();await page.getByRole('combobox',{name:'批量匹配目标季',exact:true}).click();await page.keyboard.press('Escape')
    assert.equal(await page.locator('.subtitle-batch').count(),1,'Escape 优先关闭内部下拉')
    await page.keyboard.press('Escape');assert.equal(await page.locator('.subtitle-batch').count(),0)
    passed.push('Escape 逐级返回，保留列表')
    for(const width of [980,390,320]) for(const dark of [false,true]) {
      await page.setViewportSize({width,height:660});await page.evaluate(dark=>document.documentElement.classList.toggle('dark',dark),dark);await mount()
      const label=width+'-'+(dark?'dark':'light')
      await layout(label+'-select')
      await button('保留多版').click();await layout(label+'-batch')
      await button('取消').click();await button('批量处理').click();await layout(label+'-match')
      await page.getByRole('button',{name:'设置语言',exact:true}).click();await layout(label+'-language')
      await button('取消').click();await button('更换字幕').click();await layout(label+'-search')
    }
    passed.push('980/390/320 × 深浅主题：列表、批量、搜索布局')
    assert.deepEqual(errors,[],'浏览器运行错误')
    console.log(JSON.stringify({passed,artifacts},null,2))
  } finally { await browser.close() }
})().catch(error=>{console.error(error);process.exitCode=1})
