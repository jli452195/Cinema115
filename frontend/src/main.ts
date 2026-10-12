import { createApp } from 'vue'
import './style.css'
import './styles/mediaSearchDialog.css'
import './styles/dashboardFilters.css'
import App from './App.vue'
import { router } from './router'
import { useTheme } from './composables/useTheme'

useTheme()


const app = createApp(App).use(router)
void router.isReady().then(() => app.mount('#app'))
