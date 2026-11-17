<script setup lang="ts">
import { IconEdit, IconFolder, IconPlus, IconTrash } from '@tabler/icons-vue'
import type { ED2KSTRMTarget } from '../types/ed2kSTRM'
import { strmDirectoryPath } from '../utils/strmDirectory'

defineProps<{
  targets: ED2KSTRMTarget[]
  busy: boolean
}>()

const emit = defineEmits<{
  add: []
  edit: [target: ED2KSTRMTarget]
  delete: [target: ED2KSTRMTarget]
}>()
</script>

<template>
  <section class="es-targets-section" aria-label="本地输出目录">
    <header class="es-targets-header">
      <div class="es-targets-header-main">
        <h2>
          <IconFolder :size="18" aria-hidden="true" />
          <span>本地输出目录</span>
          <span class="es-counter">{{ targets.length }}</span>
        </h2>
        <p class="es-help">
          每次导入选择一个目录，STRM 按文件名直接平铺写入该目录。目录可分别在 Telegram 配置中分配给 Bot。
        </p>
      </div>

      <button
        v-if="targets.length > 0"
        type="button"
        class="es-button es-targets-header-add"
        :disabled="!!busy"
        @click="emit('add')"
      >
        <IconPlus :size="16" aria-hidden="true" />添加目录
      </button>
    </header>

    <!-- 0 目录引导空状态 -->
    <div v-if="!targets.length" class="es-targets-empty">
      <div class="es-targets-empty-icon" aria-hidden="true">
        <IconFolder :size="22" />
      </div>
      <p class="es-targets-empty-title">尚未配置本地输出目录</p>
      <p class="es-help">先添加一个输出目录，再前往「生成 STRM」导入 ED2K 链接。</p>
      <button type="button" class="es-button es-secondary" :disabled="!!busy" @click="emit('add')">
        <IconPlus :size="16" aria-hidden="true" />添加输出目录
      </button>
    </div>

    <!-- 一目录一卡片自适应网格 -->
    <ul v-else class="es-target-grid">
      <li v-for="target in targets" :key="target.id" class="es-target-card">
        <!-- 顶栏：目录图标、名称与编辑/删除操作 -->
        <div class="es-target-card-head">
          <div class="es-target-card-name">
            <span class="es-target-folder-badge" aria-hidden="true">
              <IconFolder :size="16" />
            </span>
            <strong>{{ target.name }}</strong>
          </div>

          <div class="es-target-card-actions">
            <button
              type="button"
              class="es-target-icon-btn"
              :disabled="!!busy"
              :aria-label="'编辑目录 ' + target.name"
              title="编辑目录"
              @click="emit('edit', target)"
            >
              <IconEdit :size="16" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="es-target-icon-btn is-danger"
              :disabled="!!busy"
              :aria-label="'删除目录 ' + target.name"
              title="删除目录"
              @click="emit('delete', target)"
            >
              <IconTrash :size="16" aria-hidden="true" />
            </button>
          </div>
        </div>

        <!-- 完整展示路径，长目录可自然换行。 -->
        <p class="es-target-path">{{ strmDirectoryPath(target.outputDir) }}</p>

        <!-- 底栏只保留每个目录的 Bot 分配状态。 -->
        <footer class="es-target-card-foot">
          <span v-if="target.assignedBotId" class="es-target-bot-pill">已分配 Bot</span>
          <span v-else class="es-target-bot-idle">未分配 Bot</span>
        </footer>
      </li>

      <!-- 网格末尾虚线新增槽位卡（仅多列布局展示，小屏隐藏避免与顶部按钮重复） -->
      <li class="es-target-add-slot">
        <button type="button" class="es-target-add-card" :disabled="!!busy" @click="emit('add')">
          <span class="es-target-add-icon" aria-hidden="true">
            <IconPlus :size="18" />
          </span>
          <span>添加输出目录</span>
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.es-targets-section {
  min-width: 0;
  margin-top: 22px;
  margin-bottom: 18px;
}

.es-targets-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 16px;
}

.es-targets-header-main {
  min-width: 0;
  flex: 1 1 260px;
}

.es-targets-header-main h2 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: rgb(var(--content-primary));
}

.es-targets-header-main h2 svg {
  flex: none;
  color: rgb(var(--action-link));
}

.es-counter,
.es-help {
  font-size: 12px;
  line-height: 1.75;
  color: rgb(var(--content-secondary));
}

.es-counter {
  font-weight: 400;
}

.es-help {
  margin-top: 6px;
}

.es-targets-header-add {
  flex: none;
}

/* 通用按钮（与父视图视觉对齐） */
.es-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 36px;
  padding: 7px 11px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 8px;
  background: rgb(var(--surface-card));
  font-size: 12px;
  font-weight: 500;
  color: rgb(var(--content-secondary));
  white-space: nowrap;
  cursor: pointer;
}

.es-secondary {
  background: rgb(59 130 246 / .08);
  color: rgb(var(--action-link));
  border-color: rgb(59 130 246 / .22);
}

.es-button:disabled {
  opacity: 0.45;
  cursor: default;
}

.es-button:focus-visible {
  outline: 2px solid #3b82f6;
  outline-offset: 2px;
}

/* 0 目录引导空状态 */
.es-targets-empty {
  display: grid;
  justify-items: center;
  gap: 8px;
  margin-top: 14px;
  padding: 28px 18px;
  border: 1px dashed rgb(var(--line-default));
  border-radius: 14px;
  background: rgb(var(--surface-card));
  text-align: center;
}

.es-targets-empty-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  border-radius: 11px;
  background: rgb(59 130 246 / .09);
  color: rgb(var(--action-link));
}

.es-targets-empty-title {
  font-size: 13px;
  font-weight: 600;
  color: rgb(var(--content-primary));
}

.es-targets-empty .es-help {
  margin-top: 0;
  max-width: 360px;
}

.es-targets-empty .es-button {
  margin-top: 4px;
}

/* 按侧栏后的实际宽度自适应，超宽屏封顶三列，保留一像素余量避免亚像素折行。 */
.es-target-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(clamp(min(100%, 290px), calc((100% - 29px) / 3), 100%), 1fr));
  gap: 14px;
  margin-top: 14px;
}

/* 单张目录卡片：整行自然等高，内部纵向弹性分布 */
.es-target-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
  padding: 15px 16px 14px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 14px;
  background: rgb(var(--surface-card));
  color: rgb(var(--content-primary));
  transition: border-color 150ms;
}

@media (hover: hover) {
  .es-target-card:hover {
    border-color: rgb(59 130 246 / .32);
  }
}

/* 卡片顶栏 */
.es-target-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
  min-width: 0;
}

.es-target-card-name {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  min-width: 0;
  flex: 1;
  padding-top: 3px;
}

.es-target-folder-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 28px;
  height: 28px;
  margin-top: -2px;
  border-radius: 8px;
  background: rgb(59 130 246 / .09);
  color: rgb(var(--action-link));
}

.es-target-card-name strong {
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.45;
  color: rgb(var(--content-primary));
  overflow-wrap: anywhere;
  word-break: break-word;
}

.es-target-card-actions {
  display: inline-flex;
  align-items: center;
  flex: none;
  gap: 4px;
  margin: -4px -6px 0 0;
}

.es-target-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 38px;
  min-height: 38px;
  padding: 8px;
  border: 1px solid transparent;
  border-radius: 8px;
  background: transparent;
  color: rgb(var(--content-secondary));
  cursor: pointer;
  transition: background-color 150ms, color 150ms;
}

@media (hover: hover) {
  .es-target-icon-btn:not(:disabled):hover {
    background: rgb(var(--surface-hover));
    color: rgb(var(--content-primary));
  }
  .es-target-icon-btn.is-danger:not(:disabled):hover {
    background: rgb(239 68 68 / .08);
    color: rgb(var(--status-danger));
  }
}

.es-target-icon-btn:disabled {
  opacity: 0.42;
  cursor: default;
}

.es-target-icon-btn:focus-visible {
  outline: 2px solid #3b82f6;
  outline-offset: 2px;
}

.es-target-path {
  display: block;
  min-width: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  color: rgb(var(--content-secondary));
  overflow-wrap: anywhere;
  word-break: break-word;
  user-select: text;
}

/* 卡片底栏：Bot 分配状态自然沉底对齐。 */
.es-target-card-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: auto;
  padding-top: 10px;
  border-top: 1px solid rgb(var(--line-default));
  font-size: 11px;
}

.es-target-bot-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 6px;
  background: rgb(59 130 246 / .1);
  color: rgb(var(--action-link));
  font-weight: 500;
}

.es-target-bot-idle {
  color: rgb(var(--content-secondary));
}

/* 网格末尾虚线添加槽位卡片 */
.es-target-add-slot {
  display: flex;
  min-width: 0;
}

.es-target-add-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  min-height: 122px;
  padding: 18px 16px;
  border: 1.5px dashed rgb(var(--line-default));
  border-radius: 14px;
  background: transparent;
  color: rgb(var(--content-secondary));
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: border-color 150ms, color 150ms, background-color 150ms;
}

.es-target-add-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 999px;
  background: rgb(var(--surface-card));
  border: 1px solid rgb(var(--line-default));
  color: rgb(var(--action-link));
  transition: border-color 150ms, background-color 150ms;
}

@media (hover: hover) {
  .es-target-add-card:not(:disabled):hover {
    border-color: rgb(59 130 246 / .48);
    background: rgb(59 130 246 / .04);
    color: rgb(var(--action-link));
  }
  .es-target-add-card:not(:disabled):hover .es-target-add-icon {
    border-color: rgb(59 130 246 / .35);
    background: rgb(59 130 246 / .1);
  }
}

.es-target-add-card:disabled {
  opacity: 0.45;
  cursor: default;
}

.es-target-add-card:focus-visible {
  outline: 2px solid #3b82f6;
  outline-offset: 2px;
  border-color: #3b82f6;
}

/* 移动端与粗指针设备：编辑、删除按钮至少 44px 触控热区。 */
@media (max-width: 767px), (pointer: coarse) {
  .es-target-icon-btn {
    min-width: 44px;
    min-height: 44px;
  }
}

/* 小屏单列（<= 639px，覆盖 390px / 320px）：隐藏末尾虚线卡，避免与顶部添加按钮重复拥挤 */
@media (max-width: 639px) {
  .es-target-add-slot {
    display: none;
  }

  .es-targets-header-add,
  .es-targets-empty .es-button {
    min-height: 44px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .es-target-card,
  .es-target-icon-btn,
  .es-target-add-card,
  .es-target-add-icon {
    transition: none;
  }
}
</style>
