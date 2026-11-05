const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 960, height: 720 } })
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/admin/login')
    const passed = await page.evaluate(async () => {
      const { createDialogLifecycle, lockBodyScroll, restoreDialogFocus } = await import('/src/lib/dialog-lifecycle.ts')
      const sidebarSource = await (await fetch('/src/components/ManagementSidebar.vue')).text()
      const dependency = name => sidebarSource.match(new RegExp(`from ["']([^"']*/${name}\\.js[^"']*)["']`))[1]
      const { createApp, h, nextTick, ref } = await import(dependency('vue'))
      const { createMemoryHistory, createRouter } = await import(dependency('vue-router'))
      const SidePanel = (await import('/src/components/P115SidePanel.vue')).default
      const FloatingDialog = (await import('/src/components/AnimeFloatingDialog.vue')).default
      const Sidebar = (await import('/src/components/ManagementSidebar.vue')).default
      const passed = []
      function check(condition, message) { if (!condition) throw new Error(message) }
      const originalOverflow = document.body.style.overflow
      document.body.style.overflow = 'clip'
      const unlockFirst = lockBodyScroll(), unlockSecond = lockBodyScroll()
      unlockFirst(); unlockFirst()
      check(document.body.style.overflow === 'hidden', '重复或乱序释放不能解除其他弹窗的锁')
      const unlockThird = lockBodyScroll()
      unlockSecond()
      check(document.body.style.overflow === 'hidden', '后打开的弹窗继续持有滚动锁')
      unlockThird(); unlockSecond(); unlockThird()
      check(document.body.style.overflow === 'clip', '最后释放后恢复原来的内联 overflow')
      passed.push('独立锁乱序、重复释放和后续新锁保留原始 overflow')

      const fixture = document.createElement('div')
      fixture.innerHTML = '<button id="lifecycle-origin">页面触发器</button><button id="lifecycle-other">其他控件</button>'
      document.body.append(fixture)
      const origin = fixture.querySelector('#lifecycle-origin'), other = fixture.querySelector('#lifecycle-other')
      const outer = document.createElement('dialog'), inner = document.createElement('dialog')
      outer.innerHTML = '<button>子弹窗触发器</button>'
      inner.innerHTML = '<button>子弹窗控件</button>'
      fixture.append(outer, inner)
      origin.focus()
      const outerLayer = createDialogLifecycle(), innerLayer = createDialogLifecycle()
      outerLayer.acquire(); outerLayer.acquire(); outer.showModal()
      outer.querySelector('button').focus(); innerLayer.acquire(); inner.showModal(); inner.querySelector('button').focus()
      outer.close()
      const active = document.activeElement
      outerLayer.release()
      check(document.activeElement === active, '非最上层乱序关闭不能抢走子弹窗焦点')
      check(document.body.style.overflow === 'hidden', '父弹窗先关闭时子弹窗继续锁住页面')
      outer.remove(); inner.close(); inner.remove(); innerLayer.release()
      check(document.activeElement === origin, '父弹窗已经移除时子弹窗沿触发链恢复页面焦点')
      check(document.body.style.overflow === 'clip', '父子乱序关闭后释放全部锁')
      other.focus(); innerLayer.release(); outerLayer.release()
      check(document.activeElement === other, '重复 release 不能再次移动焦点')
      origin.disabled = true
      check(!restoreDialogFocus(origin) && document.activeElement === other, '不向禁用控件恢复焦点')
      origin.disabled = false; origin.hidden = true
      check(!restoreDialogFocus(origin), '不向隐藏控件恢复焦点')
      origin.hidden = false
      passed.push('原最上层恢复焦点，父弹窗先移除时保留页面触发链，重复关闭不抢焦点')

      const mount = document.createElement('div')
      document.body.append(mount)
      const panelOpen = ref(false), nestedOpen = ref(false), sidebar = ref()
      const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] })
      const app = createApp({ setup: () => () => h('div', [
        h('button', { id: 'open-lifecycle-panel', onClick: () => { panelOpen.value = true } }, '打开侧面板'),
        h('button', { id: 'open-lifecycle-sidebar', onClick: event => sidebar.value.openDrawer(event) }, '打开侧栏'),
        h(Sidebar, { ref: sidebar, desktop: false, mobileOnly: true }),
        panelOpen.value && h(SidePanel, { title: '生命周期侧面板', description: '', onClose: () => { panelOpen.value = false } }, () => h('button', { id: 'open-nested-lifecycle', onClick: () => { nestedOpen.value = true } }, '打开子弹窗')),
        nestedOpen.value && h(FloatingDialog, { title: '生命周期子弹窗', closeLabel: '关闭生命周期子弹窗', onClose: () => { nestedOpen.value = false } }, () => h('button', { id: 'nested-lifecycle-action' }, '子弹窗操作')),
      ]) })
      app.use(router); await router.isReady(); app.mount(mount)
      const click = async id => { const target = document.getElementById(id); target.focus(); target.click(); await nextTick(); await nextTick() }
      await click('open-lifecycle-panel')
      check(document.querySelector('dialog[aria-labelledby] h2') || document.querySelector('dialog[open]'), '侧面板成功打开')
      check(document.body.style.overflow === 'hidden', '实际侧面板获取滚动锁')
      document.getElementById('open-nested-lifecycle').focus()
      await click('open-nested-lifecycle')
      const nested = document.querySelector('.anime-floating[open]')
      check(nested, '实际子弹窗成功打开')
      panelOpen.value = false; await nextTick()
      check(document.body.style.overflow === 'hidden' && nested.open, '实际侧面板先卸载后子弹窗仍持锁')
      nestedOpen.value = false; await nextTick()
      check(document.body.style.overflow === 'clip', '实际父子卸载后恢复 overflow')
      check(document.activeElement?.id === 'open-lifecycle-panel', '实际父子卸载后沿触发链恢复页面控件')
      passed.push('实际 SidePanel 与 FloatingDialog 嵌套、父先卸载及最终焦点恢复')

      await click('open-lifecycle-sidebar')
      check(document.querySelector('.management-drawer[open]'), '实际移动侧栏打开')
      const sidebarTrigger = document.activeElement
      nestedOpen.value = true; await nextTick()
      nestedOpen.value = false; await nextTick()
      check(document.activeElement === sidebarTrigger, '子弹窗关闭后恢复侧栏内触发器')
      check(document.body.style.overflow === 'hidden', '子弹窗关闭后侧栏继续锁住页面')
      sidebar.value.toggleDrawer(new MouseEvent('click')); await nextTick(); await nextTick()
      check(document.body.style.overflow === 'clip', '关闭侧栏后恢复原始 overflow')
      check(document.activeElement?.id === 'open-lifecycle-sidebar', '关闭侧栏后恢复入口焦点')
      await click('open-lifecycle-sidebar')
      nestedOpen.value = true; await nextTick()
      app.unmount(); await nextTick()
      check(document.body.style.overflow === 'clip', '侧栏和子弹窗同时卸载后不遗留锁')
      passed.push('实际移动侧栏与子弹窗嵌套、关闭恢复和整棵组件树卸载')
      fixture.remove(); mount.remove(); document.body.style.overflow = originalOverflow
      return passed
    })
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
