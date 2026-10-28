<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import { withBase } from 'vitepress'

const slides = [
  { title: '日本动漫', src: '/screenshots/anime-library.webp', alt: '日本动漫浏览页，按地区与年份筛选，展示海报和入库状态' },
  { title: '动漫订阅', src: '/screenshots/anime-calendar.webp', alt: '动漫订阅页，选择资源站点，按季度和星期浏览番剧' },
  { title: '115 网盘', src: '/screenshots/drive-overview.webp', alt: '115 网盘功能页，包含分享 STRM、云下载、下载整理和手动整理' },
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
const current = computed(() => slides[active.value])
const playing = computed(() => desktop.value && visible.value && !reducedMotion.value
  && !paused.value && !hovered.value && !focused.value)
let trackWidth = 0

function select(index: number) {
  const element = track.value
  if (!element) return
  const next = (index + slides.length) % slides.length
  element.scrollTo({
    left: next * element.clientWidth,
    behavior: reducedMotion.value ? 'instant' : 'smooth',
  })
}

function browse(index: number) {
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
  syncEnvironment()
  dispose = () => {
    resize.disconnect()
    motion.removeEventListener('change', syncEnvironment)
    pointer.removeEventListener('change', syncEnvironment)
    document.removeEventListener('visibilitychange', syncEnvironment)
  }
})
onBeforeUnmount(() => dispose())
</script>

<template>
  <section
    class="home-showcase"
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
            :src="withBase(slide.src)"
            :alt="slide.alt"
            width="1440"
            height="900"
            :loading="index === 0 ? 'eager' : 'lazy'"
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
      <div class="preview-header"><span>{{ current.title }}</span><button type="button" @click="preview?.close()">关闭</button></div>
      <img :src="withBase(current.src)" :alt="current.alt" width="1440" height="900">
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
}
.showcase-track::-webkit-scrollbar { display: none; }
.showcase-slide { flex: 0 0 100%; min-width: 0; scroll-snap-align: start; }
.showcase-image { display: block; width: 100%; height: 100%; cursor: zoom-in; }
.showcase-image img { display: block; width: 100%; height: 100%; object-fit: contain; }
.showcase-indicators { display: flex; justify-content: center; margin-top: 8px; }
.showcase-indicators button { display: grid; place-items: center; width: 44px; height: 28px; border-radius: 4px; }
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
