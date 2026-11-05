const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const { selectOption, inputValue, selectOptions } = require('./select-menu-helpers.cjs')

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 800, height: 700 }, serviceWorkers: 'block' })
    page.setDefaultTimeout(8000)
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/__select-menu-test', route => route.fulfill({ contentType: 'text/html', body: '<html><body><div id="fixture"></div></body></html>' }))
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/__select-menu-test')
    await page.evaluate(async () => {
      const { createApp, h, reactive } = await import('/node_modules/.vite/deps/vue.js')
      const { default: SelectMenu } = await import('/src/components/SelectMenu.vue')
      await import('/src/style.css')
      const state = window.fixture = reactive({ value: '', number: 0, disabled: false, fieldset: false, submitted: 0, changes: 0, rerenders: 0 })
      createApp({ setup: () => () => h('dialog', { open: true, style: 'width:min(90vw,500px);padding:20px' }, [
        h('form', { onSubmit: event => { event.preventDefault(); state.submitted++ } }, [
          h('label', { for: 'choice' }, 'Required choice'),
          h('fieldset', { disabled: state.fieldset }, [h(SelectMenu, {
            id: 'choice', label: 'Required choice', name: 'choice', required: true, disabled: state.disabled,
            modelValue: state.value, options: [{ value: '', label: 'Choose', disabled: true }, { value: 'old', label: 'Deleted', disabled: true }, { value: 'a', label: 'Alpha' }, { value: 'b', label: 'Beta' }],
            'onUpdate:modelValue': value => { state.value = value }, onChange: () => { state.changes++ },
          })]),
          h(SelectMenu, { label: 'Numbers', name: 'number', modelValue: state.number, options: [{ value: 0, label: 'Zero' }, { value: 6, label: 'Six' }, { value: 12, label: 'Twelve' }], 'onUpdate:modelValue': value => { state.number = value } }),
          h('span', String(state.rerenders)), h('button', { type: 'submit' }, 'Submit'), h('button', { type: 'button' }, 'Outside'),
        ]),
      ]) }).mount('#fixture')
      document.querySelector('dialog').close()
      document.querySelector('dialog').showModal()
    })
    const choice = page.getByRole('combobox', { name: 'Required choice' }), numbers = page.getByRole('combobox', { name: 'Numbers' })
    await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true })
    await page.screenshot({ path: path.join(__dirname, '../test-results/select-menu-desktop.png'), animations: 'disabled' })
    assert.equal(await page.getByRole('combobox').count(), 2, '校验控件不能重复暴露给辅助技术')
    await page.getByRole('button', { name: 'Submit', exact: true }).click()
    assert.equal(await page.evaluate(() => fixture.submitted), 0)
    assert.equal(await choice.evaluate(element => element === document.activeElement), true)
    await choice.press('Home'); await choice.press('Enter')
    assert.equal(await inputValue(choice), 'a', 'Home 跳过禁用选项')
    await choice.press('ArrowDown'); await choice.press('End'); await choice.press('Enter')
    assert.equal(await inputValue(choice), 'b')
    await choice.press('ArrowDown'); await choice.press('Home'); await choice.press('ArrowUp'); await choice.press('Enter')
    assert.equal(await inputValue(choice), 'b', '键盘循环跳过禁用选项')
    await selectOption(numbers, '12')
    assert.equal(await page.evaluate(() => typeof fixture.number), 'number')
    await page.getByRole('button', { name: 'Submit', exact: true }).click()
    assert.equal(await page.evaluate(() => fixture.submitted), 1)
    assert.deepEqual(await page.evaluate(() => Object.fromEntries(new FormData(document.querySelector('form')))), { choice: 'b', number: '12' })
    await choice.click()
    await page.evaluate(() => { fixture.rerenders++ })
    assert.equal(await choice.getAttribute('aria-expanded'), 'true', '父级无关渲染不能关闭下拉')
    await choice.press('Escape')
    assert.equal(await page.locator('dialog').evaluate(element => element.open), true, 'Escape 先关闭下拉')
    await choice.click(); await page.getByRole('button', { name: 'Outside', exact: true }).click()
    assert.equal(await choice.getAttribute('aria-expanded'), 'false')
    await choice.click(); await page.evaluate(() => { fixture.fieldset = true })
    await choice.waitFor()
    assert.equal(await choice.isDisabled(), true)
    assert.equal(await choice.getAttribute('aria-expanded'), 'false')
    await page.evaluate(() => { fixture.fieldset = false; fixture.disabled = true })
    assert.equal(await choice.isDisabled(), true)
    await page.evaluate(() => { fixture.disabled = false })
    await choice.click(); await choice.press('b'); await choice.press('Enter')
    assert.equal(await inputValue(choice), 'b')
    assert.equal((await selectOptions(choice)).length, 4, '删除项仍显示供核对')
    await page.evaluate(() => {
      const dialog = document.querySelector('dialog')
      dialog.style.height = '250px'; dialog.style.overflow = 'auto'
      const spacer = document.createElement('div'); spacer.style.height = '350px'
      document.querySelector('form').prepend(spacer)
      dialog.scrollTop = 0
    })
    await choice.click()
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.equal(await choice.getAttribute('aria-expanded'), 'true', '点击引起的自动滚动不能关闭刚展开的菜单')
    await page.getByRole('listbox', { name: 'Required choice' }).getByRole('option', { name: 'Alpha', exact: true }).click()
    assert.equal(await inputValue(choice), 'a')
    await page.evaluate(() => { document.querySelector('form > div').remove(); document.querySelector('dialog').style.height = '' })
    for (const dark of [false, true]) {
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      await page.setViewportSize({ width: 320, height: 640 })
      await choice.click()
      const panel = page.getByRole('listbox', { name: 'Required choice' }), box = await panel.boundingBox()
      assert.ok(box.x >= 0 && box.x + box.width <= 320)
      assert.ok((await choice.boundingBox()).height >= 44)
      assert.equal(await panel.evaluate(element => element.matches(':popover-open')), true)
      const option = page.getByRole('option', { name: 'Alpha', exact: true })
      assert.equal(await option.evaluate(element => {
        const box = element.getBoundingClientRect()
        return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
      }), true, '下拉须位于模态框上方且可点击')
      await page.screenshot({ path: path.join(__dirname, `../test-results/select-menu-320-${dark ? 'dark' : 'light'}.png`), animations: 'disabled' })
      await choice.press('Escape')
    }
    assert.deepEqual(errors, [])
    console.log('SelectMenu UI passed: required form, numeric values, disabled items/fieldset, keyboard, parent updates, modal stacking, mobile and themes')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exit(1) })
