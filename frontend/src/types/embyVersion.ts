export interface EmbyStream {
  Type: string; Codec: string; Width?: number; Height?: number; VideoRange?: string; DisplayTitle?: string
  Title?: string; Language?: string; DisplayLanguage?: string; Profile?: string; Level?: number | null
  AspectRatio?: string; IsInterlaced?: boolean | null; AverageFrameRate?: number | null; RealFrameRate?: number | null
  BitRate?: number | null; BitDepth?: number | null; RefFrames?: number | null; PixelFormat?: string
  ColorPrimaries?: string; ColorSpace?: string; ColorTransfer?: string; ExtendedVideoType?: string
  ExtendedVideoSubTypeDescription?: string; ChannelLayout?: string; Channels?: number | null
  SampleRate?: number | null; IsDefault?: boolean | null; Index?: number
}
export interface EmbySource { Id?: string; Name?: string; Container?: string; Size?: number; MediaStreams?: EmbyStream[] | null }
export interface EmbyVersion { itemId: string; name: string; url: string; sources: EmbySource[] | null }
export interface EmbyVersionPage { versions: EmbyVersion[]; total: number; hasMore: boolean; nextOffset: number }

export function mediaSize(value?: number) {
  if (!value || value < 0) return '大小未知'
  return value >= 1024 ** 3 ? `${(value / 1024 ** 3).toFixed(2)} GB` : `${(value / 1024 ** 2).toFixed(1)} MB`
}
export function videoRange(stream?: EmbyStream) {
  return stream?.ExtendedVideoType && stream.ExtendedVideoType !== 'None' ? stream.ExtendedVideoType : stream?.VideoRange || ''
}
export function resolution(stream?: EmbyStream) {
  const width = stream?.Width || 0, height = stream?.Height || 0
  if (width >= 7600 || height >= 4200) return '4320p'
  if (width >= 3800 || height >= 2100) return '2160p'
  if (width >= 1900 || height >= 1000) return '1080p'
  if (width >= 1200 || height >= 700) return '720p'
  return height ? `${height}p` : ''
}
