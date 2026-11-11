<script setup lang="ts">
import Teek from 'vitepress-theme-teek'
import { useData } from 'vitepress'
import { provide } from 'vue'
import HomeShowcase from './HomeShowcase.vue'
import { createAppearanceToggle } from '../appearance'

const { isDark } = useData()
provide('toggle-appearance', createAppearanceToggle(isDark))
</script>

<template>
  <Teek.Layout>
    <template #home-hero-image><HomeShowcase /></template>
  </Teek.Layout>
</template>
