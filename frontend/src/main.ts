import { createApp } from 'vue'
import './style.css'
import './styles/floatingSurface.css'
import './styles/ambientGlow.css'
import './styles/mediaSearchDialog.css'
import './styles/dashboardFilters.css'
import App from './App.vue'
import { router } from './router'
import { useTheme } from './composables/useTheme'
import { useGlassSettings } from './composables/useGlassSettings'
import { installAutoHideScrollbars } from './lib/auto-hide-scrollbar'

useTheme()
useGlassSettings()
installAutoHideScrollbars()


const app = createApp(App).use(router)
void router.isReady().then(() => app.mount('#app'))
