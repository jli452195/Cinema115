<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Compartment, EditorState } from '@codemirror/state'
import { Decoration, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { bracketMatching } from '@codemirror/language'

const props = defineProps<{ modelValue: string; label: string; disabled?: boolean; errorLine?: number; describedby?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const host = ref<HTMLElement>(), mount = ref<HTMLElement>(), plainInput = ref<HTMLTextAreaElement>(), failed = ref(false)
const access = new Compartment(), errors = new Compartment()
let view: EditorView | undefined, source = props.modelValue, original = source, normalized = '', separator = '\n'
const permissions = () => [EditorState.readOnly.of(!!props.disabled), EditorView.editable.of(!props.disabled)]
function errorDecoration(state: EditorState) {
  const line = props.errorLine
  return EditorView.decorations.of(line && line > 0 && line <= state.doc.lines
    ? Decoration.set([Decoration.line({ class: 'text-source-error-line' }).range(state.doc.line(line).from)])
    : Decoration.none)
}
function createState(value: string) {
  original = value; normalized = value.replace(/\r\n?/g, '\n'); separator = value.match(/\r\n?|\n/)?.[0] || '\n'
  return EditorState.create({
    doc: normalized,
    extensions: [lineNumbers(), highlightActiveLine(), highlightActiveLineGutter(), bracketMatching(), history(), keymap.of([...defaultKeymap, ...historyKeymap]), access.of(permissions()), errors.of([]),
      EditorView.contentAttributes.of(() => ({ role: 'textbox', 'aria-label': props.label, 'aria-multiline': 'true', 'aria-readonly': String(!!props.disabled), 'aria-invalid': String(!!props.errorLine), ...(props.describedby ? { 'aria-describedby': props.describedby } : {}), spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off', autocorrect: 'off' })),
      EditorView.updateListener.of(update => {
        if (!update.docChanged) return
        const value = update.state.doc.toString()
        source = value === normalized ? original : update.state.doc.sliceString(0, undefined, separator)
        emit('update:modelValue', source)
      }),
    ],
  })
}
function markError() { if (view) view.dispatch({ effects: errors.reconfigure(errorDecoration(view.state)) }) }
watch(() => props.modelValue, value => { if (value === source) return; source = value; view?.setState(createState(value)); markError() })
watch(() => props.disabled, () => view?.dispatch({ effects: access.reconfigure(permissions()) }))
watch(() => props.errorLine, markError)
watch(() => props.label, () => view?.dispatch({}))
onMounted(() => {
  try { view = new EditorView({ parent: mount.value!, state: createState(source) }); markError() }
  catch { failed.value = true }
})
onBeforeUnmount(() => view?.destroy())
function locate(line = 1, column = 1) {
  if (!view) {
    const input = plainInput.value
    if (!input) return
    const lines = input.value.split('\n'), row = Math.min(Math.max(1, line), lines.length) - 1
    const start = lines.slice(0, row).reduce((n, text) => n + text.length + 1, 0)
    input.focus(); input.setSelectionRange(start, start + (lines[row]?.length || 0))
    input.scrollTop = Math.max(0, row * parseFloat(getComputedStyle(input).lineHeight) - input.clientHeight / 2)
  } else {
    const target = view.state.doc.line(Math.min(Math.max(1, line), view.state.doc.lines))
    const anchor = target.from + Math.min(Math.max(column - 1, 0), target.length)
    view.dispatch({ selection: { anchor, head: target.to }, effects: EditorView.scrollIntoView(anchor, { y: 'center', x: 'nearest' }) }); view.focus()
  }
  host.value?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
}
defineExpose({ locate, focus: () => view ? view.focus() : plainInput.value?.focus() })
</script>

<template>
  <div ref="host" class="text-source-editor" :class="{ 'is-invalid': errorLine, 'is-readonly': disabled }">
    <div v-if="!failed" ref="mount"></div>
    <textarea v-else ref="plainInput" :value="modelValue" :readonly="disabled" :aria-label="label" :aria-invalid="!!errorLine" :aria-describedby="describedby" spellcheck="false" autocapitalize="off" autocomplete="off" autocorrect="off" wrap="off" @input="source = ($event.target as HTMLTextAreaElement).value; emit('update:modelValue', source)"></textarea>
  </div>
</template>

<style scoped>
.text-source-editor { min-width:0; color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-50) / .5); }
.text-source-editor:focus-within { box-shadow:inset 0 0 0 1px #2563eb; }.text-source-editor.is-invalid { box-shadow:inset 0 0 0 1px #dc2626; }.text-source-editor.is-readonly { opacity:.7; }
.text-source-editor :deep(.cm-editor),.text-source-editor textarea { height:clamp(390px,56dvh,620px); outline:none; font-size:13px; background:transparent; }
.text-source-editor :deep(.cm-scroller) { font-family:ui-monospace,SFMono-Regular,Consolas,monospace; line-height:1.9; overflow:auto; }
.text-source-editor :deep(.cm-content) { padding:12px 0; caret-color:#2563eb; }.text-source-editor :deep(.cm-line) { padding-inline:12px; }
.text-source-editor :deep(.cm-gutters) { color:rgb(var(--color-slate-400)); background:rgb(var(--color-slate-100) / .6); border-right:1px solid rgb(var(--color-slate-200) / .7); }
.text-source-editor :deep(.cm-lineNumbers .cm-gutterElement) { min-width:34px; padding-inline:8px; }
.text-source-editor :deep(.cm-activeLine),.text-source-editor :deep(.cm-activeLineGutter) { background:rgb(37 99 235 / .045); }
.text-source-editor :deep(.cm-selectionBackground),.text-source-editor :deep(.cm-content ::selection) { background:rgb(37 99 235 / .18) !important; }
.text-source-editor :deep(.cm-cursor) { border-left-color:#2563eb; }.text-source-editor :deep(.cm-matchingBracket) { background:rgb(37 99 235 / .15); }
.text-source-editor :deep(.text-source-error-line) { background:rgb(220 38 38 / .09); text-decoration:underline wavy rgb(220 38 38 / .5); }
.text-source-editor textarea { display:block; width:100%; border:0; padding:12px; resize:vertical; color:inherit; font-family:ui-monospace,SFMono-Regular,Consolas,monospace; line-height:1.9; }
.dark .text-source-editor { background:rgb(var(--color-slate-950) / .3); color:rgb(var(--color-slate-200)); }.dark .text-source-editor :deep(.cm-gutters) { background:rgb(var(--color-slate-900) / .7); border-color:rgb(var(--color-slate-800)); color:rgb(var(--color-slate-500)); }.dark .text-source-editor :deep(.cm-activeLine),.dark .text-source-editor :deep(.cm-activeLineGutter) { background:rgb(147 197 253 / .06); }
@media(max-width:639px) { .text-source-editor :deep(.cm-editor),.text-source-editor textarea { height:320px; font-size:16px; }.text-source-editor :deep(.cm-line) { padding-inline:8px; } }
</style>
