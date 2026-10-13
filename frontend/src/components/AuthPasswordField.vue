<script setup lang="ts">
import { ref } from 'vue'

defineProps<{
  id: string
  label: string
  autocomplete: 'current-password' | 'new-password'
  placeholder?: string
  minlength?: number
  readonly?: boolean
  error?: string
  hint?: string
}>()
const model = defineModel<string>({ default: '' })
const visible = ref(false)
</script>

<template>
  <div class="auth-field">
    <label :for="id">{{ label }}</label>
    <div class="auth-password">
      <input :id="id" v-model="model" :name="id" class="auth-input" :type="visible ? 'text' : 'password'" required :minlength="minlength" maxlength="128" :autocomplete="autocomplete" :placeholder="placeholder" :readonly="readonly" :aria-invalid="error ? true : undefined" :aria-describedby="[hint ? `${id}-hint` : '', error ? `${id}-error` : ''].filter(Boolean).join(' ') || undefined" />
      <button type="button" class="auth-icon-button" :aria-label="`${visible ? '隐藏' : '显示'}${label}`" :aria-pressed="visible" @click="visible = !visible">
        <svg v-if="visible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m3 3 18 18M10.6 10.6a2 2 0 0 0 2.8 2.8M9.9 5.2A10.7 10.7 0 0 1 12 5c5.5 0 9 7 9 7a16.6 16.6 0 0 1-3.3 4.2M6.3 6.3A17.3 17.3 0 0 0 3 12s3.5 7 9 7a9.7 9.7 0 0 0 5.1-1.5" /></svg>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12s3.5-7 9-7 9 7 9 7-3.5 7-9 7-9-7-9-7Z" /><circle cx="12" cy="12" r="3" /></svg>
      </button>
    </div>
    <p v-if="hint" :id="`${id}-hint`" class="auth-hint">{{ hint }}</p>
    <p v-if="error" :id="`${id}-error`" class="auth-field-error" role="alert">{{ error }}</p>
  </div>
</template>
