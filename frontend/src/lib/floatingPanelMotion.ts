type PanelAnchor = 'top' | 'bottom'

export function readFloatingPanelMotion(panel: HTMLElement) {
  const { opacity, transform } = getComputedStyle(panel)
  return { opacity, transform }
}

export function animateFloatingPanel(
  panel: HTMLElement, opening: boolean, anchor: PanelAnchor, from?: ReturnType<typeof readFloatingPanelMotion>,
) {
  const direction = anchor === 'top' ? -1 : 1
  // 平移和等比缩放整个面板，保留轻微回弹，避免逐帧改变布局与裁剪边界。
  return opening ? panel.animate([
    from ?? { opacity: 0, transform: `translateY(${14 * direction}px) scale(.87)` },
    { opacity: 1, transform: `translateY(${-2 * direction}px) scale(1.01)`, offset: .78 },
    { opacity: 1, transform: 'none' },
  ], { duration: 310, easing: 'cubic-bezier(.2,.8,.2,1)', fill: 'both' }) : panel.animate([
    from ?? { opacity: 1, transform: 'none' },
    { opacity: 0, transform: `translateY(${12 * direction}px) scale(.89)` },
  ], { duration: 170, easing: 'cubic-bezier(.4,0,.7,.25)', fill: 'both' })
}
