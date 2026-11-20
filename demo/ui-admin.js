/* 管理端 */
(function () {
  function aTab(active) {
    var items = [
      ['dash', '看板', '<path d="M4 20V10m6 10V5m6 15v-7"/>'],
      ['orders', '订单', '<rect x="4" y="3.5" width="16" height="17" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/>'],
      ['goods', '商品', '<path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z"/><path d="M4 7.5l8 4.5 8-4.5M12 12v9"/>'],
      ['riders', '骑手', '<circle cx="12" cy="8.5" r="3.6"/><path d="M4.8 20c1-3.6 3.9-5.6 7.2-5.6s6.2 2 7.2 5.6"/>']
    ];
    return '<nav class="tabbar">' + items.map(function (it) {
      var on = active === it[0];
      return '<button class="' + (on ? 'on' : '') + '"' + (on ? ' aria-current="page"' : '') + ' data-act="tab" data-val="' + it[0] + '">' +
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">' + it[2] + '</svg>' + it[1] + '</button>';
    }).join('') + '</nav>' + homebar();
  }

  function kpi(label, value, note, delta) {
    return '<div class="a-kpi"><span>' + label + (delta ? '<i>' + delta + '</i>' : '') + '</span><b>' + value + '</b><em>' + note + '</em></div>';
  }

  VIEWS.admin.dash = function () {
    var a = state.admin;
    var week = a.range === 'week';
    var extra = a.extra;
    var gmv = week ? 78120 + extra * 32.3 : 12480 + extra * 32.3;
    var cnt = (week ? 2418 : 386) + extra;
    var bars = week ? [72, 64, 81, 68, 90, 86, 96] : [38, 52, 44, 66, 58, 78, 92];
    var labels = week ? ['一', '二', '三', '四', '五', '六', '日'] : ['12', '13', '14', '15', '16', '17', '18'];
    var peak = week ? 96 : 92;
    var todos = a.todos.map(function (t, index) { return { item: t, index: index }; });
    todos.sort(function (a, b) { return Number(a.item.done) - Number(b.item.done); });
    return statusbar() +
      '<div class="scroll" style="padding-top:4px">' +
        '<div class="ahead" style="display:flex;align-items:flex-end;justify-content:space-between">' +
          '<div><div class="small muted">北苑仓 · 经营手账</div>' +
            '<h1 class="h-serif" style="font-size:24px;margin-top:5px">运营看板</h1></div>' +
          '<div class="range" style="display:flex;border:1px solid var(--line);border-radius:8px;overflow:hidden;background:var(--card)">' +
            [['today', '今日'], ['week', '本周']].map(function (r) {
              return '<button class="chip ' + (a.range === r[0] ? 'on' : '') + '" style="border:0;border-radius:0;padding:6px 10px;font-size:9.5px" data-act="aRange" data-val="' + r[0] + '">' + r[1] + '</button>';
            }).join('') + '</div></div>' +
        '<section class="admin-todos"><div class="row-between"><h2>待处理事项</h2><span>' + a.todos.filter(function (t) { return !t.done; }).length + ' 件待处理</span></div>' +
          todos.map(function (entry) {
            var t = entry.item;
            return '<button class="a-todo ' + (t.done ? 'cked' : '') + '" aria-pressed="' + t.done + '" data-act="aTodo" data-val="' + entry.index + '"><span class="ck" aria-hidden="true"></span><span class="todo-text">' + esc(t.t) + '</span><span class="todo-due">' + (t.done ? '已完成' : t.due) + '</span></button>';
          }).join('') + '</section>' +
        '<div class="sechead"><h2>经营概览</h2></div><div class="a-kpis">' +
          kpi('营业额', money(gmv), week ? '上周 ' + money(72000) : '比昨天多卖 ' + money(1910), '+18%') +
          kpi('订单', cnt, week ? '日均 345 单' : '待处理 ' + (12 + extra) + ' 单', '+9%') +
          kpi('客单价', money(32.3), '两件以上 61%') +
          kpi('履约率', '98.2%', '3 单超时 · 已赔付') +
        '</div>' +
        '<div class="abar-c"><div class="ch"><b class="h-serif">' + (week ? '本周订单' : '今日订单') + '</b><span>' + (week ? '周三' : '20:00') + ' 峰值 ' + peak + '</span></div>' +
          '<div class="abars">' + bars.map(function (v, i) {
            var on = i === bars.length - 1;
            return '<i class="' + (on ? 'on' : '') + '" style="height:' + v + '%" data-v="' + v + '"></i>';
          }).join('') + '</div>' +
          '<div class="ax">' + labels.map(function (l) { return '<span>' + l + '</span>'; }).join('') + '</div></div>' +
        '<div class="card a-feed" style="margin-bottom:12px"><div class="row-between" style="margin-bottom:4px"><b class="h-serif" style="font-size:12px">实时动态</b><span class="small muted">每 10 秒刷新</span></div>' +
          a.feed.map(function (f) {
            return '<div class="f"><time>' + f.t + '</time><i class="' + f.dot + '"></i><div>' + f.text + '</div><span style="margin-left:auto;color:#A29888;font-size:8.5px">' + f.tag + '</span></div>';
          }).join('') + '</div>' +
        '<div style="height:14px"></div>' +
      '</div>' + aTab('dash');
  };

  function adminOrders() {
    return state.orders.filter(function (o) {
      if (state.admin.filter !== '全部' && o.status !== state.admin.filter) return false;
      return orderMatches(o, state.admin.q, state.admin.listRange);
    });
  }
  function adminRows() {
    var list = adminOrders();
    if (!list.length) return emptyState('没有符合条件的订单', '换个关键词、状态或时间范围');
    return list.map(function (o) {
      var m = statusMeta(o.status);
      return '<div class="a-order" data-act="aOpen" data-val="' + o.no + '">' +
        '<div class="ao-top">' +
          (merchantStage(o) ? selectionButton('aSelect', o.no, state.admin.orderSelection, false) : '') +
          '<span class="no">' + o.no + '</span><span class="who">' + esc(o.contact || '') + '</span>' +
          '<span class="pill st ' + m[0] + '">' + m[1] + '</span>' +
          '<span class="amt">' + money(orderTotal(o)) + '</span></div>' +
        '<div class="ao-sub"><span>' + esc(o.addr) + '</span><span>' + maskPhone(o.phone) + '</span></div></div>';
    }).join('');
  }
  function batchControls() {
    var selection = state.admin.orderSelection;
    var stages = selection.map(function (no) { return merchantStage(orderByNo(no)); });
    var accept = stages.length && stages.every(function (stage) { return stage === 'accept'; });
    var ready = stages.length && stages.every(function (stage) { return stage === 'ready'; });
    return '<div class="batch-tools"><span aria-live="polite">已选 ' + selection.length + ' 单</span>' +
      batchButton('aBatch', '批量接单', !accept || selection.length > state.station.merchantLimit, 'accept') +
      batchButton('aBatch', '批量出餐', !ready, 'ready') +
      '<button class="btn sm ghost" data-act="aClear"' + (!selection.length ? ' disabled' : '') + '>清除</button></div>' +
      '<div class="small muted">每批最多接 ' + state.station.merchantLimit + ' 单，出餐后推送到配送端。</div>';
  }
  window.ALIST = function () { return { html: adminRows(), count: adminOrders().length, controls: batchControls() }; };

  VIEWS.admin.orders = function () {
    var filters = ['全部', '待商家接单', '备货中', '待骑手接单', '待取货', '配送中', '已完成'];
    var ranges = [['all', '全部时间'], ['today', '今天'], ['3d', '近 3 天']];
    return statusbar() +
      '<div class="scroll" style="padding-top:4px">' +
        '<div class="row-between"><div><div class="small muted" style="letter-spacing:.14em">ORDERS</div>' +
          '<h1 class="h-serif" style="font-size:22px;margin-top:4px">订单管理</h1></div>' +
          '<button class="btn sm ghost" data-act="aSettings">配送费与接单设置</button></div>' +
        '<div class="search" style="margin-top:12px">' +
          '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="10.5" cy="10.5" r="6.5"/><path d="M15.5 15.5L21 21"/></svg>' +
          '<input data-input="asearch" value="' + esc(state.admin.q) + '" placeholder="订单号 / 手机号 / 收货人 / 地址">' +
        '</div>' +
        '<div class="chips" style="margin-top:10px">' + ranges.map(function (r) {
          return '<button class="chip ' + (state.admin.listRange === r[0] ? 'on' : '') + '" data-act="aListRange" data-val="' + r[0] + '">' + r[1] + '</button>';
        }).join('') + '</div>' +
        '<div class="chips" style="margin-top:6px">' + filters.map(function (f) {
          return '<button class="chip ' + (state.admin.filter === f ? 'on' : '') + '" data-act="aFilter" data-val="' + f + '">' + f + '</button>';
        }).join('') + '</div>' +
        '<div id="aBatchControls">' + batchControls() + '</div>' +
        '<p class="small muted" id="aOrderCount" style="margin-top:10px">' + adminOrders().length + ' 单</p>' +
        '<div class="card" style="margin-top:6px" id="aOrderList">' + adminRows() + '</div>' +
        '<div style="height:10px"></div>' +
      '</div>' + aTab('orders');
  };

  VIEWS.admin.goods = function () {
    var low = PRODUCTS.filter(function (p) { return p.stock <= p.safe; }).length;
    var off = PRODUCTS.filter(function (p) { return p.shelf === false; }).length;
    var rows = PRODUCTS.map(function (p) {
      var pct = Math.max(4, Math.min(100, Math.round(p.stock / Math.max(1, p.safe * 3) * 100)));
      var cls = p.stock <= p.safe ? 'low' : '';
      return '<div class="goods-row ' + (p.shelf === false ? 'off' : '') + '">' +
        '<div class="goods-top">' +
          '<div class="cart-thumb ' + p.tone + '" style="width:38px;height:38px">' + productPhoto(p.image, p.name, '<span></span>') + '</div>' +
          '<div style="min-width:0;flex:1"><b>' + esc(p.name) + (promoActive(p) ? '<span class="tag-sale">促 ' + offLabel(promoBestTier(p).off) + '</span>' : '') + '</b>' +
            '<small>' + esc(p.spec) + ' · ' + money(p.price) + ' · ' + p.cat + '</small></div>' +
          '<div class="stock-step"><button data-act="stock" data-val="' + p.id + '|-1">−</button><b>' + p.stock + '</b><button data-act="stock" data-val="' + p.id + '|1">＋</button></div>' +
        '</div>' +
        '<div class="stockbar ' + cls + '"><i style="width:' + pct + '%"></i></div>' +
        '<div class="row-between" style="margin-top:8px">' +
          '<span class="small muted">安全库存 ' + p.safe + ' · ' + (p.shelf === false ? '已下架' : '在售') + '</span>' +
          '<span style="display:flex;gap:5px">' +
            '<button class="btn sm ghost" data-act="pPromo" data-val="' + p.id + '">' + (p.promo ? '改促销' : '促销') + '</button>' +
            '<button class="btn sm ghost" data-act="pEdit" data-val="' + p.id + '">编辑</button>' +
            '<button class="btn sm" data-act="shelf" data-val="' + p.id + '">' + (p.shelf === false ? '上架' : '下架') + '</button>' +
          '</span></div></div>';
    }).join('');
    return statusbar() +
      '<div class="scroll" style="padding-top:4px">' +
        '<div class="row-between"><div><div class="small muted" style="letter-spacing:.14em">INVENTORY</div>' +
          '<h1 class="h-serif" style="font-size:22px;margin-top:4px">商品与库存</h1></div>' +
          '<button class="btn sm primary" data-act="pNew">＋ 新增商品</button></div>' +
        '<div class="row-between" style="margin:12px 0 0"><span class="small muted">共 ' + PRODUCTS.length + ' 个商品</span>' +
          '<span class="small" style="color:var(--red)">' + low + ' 个低库存 · ' + off + ' 个已下架</span></div>' +
        '<div class="card" style="margin-top:10px">' + rows + '</div>' +
        '<div class="note" style="margin-top:14px"><span class="tape"></span><div class="nt"><h3 class="h-serif">库存说明</h3><span>实时同步</span></div>' +
          '<p>「＋ / −」直接改库存，用户加购会按库存上限拦；下架后用户端立刻看不到这个商品。</p>' +
          '<div class="row"><span class="mini">低库存 ' + low + ' 个</span><span class="mini">在售 ' + (PRODUCTS.length - off) + ' 个</span></div></div>' +
        '<div style="height:16px"></div>' +
      '</div>' + aTab('goods');
  };

  VIEWS.admin.riders = function () {
    var list = state.admin.riders;
    var online = list.filter(function (r) { return r.online; }).length;
    var rows = list.map(function (r, i) {
      return '<div class="rider"><div class="rv">' + r.name.charAt(0) + '</div>' +
        '<div><b>' + r.name + ' · ' + r.no + '</b><small>' + (r.online ? '在线 · 今日 ' + r.done + ' 单' : '离线 · 今日未接单') + ' · 评分 ' + r.score + '</small></div>' +
        '<span class="tg ' + (r.online ? 'on' : '') + '" data-act="aRider" data-val="' + i + '" style="margin-left:auto"></span></div>';
    }).join('');
    return statusbar() +
      '<div class="scroll" style="padding-top:4px">' +
        '<div class="row-between"><div><div class="small muted" style="letter-spacing:.14em">RIDERS</div>' +
          '<h1 class="h-serif" style="font-size:22px;margin-top:4px">骑手与站点</h1></div>' +
          '<span class="small muted">在线 ' + online + ' / ' + list.length + '</span></div>' +
        '<div class="d-brief" style="margin-top:14px"><div><span>在线骑手</span><b>' + online + '</b></div>' +
          '<div><span>待接单</span><b>' + state.deliver.avail.length + '</b></div>' +
          '<div><span>平均时长</span><b>19<i>分</i></b></div></div>' +
        '<div class="card">' + rows + '</div>' +
        '<div class="card" style="margin-top:12px"><div class="row-between"><div><b class="h-serif" style="font-size:12px">北苑站</b><div class="small muted" style="margin-top:4px">服务 6 栋宿舍 · 22:00 后转夜班</div></div>' +
          '<button class="btn sm ghost" data-act="soon" data-val="演示版：站点设置">设置</button></div></div>' +
        '<p class="small muted" style="padding:12px 2px;line-height:1.7">开关只影响演示状态；真实系统里会决定派单优先级。</p>' +
        '<div style="height:10px"></div>' +
      '</div>' + aTab('riders');
  };
})();
