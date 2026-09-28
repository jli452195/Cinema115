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
      { title: '115 助手', description: '管理账号连接、扫码登录与 Cookie 校验，为分享播放提供凭证。', to: '/helper115', icon: 'cloud' },
      { title: '115分享STRM', description: '从分享链接生成 STRM，管理转存再分享、审核重试与任务记录。', to: '/115/share', icon: 'play' },
      { title: '115 云下载', description: '提交磁力或 ED2K 到指定的 115 账号和目录，查看云下载任务进度。', to: '/115/cloud-download', icon: 'download' },
      { title: '手动整理', description: '选择文件或文件夹，手动匹配 TMDB，校正季集与命名后移动到目标目录。', to: '/115/manual-organize', icon: 'monitor' },
      { title: '115 目录分享', description: '手动分享配置目录，复用分享 STRM 设置生成文件，完成后清理临时目录。', to: '/115/folder-monitor', icon: 'monitor' },
    ],
  },
  telegram: {
    title: 'Telegram 配置', description: '连接机器人，管理消息接收与自动处理。',
    features: [{ title: 'Bot 配置', description: '配置机器人 Token、允许的聊天与消息接收。', to: '/telegram/bot', icon: 'telegram' }, { title: '用户账号 / 资源搜索', description: '登录 Telegram 用户账号，通过指定资源 Bot 搜索影视资源。', to: '/telegram/user', icon: 'telegram' }],
  },
  emby: {
    title: 'Emby', description: '连接媒体服务器，管理媒体库与入库检测。',
    features: [{ title: '媒体库同步', description: '配置 Emby 连接与同步范围，通过 Webhook 更新影视入库状态。', to: '/emby/library', icon: 'emby' }, { title: 'Emby 302 反代', description: '设置独立播放端口与 115 账号，将分享 STRM 直放跳转到 115。', to: '/emby/proxy', icon: 'emby' }, { title: '缓存管理', description: '查看分享直取与秒传播放直链缓存，搜索、移除或清空临时地址。', to: '/emby/cache', icon: 'emby' }, { title: '用户管理', description: '绑定 Emby 用户使用的 115 账号。', to: '/emby/users', icon: 'emby' }],
  },
}
const current = computed(() => categories[props.category])
</script>

<template>
  <ManagementLayout :title="current.title" :description="current.description" @logout="emit('logout')">
    <div class="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
      <RouterLink v-for="feature in current.features" :key="feature.to" :to="feature.to" class="group flex min-w-0 flex-col rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-6 shadow-sm transition hover:border-slate-300 dark:border-slate-600 hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-amber-400">
        <span class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl" :class="feature.icon === 'emby' ? 'bg-emerald-50 dark:bg-emerald-900/30' : 'bg-blue-50 dark:bg-blue-900/30 text-blue-600'">
          <img v-if="feature.icon === 'emby'" :src="embyIcon" alt="" class="h-7 w-7" />
          <svg v-else class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true">
            <path v-if="feature.icon === 'cloud'" d="M6 19a5 5 0 0 1-1-9.9A7 7 0 0 1 18.7 8a5.5 5.5 0 0 1-.2 11H6Z" />
            <template v-else-if="feature.icon === 'play'"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="m10 8 6 4-6 4V8Z"/></template>
            <template v-else-if="feature.icon === 'download'"><path d="M12 3v11m0 0 4-4m-4 4-4-4"/><path d="M5 17v2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-2"/></template>
            <template v-else-if="feature.icon === 'monitor'"><path d="M4 5h6l2 2h8v12H4z"/><path d="M8 12h8m-4-4v8"/></template>
            <path v-else d="m21 3-5 18-5-7-8-3L21 3Zm-10 11 5-6" />
          </svg>
        </span>
        <h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ feature.title }}</h2>
        <p class="mb-6 mt-2 flex-1 text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ feature.description }}</p>
        <span class="flex items-center justify-between border-t border-slate-100 dark:border-slate-800 pt-4 text-xs font-medium text-slate-500 dark:text-slate-400 dark:text-slate-500 group-hover:text-blue-600">进入功能<span aria-hidden="true">→</span></span>
      </RouterLink>
    </div>
  </ManagementLayout>
</template>
