// Fixed server responses for interaction tests. The real template renderer is
// exercised in Go; this fixture only provides predictable names for editor rows.
exports.mediaNamingFixture = (title, year) => {
  let current
  return {
    preview(input, revision = 3) {
      const items = input.files.map(file => {
        if (input.kind === 'tv' && (file.episode < 1 || file.episode > 10000 || file.season < 0)) return { id: file.id, error: '请填写有效目标季集' }
        const values = file.namingValues || {}
        const numbered = input.kind === 'tv' ? `.S${String(file.season).padStart(2, '0')}E${String(file.episode).padStart(2, '0')}` : ` (${year})`
        const stem = `${title}${numbered}${values.resolution ? `.${values.resolution}` : ''}${values.languages ? `.[${values.languages}]` : ''}${values.releaseGroup ? `-${values.releaseGroup}` : ''}`
        const extension = file.titleOnly ? '' : require('node:path').extname(file.name).toLowerCase()
        return { id: file.id, result: { directory: '', stem, extension, name: stem + extension, relativePath: stem + extension } }
      })
      for (const sub of input.subtitles) {
        const owner = items.find(item => item.id === sub.videoId)?.result
        if (!owner) continue
        const stem = owner.stem + (sub.tail || ''), extension = require('node:path').extname(sub.file.name).toLowerCase()
        items.push({ id: sub.file.id, result: { directory: '', stem, extension, name: stem + extension, relativePath: stem + extension } })
      }
      current = { naming: { revision, template: { kind: input.kind, directories: [], filename: [] } }, items }
      return current
    },
    plan(files, prefix = '') {
      return { naming: current.naming, items: files.map(file => ({ file, ...current.items.find(item => item.id === prefix + file.id)?.result })) }
    },
  }
}
