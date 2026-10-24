import type { DownloadFileSpec } from '../types/manualOrganize'

export interface DownloadNameFields { fansub: boolean; language: boolean; resolution: boolean }
export function downloadSuffix(spec: DownloadFileSpec, fansub: string, fields: DownloadNameFields) {
  const languages = [['zh-Hans', '简体'], ['zh-Hant', '繁体'], ['ja', '日语'], ['en', '英语']]
    .filter(([key]) => spec.languages?.includes(key!)).map(([, label]) => label).join('+')
  return [fields.fansub ? spec.fansub || fansub : '', fields.language ? languages : '', fields.resolution ? spec.resolution : ''].filter(Boolean).join(' - ')
}
