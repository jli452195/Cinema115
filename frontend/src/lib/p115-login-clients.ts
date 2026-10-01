export const p115LoginClients = [
  { value: 'alipay-miniapp', label: '支付宝小程序' },
  { value: 'wechat-miniapp', label: '微信小程序' },
  { value: '115-life-android', label: '115生活 Android' },
  { value: '115-android', label: '115 Android' },
  { value: '115-life-ios', label: '115生活 iOS' },
  { value: '115-ios', label: '115 iOS' },
  { value: '115-ipad', label: '115 iPad' },
  { value: '115-tv', label: 'TV' },
]

const loginApps: Record<string, string> = {
  web: '115 网页端', alipaymini: '支付宝小程序', wechatmini: '微信小程序',
  android: '115生活 Android', ios: '115生活 iOS', '115android': '115 Android',
  '115ios': '115 iOS', '115ipad': '115 iPad', tv: 'TV',
}
export function loginClientLabel(app: string) { return loginApps[app] || '' }
