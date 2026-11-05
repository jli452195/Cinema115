export function requestErrorMessage(value: unknown, action: string) {
  if (value instanceof Error && value.name === 'TimeoutError') return `${action}超时，请稍后重新尝试。`
  if (value instanceof Error && value.name === 'AbortError') return `${action}已取消，请重新尝试。`
  return value instanceof Error ? value.message : `${action}失败`
}
