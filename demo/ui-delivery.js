/* 配送端 */
(function () {
  function dTab(active) {
    var items = [
      ['hall', '任务', '<path d="M4 6h16M4 12h16M4 18h10"/>'],
      ['orders', '订单', '<rect x="4" y="3.5" width="16" height="17" rx="2"/><path d="M8 8h8M8 12h8"/>'],
      ['income', '收入', '<rect x="3" y="6" width="18" height="13" rx="3"/><path d="M3 10h18"/>'],
      ['me', '我的', '<circle cx="12" cy="8.5" r="3.6"/><path d="M4.8 20c1-3.6 3.9-5.6 7.2-5.6s6.2 2 7.2 5.6"/>']
    ];
    return '<nav class="tabbar">' + items.map(function (it) {
      var on = active === it[0] || (active === 'task' && it[0] === 'hall');
      return '<a class="' + (on ? 'on' : '') + '" data-act="tab" data-val="' + it[0] + '">' +
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">' + it[2] + '</svg>' + it[1] + '</a>';
    }).join('') + '</nav>' + homebar();
  }

  function ticket(a, stage) {
    var steps = ['取货', '配送', '送达'];
    return '<div class="ticket" style="margin-top:12px">' +
      '<div class="tk-top"><span class="id">' + a.no + ' · ' + a.at + '</span><span class="stamp">急</span></div>' +
      '<div class="tk-title"><h3>' + a.addr + '</h3><p>' + a.km + ' · ' + a.items + ' 件 · 配送费 ' + money(a.fee) + '</p></div>' +
      '<div class="tk-dash"></div>' +
      '<div style="padding:0 14px"><div class="d-steps">' + steps.map(function (s, i) {
        var cls = i < stage ? 'on' : (i === stage ? 'now' : '');
        return '<div class="d-step ' + cls + '"><i></i>' + s + '</div>';
      }).join('') + '</div></div>' +
      '<div class="tk-route">' +
        '<div class="row" style="border:0;padding:6px 0"><span class="ic" style="width:26px;height:26px;border-radius:9px;background:rgba(53,102,79,.1);color:var(--green)">取</span>' +
          '<div><b>北苑仓 A-12 货架</b><small>' + (stage > 0 ? '已取货 · 商品已核对' : '待取货 · 报单号 ' + a.no) + '</small></div></div>' +
        '<div class="row" style="border:0;padding:6px 0"><span class="ic" style="width:26px;height:26px;border-radius:9px;background:rgba(217,96,58,.12);color:var(--red)">送</span>' +
          '<div><b>' + a.addr + '</b><small>备注：放门口，谢谢 · 到了发条消息</small></div></div>' +
      '</div>' +
      '<div class="tk-dash"></div>' +
      '<div class="tk-foot"><div class="sum"><b>' + a.items + ' 件商品</b>超时前送达可拿全额</div>' +
        '<button class="btn sm ghost" data-act="contact">联系买家</button></div></div>';
  }

  VIEWS.delivery.hall = function () {
    var d = state.deliver;
    var avail = d.avail.map(function (t) {
      return '<div class="avail"><div><b>' + t.addr + '</b><small>' + t.km + ' · ' + t.items + ' 件 · ' + t.tag + '</small></div>' +
        '<div class="fee"><b>+' + money(t.fee) + '</b><span>配送费</span></div>' +
        '<button class="btn sm ' + (d.online && !d.active ? 'primary' : '') + '" data-act="accept" data-val="' + t.id + '" style="margin-left:10px">接单</button></div>';
    }).join('');
    return statusbar() +
      '<div class="scroll" style="padding-top:4px">' +
        '<div class="d-top"><div class="who">今天第 ' + (d.done + 1) + ' 单<b class="h-serif">' + (d.active ? '配送中' : '待接单') + '</b></div>' +
          '<div style="display:flex;align-items:center;gap:7px">' +
            '<button class="bell" data-act="dMsg" aria-label="消息提醒">' +
              '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M6 9a6 6 0 1 1 12 0c0 5 2 6 2 6H4s2-1 2-6z"/><path d="M10 19a2 2 0 0 0 4 0"/></svg>' +
              '<span class="bell-badge" id="msgBadge" style="display:' + (unreadMsg() ? 'grid' : 'none') + '">' + unreadMsg() + '</span>' +
            '</button>' +
            '<button class="d-switch ' + (d.online ? '' : 'off') + '" data-act="dOnline"><i></i>' + (d.online ? '接单中' : '已收工') + '</button>' +
          '</div></div>' +
        '<div class="d-brief"><div><span>今日收入</span><b><i>¥</i>' + d.income.toFixed(1) + '</b></div>' +
          '<div><span>已送达</span><b>' + d.done + '</b></div>' +
          '<div><span>评分</span><b>4.98</b></div></div>' +
        (d.active ? ticket(d.active, d.active.stage) +
          '<button class="btn primary block" style="margin-top:12px" data-act="openTask">继续配送 →</button>'
          : '<div class="note" style="margin-top:2px"><span class="tape"></span><div class="nt"><h3 class="h-serif">现在没在跑单</h3><span>' + (d.online ? '听单中' : '已收工') + '</span></div>' +
            '<p>' + (d.online ? '附近有 ' + d.avail.length + ' 单待抢，最近一单 0.8 km。' : '上线后才能接到新单，点右上角开关。') + '</p>' +
            '<div class="row"><span class="mini">在线 3h12m</span><span class="mini">准时率 98%</span></div></div>') +
        '<div class="sechead"><h2>附近可接 <small>' + d.avail.length + ' 单</small></h2>' +
          '<button class="refresh" data-act="refresh"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M20 11a8 8 0 1 0-2.3 6"/><path d="M20 5v6h-6"/></svg>刷新<span id="refreshHint">10s</span></button></div>' +
        (avail || '<p class="small muted" style="padding:6px 0 14px">附近暂时没有新单，等一等。</p>') +
        '<div class="divider"><span>今日记录</span><i></i></div>' +
        d.records.slice(0, 3).map(function (r) {
          return '<div class="row"><div><b>' + r.no + '</b><small>' + r.t + ' 已送达</small></div><span class="rt">+' + money(r.fee) + '</span></div>';
        }).join('') +
        '<div style="height:14px"></div>' +
      '</div>' + dTab('hall');
  };

  VIEWS.delivery.task = function () {
    var a = state.deliver.active;
    if (!a) {
      return statusbar() + dTab('hall') + '<div class="scroll">' + emptyState('手上没有进行中的单', '回大厅抢一单', '<button class="btn primary" data-act="tab" data-val="hall">回大厅</button>') + '</div>';
    }
    var label = a.stage === 0 ? '到店取货' : a.stage === 1 ? '确认送达' : '这单已完成';
    var btn = a.stage === 2
      ? '<button class="btn primary block" style="margin-top:12px" data-act="finishTask">返回大厅接下一单</button>'
      : '<button class="btn primary block" style="margin-top:12px" data-act="dStep">' + label + '</button>';
    return statusbar() +
      '<div class="scroll" style="padding-top:2px">' +
        '<div class="nav"><button class="back" data-act="tab" data-val="hall"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 5l-7 7 7 7"/></svg></button>' +
          '<h1>当前任务</h1><span class="rt">' + a.km + '</span></div>' +
        ticket(a, a.stage) +
        '<div class="card" style="margin-top:12px"><div class="co-h">时间与收入</div>' +
          '<div class="co-line"><span>接单时间</span><b>' + a.at + '</b></div>' +
          '<div class="co-line"><span>预计送达</span><b>' + (a.stage === 2 ? '已送达' : '20:31 前') + '</b></div>' +
          '<div class="co-line total"><span>本单收入</span><b>' + money(a.fee) + '</b></div></div>' +
        '<div style="display:flex;gap:9px;margin-top:12px">' +
          '<button class="btn block ghost" data-act="contact">联系买家</button>' +
          '<button class="btn block ghost" data-act="soon" data-val="演示版：异常上报（联系站长）">异常上报</button></div>' +
        btn +
        (a.stage === 2 ? '<div class="note" style="margin-top:12px"><span class="tape"></span><div class="nt"><h3 class="h-serif">这一单完成了</h3><span>+' + money(a.fee) + '</span></div>' +
          '<p>收入已计入今日，可在「收入」里查看明细并提现。</p></div>' : '') +
        '<div style="height:16px"></div>' +
      '</div>' + dTab('task');
  };

  function dOrders() {
    var t = state.deliver.tab;
    return state.orders.filter(function (o) {
      var active = o.status === '待取货' || o.status === '配送中' || o.status === '备货中';
      if (t === 'active' ? !active : active) return false;
      return orderMatches(o, state.deliver.q, state.deliver.range);
    });
  }
  function dCard(o) {
    var m = statusMeta(o.status);
    var count = o.items.reduce(function (a, i) { return a + i.qty; }, 0);
    return '<div class="dord" data-act="dOrder" data-val="' + o.no + '">' +
      '<div class="dt"><span class="no">' + o.no + '</span><span class="pill st ' + m[0] + '">' + m[1] + '</span><span class="tm">' + o.created + '</span></div>' +
      '<div class="da">' + o.addr + '</div>' +
      '<div class="dc">' + (o.contact || '—') + ' · ' + maskPhone(o.phone) + '</div>' +
      '<div class="df"><span>' + count + ' 件 · ' + o.pay + '</span><b>' + money(orderTotal(o)) + '</b></div></div>';
  }
  function dListHtml() {
    var list = dOrders();
    return list.length ? list.map(dCard).join('') : emptyState('没有符合条件的订单', '换个关键词或时间范围试试');
  }

  VIEWS.delivery.orders = function () {
    var list = dOrders();
    var ranges = [['all', '全部时间'], ['today', '今天'], ['3d', '近 3 天']];
    return statusbar() +
      '<div class="scroll" style="padding-top:4px">' +
        '<div class="row-between"><div><div class="small muted" style="letter-spacing:.14em">ORDERS</div>' +
          '<h1 class="h-serif" style="font-size:22px;margin-top:4px">我的订单</h1></div>' +
          '<span class="small muted" id="dOrderCount">' + list.length + ' 单</span></div>' +
        '<div class="search" style="margin-top:12px">' +
          '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="10.5" cy="10.5" r="6.5"/><path d="M15.5 15.5L21 21"/></svg>' +
          '<input data-input="dorder" value="' + esc(state.deliver.q) + '" placeholder="订单号 / 手机号 / 收货人 / 地址">' +
        '</div>' +
        '<div class="chips" style="margin-top:10px">' + ranges.map(function (r) {
          return '<button class="chip ' + (state.deliver.range === r[0] ? 'on' : '') + '" data-act="dRange" data-val="' + r[0] + '">' + r[1] + '</button>';
        }).join('') + '</div>' +
        '<div class="seg" style="margin-top:14px">' +
          '<button class="' + (state.deliver.tab === 'active' ? 'on' : '') + '" data-act="dTab" data-val="active">正在配送</button>' +
          '<button class="' + (state.deliver.tab === 'history' ? 'on' : '') + '" data-act="dTab" data-val="history">历史订单</button>' +
        '</div>' +
        '<div id="dOrderList">' + dListHtml() + '</div>' +
        '<div style="height:12px"></div>' +
      '</div>' + dTab('orders');
  };

  VIEWS.delivery.income = function () {
    var d = state.deliver;
    var rows = d.records.map(function (r) {
      return '<div class="row"><div><b>' + r.no + '</b><small>' + r.t + ' · 北苑仓 → 宿舍楼</small></div><span class="rt" style="color:var(--green);font-weight:600">+' + money(r.fee) + '</span></div>';
    }).join('');
    return statusbar() +
      '<div class="scroll" style="padding-top:6px">' +
        '<h1 class="h-serif" style="font-size:22px;padding:4px 0 4px">今日收入</h1>' +
        '<p class="small muted">11 月 18 日 · 已送达 ' + d.done + ' 单</p>' +
        '<div class="card" style="margin-top:14px;background:#FFF3D6;transform:rotate(-.5deg)">' +
          '<div class="co-h">可提现</div>' +
          '<div style="font-size:32px;font-weight:600;letter-spacing:-.02em">' + money(d.income) + '</div>' +
          '<div class="small muted" style="margin-top:6px">含基础配送费 + 高峰补贴</div>' +
          '<button class="btn primary block" style="margin-top:12px" data-act="withdraw">提现到微信</button></div>' +
        '<div class="card" style="margin-top:12px"><div class="co-h">今日明细</div>' + rows + '</div>' +
        '<div class="note" style="margin-top:14px"><span class="tape"></span><div class="nt"><h3 class="h-serif">本周小目标</h3><span>还差 3 单</span></div>' +
          '<p>再送 3 单就能拿到 60 元全勤奖，今晚还有 2 小时。</p><div class="row"><span class="mini">已送 27 单</span><span class="mini">目标 30 单</span></div></div>' +
        '<div style="height:16px"></div>' +
      '</div>' + dTab('income');
  };

  window.DLIST = function () { return { html: dListHtml(), count: dOrders().length }; };

  VIEWS.delivery.me = function () {
    var d = state.deliver;
    return statusbar() +
      '<div class="scroll" style="padding-top:6px">' +
        '<div class="me-head"><div class="av">李</div><div><b>李海</b><small>北苑站 · 骑手 0231 · 铜牌</small></div></div>' +
        '<div class="me-stats"><div><b>' + d.done + '</b><span>今日单量</span></div><div><b>4.98</b><span>评分</span></div><div><b>27</b><span>本周单量</span></div></div>' +
        '<div class="card" style="margin-top:12px;padding:4px 14px">' +
          '<div class="row"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 12h4l2-3 4 6 2-3h4"/></svg></span><div><b>接单状态</b><small>' + (d.online ? '在线 · 正在听单' : '已收工') + '</small></div>' +
            '<span class="tg ' + (d.online ? 'on' : '') + '" data-act="dOnline" style="margin-left:auto"></span></div>' +
          '<div class="row" data-act="soon" data-val="演示版：站点与排班"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="4" y="5" width="16" height="15" rx="2"/><path d="M8 3v4M16 3v4M4 10h16"/></svg></span><div><b>排班与站点</b><small>北苑站 · 晚班 18:00–24:00</small></div><span class="rt">→</span></div>' +
          '<div class="row" data-act="soon" data-val="演示版：装备与培训"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M6 8h12v8H6z"/><path d="M9 8V6h6v2"/></svg></span><div><b>装备与培训</b><small>保温箱、头盔、校园路线</small></div><span class="rt">→</span></div>' +
          '<div class="row" data-act="soon" data-val="演示版：客服与申诉"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 5h16v11H8l-4 4z"/></svg></span><div><b>客服与申诉</b><small>超时、差评、异常单</small></div><span class="rt">→</span></div>' +
        '</div>' +
        '<div style="height:16px"></div>' +
      '</div>' + dTab('me');
  };
})();
