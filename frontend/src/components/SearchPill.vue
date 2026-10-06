<template>
  <button 
    type="button" 
    class="search-pill inline-flex items-center justify-center gap-1.5 rounded-full border-0 px-4 py-2 text-xs font-medium"
  >
    <slot />
  </button>
</template>

<style scoped>
.search-pill {
  background: var(--md-sys-color-surface-container-high, rgb(var(--color-slate-100)));
  color: var(--md-sys-color-on-surface, rgb(var(--color-slate-700)));
  box-shadow: 0 1px 2px rgb(0 0 0 / 5%);
  transition: transform 300ms ease-out, box-shadow 300ms ease-out, background-color 200ms;
}
.dark .search-pill {
  background: var(--md-sys-color-surface-container-high, rgb(var(--color-slate-800)));
  color: var(--md-sys-color-on-surface, rgb(var(--color-slate-300)));
}
.search-pill:hover, .search-pill:active {
  background: var(--md-sys-color-surface-container-highest, rgb(var(--color-slate-200)));
}
.dark .search-pill:hover, .dark .search-pill:active {
  background: var(--md-sys-color-surface-container-highest, rgb(var(--color-slate-700)));
}
@media (hover: hover) {
  .search-pill:hover {
    transform: translateY(-2px);
    box-shadow: 0 4px 6px -1px rgb(0 0 0 / 10%), 0 2px 4px -2px rgb(0 0 0 / 10%);
  }
  .dark .search-pill:hover {
    box-shadow: 0 8px 15px -3px rgb(0 0 0 / 40%);
  }
}
.search-pill:active {
  transform: translateY(0) scale(0.95);
  box-shadow: 0 1px 2px rgb(0 0 0 / 5%);
}
.dark .search-pill:active { box-shadow: 0 1px 2px rgb(0 0 0 / 20%); }
.search-pill:focus-visible {
  outline: 2px solid var(--md-sys-color-primary, rgb(var(--color-accent-text)));
  outline-offset: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .search-pill { transition: none; }
  .search-pill:hover, .search-pill:active { transform: none; }
}
</style>
