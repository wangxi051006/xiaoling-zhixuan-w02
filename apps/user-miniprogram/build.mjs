import { cp, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const { WX_APP_ID: appid, USER_ORIGIN: origin } = process.env;
if (!/^wx[0-9a-f]{16}$/.test(appid ?? '')) {
  throw new Error('WX_APP_ID must be the AppID of the user Mini Program');
}

const storefront = new URL(origin ?? '');
if (storefront.protocol !== 'https:' || storefront.origin !== origin || storefront.port) {
  throw new Error('USER_ORIGIN must be a canonical HTTPS origin on the default port');
}

const dist = resolve(process.argv[2] ?? fileURLToPath(new URL('./dist/', import.meta.url)));
await mkdir(dist);
for (const name of ['app.json', 'app.js', 'pages']) {
  await cp(new URL(name, import.meta.url), resolve(dist, name), { recursive: true });
}
await writeFile(resolve(dist, 'storefront.js'), `module.exports = ${JSON.stringify({ storefrontUrl: storefront.href })};\n`);
await writeFile(resolve(dist, 'project.config.json'), JSON.stringify({
  appid, projectname: 'xiaoling-user', compileType: 'miniprogram',
  setting: { urlCheck: true, es6: true },
}, null, 2) + '\n');
console.log(dist);
