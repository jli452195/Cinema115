export interface TelegramBotIdentity {
  id: number
  first_name: string
  username: string
  webhookConfigured: boolean
}

export interface TelegramBotConfig {
  id: string
  name: string
  configured: boolean
  enabled: boolean
  allowedChatIDs: string
  notificationChatID: string
  identity: TelegramBotIdentity
  revision: number
  running: boolean
  connected: boolean
  lastError: string
}
