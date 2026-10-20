let locks = 0
let overflow = ''

export function lockAnimeDialogScroll() {
  if (locks++ === 0) { overflow = document.body.style.overflow; document.body.style.overflow = 'hidden' }
  let released = false
  return () => {
    if (released) return
    released = true
    if (--locks === 0) document.body.style.overflow = overflow
  }
}
