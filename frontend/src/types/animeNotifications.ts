export interface AnimeNotificationSettings {
  revision: number
  botId: string
  downloadComplete: boolean
  organized: boolean
  failed: boolean
  template: string
  imageMode: 'none' | 'cover' | 'custom'
  imageUrl: string
}

export type AnimeNotificationKind = 'downloaded' | 'organized' | 'failed'

export interface AnimeNotificationConfig {
  settings: AnimeNotificationSettings
  defaultTemplate: string
  variables: { name: string; label: string }[]
  preview: { html: string; photoUrl: string }
  maxTemplateLength: number
}
