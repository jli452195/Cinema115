<script setup lang="ts">
import { IconCode } from '@tabler/icons-vue'

withDefaults(defineProps<{ content: string; description?: string }>(), {
  description: '示例字段，生成时替换为实际文件信息；支持播放器直接播放，也支持通过 Cinema115 的 Emby 反代播放。',
})
</script>

<template>
  <div class="strm-preview">
    <h3 class="strm-preview-title"><IconCode :size="16" aria-hidden="true" />STRM 内容预览</h3>
    <code>{{ content }}</code>
    <p>{{ description }}</p>
  </div>
</template>

<style scoped>
.strm-preview {
  min-width: 0;
  margin-top: 14px;
  color: rgb(var(--content-secondary));
  font-size: 12px;
}
.strm-preview-title {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
  font-size: inherit;
  font-weight: 500;
  line-height: 1.75;
}
.strm-preview-title svg { flex: none; }
.strm-preview code {
  display: block;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgb(var(--surface-page));
  color: rgb(var(--content-primary));
  font-size: 12px;
  line-height: 1.8;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.strm-preview p { margin-top: 6px; line-height: 1.75; }
</style>
