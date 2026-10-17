<script setup lang="ts">
import { computed } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import NavigationCard from '../components/NavigationCard.vue'
import NavigationIcon from '../components/NavigationIcon.vue'
import embyIcon from '../assets/emby.svg'
import telegramIcon from '../assets/telegram/telegram.svg'
import { managementCategories } from '../utils/managementFeatures'

const props = defineProps<{ username: string; category: keyof typeof managementCategories }>()
const emit = defineEmits<{ openDashboard: []; category: [value: 'p115' | 'telegram']; openP115: []; openShare: []; openTelegram: []; openLogs: []; logout: [] }>()
// 分类页只展示功能入口，不加载业务配置或启动状态轮询。
const current = computed(() => managementCategories[props.category])
</script>

<template>
  <ManagementLayout :title="current.title" :description="current.description" @logout="emit('logout')">
    <div class="grid items-start gap-3 sm:gap-4 md:grid-cols-2">
      <NavigationCard v-for="feature in current.features" :key="feature.to" :to="feature.to" :title="feature.title" :description="feature.description">
        <template #icon>
          <img v-if="feature.icon === 'emby'" :src="embyIcon" alt="" aria-hidden="true" class="h-7 w-7" draggable="false" />
          <img v-else-if="feature.icon === 'telegram'" :src="telegramIcon" alt="" aria-hidden="true" class="h-6 w-6 object-contain" draggable="false" />
          <NavigationIcon v-else :name="feature.menuIcon" :size="24" />
        </template>
      </NavigationCard>
    </div>
  </ManagementLayout>
</template>
