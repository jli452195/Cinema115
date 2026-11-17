import { ref } from 'vue'

export const appearanceSettingsOpen = ref(false)
export function openAppearanceSettings() { appearanceSettingsOpen.value = true }
export function closeAppearanceSettings() { appearanceSettingsOpen.value = false }
