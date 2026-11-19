<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import BottomSheetDialog from './BottomSheetDialog.vue'
import DialogPage from './DialogPage.vue'
import { useSheetFlow } from '../composables/useSheetFlow'
import type { DismissDecision, DismissReason } from '../composables/useMobileSheet'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{
  title: string
  closeLabel: string
  headingID?: string
  size?: 'config' | 'preview' | 'organize'
  closeDisabled?: boolean
  mobilePresentation?: 'dialog' | 'sheet'
  active?: boolean
  standalone?: boolean
  flowRoot?: boolean
  beforeDismiss?: (reason: DismissReason) => DismissDecision | Promise<DismissDecision>
}>(), { mobilePresentation: 'sheet', active: true, standalone: false })
const emit = defineEmits<{ close: []; scroll: [top: number] }>()
const flow = useSheetFlow()
const inFlow = !!flow && !props.standalone
const flowRoot = inFlow && props.flowRoot
const page = ref<InstanceType<typeof DialogPage>>()
const headingID = props.headingID || useId(), pageID = useId()
const body = computed(() => page.value?.body)
const activePage = computed(() => flowRoot ? flow?.depth.value === 1 : props.active && flow?.isActive(pageID))
let savedTop = 0

// 页面隐藏只注销导航位置，保留同次搜索中的整理实例及未提交草稿。
watch(() => [flow?.stage.value, props.active] as const, ([stage, active], _, onCleanup) => {
  if (!inFlow || flowRoot || !stage || !active) return
  const unregister = flow!.registerPage({
    id: pageID, headingID,
    getBody: () => body.value,
    getContainer: () => page.value?.container,
    isBlocked: () => !!props.closeDisabled,
    beforeDismiss: reason => props.beforeDismiss?.(reason) ?? 'dismiss',
    onPop: () => emit('close'),
  })
  void nextTick(() => { if (activePage.value && body.value) body.value.scrollTop = savedTop })
  onCleanup(() => { savedTop = body.value?.scrollTop ?? savedTop; unregister() })
}, { immediate: true, flush: 'pre' })

defineExpose({ body, scrollTo: (top: number) => page.value?.scrollTo(top) })
</script>

<template>
  <template v-if="inFlow">
    <!-- 目标尚未就绪时等待；不能临时创建另一个原生弹窗。 -->
    <Teleport v-if="flow?.stage.value" :to="flow.stage.value">
      <DialogPage v-show="activePage" ref="page" v-bind="$attrs" class="floating-dialog sheet-flow-page" :class="size && `floating-dialog-${size}`" :inert="!activePage" :title="title" :heading-i-d="headingID" :close-label="flowRoot ? closeLabel : '关闭整个窗口'" :back-label="'返回上一级'" :back-disabled="closeDisabled || flow.blocked.value" :close-disabled="flow.blocked.value" :back="!flowRoot" closable :expanded="flow.expanded.value" :expandable="mobilePresentation === 'sheet'" @back="flow.requestDismiss('button')" @close="flow.requestCloseAll()" @expand="flow.toggleExpanded" @scroll="emit('scroll', $event)">
        <template v-for="(_, name) in $slots" #[name]="slotProps"><slot :name="name" v-bind="slotProps" /></template>
      </DialogPage>
    </Teleport>
  </template>
  <Teleport v-else to="body">
    <BottomSheetDialog v-bind="$attrs" class="floating-dialog" :class="size && `floating-dialog-${size}`" :aria-labelledby="headingID" :mobile-sheet="mobilePresentation === 'sheet'" :close-disabled="closeDisabled" :before-close="beforeDismiss" desktop-backdrop-dismiss @close="emit('close')" v-slot="{ close, expanded, toggleExpanded }">
      <DialogPage ref="page" :title="title" :heading-i-d="headingID" :close-label="closeLabel" :close-disabled="closeDisabled" closable :expanded="expanded" :expandable="mobilePresentation === 'sheet'" @close="close" @expand="toggleExpanded" @scroll="emit('scroll', $event)">
        <template v-for="(_, name) in $slots" #[name]="slotProps"><slot :name="name" v-bind="slotProps" /></template>
      </DialogPage>
    </BottomSheetDialog>
  </Teleport>
</template>

<style scoped>
.floating-dialog {
  --dialog-control-height: 36px;
  --dialog-control-font: 13px;
  --dialog-input-font: 13px;
  --dialog-control-padding: 6px 10px;
  --dialog-content-inset: 22px;
  min-width: 0;
  padding: 0;
  overflow: clip;
  color: var(--floating-surface-text);
  font-size: 13px;
  line-height: 1.6;
}
dialog.floating-dialog[open] { display: flex; flex-direction: column; }
.sheet-flow-page { flex: 1 1 auto; width: 100%; min-height: 0; margin: 0; border: 0; border-radius: 0; background: var(--floating-surface-container); }
.floating-dialog-preview :deep(.floating-dialog-body) { display: flex; overflow: hidden; padding-bottom: 4px; }
.floating-dialog-preview :deep(.floating-dialog-footer) { padding-top: 6px; border-top: 0; }
/* 尺寸只约束独立外壳，内部页面始终贴合持续存在的宿主。 */
@media (min-width: 640px) {
  dialog.floating-dialog { width: min(760px, calc(100vw - 32px)); max-height: min(880px, calc(100dvh - 48px)); margin: auto; border-radius: 20px; }
  dialog.floating-dialog-config { width: min(820px, calc(100vw - 32px)); height: min(760px, calc(100dvh - 48px)); }
  dialog.floating-dialog-organize { width: min(1040px, calc(100vw - 48px)); height: min(820px, calc(100dvh - 48px)); border-radius: 24px; }
  dialog.floating-dialog-preview { width: min(1600px, calc(100vw - 40px)); height: min(760px, calc(100dvh - 40px)); max-height: calc(100dvh - 40px); }
  :is(.floating-dialog-config, .floating-dialog-organize) :deep(.floating-dialog-body) { scrollbar-gutter: stable; }
  .floating-dialog-organize :deep(.floating-dialog-body) { padding-top: 0; }
}
@media (max-width: 639px), (pointer: coarse) {
  .floating-dialog { --dialog-control-height: var(--sheet-touch-size); --dialog-control-font: var(--sheet-font-body); --dialog-input-font: var(--sheet-font-input); --dialog-control-padding: 8px 10px; }
}
@media (max-width: 639px) {
  .floating-dialog { --dialog-content-inset: var(--sheet-content-inset); font-size: var(--sheet-font-body); line-height: var(--sheet-line-body); }
  dialog.floating-dialog:not(.bottom-sheet-dialog-mobile) { width: calc(100vw - 24px); max-height: calc(100dvh - 24px); margin: auto; border-radius: 20px; }
}
</style>
