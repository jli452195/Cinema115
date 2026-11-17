<template>
  <button type="button" class="search-pill"><slot /></button>
</template>

<style scoped>
.search-pill {
  position: relative;
  isolation: isolate;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 0;
  min-height: 44px;
  padding: 6px 14px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--glass-text);
  font-size: 12px;
  font-weight: 500;
  line-height: 20px;
  white-space: nowrap;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: transform 140ms cubic-bezier(.2, 0, 0, 1);
}
/* 视觉胶囊内缩，完整点击区域保留；滤镜只采样背景，文字与图标保持锐利。 */
.search-pill::before {
  position: absolute;
  z-index: -1;
  inset: 4px 0;
  border: 1px solid var(--glass-edge);
  border-radius: inherit;
  background: var(--glass-pill-background);
  box-shadow: var(--glass-card-shadow);
  -webkit-backdrop-filter: var(--glass-pill-filter);
  backdrop-filter: var(--glass-pill-filter);
  pointer-events: none;
  content: '';
  transition: background-color 150ms ease, border-color 150ms ease;
}
.search-pill:disabled { cursor: default; opacity: .5; }
.search-pill:focus-visible { outline: 2px solid rgb(var(--focus, 37 99 235)); outline-offset: 2px; }
.search-pill:active:not(:disabled) { transform: scale(.97); }
@media (hover: hover) {
  .search-pill:hover:not(:disabled) { transform: translateY(-1px); }
  .search-pill:hover:not(:disabled)::before { background: var(--glass-pill-hover); border-color: rgb(59 130 246 / 40%); }
  .dark .search-pill:hover:not(:disabled)::before { border-color: rgb(147 197 253 / 38%); }
}
@media (prefers-reduced-motion: reduce) {
  .search-pill, .search-pill::before { transition: none; }
  .search-pill:hover, .search-pill:active { transform: none; }
}
</style>
