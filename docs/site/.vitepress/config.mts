import { defineConfig } from 'vitepress'
import { defineTeekConfig } from 'vitepress-theme-teek/config'

const sections = [
  { text: '开始使用', link: '/guide/', activeMatch: '/guide/' },
  { text: '资源搜索与整理', link: '/resources/', activeMatch: '/resources/' },
  { text: '动漫订阅', link: '/anime/', activeMatch: '/anime/' },
  { text: '常见问题', link: '/faq/', activeMatch: '/faq/' },
  { text: '编写文档', link: '/contributing/', activeMatch: '/contributing/' },
]

// 动漫教程提供常用操作直达入口，避免用户只能从长文逐段寻找。
const animeSidebar = [
  {
    text: 'AniBT 订阅入门',
    link: '/anime/',
    items: [
      { text: '下载与整理目录', link: '/anime/#storage-settings' },
      { text: '字幕组与资源规格', link: '/anime/#source-selection' },
      { text: 'Bangumi 集数在哪里看', link: '/anime/#bangumi-episodes' },
    ],
  },
  {
    text: '匹配规则与命名',
    link: '/anime/rules-and-naming',
    items: [
      { text: '添加备用 RSS', link: '/anime/rules-and-naming#backup-rss' },
      { text: 'TMDB、总集数与偏移', link: '/anime/rules-and-naming#episode-naming' },
      { text: '预览与重新下载', link: '/anime/rules-and-naming#release-preview' },
    ],
  },
  {
    text: '订阅管理与故障排查',
    link: '/anime/management',
    items: [
      { text: '下载记录与整理进度', link: '/anime/management#download-records' },
      { text: 'RSS 与 Telegram 通知', link: '/anime/management#rss-and-notifications' },
    ],
  },
]

const resourcesSidebar = [{
  text: '操作指南',
  link: '/resources/',
}, {
  text: '实战：妹妹公主两季 BDRip',
  link: '/resources/sister-princess',
}]

const teekConfig = defineTeekConfig({
  teekHome: false,
  windowTransition: false,
  viewTransition: { enabled: false },
  themeEnhance: { enabled: false },
  articleAnalyze: { showInfo: false },
  articleUpdate: { enabled: false },
  breadcrumb: { enabled: false },
  footerInfo: {
    topMessage: 'Cinema115 使用文档',
    copyright: { show: false },
  },
  vitePlugins: {
    permalink: false,
    mdH1: false,
    docAnalysis: false,
    sidebarOption: {
      type: 'array',
      fileIndexPrefix: false,
      scannerRootMd: false,
      collapsed: false,
      restart: true,
      // 顶级分类沿用导航顺序；动漫教程使用操作导航，其余分类自动扫描和排序。
      sidebarResolved: sidebar => Array.isArray(sidebar)
        ? sections.map(section => ({
            text: section.text,
            collapsed: false,
            items: section.link === '/anime/' ? animeSidebar : section.link === '/resources/' ? resourcesSidebar : sidebar.find(group =>
              group.items?.some(item => item.link?.startsWith(section.link)),
            )?.items ?? [],
          }))
        : sidebar,
    },
  },
})

export default defineConfig({
  extends: teekConfig,
  lang: 'zh-CN',
  title: 'Cinema115',
  description: 'Cinema115 使用文档：安装部署、115 账号配置、资源搜索与下载整理、字幕匹配、动漫订阅与更新备份。',
  base: '/Cinema115/',
  // Teek 默认忽略死链；用户文档保留构建检查，避免发布失效的教程链接。
  ignoreDeadLinks: false,
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/Cinema115/cinema115-mark.svg' }]],
  themeConfig: {
    logo: { src: '/cinema115-mark.svg', alt: 'Cinema115' },
    siteTitle: 'Cinema115 文档',
    nav: sections,
    socialLinks: [{ icon: 'github', link: 'https://github.com/jli452195/Cinema115' }],
    search: {
      provider: 'local',
      options: {
        locales: {
          root: {
            translations: {
              button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
              modal: {
                displayDetails: '显示详细结果',
                resetButtonTitle: '清除搜索',
                backButtonTitle: '关闭搜索',
                noResultsText: '没有找到相关文档',
                footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
              },
            },
          },
        },
      },
    },
    outline: { level: [2, 3], label: '本页目录' },
    skipToContentLabel: '跳转到正文',
    sidebarMenuLabel: '文档目录',
    returnToTopLabel: '返回顶部',
    darkModeSwitchLabel: '切换深浅主题',
    lightModeSwitchTitle: '切换到浅色模式',
    darkModeSwitchTitle: '切换到深色模式',
    docFooter: { prev: '上一篇', next: '下一篇' },
    editLink: {
      pattern: 'https://github.com/jli452195/Cinema115/edit/master/docs/site/:path',
      text: '在 GitHub 上编辑此页',
    },
  },
})
