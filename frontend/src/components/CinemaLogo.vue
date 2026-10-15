<template>
  <span class="group inline-flex min-w-0 select-none items-center gap-3">
    <svg class="h-7 w-[72px] shrink-0 overflow-visible drop-shadow-[0_4px_10px_rgba(14,165,233,0.16)] transition duration-[600ms] ease-[cubic-bezier(0.22,1,0.36,1)] group-hover:scale-[1.035] group-hover:drop-shadow-[0_5px_14px_rgba(14,165,233,0.34)]" viewBox="0 0 72 28" fill="none" aria-hidden="true">
      <defs>
        <linearGradient id="cinema-lockup-mark" x1="3" y1="4" x2="69" y2="25" gradientUnits="userSpaceOnUse">
          <stop stop-color="#2563EB" />
          <stop offset="1" stop-color="#06B6D4" />
        </linearGradient>
      </defs>
      <path d="M20 4.5A10.5 10.5 0 1 0 20 23.5" stroke="url(#cinema-lockup-mark)" stroke-width="2.5" stroke-linecap="round" />
      <path d="m12.5 8.5 9 5.5-9 5.5v-11Z" fill="url(#cinema-lockup-mark)" />
      <circle cx="7.2" cy="9.5" r="1" fill="currentColor" fill-opacity="0.7" />
      <circle cx="5" cy="15.2" r="1" fill="currentColor" fill-opacity="0.7" />
      <circle cx="8.2" cy="20.6" r="1" fill="currentColor" fill-opacity="0.7" />
      <path d="m27.5 9 5.5-4.5v19M39 9l5.5-4.5v19" stroke="url(#cinema-lockup-mark)" stroke-width="3" stroke-linecap="square" stroke-linejoin="bevel" />
      <path d="M68 4.8H52.5v9h6.2c6.3 0 9.7 2.2 9.7 6.2 0 3.1-2.1 5-5.6 5.9-3.2.8-7 .1-10.8-2.1" stroke="url(#cinema-lockup-mark)" stroke-width="2.8" stroke-linecap="square" stroke-linejoin="bevel" />
      <path d="M53 10.2h12" stroke="url(#cinema-lockup-mark)" stroke-width="1" opacity="0.42" />
    </svg>

    <span class="hidden flex-col justify-center sm:flex">
      <span class="brand-spectrum whitespace-nowrap font-mono text-[15px] font-black uppercase leading-[1]">Cinema115</span>
      <span class="brand-caption mt-1 whitespace-nowrap text-[9px] font-semibold leading-[1] text-slate-500 dark:text-slate-300">
        影视资源与媒体自动化
      </span>
    </span>
  </span>
</template>

<style scoped>
.brand-spectrum {
  color: transparent;
  background: linear-gradient(100deg, #1d4ed8, #0891b2 28%, #d97706 52%, #0891b2 74%, #1d4ed8);
  background-size: 260% 100%;
  background-clip: text;
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  filter: drop-shadow(0 1px 1px rgb(15 23 42 / 0.12));
  animation: cinema-brand-spectrum 9s linear infinite;
}

.dark .brand-spectrum {
  background-image: linear-gradient(100deg, #93c5fd, #67e8f9 28%, #fcd34d 52%, #67e8f9 74%, #93c5fd);
}

@keyframes cinema-brand-spectrum {
  from { background-position: 0 center; }
  to { background-position: 260% center; }
}

@media (prefers-reduced-motion: reduce) {
  .brand-spectrum { animation: none; background-position: 50% center; }
}
</style>
