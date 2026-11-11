import Teek from 'vitepress-theme-teek'
import DocsLayout from './components/DocsLayout.vue'
// 使用纯样式入口；本站图标均为 SVG，无需加载社交 iconfont 字体。
import 'vitepress-theme-teek/theme-chalk/index.css'
import './style.css'
import './appearance.css'

export default {
  extends: Teek,
  Layout: DocsLayout,
}
