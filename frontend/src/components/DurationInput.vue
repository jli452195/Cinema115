<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import SelectMenu from './SelectMenu.vue'

const seconds = defineModel<number>({ required: true })
const props = withDefaults(defineProps<{ label: string; caption?: string; minimum?: number; disabled?: boolean; initialUnit?: 'seconds' | 'minutes' }>(), { minimum: 1 })
const inputID = `duration-${useId()}`
const unit = ref(props.initialUnit || (seconds.value >= 60 && seconds.value % 60 === 0 ? 'minutes' : 'seconds'))
const options = [{ value: 'seconds', label: '秒' }, { value: 'minutes', label: '分钟' }]
const factor = computed(() => unit.value === 'minutes' ? 60 : 1)
const amount = computed({ get: () => seconds.value / factor.value, set: value => { seconds.value = Number(value) * factor.value } })
const valid = computed(() => Number.isInteger(seconds.value) && seconds.value >= props.minimum && seconds.value <= 86400)
const input = ref<HTMLInputElement>()
watch([valid, input], ([value]) => input.value?.setCustomValidity(value ? '' : `请输入 ${props.minimum} 至 86400 的整数秒数`), { immediate: true, flush: 'post' })
</script>

<template>
  <div class="duration-input">
    <label :for="inputID">{{ caption || label }}</label>
    <div class="duration-value">
      <input :id="inputID" ref="input" v-model.number="amount" :aria-label="label" type="number" :min="minimum / factor" :max="86400 / factor" step="any" required :disabled="disabled" />
      <SelectMenu v-model="unit" class="duration-unit" :options="options" :label="`${label}单位`" :disabled="disabled" />
    </div>
  </div>
</template>

<style scoped>
.duration-input{display:flex;min-width:0;align-items:center;gap:10px}.duration-input>label{flex:none;margin:0;white-space:nowrap;font-size:13px;font-weight:400;line-height:20px;color:rgb(var(--color-slate-700))}.duration-value{display:flex;min-width:0;align-items:center;gap:8px}.duration-value input{width:100px;min-width:0;min-height:38px;padding:7px 10px;border:1px solid rgb(148 163 184/.25);border-radius:9px;background:rgb(var(--color-slate-50));color:rgb(var(--color-slate-900));outline:none;font-size:13px}.duration-value input:focus{border-color:#3b82f6}.duration-value input:disabled{opacity:.5}.duration-unit{width:82px;min-width:0!important}.duration-unit :deep(button[role=combobox]){height:38px;gap:8px;padding:7px 10px;font-size:13px}:global(.dark .duration-input>label){color:#cbd5e1}:global(.dark .duration-value input){background:#1e293b;color:#e2e8f0;border-color:rgb(148 163 184/.3)}
@media(max-width:639px){.duration-value input{min-height:40px}.duration-unit :deep(button[role=combobox]){height:40px}}
</style>
