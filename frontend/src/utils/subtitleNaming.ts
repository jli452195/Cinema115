import rules from './subtitleNamingRules.json'

function aliasEntries(groups: Record<string, string[]>) {
  return Object.entries(groups).flatMap(([value, aliases]) => aliases.map(alias => [alias, value] as const))
}

const languageAliases = new Map(aliasEntries(rules.languages))
const bilingualAliases = new Map(Object.entries(rules.bilingual).flatMap(([tag, languages]) =>
  aliasEntries(languages).map(([alias, language]) => [alias, { language, tag }] as const),
))
export const isSubtitleBilingualAlias = (tag: string) => bilingualAliases.has(tag.toLowerCase())
const tagAliases = new Map([...aliasEntries(rules.variants), ...aliasEntries(rules.flags)])
const conditionalFlags = new Set(rules.flagsRequiringLanguage)
const variants = Object.keys(rules.variants)
const flags = Object.keys(rules.flags)
const audioBlock = /(?:^|[^a-z0-9])(?:AAC|FLAC|ALAC|AC-?3|E-?AC-?3|DDP?|DTS(?:-HD)?|TRUEHD|OPUS|AUDIO)[ ._-]*\[[^\[\]]*\]/gi
const subtitleBlock = /(^|[^a-z0-9])SUB(?:TITLE)?S?[ ._:-]*\[([^\[\]]*)\]/gi
const bracketBlock = /[\[【(（]([^\[\]【】()（）]+)[\]】)）]/g
const extension = /\.[^./\\]+$/

function normalizeTag(tag: string) {
  return tag.toLowerCase().replace(/\bzh[._](cn|tw|hk|sg|hans|hant|en)\b/g, 'zh-$1')
}

function metadataTokens(group: string): string[] {
  const tokens = normalizeTag(group.trim()).split(/[\s._+&/]+/).filter(Boolean)
  return tokens.every(token => languageAliases.has(token) || bilingualAliases.has(token) || tagAliases.has(token)) ? tokens : []
}

function inspectTokens(tokens: readonly string[]) {
  const languages = new Set<string>()
  const tags = new Set<string>()
  for (const token of tokens) {
    const bilingual = bilingualAliases.get(token)
    const language = languageAliases.get(token) || bilingual?.language
    const tag = tagAliases.get(token)
    if (language) languages.add(language)
    if (tag && !conditionalFlags.has(token)) tags.add(tag)
    if (bilingual) tags.add(bilingual.tag)
  }
  const chinese = [...languages].filter(language => language.startsWith('zh-'))
  let language = ''
  if (chinese.length === 1 || chinese.length === 0 && languages.has('zh')) {
    language = chinese[0] || 'zh'
    if (languages.has('en')) tags.add('bilingual')
    if (languages.has('ja')) tags.add('bilingual-ja')
  } else if (languages.size === 1) {
    language = [...languages][0]!
  }
  // 「简日双语」只需一个标记。明确写出的中英或英文信息继续保留。
  if (tags.has('bilingual-ja') && !languages.has('en') && !tokens.some(token => token === 'bilingual' || bilingualAliases.get(token)?.tag === 'bilingual')) tags.delete('bilingual')
  return { language, tags }
}

export function subtitleMetadata(name: string, selectedLanguage?: string) {
  const original = (name.split(/[\\/]/).pop() || '').replace(extension, '').replace(audioBlock, ' ')
  const explicit = [...original.matchAll(subtitleBlock)].map(match => metadataTokens(match[2]))
  const withoutSubtitleBlocks = original.replace(subtitleBlock, '$1 ')
  const bracketTokens = [...withoutSubtitleBlocks.matchAll(bracketBlock)].flatMap(match => metadataTokens(match[1]))
  const unbracketed = normalizeTag(withoutSubtitleBlocks.replace(bracketBlock, ' ')).trim()
  const dotParts = unbracketed.split('.')
  const trailingTokens: string[] = []
  const lastPart = dotParts[dotParts.length - 1]?.split(/[\s_]+/) || []
  for (let i = lastPart.length - 1; i >= 0; i--) {
    const tokens = metadataTokens(lastPart[i]!)
    if (!tokens.length) break
    trailingTokens.push(...tokens)
  }
  const suffixParts = dotParts.length === 1 ? dotParts : dotParts.slice(1)
  const ambient = [...suffixParts.flatMap(metadataTokens), ...trailingTokens, ...bracketTokens]
  const explicitFacts = explicit.map(inspectTokens)
  const source = inspectTokens(explicit.length ? ambient.filter(token => !languageAliases.has(token) && !bilingualAliases.has(token)) : ambient)
  const explicitLanguage = explicitFacts[0]?.language || ''
  const inferred = explicit.length ? (explicitFacts.every(facts => facts.language === explicitLanguage) ? explicitLanguage : '') : source.language
  const selected = selectedLanguage === undefined ? undefined : normalizeTag(selectedLanguage)
  const language = selected === undefined ? inferred : selected === 'zh-en' ? (inferred.startsWith('zh-') ? inferred : 'zh') : languageAliases.get(selected) || ''
  const tags = new Set([...source.tags, ...explicitFacts.flatMap(facts => [...facts.tags])])
  if (selected === 'zh-en') tags.add('bilingual')
  if (inferred || language) {
    for (const token of [...ambient, ...explicit.flat()]) {
      const tag = tagAliases.get(token)
      if (tag && conditionalFlags.has(token)) tags.add(tag)
    }
  }
  return { language, variants: variants.filter(tag => tags.has(tag)), flags: flags.filter(tag => tags.has(tag)) }
}

export function subtitleLanguage(name: string): string {
  return subtitleMetadata(name).language
}

export function subtitleTargetName(videoStem: string, file: string, language: string, version = '') {
  const metadata = subtitleMetadata(file, language)
  const explicitVersion = version.toLowerCase()
  const tags = [...metadata.variants, ...metadata.flags].filter(tag => tag !== explicitVersion)
  const tail = [metadata.language, version, ...tags].filter(Boolean).map(tag => `.${tag}`).join('')
  return videoStem + tail + (file.match(extension)?.[0].toLowerCase() || '')
}
