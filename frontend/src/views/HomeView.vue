<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import embyIcon from '../assets/emby.svg'

const props = defineProps<{ username: string; category: 'p115' | 'telegram' | 'emby' }>()
const emit = defineEmits<{ openDashboard: []; category: [value: 'p115' | 'telegram']; openP115: []; openShare: []; openTelegram: []; openLogs: []; logout: [] }>()
// 分类页只展示功能入口，不加载业务配置或启动状态轮询。
const categories = {
  p115: {
    title: '115 网盘', description: '管理网盘连接、分享播放与 STRM 生成。',
    features: [
      { title: '115 登录配置', description: '使用 Cookie 或扫码连接网盘，为分享播放提供登录凭证。', to: '/115/login', icon: 'cloud' },
      { title: '115分享STRM', description: '从分享链接生成 STRM，管理转存再分享、审核重试与任务记录。', to: '/115/share', icon: 'play' },
    ],
  },
  telegram: {
    title: 'Telegram 配置', description: '连接机器人，管理消息接收与自动处理。',
    features: [{ title: 'Bot 配置', description: '配置机器人 Token、允许的聊天与消息接收。', to: '/telegram/bot', icon: 'telegram' }],
  },
  emby: {
    title: 'Emby', description: '连接媒体服务器，管理媒体库与入库检测。',
    features: [{ title: '媒体库同步', description: '配置 Emby 连接与同步范围，通过 Webhook 更新影视入库状态。', to: '/emby/library', icon: 'emby' }],
  },
}
const current = computed(() => categories[props.category])
</script>

<template>
  <ManagementLayout :title="current.title" :description="current.description" @logout="emit('logout')">
    <div class="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
      <RouterLink v-for="feature in current.features" :key="feature.to" :to="feature.to" class="group flex min-w-0 flex-col rounded-2xl border border-slate-200 bg-white p-6 shadow-sm transition hover:border-slate-300 hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-amber-400">
        <span class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl" :class="feature.icon === 'emby' ? 'bg-emerald-50' : 'bg-blue-50 text-blue-600'">
          <img v-if="feature.icon === 'emby'" :src="embyIcon" alt="" class="h-7 w-7" />
          <svg v-else class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true">
            <path v-if="feature.icon === 'cloud'" d="M6 19a5 5 0 0 1-1-9.9A7 7 0 0 1 18.7 8a5.5 5.5 0 0 1-.2 11H6Z" />
            <template v-else-if="feature.icon === 'play'"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="m10 8 6 4-6 4V8Z"/></template>
            <path v-else d="m21 3-5 18-5-7-8-3L21 3Zm-10 11 5-6" />
          </svg>
        </span>
        <h2 class="text-base font-semibold text-slate-900">{{ feature.title }}</h2>
        <p class="mb-6 mt-2 flex-1 text-sm leading-6 text-slate-500">{{ feature.description }}</p>
        <span class="flex items-center justify-between border-t border-slate-100 pt-4 text-xs font-medium text-slate-500 group-hover:text-blue-600">进入功能<span aria-hidden="true">→</span></span>
      </RouterLink>
    </div>
  </ManagementLayout>
</template>
