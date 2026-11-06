<script setup lang="ts">
/**
 * PatternBackground.vue
 *
 * 点阵动效背景组件
 * 灵感与技术参考自 Inspira UI (Pattern Background / Dot Pattern)
 * 来源: https://inspira-ui.com/docs/en/components/backgrounds/pattern-background
 *
 * 核心技术实现：
 * 1. 采用原生 CSS radial-gradient 渲染规则点阵；
 * 2. 结合 background-position 的垂直位移动画实现轻量平滑的持续向下流动效果；
 * 3. 采用固定 ellipse 径向渐变遮罩（mask-image）实现中心清晰、边缘柔和渐隐；
 * 4. 支持深色/浅色主题颜色自适应；
 * 5. 遵循无障碍设计规范：aria-hidden="true"、pointer-events: none、支持 prefers-reduced-motion 静止点阵。
 */
import { computed } from 'vue'

interface Props {
  /** 动效开关：是否开启动态漂移动画；关闭或在 prefers-reduced-motion 模式下为静态点阵 */
  animate?: boolean
  /** 移动方向：默认 'bottom' 向下移动 */
  direction?: 'bottom' | 'top' | 'left' | 'right'
  /** 点阵变体：'dot' 为圆点点阵，'grid' 为网格线 */
  variant?: 'dot' | 'grid'
  /** 点阵尺寸等级或具体像素，默认 'xl' 对应 40px */
  size?: 'sm' | 'md' | 'lg' | 'xl' | number
  /** 遮罩样式：'ellipse' 为边缘柔和渐隐，'none' 为全铺无渐隐 */
  mask?: 'ellipse' | 'none'
  /** 动效循环时长（毫秒），默认 17000ms */
  speed?: number
  /** 圆点半径（px），默认 1px */
  dotRadius?: number
  /** 点阵颜色，支持自定义颜色值或 CSS 变量；默认跟随主题的 --auth-pattern-dot */
  color?: string
}

const props = withDefaults(defineProps<Props>(), {
  animate: true,
  direction: 'bottom',
  variant: 'dot',
  size: 'xl',
  mask: 'ellipse',
  speed: 40000,
  dotRadius: 1,
  color: '',
})

// 预设计算尺寸映射表
const sizeMap: Record<string, number> = {
  sm: 16,
  md: 24,
  lg: 32,
  xl: 40,
}

const numericSize = computed(() => {
  if (typeof props.size === 'number') {
    return props.size
  }
  return sizeMap[props.size] ?? 40
})

const canvasStyle = computed(() => {
  const dotColor = props.color || 'var(--auth-pattern-dot, var(--pattern-color, rgb(148 163 184 / 45%)))'
  const sizePx = `${numericSize.value}px`
  const radiusPx = `${props.dotRadius}px`
  // 增加 0.5px 的抗锯齿平滑过渡，保证在 Retina 和不同 DPR 屏幕上圆点边缘精致无毛刺
  const antialiasPx = `${props.dotRadius + 0.5}px`

  const backgroundImage =
    props.variant === 'dot'
      ? `radial-gradient(circle, ${dotColor} ${radiusPx}, transparent ${antialiasPx})`
      : `linear-gradient(to right, ${dotColor} 1px, transparent 1px), linear-gradient(to bottom, ${dotColor} 1px, transparent 1px)`

  // 一个周期移动 24 个完整网格单元（例如 24 * 40px = 960px），首尾无缝衔接且具有可观的持续下流速度
  const moveDistance = numericSize.value * 24
  let moveX = '0px'
  let moveY = '0px'

  switch (props.direction) {
    case 'top':
      moveY = `-${moveDistance}px`
      break
    case 'left':
      moveX = `-${moveDistance}px`
      break
    case 'right':
      moveX = `${moveDistance}px`
      break
    case 'bottom':
    default:
      moveY = `${moveDistance}px`
      break
  }

  return {
    '--pattern-size': sizePx,
    '--pattern-speed': `${props.speed}ms`,
    '--pattern-move-x': moveX,
    '--pattern-move-y': moveY,
    backgroundImage,
    backgroundSize: `${sizePx} ${sizePx}`,
  }
})
</script>

<template>
  <div class="pattern-bg-wrapper" aria-hidden="true" tabindex="-1">
    <div
      class="pattern-bg-canvas"
      :class="[
        `mask-${mask}`,
        { 'is-animated': animate }
      ]"
      :style="canvasStyle"
    />
  </div>
</template>

<style scoped>
.pattern-bg-wrapper {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  user-select: none;
  z-index: 0;
}

.pattern-bg-canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  background-repeat: repeat;
}

/* 动效：利用 background-position 位移 24 个网格单元，实现平滑连续且明显的向下流动 */
.pattern-bg-canvas.is-animated {
  animation: pattern-drift var(--pattern-speed, 17000ms) linear infinite;
}

/* 遮罩：中心保持清晰，向四周边缘柔和渐隐，遮罩位置固定不受流动位移影响 */
.pattern-bg-canvas.mask-ellipse {
  -webkit-mask-image: radial-gradient(ellipse at 50% 50%, #000 45%, transparent 85%);
  mask-image: radial-gradient(ellipse at 50% 50%, #000 45%, transparent 85%);
  -webkit-mask-repeat: no-repeat;
  mask-repeat: no-repeat;
  -webkit-mask-size: 100% 100%;
  mask-size: 100% 100%;
}

@keyframes pattern-drift {
  0% {
    background-position: 0px 0px;
  }
  100% {
    background-position: var(--pattern-move-x, 0px) var(--pattern-move-y, 960px);
  }
}

/* 用户偏好减少动画时：停止动效，保留清晰优雅的静态点阵背景 */
@media (prefers-reduced-motion: reduce) {
  .pattern-bg-canvas.is-animated {
    animation: none !important;
  }
}
</style>
