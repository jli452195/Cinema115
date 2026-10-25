const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const { mkdtempSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { test } = require('node:test');
const { generateReleaseNotes } = require('./release-notes.cjs');

function repository(t) {
  const cwd = mkdtempSync(join(tmpdir(), 'cinema115-release-notes-'));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', '-c', 'tag.gpgsign=false', ...args], {
    cwd, encoding: 'utf8',
    env: { ...process.env, GIT_AUTHOR_NAME: 'Release test', GIT_AUTHOR_EMAIL: 'release@example.invalid', GIT_COMMITTER_NAME: 'Release test', GIT_COMMITTER_EMAIL: 'release@example.invalid' },
  }).trim();
  git('init', '--quiet', '--initial-branch=main');
  const commit = title => { git('commit', '--quiet', '--allow-empty', '-m', title); return git('rev-parse', 'HEAD'); };
  const releases = [];
  const release = (tag, draft = false) => {
    git('tag', '-a', tag, '-m', tag);
    releases.push({ tag_name: tag, draft });
  };
  const notes = tag => generateReleaseNotes({
    cwd, tag, revision: git('rev-parse', 'HEAD'), releases,
    repoUrl: 'https://github.com/example/cinema115', image: 'example/cinema115',
  });
  return { git, commit, release, notes };
}

test('stable notes cover the previous published stable tag and classify direct commits', t => {
  const { git, commit, release, notes } = repository(t);
  commit('旧功能，不应重复出现');
  release('v0.1.2');
  commit('feat(rss): 支持重新下载');
  release('v0.1.3-rc.1');
  commit('fix: 修复整理恢复');
  release('v0.1.3', true);
  commit('perf: 减少网盘请求');
  commit('未加前缀的中文更新');
  commit('refactor: 复用命名模块');
  commit('constructor: 其他类型更新');
  commit('fix(ui): 显示 [字幕组] <标签> & 名称');
  for (const type of ['chore', 'ci', 'test', 'build']) commit(`${type}: 内部维护`);
  commit('chore!: 删除旧配置格式');
  release('v9.0.0'); // A newer version must not become the baseline for a backport.
  git('checkout', '--quiet', '-b', 'unrelated', 'v0.1.2');
  commit('fix: 其他分支提交');
  release('v0.1.9');
  git('checkout', '--quiet', 'main');
  const { previousTag, body } = notes('v0.1.4');
  assert.equal(previousTag, 'v0.1.2');
  for (const heading of ['不兼容变更', '新增功能', '问题修复', '性能优化', '其他更新']) assert.ok(body.includes(`### ${heading}`));
  for (const title of ['支持重新下载', '修复整理恢复', '减少网盘请求', '未加前缀的中文更新', '复用命名模块', '其他类型更新', '删除旧配置格式']) assert.ok(body.includes(title));
  assert.ok(body.includes('显示 \\[字幕组\\] \\<标签\\> &amp; 名称'));
  assert.doesNotMatch(body, /内部维护|旧功能|其他分支提交/);
  assert.ok(body.includes('/compare/v0.1.2...v0.1.4'));
  assert.ok(body.includes('Docker: `example/cinema115:0.1.4`'));
});

test('prerelease ordering is numeric and same-commit tags remain valid baselines', t => {
  const { commit, release, notes } = repository(t);
  commit('初始化');
  release('v0.1.9');
  release('v0.1.10');
  release('v0.2.0-alpha.10');
  release('v0.2.0-beta.2');
  release('v0.2.0-beta.10');
  assert.equal(notes('v0.2.0-beta.11').previousTag, 'v0.2.0-beta.10');
  release('v0.2.0-rc.1');
  assert.equal(notes('v0.2.0-rc.2').previousTag, 'v0.2.0-rc.1');
  assert.equal(notes('v0.2.0').previousTag, 'v0.1.10');
  assert.ok(notes('v0.1.11').body.includes('本版本没有新增提交。'));
});

test('first release includes history; first stable falls back to its prerelease', t => {
  const { commit, release, notes } = repository(t);
  const sha = commit('feat: 首个功能');
  release('not-a-version');
  const first = notes('v0.1.0-beta.1');
  assert.equal(first.previousTag, null);
  assert.ok(first.body.includes('首个功能'));
  assert.ok(first.body.includes(`/commit/${sha}`));
  assert.ok(first.body.includes('/commits/v0.1.0-beta.1'));
  release('v0.1.0-beta.1');
  commit('ci: 维护工作流');
  const stable = notes('v0.1.0');
  assert.equal(stable.previousTag, 'v0.1.0-beta.1');
  assert.ok(stable.body.includes('本版本仅包含维护更新。'));
});
