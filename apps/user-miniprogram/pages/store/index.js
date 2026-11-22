const { storefrontUrl } = require('../../storefront.js');

Page({
  data: { url: storefrontUrl, failed: false },
  showError() {
    this.setData({ failed: true });
  },
  retry() {
    this.setData({ failed: false });
  },
});
