// Category pages and the account shortcuts share the same destinations and labels.
export const managementCategories = {
  users: {
    title: '用户与邀请', description: '管理 Emby 用户的播放来源、子账号与注册邀请。',
    features: [
      { title: '用户管理', description: '查看 Emby 用户，管理播放网盘、播放会话与子账号。', to: '/users/list', icon: 'users', menuIcon: 'users' },
      { title: '注册邀请', description: '生成与管理注册邀请，设置新账号的默认媒体库。', to: '/users/invites', icon: 'invite', menuIcon: 'invite' },
    ],
  },
  p115: {
    title: '115 网盘', description: '管理网盘连接、分享播放与 STRM 生成。',
    features: [
      { title: '115 助手', description: '管理账号连接、扫码登录与 Cookie 校验，为分享播放提供凭证。', to: '/helper115', icon: 'cloud', menuIcon: 'cloud' },
      { title: '115分享STRM', description: '从分享链接生成 STRM，管理转存再分享、审核重试与任务记录。', to: '/115/share', icon: 'play', menuIcon: 'share' },
      { title: '115网盘STRM', description: '监控主网盘目录，保留层级生成本地 STRM，按需下载字幕与图片。', to: '/115/drive-strm', icon: 'monitor', menuIcon: 'folder' },
      { title: '115 转存', description: '磁力下载与 115 分享链接接收目录。', to: '/115/cloud-download', icon: 'download', menuIcon: 'download' },
      { title: '下载整理', description: '查看详情页发起的下载整理任务，检查进度并继续未完成的文件。', to: '/115/download-organize', icon: 'download', menuIcon: 'organize' },
      { title: '115 监控分享', description: '监控指定网盘目录，自动创建分享并生成本地 STRM。', to: '/115/folder-monitor', icon: 'monitor', menuIcon: 'folder' },
    ],
  },
  telegram: {
    title: 'Telegram 配置', description: '管理 Telegram 用户账号、机器人与频道转发。',
    features: [
      { title: '用户账号', description: '配置 Telegram API，登录共用的用户账号。', to: '/telegram/user', icon: 'telegram', menuIcon: 'telegram' },
      { title: 'Bot 配置', description: '配置机器人 Token、允许的聊天与消息接收。', to: '/telegram/bot', icon: 'telegram', menuIcon: 'telegram' },
      { title: '频道转发', description: '选择来源频道与目标，将资源链接自动投递到机器人、频道或群组。', to: '/telegram/relay', icon: 'telegram', menuIcon: 'telegram' },
    ],
  },
  emby: {
    title: 'Emby', description: '连接媒体服务器，管理媒体库与入库检测。',
    features: [
      { title: '媒体库同步', description: '配置 Emby 连接与同步范围，通过 Webhook 更新影视入库状态。', to: '/emby/library', icon: 'emby', menuIcon: 'library' },
      { title: 'Emby 刷新', description: '配置整理后的自动刷新通知与路径替换。', to: '/emby/refresh', icon: 'emby', menuIcon: 'library' },
      { title: '删除联动', description: '接收神医助手的删除通知，将对应主网盘视频移入回收站。', to: '/emby/deep-delete', icon: 'emby', menuIcon: 'library' },
      { title: '缺集检测', description: '按季检查第 1 集至已入库最后一集，查看缺失集号。', to: '/emby/missing', icon: 'emby', menuIcon: 'missing' },
      { title: 'Emby 302 反代', description: '设置独立播放端口，将分享 STRM 直放跳转到 115。', to: '/emby/proxy', icon: 'emby', menuIcon: 'proxy' },
      { title: '缓存管理', description: '查看分享直取与秒传播放直链缓存，搜索、移除或清空临时地址。', to: '/emby/cache', icon: 'emby', menuIcon: 'cache' },
    ],
  },
} as const

export const resourceSources = [
  { id: 'gying', title: '观影配置', description: '管理观影站地址、账号登录与资源优先级。', settings: '/gying', menuIcon: 'film' },
  { id: 'woniu', title: '蜗牛配置', description: '登录蜗牛 I 4K，配置详情页网盘搜索。', settings: '/woniu', menuIcon: 'search' },
  { id: 're0', title: 'RE0 配置', description: '管理 NanShare 连接，检查 RE0 搜索与分享 STRM 配置。', settings: '/nanshare', menuIcon: 'link' },
  { id: 'telegram', title: 'Telegram 搜索', description: '配置资源 Bot 与频道，管理详情页的聚合搜索来源。', settings: '/resources/telegram', menuIcon: 'telegram' },
  { id: 'subtitles', title: '字幕', description: '管理字幕库、射手网及在线字幕 API 配置。', settings: '/resources/subtitles', menuIcon: 'subtitles' },
  { id: 'search', title: '搜索设置', description: '拖动调整详情页搜索按钮顺序，预览不同影片的展示效果。', settings: '/resources/search', menuIcon: 'search' },
  { id: 'network', title: '网络代理', description: '配置 NAS 访问外部资源服务的代理与服务范围。', settings: '/resources/network', menuIcon: 'proxy' },
  { id: 'cd2', title: 'CloudDrive2', description: '配置共用的 CD2 连接，浏览网盘目录并用于自动整理。', settings: '/resources/clouddrive2', menuIcon: 'cloud' },
] as const
