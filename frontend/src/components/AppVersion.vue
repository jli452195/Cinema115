<script setup lang="ts">
import { readAppVersion } from '../utils/appVersion'

// 静态读取构建注入的版本元数据
const info = readAppVersion()
</script>

<template>
  <span class="app-version" :title="info.title" aria-label="应用版本">
    {{ info.displayVersion }}
  </span>
</template>

<style scoped>
.app-version {
  display: block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  line-height: 1.4;
  letter-spacing: 0.01em;
  color: rgb(var(--color-slate-500));
  user-select: none;
  cursor: default;
}
</style>

