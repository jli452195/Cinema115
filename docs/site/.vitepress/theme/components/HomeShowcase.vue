<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import { useData, withBase } from 'vitepress'

const { isDark } = useData()
const screenshot = (src: string, theme: 'light' | 'dark') => withBase(`${src}-${theme}.webp`)

const slides = [
  { title: '日本动漫', src: '/screenshots/anime-library', alt: '日本动漫浏览页，按地区与年份筛选，展示海报和入库状态' },
  { title: '动漫订阅', src: '/screenshots/anime-calendar', alt: '动漫订阅页，选择资源站点，按季度和星期浏览番剧' },
  { title: '115 网盘', src: '/screenshots/drive-overview', alt: '115 网盘功能页，提供分享 STRM、网盘 STRM、云下载与目录监控等入口' },
]

const track = ref<HTMLDivElement>()
const preview = ref<HTMLDialogElement>()
const active = ref(0)
const hovered = ref(false)
const focused = ref(false)
const paused = ref(false)
const visible = ref(false)
const desktop = ref(false)
const reducedMotion = ref(true)
const themeTransitioning = ref(false)
const current = computed(() => slides[active.value])
const playing = computed(() => desktop.value && visible.value && !reducedMotion.value
  && !paused.value && !hovered.value && !focused.value && !themeTransitioning.value)
let trackWidth = 0
let transitionId = 0

function select(index: number) {
  const element = track.value
  if (!element || themeTransitioning.value) return
  const next = (index + slides.length) % slides.length
  element.scrollTo({
    left: next * element.clientWidth,
    behavior: reducedMotion.value ? 'instant' : 'smooth',
  })
}

function browse(index: number) {
  if (themeTransitioning.value) return
  paused.value = true
  select(index)
}

function syncActive() {
  const element = track.value
  if (element?.clientWidth && element.clientWidth === trackWidth) {
    active.value = Math.max(0, Math.min(slides.length - 1, Math.round(element.scrollLeft / element.clientWidth)))
  }
}

function syncFocus(event: FocusEvent) {
  focused.value = (event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)
}

function closeBackdrop(event: MouseEvent) {
  if (event.target === preview.value) preview.value?.close()
}

watchEffect(onCleanup => {
  if (!playing.value) return
  const index = active.value
  const timer = window.setTimeout(() => select(index + 1), 5000)
  onCleanup(() => window.clearTimeout(timer))
})

let dispose = () => {}
onMounted(() => {
  const element = track.value!
  trackWidth = element.clientWidth
  const resize = new ResizeObserver(() => {
    if (element.clientWidth === trackWidth) return
    trackWidth = element.clientWidth
    element.scrollTo({ left: active.value * trackWidth, behavior: 'instant' })
  })
  resize.observe(element)
  const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
  const pointer = window.matchMedia('(min-width: 960px) and (hover: hover) and (pointer: fine)')
  const syncEnvironment = () => {
    reducedMotion.value = motion.matches
    desktop.value = pointer.matches
    visible.value = !document.hidden
  }
  motion.addEventListener('change', syncEnvironment)
  pointer.addEventListener('change', syncEnvironment)
  document.addEventListener('visibilitychange', syncEnvironment)
  const syncThemeTransition = (event: Event) => {
    const detail = (event as CustomEvent<{ phase: 'start' | 'end'; id: number }>).detail
    if (detail.phase === 'start') {
      transitionId = detail.id
      themeTransitioning.value = true
      // 中断进行中的平滑滚动并对齐最近一张，确保深浅根快照保持相同画面。
      syncActive()
      element.scrollTo({ left: active.value * element.clientWidth, behavior: 'instant' })
    } else if (detail.id === transitionId) {
      themeTransitioning.value = false
    }
  }
  document.addEventListener('wiki-theme-transition', syncThemeTransition)
  syncEnvironment()
  dispose = () => {
    resize.disconnect()
    motion.removeEventListener('change', syncEnvironment)
    pointer.removeEventListener('change', syncEnvironment)
    document.removeEventListener('visibilitychange', syncEnvironment)
    document.removeEventListener('wiki-theme-transition', syncThemeTransition)
  }
})
onBeforeUnmount(() => dispose())
</script>

<template>
  <section
    class="home-showcase"
    :class="{ 'is-theme-transitioning': themeTransitioning }"
    aria-label="Cinema115 界面预览"
    aria-roledescription="轮播"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @focusin="focused = true"
    @focusout="syncFocus"
    @keydown.left.prevent="browse(active - 1)"
    @keydown.right.prevent="browse(active + 1)"
  >
    <div ref="track" class="showcase-track" @scroll.passive="syncActive" @pointerdown="paused = true">
      <div
        v-for="(slide, index) in slides"
        :key="slide.src"
        class="showcase-slide"
        role="group"
        aria-roledescription="幻灯片"
        :aria-label="`${index + 1} / ${slides.length}：${slide.title}`"
        :aria-hidden="index !== active"
      >
        <button
          class="showcase-image"
          type="button"
          :aria-label="`放大查看：${slide.title}`"
          :tabindex="index === active ? 0 : -1"
          @click="preview?.showModal()"
        >
          <img
            v-for="theme in (['light', 'dark'] as const)"
            :key="theme"
            :class="`screenshot-${theme}`"
            data-theme-paired
            :aria-hidden="theme !== (isDark ? 'dark' : 'light')"
            :src="screenshot(slide.src, theme)"
            :alt="theme === (isDark ? 'dark' : 'light') ? slide.alt : ''"
            width="1440"
            height="900"
            loading="eager"
            :fetchpriority="index === 0 ? 'high' : 'auto'"
            decoding="async"
            draggable="false"
          >
        </button>
      </div>
    </div>
    <div class="showcase-indicators" role="group" aria-label="选择预览界面">
      <button
        v-for="(slide, index) in slides"
        :key="slide.src"
        type="button"
        :title="slide.title"
        :aria-label="`查看${slide.title}`"
        :aria-current="index === active ? 'true' : undefined"
        @click="browse(index)"
      ><span /></button>
    </div>
    <dialog ref="preview" class="showcase-preview" aria-label="界面截图大图" @click="closeBackdrop">
      <div class="preview-header"><span>{{ current.title }} · {{ isDark ? '深色' : '浅色' }}</span><button type="button" @click="preview?.close()">关闭</button></div>
      <img v-for="theme in (['light', 'dark'] as const)" :key="theme" :class="`screenshot-${theme}`" data-theme-paired :aria-hidden="theme !== (isDark ? 'dark' : 'light')" :src="screenshot(current.src, theme)" :alt="theme === (isDark ? 'dark' : 'light') ? current.alt : ''" width="1440" height="900" loading="eager">
    </dialog>
  </section>
</template>

<style scoped>
.home-showcase {
  width: 100%;
}
.showcase-track {
  display: flex;
  overflow-x: auto;
  aspect-ratio: 16 / 10;
  scroll-snap-type: x mandatory;
  scrollbar-width: none;
  overscroll-behavior-x: contain;
  border-radius: 6px;
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
}
.showcase-track::-webkit-scrollbar { display: none; }
.showcase-slide { flex: 0 0 100%; min-width: 0; scroll-snap-align: start; }
.showcase-image { display: block; width: 100%; height: 100%; cursor: zoom-in; }
.showcase-image img { display: block; width: 100%; height: 100%; object-fit: contain; }
.home-showcase .screenshot-dark { display: none; }
:global(html.dark .home-showcase .screenshot-light) { display: none; }
:global(html.dark .home-showcase .screenshot-dark) { display: block; }
.is-theme-transitioning .showcase-track { overflow-x: hidden; }
.showcase-indicators { position: relative; z-index: 2; display: flex; justify-content: center; gap: 2px; min-height: 28px; margin-top: 8px; }
.showcase-indicators button { display: grid; place-items: center; width: 44px; height: 28px; border-radius: 4px; background: transparent; }
.showcase-indicators span { width: 28px; height: 3px; border-radius: 2px; background: var(--vp-c-text-3); opacity: .4; transition: opacity .2s, background-color .2s; }
.showcase-indicators button:hover span { opacity: .8; }
.showcase-indicators [aria-current] span { background: var(--vp-c-brand-1); opacity: 1; }
.showcase-indicators button:focus-visible, .preview-header button:focus-visible { outline: 2px solid var(--vp-c-brand-1); outline-offset: -2px; }
.showcase-image:focus-visible { outline: 3px solid var(--vp-c-brand-1); outline-offset: -3px; }
.showcase-preview { position: fixed; inset: 0; width: min(1440px, calc(100vw - 32px)); max-width: none; max-height: calc(100dvh - 32px); margin: auto; padding: 0; overflow: auto; border: 1px solid var(--vp-c-divider); border-radius: 12px; background: var(--vp-c-bg); color: var(--vp-c-text-1); box-shadow: 0 24px 80px rgb(0 0 0 / 24%); }
.showcase-preview::backdrop { background: rgb(15 23 42 / 68%); }
.preview-header { display: flex; align-items: center; justify-content: space-between; padding: 8px 16px; font-size: 14px; }
.preview-header button { padding: 4px 12px; border-radius: 6px; background: var(--vp-c-bg-soft); }
.showcase-preview img { display: block; width: 100%; height: auto; }
@media (prefers-reduced-motion: reduce) {
  .home-showcase * { transition: none; }
}
</style>
