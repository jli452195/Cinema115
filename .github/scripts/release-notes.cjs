const { execFileSync } = require('node:child_process');

function version(tag) {
  const number = '(0|[1-9][0-9]*)';
  const match = new RegExp(`^v${number}\\.${number}\\.${number}(?:-(alpha|beta|rc)\\.${number})?$`).exec(tag);
  if (!match) return null;
  return [
    ...match.slice(1, 4).map(BigInt),
    BigInt({ alpha: 0, beta: 1, rc: 2 }[match[4]] ?? 3),
    BigInt(match[5] ?? 0),
  ];
}

function compare(left, right) {
  for (let i = 0; i < left.length; i++) {
    if (left[i] !== right[i]) return left[i] < right[i] ? -1 : 1;
  }
  return 0;
}

function escapeMarkdown(value) {
  return value.replace(/&/g, '&amp;').replace(/[\\`*_[\]<>]/g, '\\$&');
}

function generateReleaseNotes({ tag, revision, releases, repoUrl, image, cwd = process.cwd() }) {
  const current = version(tag);
  if (!current) throw new Error(`Invalid release tag: ${tag}`);
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
  const reachable = new Set(git('tag', '--merged', revision).trim().split(/\r?\n/));
  let candidates = releases
    .filter(release => !release.draft && reachable.has(release.tag_name))
    .map(release => ({ tag: release.tag_name, version: version(release.tag_name) }))
    .filter(release => release.version && compare(release.version, current) < 0);
  // Stable releases include changes since the previous stable, even after RCs.
  if (current[3] === 3n && candidates.some(release => release.version[3] === 3n)) {
    candidates = candidates.filter(release => release.version[3] === 3n);
  }
  candidates.sort((left, right) => compare(right.version, left.version));
  const previousTag = candidates[0]?.tag ?? null;
  const range = previousTag ? `${previousTag}..${revision}` : revision;
  const entries = git('log', '--no-merges', '--format=%H%x00%s', '-z', range, '--').split('\0');
  const sections = new Map([
    ['不兼容变更', []], ['新增功能', []], ['问题修复', []], ['性能优化', []], ['其他更新', []],
  ]);
  const headings = new Map([['feat', '新增功能'], ['fix', '问题修复'], ['perf', '性能优化']]);
  const hidden = new Set(['chore', 'ci', 'test', 'build']);
  for (let i = 0; i + 1 < entries.length; i += 2) {
    const [sha, subject] = entries.slice(i, i + 2);
    const match = /^([a-z][a-z0-9-]*)(?:\([^)]+\))?(!)?:\s*(.+)$/i.exec(subject);
    const type = match?.[1].toLowerCase();
    if (hidden.has(type) && !match[2]) continue;
    const heading = match?.[2] ? '不兼容变更' : (headings.get(type) ?? '其他更新');
    const title = escapeMarkdown(match ? match[3] : subject);
    sections.get(heading).push(`- ${title} ([${sha.slice(0, 7)}](${repoUrl}/commit/${sha}))`);
  }
  const changes = [...sections]
    .filter(([, items]) => items.length)
    .map(([heading, items]) => `### ${heading}\n\n${items.join('\n')}`)
    .join('\n\n');
  const changelog = previousTag
    ? `${repoUrl}/compare/${previousTag}...${tag}`
    : `${repoUrl}/commits/${tag}`;
  const body = [
    `Docker: \`${image}:${tag.slice(1)}\``,
    '下载附件 compose.yaml，填写凭据和挂载目录后部署。',
    '## 更新内容',
    changes || (entries.length > 1 ? '本版本仅包含维护更新。' : '本版本没有新增提交。'),
    `[完整变更](${changelog})`,
    `Commit: ${revision}`,
  ].join('\n\n');
  return { previousTag, body };
}

module.exports = { generateReleaseNotes };
