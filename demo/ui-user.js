/* 用户端 */
(function () {
  function uTab(active) {
    var items = [
      ['home', '首页', '<path d="M4 10.5L12 4l8 6.5V20H4z"/><path d="M9.6 20v-5.4h4.8V20"/>'],
      ['category', '分类', '<rect x="4" y="4" width="7" height="7" rx="2"/><rect x="13" y="4" width="7" height="7" rx="2"/><rect x="4" y="13" width="7" height="7" rx="2"/><rect x="13" y="13" width="7" height="7" rx="2"/>'],
      ['orders', '订单', '<path d="M6 3h12v18l-6-4-6 4z"/>'],
      ['me', '我的', '<circle cx="12" cy="8.5" r="3.6"/><path d="M4.8 20c1-3.6 3.9-5.6 7.2-5.6s6.2 2 7.2 5.6"/>']
    ];
    return '<nav class="tabbar">' + items.map(function (it) {
      var on = active === it[0];
      return '<button class="' + (on ? 'on' : '') + '"' + (on ? ' aria-current="page"' : '') + ' data-act="tab" data-val="' + it[0] + '">' +
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">' + it[2] + '</svg>' + it[1] + '</button>';
    }).join('') + '</nav>' + homebar();
  }

  function nav(title, rt) {
    return '<div class="nav"><button class="back" data-act="back"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 5l-7 7 7 7"/></svg></button>' +
      '<h1>' + title + '</h1><span class="rt">' + (rt || '') + '</span></div>';
  }

  function bagBtn() {
    return '<button class="bag" data-act="tab" data-val="cart" aria-label="购物袋">' +
      '<svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 8h16l-1.2 12H5.2L4 8z"/><path d="M8.5 8V6.2a3.5 3.5 0 0 1 7 0V8"/></svg>' +
      '<span class="badge" id="bagBadge" style="display:' + (cartCount() ? 'grid' : 'none') + '">' + cartCount() + '</span></button>';
  }

  function catIcon(i) {
    var paths = [
      '<path d="M6 8h12l-1 12H7z"/><path d="M9 8V6a3 3 0 0 1 6 0v2"/>',
      '<path d="M8 4h8l2 9a5 5 0 0 1-12 0z"/><path d="M7 20h10"/>',
      '<circle cx="12" cy="12" r="7"/><path d="M9 12h6"/>',
      '<rect x="5" y="4" width="14" height="16" rx="1.5"/><path d="M9 8h6M9 12h6"/>',
      '<path d="M12 4v16M4 12h16"/>'
    ];
    return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">' + paths[i] + '</svg>';
  }

  function timeline(o) {
    var steps = [
      ['已下单', o.created || '今天 20:04', true],
      ['备货完成', '北苑仓 A-12', false],
      ['骑手取货', o.rider && o.rider !== '待分配' ? o.rider + ' 已接单' : '等待分配骑手', false],
      ['送达', o.status === '已完成' ? '已送达，感谢下单' : (o.status === '配送中' ? '预计 20:31 送达' : '预计 30 分钟内'), false]
    ];
    var idx = o.status === '备货中' ? 1 : o.status === '待取货' ? 2 : o.status === '配送中' ? 2 : 3;
    if (o.status === '已完成') idx = 4;
    if (o.status === '已取消' || o.status === '已退款') {
      return '<div class="timeline"><div class="tl now"><span class="dot"></span><div><b>' + o.status + '</b><small>退款已原路退回，预计 1 分钟内到账</small></div></div></div>';
    }
    return '<div class="timeline">' + steps.map(function (s, i) {
      var cls = i < idx ? 'on' : (i === idx ? 'now' : '');
      return '<div class="tl ' + cls + '"><span class="dot"></span><span class="ln"></span><div><b>' + s[0] + '</b><small>' + s[1] + '</small></div></div>';
    }).join('') + '</div>';
  }

  VIEWS.user.home = function () {
    var list = filtered(state.search, '全部');
    var cats = ['全部', '冰饮', '零食', '文具', '日用'];
    var pick = byId('p3');
    var address = state.me.addrs[state.checkout.addrIndex];
    var promos = promoList().filter(function (p) { return p.stock > 0 && promoBestTier(p); });
    return statusbar() +
      '<div class="scroll">' +
        '<div class="hello"><div><div class="k">校零智选 · 北苑校园店</div><h1>宿舍采购清单</h1></div>' + bagBtn() + '</div>' +
        '<button class="loc" data-act="addrSheet">' +
          '<svg width="11" height="13" viewBox="0 0 12 15" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M6 1.5c2.6 0 4.6 2 4.6 4.5 0 3.2-4.6 7.5-4.6 7.5S1.4 9.2 1.4 6C1.4 3.5 3.4 1.5 6 1.5z"/><circle cx="6" cy="6" r="1.5"/></svg>' +
          (address ? '配送至 <b>' + esc(address.name) + '</b><span class="t">约 30 分钟</span>' : '<b>添加收货地址</b>') +
          '<svg width="9" height="6" viewBox="0 0 9 6" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M1 1l3.5 3.4L8 1"/></svg>' +
        '</button>' +
        '<div class="search">' +
          '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="10.5" cy="10.5" r="6.5"/><path d="M15.5 15.5L21 21"/></svg>' +
          '<input type="search" data-input="search" aria-label="搜索商品" value="' + esc(state.search) + '" placeholder="搜零食、冰饮、文具">' +
        '</div>' +
        '<div class="catrow">' + cats.map(function (c, i) {
          return '<button class="cat" data-act="openCat" data-val="' + c + '"><i>' + catIcon(i) + '</i>' + c + '</button>';
        }).join('') + '</div>' +
        (pick && pick.shelf && pick.stock > 0 ? '<div class="note home-note"><span class="tape"></span>' +
          '<div class="nt"><h3 class="h-serif">今晚的推荐</h3><span>20:00–23:00</span></div>' +
          '<div class="note-buy"><p>' + esc(pick.name) + ' <strong>' + money(unitPrice(pick, 1)) + '</strong></p>' +
          '<button class="cta" data-act="openP" data-val="' + pick.id + '">查看商品</button></div>' +
        '</div>' : '') +
        '<div class="sechead"><h2 id="secTitle">' + (state.search.trim() ? '搜索「' + esc(state.search.trim()) + '」' : '常买清单') + '<small>共 ' + list.length + ' 件</small></h2>' +
          '<button class="more" data-act="openCat" data-val="全部">全部 →</button></div>' +
        '<div class="grid" id="goodsGrid">' + (list.length ? tiles(list) : '') + '</div>' +
        '<div id="homeEmpty"' + (list.length ? ' hidden' : '') + '>' + emptyState('没有找到相关商品', '换个关键词试试，比如「薯片」「冰饮」') + '</div>' +
        (promos.length ? '<section id="homePromos"' + (state.search.trim() ? ' hidden' : '') + '>' +
          '<div class="sechead"><h2>限时促销 <small>' + promos.length + ' 件</small></h2></div>' +
          '<div class="promo-row">' + promos.map(function (p) {
            return '<button class="pcard2" data-act="openP" data-val="' + p.id + '">' +
              '<span class="pimg2 ' + p.tone + '">' + productPhoto(p.image, p.name, '<span></span>') + '<u>低至 ' + offLabel(promoBestTier(p).off) + '</u></span>' +
              '<b>' + esc(p.name) + '</b><span class="pp"><b>' + money(promoLowPrice(p)) + '</b><s>' + money(p.price) + '</s></span></button>';
          }).join('') + '</div></section>' : '') +
        '<div style="height:16px"></div>' +
      '</div>' + uTab('home');
  };

  VIEWS.user.category = function () {
    var list = filtered('', state.cat);
    return statusbar() +
      '<div class="nav"><button class="back" data-act="back"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 5l-7 7 7 7"/></svg></button>' +
        '<h1>分类</h1><span class="rt">' + list.length + ' 件</span></div>' +
      '<div class="scroll" style="padding-top:0">' +
        '<div class="chips">' + CATS.map(function (c) {
          return '<button class="chip ' + (state.cat === c ? 'on' : '') + '" data-act="cat" data-val="' + c + '">' + c + '</button>';
        }).join('') + '</div>' +
        '<div style="height:12px"></div>' +
        '<div class="grid">' + tiles(list) + '</div>' +
        (list.length ? '' : emptyState('这个分类还空着', '换个分类看看')) +
        '<div style="height:16px"></div>' +
      '</div>' + uTab('category');
  };

  function promoBlock(p, qty) {
    var tier = tierFor(p, qty);
    var next = p.promo.tiers.filter(function (t) { return t.min > qty; }).sort(function (a, b) { return a.min - b.min; })[0];
    var hint = tier
      ? (next ? '再买 ' + (next.min - qty) + ' 件享 ' + offLabel(next.off) : '已享最低档')
      : ('满 ' + p.promo.tiers[0].min + ' 件享 ' + offLabel(p.promo.tiers[0].off));
    var rows = p.promo.tiers.map(function (t) {
      return '<div class="tier-row ' + (tier && tier.min === t.min ? 'on' : '') + '"><span>满 ' + t.min + ' 件</span><b>' + offLabel(t.off) + '</b></div>';
    }).join('');
    return '<div class="promo-wrap">' +
      '<div class="promo-banner"><div class="pb-l"><b>限时折扣</b><em>' + hint + '</em></div>' +
        '<div class="pb-r"><small>距结束</small><b class="cd" data-end="' + p.promo.end + 'T23:59:59">--:--:--</b></div></div>' +
      '<div class="tier-table">' + rows + '</div></div>';
  }

  VIEWS.user.product = function () {
    var p = byId(state.productId) || PRODUCTS[0];
    var pack = state.pd.spec === '三件装' ? 3 : 1;
    var base = packBase(p, pack);
    var qty = state.pd.qty;
    var tier = tierFor(p, qty);
    var unit = unitPrice(p, qty, pack);
    var offer = pack === 1 ? p.secondItemOffer : null;
    var offerLabel = offer ? secondItemLabel(offer) : '';
    var onSale = promoActive(p);
    var shape = p.tone === 't2' ? 'g' : (p.tone === 't3' ? 'y' : '');
    var reviews = p.reviews.length ? p.reviews.map(function (r) {
      return '<div class="review"><span class="av">' + r[0] + '</span><div><b>' + r[0] + '同学</b><p>' + r[1] + '</p></div></div>';
    }).join('') : '<p class="pd-desc">还没有评价，买一个试试。</p>';
    return statusbar() +
      '<div class="scroll" style="padding-top:0">' +
        '<div class="pd-hero"><button class="back" data-act="back"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 5l-7 7 7 7"/></svg></button>' +
          '<span class="tagpill">' + esc(offerLabel || p.tag) + '</span>' + productPhoto(p.image, p.name, '<span class="shape ' + shape + '"></span>') + '</div>' +
        '<div class="pd-card"><h2 class="h-serif">' + esc(p.name) + '</h2><div class="sub">' + esc(p.spec) + ' · 已售 ' + p.sold + ' 件</div>' +
          '<div class="pd-price"><b>' + money(unit) + '</b>' +
          (onSale ? '<small>' + money(base) + '</small>' : (p.old ? '<small>' + money(p.old) + '</small>' : '')) +
          (onSale || !offer ? '<span class="save">' + (onSale ? (tier ? offLabel(tier.off) : '促销') : esc(p.tag)) + '</span>' : '') + '</div>' +
          (offer ? '<div class="co-line"><span>商品小计 · 单件规格每两件享' + offerLabel + '</span><b>' + money(lineSubtotal(p, qty, pack)) + '</b></div>' : '') +
          (onSale ? promoBlock(p, qty) : '') + '</div>' +
        '<div class="pd-block"><div class="bh"><b>规格</b><span>可选</span></div><div class="specs">' +
          ['单件', '三件装'].map(function (s) {
            return '<button class="' + (state.pd.spec === s ? 'on' : '') + '" data-act="pdSpec" data-val="' + s + '">' + s + '</button>';
          }).join('') +
        '</div></div>' +
        '<div class="pd-block"><div class="bh"><b>购买数量</b><span>库存充足</span></div>' +
          '<div class="stepper"><button data-act="pdQty" data-val="-1">−</button><b id="pdQty">' + state.pd.qty + '</b><button data-act="pdQty" data-val="1">＋</button></div></div>' +
        '<div class="pd-block card"><div class="bh"><b>商品说明</b><span>北苑仓现货</span></div><p class="pd-desc">' + esc(p.desc) + '</p></div>' +
        '<div class="pd-block card"><div class="bh"><b>同学评价</b><span>' + p.reviews.length + ' 条</span></div>' + reviews + '</div>' +
        '<div style="height:14px"></div>' +
      '</div>' +
      '<div class="pd-bar"><div class="stepper"><button data-act="pdQty" data-val="-1">−</button><b>' + state.pd.qty + '</b><button data-act="pdQty" data-val="1">＋</button></div>' +
        '<button class="btn primary" data-act="pdAdd">加入购物袋</button>' +
        '<button class="btn red" data-act="buyNow">立即买</button></div>';
  };

  VIEWS.user.cart = function () {
    if (!state.cart.length) {
      return statusbar() + nav('购物袋', '0 件') +
        '<div class="scroll" style="padding-top:0">' + emptyState('购物袋还是空的', '去首页挑点吃的，宿舍常备都在那儿', '<button class="btn primary" data-act="tab" data-val="home">去逛逛</button>') + '</div>' + uTab('home');
    }
    var rows = state.cart.map(function (c, i) {
      var p = byId(c.id);
      var subtotal = lineSubtotal(p, c.qty, c.pack);
      var baseSubtotal = packBase(p, c.pack) * c.qty;
      return '<div class="cart-row"><span class="check ' + (c.checked ? 'on' : '') + '" data-act="cartCheck" data-val="' + i + '"></span>' +
        '<div class="cart-thumb ' + p.tone + '">' + productPhoto(p.image, p.name, '<span></span>') + '</div>' +
        '<div class="cart-info"><b>' + esc(p.name) + (subtotal < baseSubtotal ? ' <em class="sale-tag">促</em>' : '') + '</b><small>' + (c.pack === 3 ? '三件装 · ' : '') + esc(p.spec) + '</small></div>' +
        '<div class="cart-right"><span class="p">' + money(subtotal) + '</span>' +
        '<span class="stepper"><button data-act="cartQty" data-val="' + i + ':-1">−</button><b>' + c.qty + '</b><button data-act="cartQty" data-val="' + i + ':1">＋</button></span></div></div>';
    }).join('');
    var allchecked = state.cart.every(function (c) { return c.checked; });
    return statusbar() + nav('购物袋', cartCount() + ' 件') +
      '<div class="scroll" style="padding-top:0">' +
        '<div class="row-between" style="padding:4px 0 2px"><span class="small muted">北苑仓 · 30 分钟送达</span>' +
          '<span class="small muted" data-act="soon" data-val="演示版：清空失效商品">清空失效</span></div>' +
        rows +
        '<div style="height:10px"></div>' +
      '</div>' +
      '<div class="cart-foot"><span class="check ' + (allchecked ? 'on' : '') + '" data-act="cartAll"></span>' +
        '<span class="small muted">全选</span>' +
        '<span class="sum">合计<b>' + money(cartTotal()) + '</b></span>' +
        '<button class="btn red" data-act="checkout">去结算（' + checkedCount() + '）</button></div>';
  };

  VIEWS.user.checkout = function () {
    var addr = state.me.addrs[state.checkout.addrIndex];
    var items = state.cart.filter(function (c) { return c.checked; });
    var subtotal = cartTotal();
    var fee = state.station.fee;
    var sum = subtotal + fee;
    var rows = items.map(function (c) {
      var p = byId(c.id);
      var amount = lineSubtotal(p, c.qty, c.pack);
      return '<div class="co-line"><span>' + esc(p.name) + (c.pack === 3 ? ' 三件装' : '') + ' × ' + c.qty + (amount < packBase(p, c.pack) * c.qty ? ' <em class="sale-tag">促</em>' : '') + '</span><b>' + money(amount) + '</b></div>';
    }).join('');
    var times = ['尽快送达（约 20 分钟）', '20:30 – 20:45', '21:00 – 21:15'];
    var pays = [['微信支付', '线上支付 · 立即扣款', '微'], ['货到付款', '送达时当面付款', '付']];
    return statusbar() + nav('确认订单', '共 ' + checkedCount() + ' 件') +
      '<div class="scroll" style="padding-top:0">' +
        '<div class="co-card co-addr" data-act="addrSheet"><span class="pin">' +
          '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M12 3c3.6 0 6.4 2.8 6.4 6.3 0 4.4-6.4 10.2-6.4 10.2S5.6 13.7 5.6 9.3C5.6 5.8 8.4 3 12 3z"/><circle cx="12" cy="9.3" r="2.1"/></svg></span>' +
          '<div><b>' + (addr ? esc(addr.name) : '添加收货地址') + '</b><small>' + (addr ? esc(addr.sub) : '请选择送达位置') + '</small></div>' +
          '<span class="arrow"><svg width="8" height="12" viewBox="0 0 8 12" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M1 1l5 5-5 5"/></svg></span></div>' +
        '<div class="co-card"><div class="co-h">送达时间</div><div class="chips">' + times.map(function (t) {
          return '<button class="chip ' + (state.checkout.time === t ? 'on' : '') + '" data-act="setTime" data-val="' + t + '">' + t + '</button>';
        }).join('') + '</div></div>' +
        '<div class="co-card"><div class="co-h">商品 ' + items.length + ' 件</div>' + rows +
          '<div class="co-line"><span>商品小计</span><b>' + money(subtotal) + '</b></div>' +
          '<div class="co-line"><span>配送费</span><b>' + money(fee) + '</b></div>' +
          '<div class="co-line total"><span>合计</span><b>' + money(sum) + '</b></div></div>' +
        '<div class="co-card"><div class="co-h">支付方式</div>' + pays.map(function (p) {
          return '<div class="pay-opt ' + (state.checkout.pay === p[0] ? 'on' : '') + '" data-act="setPay" data-val="' + p[0] + '">' +
            '<span class="radio"></span><span class="ic">' + p[2] + '</span>' +
            '<span class="pt"><b>' + p[0] + '</b><small>' + p[1] + '</small></span></div>';
        }).join('') + '</div>' +
        '<div class="co-card"><div class="co-h">备注</div><textarea class="note-input" data-input="note" placeholder="比如：放门口、不要辣、到了打电话">' + esc(state.checkout.note) + '</textarea></div>' +
        '<div class="co-card"><div class="co-h">配送信息</div><div class="co-line"><span>配送站点</span><b>北苑仓</b></div>' +
          '<div class="co-line"><span>预计送达</span><b>' + state.checkout.time + '</b></div></div>' +
        '<div style="height:8px"></div>' +
      '</div>' +
      '<div class="pd-bar"><div style="flex:1"><span class="small muted">应付</span><div style="font-size:19px;color:var(--red);font-weight:600">' + money(sum) + '</div></div>' +
          '<button class="btn red" data-act="' + (addr ? 'submit' : 'addrSheet') + '" style="flex:0 0 150px">' + (addr ? '提交订单' : '添加收货地址') + '</button></div>';
  };

  VIEWS.user.success = function () {
    var o = state.orders.filter(function (x) { return x.no === state.lastNo; })[0];
    return statusbar() +
      '<div class="done"><div class="stamp"><svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M4 12.5l5 5L20 6.5"/></svg></div>' +
        '<h2>下单成功</h2><p>订单 ' + (o ? o.no : '') + ' 已送到北苑仓<br>' + (o ? esc(o.addr) : '') + ' · ' +
          (o && o.pay === '货到付款' ? '货到付款，送达时支付' : '已支付 · 预计 20 分钟后送达') + '</p>' +
        '<div class="acts"><button class="btn ghost" data-act="tab" data-val="home">继续逛</button>' +
        '<button class="btn primary" data-act="go" data-val="orders">查看订单</button></div></div>' + homebar();
  };

  VIEWS.user.orders = function () {
    var tabs = ['全部', '进行中', '已完成'];
    var list = state.orders.filter(function (o) {
      if (state.orderTab === '进行中') return orderInProgress(o);
      if (state.orderTab === '已完成') return o.status === '已完成';
      return true;
    });
    var cards = list.map(function (o) {
      var count = o.items.reduce(function (a, i) { return a + i.qty; }, 0);
      var thumbs = o.items.slice(0, 3).map(function (it) {
        var p = byId(it.id);
        return '<span>' + productPhoto(p.image, p.name, '<i></i>') + '</span>';
      }).join('');
      var acts = o.status === '已完成'
        ? '<button class="btn sm" data-act="reorder" data-val="' + o.no + '">再来一单</button><button class="btn sm ghost" data-act="openOrder" data-val="' + o.no + '">查看详情</button>'
        : (orderInProgress(o) && o.status !== '配送中')
          ? '<button class="btn sm ghost" data-act="urge" data-val="' + o.no + '">催单</button>' +
            '<button class="btn sm ghost" data-act="cancelOrder" data-val="' + o.no + '">取消</button>' +
            '<button class="btn sm" data-act="openOrder" data-val="' + o.no + '">订单详情</button>'
          : (o.status === '已取消' || o.status === '已退款')
            ? '<button class="btn sm" data-act="reorder" data-val="' + o.no + '">重新下单</button><button class="btn sm ghost" data-act="openOrder" data-val="' + o.no + '">订单详情</button>'
            : '<button class="btn sm ghost" data-act="urge" data-val="' + o.no + '">催单</button>' +
              '<button class="btn sm ghost" data-act="contact">联系骑手</button>' +
              '<button class="btn sm" data-act="openOrder" data-val="' + o.no + '">订单详情</button>';
      return '<div class="ord" data-act="openOrder" data-val="' + o.no + '">' +
        '<div class="ord-h"><span class="no">' + o.no + '</span><span>' + o.time + '</span>' + pill(o.status) + '</div>' +
        '<div class="ord-items"><div class="thumbs">' + thumbs + '</div><span class="meta">共 ' + count + ' 件</span>' +
          '<div class="total"><b>' + money(orderTotal(o)) + '</b><small>' + esc(o.addr) + '</small></div></div>' +
        '<div class="ord-acts">' + acts + '</div></div>';
    }).join('');
    return statusbar() +
      '<div class="scroll" style="padding-top:2px">' +
        '<h1 class="h-serif" style="font-size:22px;padding:4px 0 14px">我的订单</h1>' +
        '<div class="seg">' + tabs.map(function (t) {
          return '<button class="' + (state.orderTab === t ? 'on' : '') + '" data-act="orderTab" data-val="' + t + '">' + t + '</button>';
        }).join('') + '</div>' +
        (cards || emptyState('这里还没有订单', '去首页下第一单吧', '<button class="btn primary" data-act="tab" data-val="home">去下单</button>')) +
        '<div style="height:14px"></div>' +
      '</div>' + uTab('orders');
  };

  VIEWS.user.order = function () {
    var o = state.orders.filter(function (x) { return x.no === state.detailNo; })[0];
    if (!o) return statusbar() + nav('订单详情') + '<div class="scroll">' + emptyState('订单不见了', '回到列表看看') + '</div>';
    var rows = o.items.map(function (it) {
      var p = byId(it.id);
      return '<div class="cart-row" style="border:0;padding:8px 0"><div class="cart-thumb ' + p.tone + '">' + productPhoto(p.image, p.name, '<span></span>') + '</div>' +
        '<div class="cart-info"><b>' + esc(p.name) + '</b><small>' + esc(p.spec) + '</small></div>' +
        '<div class="cart-right"><span class="p">' + money(orderItemSubtotal(it)) + '</span><small class="muted">×' + it.qty + '</small></div></div>';
    }).join('');
    var acts = o.status === '已完成'
      ? '<button class="btn block" data-act="reorder" data-val="' + o.no + '">再来一单</button><button class="btn block ghost" data-act="soon" data-val="演示版：评价晒单">评价</button>'
      : (o.status === '已取消' || o.status === '已退款')
        ? '<button class="btn block" data-act="reorder" data-val="' + o.no + '">重新下单</button>'
        : '<button class="btn block ghost" data-act="urge" data-val="' + o.no + '">催单</button>' +
          '<button class="btn block ghost" data-act="contact">联系骑手</button>' +
          '<button class="btn block" data-act="cancelOrder" data-val="' + o.no + '">取消订单</button>';
    return statusbar() + nav('订单详情', o.no) +
      '<div class="scroll" style="padding-top:0">' +
        '<div class="card" style="margin-bottom:11px"><div class="ord-h" style="margin-bottom:8px"><span class="no">' + o.no + '</span>' + pill(o.status) + '</div>' +
          logsHtml(o) + '</div>' +
        '<div class="card" style="margin-bottom:11px"><div class="co-h">配送信息</div>' +
          '<div class="co-line"><span>收货地址</span><b>' + esc(o.addr) + '</b></div>' +
          '<div class="co-line"><span>骑手</span><b>' + o.rider + '</b></div>' +
          '<div class="co-line"><span>支付方式</span><b>' + o.pay + (o.pay === '货到付款' ? (o.status === '已完成' ? ' · 已收款' : ' · 送达时收取') : ' · 已支付') + '</b></div>' +
          '<div class="co-line"><span>备注</span><b>' + (esc(o.note) || '无') + '</b></div></div>' +
        '<div class="card" style="margin-bottom:11px"><div class="co-h">商品</div>' + rows +
          '<div class="co-line"><span>商品小计</span><b>' + money(orderSubtotal(o)) + '</b></div>' +
          '<div class="co-line"><span>配送费</span><b>' + money(o.deliveryFee || 0) + '</b></div>' +
          '<div class="co-line total"><span>' + (o.pay === '货到付款' && o.status !== '已完成' ? '应付' : '实付') + '</span><b>' + money(orderTotal(o)) + '</b></div></div>' +
        '<div style="display:flex;gap:9px;margin-top:14px">' + acts + '</div>' +
        '<div style="height:16px"></div>' +
      '</div>';
  };

  VIEWS.user.me = function () {
    var address = state.me.addrs[state.checkout.addrIndex];
    return statusbar() +
      '<div class="scroll" style="padding-top:6px">' +
        '<div class="me-head"><div class="av">满</div><div><b>小满</b><small>北苑 3 号楼 · 2023 级 软件工程</small></div></div>' +
        '<div class="me-stats"><div><b>' + state.orders.length + '</b><span>订单</span></div>' +
          '<div><b>' + state.me.coupons + '</b><span>优惠券</span></div>' +
          '<div><b>' + state.me.points + '</b><span>积分</span></div></div>' +
        '<div class="card" style="margin-top:12px;padding:4px 14px">' +
          '<div class="row" data-act="addrSheet" style="cursor:pointer"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M12 3c3.6 0 6.4 2.8 6.4 6.3 0 4.4-6.4 10.2-6.4 10.2S5.6 13.7 5.6 9.3C5.6 5.8 8.4 3 12 3z"/><circle cx="12" cy="9.3" r="2.1"/></svg></span><div><b>收货地址</b><small>' + (address ? esc(address.name) : '添加收货地址') + '</small></div><span class="rt">管理 →</span></div>' +
          '<div class="row" data-act="soon" data-val="演示版：优惠券包"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 9a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v2a2 2 0 0 0 0 4v2a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-2a2 2 0 0 0 0-4z"/></svg></span><div><b>优惠券</b><small>' + state.me.coupons + ' 张可用</small></div><span class="rt">去看看 →</span></div>' +
          '<div class="row" data-act="soon" data-val="演示版：联系客服"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 5h16v11H8l-4 4z"/></svg></span><div><b>联系客服</b><small>在线时间 09:00–24:00</small></div><span class="rt">→</span></div>' +
          '<div class="row" data-act="soon" data-val="演示版：设置"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="12" cy="12" r="3"/><path d="M12 3v2m0 14v2M3 12h2m14 0h2M5.6 5.6l1.4 1.4m10 10l1.4 1.4m0-12.8l-1.4 1.4m-10 10l-1.4 1.4"/></svg></span><div><b>设置</b><small>通知、隐私、账号</small></div><span class="rt">→</span></div>' +
        '</div>' +
        '<div class="note" style="margin-top:14px"><span class="tape"></span><div class="nt"><h3 class="h-serif">这个月吃了 12 单</h3><span>省下 36 块</span></div>' +
          '<p>常点：脆浪薯片、白桃气泡水。建议把宿舍常备加进常买清单。</p>' +
          '<div class="row"><span class="mini">积分 +128</span><span class="mini">环保袋 0 次</span></div></div>' +
        '<div style="height:16px"></div>' +
      '</div>' + uTab('me');
  };
})();
