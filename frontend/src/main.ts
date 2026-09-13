import { createApp } from 'vue'
import './style.css'
import App from './App.vue'
import { router } from './router'

const app = createApp(App).use(router)
void router.isReady().then(() => app.mount('#app'))
