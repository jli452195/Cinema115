# 前端组件复用

`frontend/src/components/MediaCard.vue` 用于展示电影和剧集卡片，接收一个 `item` 属性。看板已使用此组件。组件负责海报懒加载、加载失败占位、评分、片名、年份与详情跳转；父页面负责查询数据、分页和排列卡片。

在已注册项目路由的页面中使用：

```vue
<script setup lang="ts">
import MediaCard from '../components/MediaCard.vue'
import type { MediaCardItem } from '../types/media'

defineProps<{ items: MediaCardItem[] }>()
</script>

<template>
  <div class="grid grid-cols-2 gap-5 md:grid-cols-4">
    <MediaCard
      v-for="item in items"
      :key="`${item.kind}-${item.id}`"
      :item="item"
    />
  </div>
</template>
```

数据类型定义在 `frontend/src/types/media.ts`：

| 字段 | 用途 |
| --- | --- |
| `id` | TMDB ID |
| `kind` | `movie` 或 `tv`，对应 `/movie/:id` 或 `/tv/:id` |
| `title` | 显示片名 |
| `posterUrl` | 海报地址，空字符串或加载失败时显示占位 |
| `date` | 上映／首播日期，显示前四位年份；空字符串显示「年份未知」 |
| `rating`、`votes` | TMDB 评分与评价人数；没有评价时显示「暂无评分」 |
| `overview`、`originalTitle` | 可选，鼠标悬停提示优先使用简介，其次原名、片名 |

卡片宽度随父容器变化，海报保持 2:3 比例。组件不发起影视数据请求，也不依赖管理员令牌或看板筛选状态。
