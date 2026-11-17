<script setup lang="ts">
// 中心保持中性，仅在边缘渐变位移；静态位移图不需要逐帧采样或外部资源。
const edgeMap = `data:image/svg+xml,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="80"><defs><linearGradient id="x"><stop stop-color="#008080"/><stop offset=".12" stop-color="#808080"/><stop offset=".88" stop-color="#808080"/><stop offset="1" stop-color="#ff8080"/></linearGradient><linearGradient id="y" x2="0" y2="1"><stop stop-color="#800080"/><stop offset=".28" stop-color="#808080" stop-opacity="0"/><stop offset=".72" stop-color="#808080" stop-opacity="0"/><stop offset="1" stop-color="#80ff80"/></linearGradient></defs><rect width="200" height="80" fill="url(#x)"/><rect width="200" height="80" fill="url(#y)"/></svg>`)}`
</script>

<template>
  <svg class="glass-filter-defs" width="0" height="0" aria-hidden="true" focusable="false">
    <defs>
      <filter id="cinema-glass-refraction" x="0" y="0" width="100%" height="100%" color-interpolation-filters="sRGB">
        <feImage :href="edgeMap" x="0" y="0" width="100%" height="100%" preserveAspectRatio="none" result="edge-map" />
        <feDisplacementMap in="SourceGraphic" in2="edge-map" scale="5" xChannelSelector="R" yChannelSelector="G" />
      </filter>
    </defs>
  </svg>
</template>

<style scoped>
.glass-filter-defs { position: fixed; overflow: hidden; pointer-events: none; }
</style>
