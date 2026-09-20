// HTTP 局域网页面没有 Clipboard API；兼容复制必须在当前模态弹窗内部选中文本。
export async function copyText(text: string, container?: HTMLElement | null): Promise<void> {
  if (!text) throw new Error('没有可复制的内容')
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // HTTPS 下权限被拒绝时仍尝试兼容方式，失败由界面提供手动复制入口。
    }
  }
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const selection = window.getSelection()
  const ranges = selection ? Array.from({ length: selection.rangeCount }, (_, i) => selection.getRangeAt(i).cloneRange()) : []
  const previousInput = previous instanceof HTMLInputElement || previous instanceof HTMLTextAreaElement ? previous : null
  const start = previousInput?.selectionStart, end = previousInput?.selectionEnd
  let modal: Element | null = null
  try { modal = document.querySelector('dialog:modal') } catch { modal = previous?.closest('dialog[open]') || null }
  const host = modal || container || document.body
  const input = document.createElement('textarea')
  input.value = text
  input.readOnly = true
  input.setAttribute('aria-label', '复制内容')
  input.style.cssText = 'position:fixed;left:0;top:0;width:1px;height:1px;padding:0;border:0;opacity:0;font-size:16px;'
  try {
    host.appendChild(input)
    input.focus({ preventScroll: true })
    input.select()
    input.setSelectionRange(0, text.length)
    // 不能只相信 execCommand 返回值：未获得焦点时也可能返回 true。
    if (document.activeElement !== input || input.selectionStart !== 0 || input.selectionEnd !== text.length) throw new Error('无法选中复制内容')
    if (!document.execCommand('copy')) throw new Error('浏览器未允许复制')
  } finally {
    input.remove()
    if (previous?.isConnected) previous.focus({ preventScroll: true })
    if (previousInput?.isConnected && start != null && end != null) previousInput.setSelectionRange(start, end)
    if (selection) {
      selection.removeAllRanges()
      for (const range of ranges) if (range.commonAncestorContainer.isConnected) selection.addRange(range)
    }
  }
}
