import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { schemas } from './schemas.mjs';

const from = path => new URL(path, import.meta.url);
const read = async path => JSON.parse(await readFile(from(path), 'utf8'));
const enums = await read('enums.json');
const operations = await read('operations.json');
const schemasByName = schemas();
for (const [name, values] of Object.entries(enums)) schemasByName[name] = { type: 'string', enum: values };
const schema = name => {
  assert(Object.hasOwn(schemasByName, name), `undefined schema: ${name}`);
  return { $ref: `#/components/schemas/${name}` };
};
const json = name => ({ 'application/json': { schema: schema(name) } });
const param = name => ({ $ref: `#/components/parameters/${name}` });
const text = (maxLength = 128) => ({ type: 'string', minLength: 1, maxLength });
const parameter = (name, where, type, required = false) => ({ name, in: where, required, schema: type });
const settings = {
  openapi: '3.1.0',
  info: { title: 'Xiaoling Campus Ordering', version: '1.0.0', description: 'Contract baseline, not a list of deployed operations.' },
  servers: [{ url: '/api/v1' }], paths: {},
  components: {
    securitySchemes: { session: { type: 'apiKey', in: 'cookie', name: '__Host-xiaoling-session' } },
    parameters: {
      IdempotencyKey: parameter('Idempotency-Key','header',text(),true),
      CSRF: parameter('X-CSRF-Token','header',text(),true),
      ID: parameter('id','path',text(),true),
      Cursor: parameter('cursor','query',text()),
      Limit: parameter('limit','query',{ type:'integer', minimum:1, maximum:100, default:20 }),
      Date: parameter('date','query',{ type:'string', format:'date' }),
      Version: parameter('If-Match','header',{ type:'string', pattern:'^"[1-9][0-9]*"$' },true),
    },
    schemas: schemasByName,
  },
};
const ids = new Set();
for (const [owner, method, path, operationId, role, request, response, status = 200, flags = ''] of operations) {
  assert(!ids.has(operationId), `duplicate operation ID: ${operationId}`);
  ids.add(operationId);
  assert(!settings.paths[path]?.[method], `duplicate operation: ${method} ${path}`);
  const modes = new Set(flags.split(' '));
  const roles = role === 'PUBLIC' ? [] : role === 'ALL' ? enums.Role : role.split(' ');
  assert(roles.every(r => enums.Role.includes(r)), `unknown role: ${role}`);
  const parameters = [];
  if (path.includes('{id}')) parameters.push(param('ID'));
  if (modes.has('key')) parameters.push(param('IdempotencyKey'));
  if (modes.has('version')) parameters.push(param('Version'));
  if (modes.has('date')) parameters.push(param('Date'));
  if (modes.has('list')) parameters.push(param('Limit'), param('Cursor'));
  if (operationId === 'listCatalogProducts') parameters.push(parameter('q','query',text(80)), parameter('category','query',text(80)));
  if (roles.length && method !== 'get') parameters.push(param('CSRF'));
  if (modes.has('callback')) {
    parameters.push(parameter('state','query',text(512),true), parameter('code','query',text(2048)), parameter('error','query',text(128)));
  }
  const success = { description: status === 302 ? 'Trusted redirect' : status === 204 ? 'No content' : 'Success' };
  if (response) success.content = json(response);
  if (status === 302) success.headers = { Location: { required:true, schema: { type:'string', format:'uri-reference' } } };
  if (operationId === 'verifyOTP' || operationId === 'verifyEmailCode' || operationId === 'completeStaffLogin' || operationId === 'logout') success.headers = { ...success.headers, 'Set-Cookie': { schema: { type:'string' } } };
  const responses = { [status]: success };
  const errors = { 400:'Invalid request', 401:'Authentication required', 403:'Forbidden', 404:'Not found', 409:'State or version conflict', 413:'Request too large', 429:'Rate limited', 500:'Internal error', 503:'Service unavailable' };
  for (const [code, description] of Object.entries(errors)) responses[code] = { description, content: json('ApiError') };
  (settings.paths[path] ??= {})[method] = {
    operationId, summary: operationId.replace(/([a-z])([A-Z])/g, '$1 $2'), tags: [owner],
    'x-roles': roles, 'x-idempotency-required': modes.has('key'), 'x-recent-mfa': modes.has('mfa'),
    security: roles.length ? [{ session: [] }] : [], parameters,
    ...(request ? { requestBody: { required: true, content: json(request) } } : {}), responses,
  };
}

function type(s) {
  if (s.$ref) return s.$ref.split('/').at(-1);
  if (s.const !== undefined) return JSON.stringify(s.const);
  if (s.enum) return s.enum.map(v => JSON.stringify(v)).join(' | ');
  if (s.oneOf) return s.oneOf.map(type).join(' | ');
  if (s.type === 'array') return `Array<${type(s.items)}>`;
  if (s.type === 'object') return `{ ${Object.entries(s.properties).map(([n,v]) => `${n}${s.required.includes(n) ? '' : '?'}: ${type(v)}`).join('; ')} }`;
  if (s.type === 'integer') return 'number';
  assert(['string','boolean','null'].includes(s.type), `unsupported schema type: ${s.type}`);
  return s.type;
}
const title = name => name.split('_').map(piece => piece[0] + piece.slice(1).toLowerCase()).join('');
const ts = ['// Generated by contracts/generate.mjs. Do not edit.'];
const go = ['// Code generated by contracts/generate.mjs. DO NOT EDIT.','', 'package contract',''];
for (const [name, values] of Object.entries(enums)) {
  ts.push(`export const ${name}Values = ${JSON.stringify(values)} as const;`, `export type ${name} = typeof ${name}Values[number];`);
  go.push(`type ${name} string`, '', 'const (');
  for (const value of values) go.push(`\t${name}${title(value)} ${name} = "${value}"`);
  go.push(')', '');
}
go.push('func ContractEnums() map[string][]string {', '\treturn map[string][]string{');
for (const [name, values] of Object.entries(enums)) go.push(`\t\t${JSON.stringify(name)}: {${values.map(v => `string(${name}${title(v)})`).join(', ')}},`);
go.push('\t}', '}');
ts.push('');
for (const [name, s] of Object.entries(schemasByName)) if (!Object.hasOwn(enums, name)) ts.push(`export type ${name} = ${type(s)};`);
ts.push('', 'export interface Operations {');
for (const [, method, path, id, , request, response, status = 200] of operations) ts.push(`  ${id}: { method: '${method.toUpperCase()}'; path: '${path}'; body: ${request ?? 'undefined'}; result: ${response ?? 'undefined'}; status: ${status} };`);
ts.push('}');
const outputs = {
  'openapi.yaml': JSON.stringify(settings, null, 2),
  '../web/src/shared/generated.ts': ts.join('\n'),
  '../backend/internal/contract/enums_generated.go': go.join('\n'),
};
for (const [path, contents] of Object.entries(outputs)) {
  const bytes = `${contents}\n`;
  if (process.argv.includes('--check')) {
    assert.equal(await readFile(from(path),'utf8'), bytes, `${path} is stale; run node contracts/generate.mjs`);
  } else {
    await mkdir(new URL('.',from(path)),{ recursive:true });
    await writeFile(from(path), bytes);
  }
}
console.log(`${operations.length} operations and ${Object.keys(enums).length} enums ${process.argv.includes('--check') ? 'verified' : 'generated'}`);
