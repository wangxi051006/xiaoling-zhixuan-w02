// npm run build -- --manifest; then split an immutable build by manifest closure.
import { readFileSync, writeFileSync, mkdirSync, copyFileSync, existsSync, realpathSync, lstatSync } from 'node:fs';
import { resolve, relative, dirname, sep, isAbsolute } from 'node:path';
import { createHash } from 'node:crypto';
import assert from 'node:assert/strict';
const args = process.argv.slice(2);
assert.equal(args.length, 4, 'usage: split-static.mjs --dist <vite-dist> --out <new-static-directory>');
assert.equal(args[0], '--dist'); assert.equal(args[2], '--out');
const dist = realpathSync(resolve(args[1]));
const output = resolve(args[3]);
assert.ok(!existsSync(output), 'output must not exist; never overwrite an installed release');
const relOutput = relative(dist, output);
assert.ok(relOutput.startsWith('..' + sep) || isAbsolute(relOutput), 'output must be outside dist');
const manifest = JSON.parse(readFileSync(resolve(dist, '.vite/manifest.json'), 'utf8'));
const digest = path => createHash('sha256').update(readFileSync(path)).digest('hex');
function source(path) {
  assert.ok(!isAbsolute(path) && !path.includes('\\') && !path.split('/').includes('..'), `unsafe artifact path: ${path}`);
  const absolute = resolve(dist, path);
  const actual = realpathSync(absolute);
  assert.ok(actual.startsWith(dist + sep) && !lstatSync(absolute).isSymbolicLink(), `artifact escapes dist: ${path}`);
  return absolute;
}
// Check everything before creating the new output directory.
const result = { schema: 1, source: 'Vite manifest dependency closure', entries: {} };
for (const entry of ['user', 'rider', 'admin']) {
  const html = `${entry}.html`;
  assert.ok(manifest[html]?.isEntry, `missing manifest entry: ${html}`);
  const files = new Set([html]);
  const visited = new Set();
  const visit = key => {
    if (visited.has(key)) return;
    visited.add(key);
    const item = manifest[key];
    assert.ok(item, `missing manifest import: ${key}`);
    for (const file of [item.file, ...(item.css ?? []), ...(item.assets ?? [])]) {
      assert.ok(file?.startsWith('assets/'), `only hashed assets may be published: ${file}`);
      files.add(file);
    }
    for (const dep of [...(item.imports ?? []), ...(item.dynamicImports ?? [])]) visit(dep);
  };
  visit(html);
  const text = readFileSync(source(html), 'utf8');
  assert.ok(!/<script\b(?![^>]*\bsrc=)[^>]*>\s*[^<\s]/i.test(text), 'inline executable script violates edge CSP');
  assert.ok(!/\bon[a-z]+\s*=|javascript\s*:/i.test(text), 'inline event handler violates edge CSP');
  for (const match of text.matchAll(/<(?:script|link|img|source)\b[^>]*\b(?:src|href)=["']([^"']+)["']/gi)) {
    const path = match[1].split(/[?#]/)[0];
    if (path.startsWith('data:')) continue;
    assert.ok(path.startsWith('/assets/') || path.startsWith('assets/'), `unpackaged/external resource: ${path}`);
    assert.ok(files.has(path.replace(/^\//, '')), `HTML resource absent from manifest closure: ${path}`);
  }
  result.entries[entry] = [...files].sort().map(path => ({ path, sha256: digest(source(path)) }));
}
mkdirSync(output, { recursive: true });
for (const [entry, files] of Object.entries(result.entries)) for (const file of files) {
  const target = resolve(output, entry, file.path);
  mkdirSync(dirname(target), { recursive: true });
  copyFileSync(source(file.path), target);
}
writeFileSync(resolve(output, 'artifact-layout.json'), JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify({ status: 'PASS', entries: Object.fromEntries(Object.entries(result.entries).map(([key, value]) => [key, value.length])) }));
