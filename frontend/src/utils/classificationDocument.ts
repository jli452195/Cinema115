import { isMap, isScalar, isSeq, parseDocument, type Document, type YAMLMap } from 'yaml'
import type { ClassificationCondition, ClassificationConfig, ClassificationKind, ClassificationRule } from '../types/classification'

export const classificationConditions: { key: ClassificationCondition; label: string }[] = [
  { key: 'genres', label: '类型' }, { key: 'origin_countries', label: '原产地' },
  { key: 'production_countries', label: '制作国家' }, { key: 'languages', label: '原始语言' },
]
export interface ClassificationEditorDocument {
  document: Document
  config: ClassificationConfig
  nodes: Map<string, YAMLMap>
}

// Edit YAML nodes rather than stringify a JSON projection, preserving comments
// on documents, sections, rules, fields and values when reordering or editing.
export function parseClassification(source: string): ClassificationEditorDocument {
  if (new TextEncoder().encode(source).length > 256 * 1024) throw new Error('分类 YAML 不能超过 256 KiB')
  const document = parseDocument(source, { uniqueKeys: true, strict: true })
  if (document.errors.length || document.warnings.length) throw new Error(document.errors[0]?.message || document.warnings[0]!.message)
  const mapping = (node: unknown, keys: string[], required: string[]) => {
    if (!isMap(node) || node.anchor || node.tag && node.tag !== 'tag:yaml.org,2002:map') throw new Error('分类配置须使用普通 YAML 映射，不支持锚点或标签')
    for (const pair of node.items) if (!isScalar(pair.key) || pair.key.anchor || typeof pair.key.value !== 'string' || !keys.includes(pair.key.value)) throw new Error('分类配置包含不支持的字段')
    for (const key of required) if (!node.has(key)) throw new Error(`分类配置缺少 ${key}`)
    return node
  }
  const text = (value: unknown): string => {
    if (!isScalar(value) || value.anchor || value.tag && value.tag !== 'tag:yaml.org,2002:str' || typeof value.value !== 'string') throw new Error('分类名称与目录须为普通文本')
    return value.value
  }
  const root = mapping(document.contents, ['version', 'movie', 'tv'], ['version', 'movie', 'tv'])
  const version = root.get('version', true)
  if (!isScalar(version) || version.anchor || version.tag && version.tag !== 'tag:yaml.org,2002:int' || version.value !== 1) throw new Error('仅支持分类配置版本 1')
  const config = {} as ClassificationConfig, nodes = new Map<string, YAMLMap>()
  for (const kind of ['movie', 'tv'] as const) {
    const section = mapping(root.get(kind, true), ['fallback', 'rules'], ['fallback', 'rules'])
    const sequence = section.get('rules', true)
    if (!isSeq(sequence) || sequence.anchor || sequence.tag && sequence.tag !== 'tag:yaml.org,2002:seq') throw new Error('分类规则须为普通列表')
    config[kind] = { fallback: text(section.get('fallback', true)), rules: sequence.items.map((item, index) => {
      const rule = mapping(item, ['name', 'path', 'match'], ['name', 'path', 'match'])
      const match = mapping(rule.get('match', true), classificationConditions.map(item => item.key), [])
      const parsed: ClassificationRule = { key: `${kind}-${index}`, name: text(rule.get('name', true)), path: text(rule.get('path', true)), match: {} }
      for (const { key } of classificationConditions) {
        if (!match.has(key)) continue
        const values = match.get(key, true)
        if (!isSeq(values) || values.anchor || values.tag && values.tag !== 'tag:yaml.org,2002:seq' || !values.items.length) throw new Error('条件须为非空普通列表')
        parsed.match[key] = values.items.map(value => {
          if (!isScalar(value) || value.anchor || value.tag && value.tag !== `tag:yaml.org,2002:${key === 'genres' ? 'int' : 'str'}`) throw new Error('条件不支持别名、锚点或自定义标签')
          if (key === 'genres') {
            if (!Number.isSafeInteger(value.value) || Number(value.value) <= 0) throw new Error('类型 ID 须为正整数')
            return value.value as number
          }
          if (typeof value.value !== 'string' || !/^[a-z]{2}$/i.test(value.value.trim())) throw new Error('国家和语言须为两位字母代码')
          return key === 'languages' ? value.value.trim().toLowerCase() : value.value.trim().toUpperCase()
        })
        if (new Set(parsed.match[key]).size !== parsed.match[key]!.length) throw new Error('同一条件不能包含重复值')
      }
      nodes.set(parsed.key, rule)
      return parsed
    }) }
  }
  return { document, config, nodes }
}

export function classificationSignature(config: ClassificationConfig) {
  return JSON.stringify(['movie', 'tv'].map(kind => {
    const section = config[kind as ClassificationKind]
    return [section.fallback, section.rules.map(rule => [rule.name, rule.path, classificationConditions.map(({ key }) => rule.match[key])])]
  }))
}

export function classificationIssue(config: ClassificationConfig): { kind: ClassificationKind; key?: string; message: string } | undefined {
  const directoryError = (value: string) => !value || value.split('/').some(part => !part || part === '.' || part === '..' || /[\\:*?"<>|\u0000-\u001f\u007f]/.test(part) || part !== part.trim() || /\.$/.test(part))
  for (const kind of ['movie', 'tv'] as const) {
    if (directoryError(config[kind].fallback)) return { kind, message: `请填写有效的兜底相对目录，例如 ${kind === 'movie' ? '电影' : '剧集'}/其他` }
    const names = new Set<string>()
    for (const rule of config[kind].rules) {
      const name = rule.name.trim()
      const message = !name ? '请填写规则名称' : names.has(name) ? '当前类型存在同名规则' : directoryError(rule.path) ? '请填写有效的相对目录，不能包含绝对路径或上级目录' : !Object.keys(rule.match).length ? '至少选择一项匹配条件' : Object.values(rule.match).some(values => !values?.length) ? '每项条件至少选择一个匹配值，或移除该条件' : ''
      if (message) return { kind, key: rule.key, message }
      names.add(name)
    }
  }
}

export function writeClassification(editor: ClassificationEditorDocument, config: ClassificationConfig): string {
  const document = editor.document.clone()
  const setText = (node: YAMLMap, key: string, value: string) => {
    const scalar = node.get(key, true)
    if (isScalar(scalar)) scalar.value = value
    else node.set(key, value)
  }
  for (const kind of ['movie', 'tv'] as const) {
    const section = document.get(kind, true) as YAMLMap
    setText(section, 'fallback', config[kind].fallback)
    const sequence = section.get('rules', true)
    if (!isSeq(sequence)) throw new Error('分类规则列表无效')
    sequence.items = config[kind].rules.map(rule => {
      const node = (editor.nodes.get(rule.key)?.clone() || document.createNode({ name: '', path: '', match: {} })) as YAMLMap
      setText(node, 'name', rule.name); setText(node, 'path', rule.path)
      const match = node.get('match', true)
      if (!isMap(match)) throw new Error('分类匹配条件无效')
      for (const { key } of classificationConditions) {
        const values = rule.match[key]
        if (!values?.length) { match.delete(key); continue }
        const previous = match.get(key, true)
        if (isSeq(previous)) previous.items = values.map(value => previous.items.find(item => isScalar(item) && String(item.value).trim().toLowerCase() === String(value).toLowerCase()) || document.createNode(value))
        else match.set(key, document.createNode(values))
      }
      return node
    })
  }
  return document.toString()
}
