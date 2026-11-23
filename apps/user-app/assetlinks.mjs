const packages = { user: 'cn.xiaoling.user', merchant: 'cn.xiaoling.merchant', rider: 'cn.xiaoling.rider' };
const [role, rawFingerprint] = process.argv.slice(2);
const fingerprint = rawFingerprint?.toUpperCase();
if (!Object.hasOwn(packages, role) || !/^(?:[0-9A-F]{2}:){31}[0-9A-F]{2}$/.test(fingerprint ?? '')) {
  throw new Error('Usage: node assetlinks.mjs <user|merchant|rider> <SHA-256 certificate fingerprint>');
}

console.log(JSON.stringify([{
  relation: ['delegate_permission/common.handle_all_urls'],
  target: {
    namespace: 'android_app',
    package_name: packages[role],
    sha256_cert_fingerprints: [fingerprint],
  },
}], null, 2));
