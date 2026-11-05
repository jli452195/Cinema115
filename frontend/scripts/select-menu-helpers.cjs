// 通过实际点击公共下拉选择选项，普通原生控件仍使用 Playwright 原有操作。
async function selectOption(control, value) {
  control = control.and(control.page().getByRole('combobox'))
  if (await control.evaluate(element => element.tagName === 'SELECT')) return control.selectOption(value)
  const selected = typeof value === 'object' ? value.value : String(value)
  await control.click()
  const panelID = await control.getAttribute('aria-controls')
  const panel = control.page().locator(`[id=${JSON.stringify(panelID)}]`)
  // 选项值存于 listbox 子项，避免按显示文字选择时混淆重名选项。
  const target = typeof value === 'object' && value.label
    ? panel.getByRole('option', { name: value.label, exact: true })
    : panel.locator(`[role="option"][data-value=${JSON.stringify(selected)}]`)
  await target.click()
}

async function inputValue(control) {
  return await control.evaluate(element => element.getAttribute('role') === 'combobox' && element.tagName === 'BUTTON')
    ? control.getAttribute('data-value')
    : control.inputValue()
}

async function selectOptions(control) {
  control = control.and(control.page().getByRole('combobox'))
  await control.click()
  const panel = control.page().locator(`[id=${JSON.stringify(await control.getAttribute('aria-controls'))}]`)
  const values = await panel.getByRole('option').evaluateAll(elements => elements.map(element => ({ value: element.dataset.value, label: element.textContent.trim() })))
  await control.press('Escape')
  return values
}

module.exports = { selectOption, inputValue, selectOptions }
