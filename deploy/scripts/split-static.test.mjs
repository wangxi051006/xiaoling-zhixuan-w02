import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, existsSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const script = fileURLToPath(new URL('./split-static.mjs', import.meta.url));
function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'p07-static-test-'));
  const dist = join(root, 'dist'); mkdirSync(join(dist, '.vite'), { recursive: true }); mkdirSync(join(dist, 'assets'));
  const manifest = { shared: { file: 'assets/shared.js' } };
  for (const entry of ['user', 'rider', 'admin']) {
    manifest[`${entry}.html`] = { isEntry: true, file: `assets/${entry}.js`, imports: ['shared'], css: [`assets/${entry}.css`] };
    writeFileSync(join(dist, `${entry}.html`), `<script type="module" src="/assets/${entry}.js"></script>`);
    writeFileSync(join(dist, 'assets', `${entry}.js`), 'import "./shared.js";');
    writeFileSync(join(dist, 'assets', `${entry}.css`), 'body{margin:0}');
  }
  writeFileSync(join(dist, 'assets/shared.js'), 'export const x=1;');
  const save = () => writeFileSync(join(dist, '.vite/manifest.json'), JSON.stringify(manifest)); save();
  const out = join(root, 'static');
  return { root, dist, out, manifest, save, run: () => spawnSync(process.execPath, [script, '--dist', dist, '--out', out], { encoding: 'utf8' }), close: () => rmSync(root, { recursive: true, force: true }) };
}
test('each entry receives only its dependency closure and a matching digest manifest', () => {
  const f = fixture(); try {
    const r = f.run(); assert.equal(r.status, 0, r.stderr);
    for (const entry of ['user', 'rider', 'admin']) {
      assert.ok(existsSync(join(f.out, entry, `${entry}.html`)));
      assert.ok(existsSync(join(f.out, entry, 'assets/shared.js')));
      for (const other of ['user', 'rider', 'admin'].filter(x => x !== entry)) {
        assert.ok(!existsSync(join(f.out, entry, `${other}.html`)));
        assert.ok(!existsSync(join(f.out, entry, `assets/${other}.js`)));
      }
    }
    const layout = JSON.parse(readFileSync(join(f.out, 'artifact-layout.json'), 'utf8'));
    assert.equal(layout.entries.user.length, 4); assert.match(layout.entries.user[0].sha256, /^[a-f0-9]{64}$/);
  } finally { f.close(); }
});
test('rejects an unlisted external executable before creating output', () => {
  const f = fixture(); try { writeFileSync(join(f.dist, 'user.html'), '<script src="https://external.invalid/x.js"></script>'); assert.notEqual(f.run().status, 0); assert.ok(!existsSync(f.out)); } finally { f.close(); }
});
test('rejects inline executable and never overwrites a release', () => {
  const f = fixture(); try { writeFileSync(join(f.dist, 'user.html'), '<script>alert(1)</script>'); assert.notEqual(f.run().status, 0); assert.ok(!existsSync(f.out)); mkdirSync(f.out); writeFileSync(join(f.out, 'keep'), 'existing'); assert.notEqual(f.run().status, 0); assert.equal(readFileSync(join(f.out, 'keep'), 'utf8'), 'existing'); } finally { f.close(); }
});
test('rejects traversal and missing closure references', () => {
  const f = fixture(); try { f.manifest.shared.file = '../outside.js'; f.save(); assert.notEqual(f.run().status, 0); assert.ok(!existsSync(f.out)); f.manifest.shared.file = 'assets/shared.js'; f.manifest.shared.imports = ['missing']; f.save(); assert.notEqual(f.run().status, 0); assert.ok(!existsSync(f.out)); } finally { f.close(); }
});
