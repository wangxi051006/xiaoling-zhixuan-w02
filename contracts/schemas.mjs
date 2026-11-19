export function schemas() {
  const ref = name => ({ $ref: `#/components/schemas/${name}` });
  const obj = (properties, optional = []) => ({ type: 'object', additionalProperties: false, properties, required: Object.keys(properties).filter(k => !optional.includes(k)) });
  const arr = (items, maxItems = 100) => ({ type: 'array', items, maxItems });
  const str = (maxLength = 200) => ({ type: 'string', minLength: 1, maxLength });
  const int = (minimum = 0, maximum = Number.MAX_SAFE_INTEGER) => ({ type: 'integer', minimum, maximum });
  const id = str(128), text = str(), date = { type: 'string', format: 'date' }, time = { type: 'string', format: 'date-time' }, bool = { type: 'boolean' }, version = int(1), qty = int(1, 999), fen = int(), signedFen = int(-Number.MAX_SAFE_INTEGER), reason = { type: 'string', pattern: '^[A-Z0-9_]{1,64}$' };
  const phone = { type: 'string', pattern: '^\\+?[0-9]{7,15}$' };
  const email = { type: 'string', maxLength: 254, pattern: '^[^@\\s]{1,64}@[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?([.][a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$' };
  const address = { building_id: id, room: str(40), contact_name: str(60), contact_phone: phone };
  const sku = { label: str(60), units_per_pack: { type: 'integer', enum: [1, 3] }, unit_price_fen: fen };
  const product = { name: text, description: { type: 'string', maxLength: 2000 } };
  const slot = { starts_at: time, ends_at: time, dispatch_at: time, accepting: bool };
  const line = { sku_id: id, qty };
  const pricedLine = { ...line, name: text, spec: str(60), unit_price_fen: fen, line_total_fen: fen };
  const orderInput = { lines: { ...arr(ref('OrderLineInput'), 50), minItems: 1 }, address_id: id, delivery_mode: ref('DeliveryMode'), slot_id: id, payment_method: ref('PaymentMethod') };
  const order = { id, delivery_mode: ref('DeliveryMode'), slot: ref('Slot'), status: ref('OrderStatus'), stock_status: ref('StockStatus'), settlement_status: ref('SettlementStatus'), payment_method: ref('PaymentMethod'), total_fen: fen, lines: arr(ref('PricedLine'), 50), created_at: time };
  const task = { id, building_name: text, item_count: qty, fee_fen: fen, slot: ref('Slot'), order_status: ref('OrderStatus') };
  const s = {
    ApiError: obj({ code: ref('ErrorCode'), message: str(500), request_id: id }),
    OTPRequest: obj({ phone }), OTPChallenge: obj({ challenge_id: id, expires_at: time }), OTPVerify: obj({ challenge_id: id, code: { type: 'string', pattern: '^[0-9]{6}$' } }),
    EmailCodeRequest: obj({ email, purpose: { type: 'string', enum: ['REGISTER', 'LOGIN'] } }),
    EmailBindRequest: obj({ email }), EmailCodeVerify: obj({ code: { type: 'string', pattern: '^[0-9]{6}$' } }),
    EmailChallenge: obj({ challenge_id: id, expires_at: time, resend_after_seconds: int(1, 600) }),
    RiderProvision: obj({ issuer: str(255), subject: str(255), display_name: str(60), reason_code: reason }),
    Me: obj({ id, station_id: id, roles: arr(ref('Role'), 5), csrf_token: id, email: { oneOf: [email, { type: 'null' }] } }),
    AddressInput: obj(address), AddressUpdate: obj({ ...address, expected_version: version }, Object.keys(address)), Address: obj({ id, ...address, building_name: text, version }),
    Rider: obj({ id, display_name: text, enabled: bool, available: bool, version }), RiderUpdate: obj({ enabled: bool, expected_version: version, reason_code: reason }),
    SKUInput: obj(sku), SKU: obj({ id, ...sku }),
    Product: obj({ id, ...product, skus: arr(ref('SKU'), 8) }),
    ProductInput: obj({ ...product, skus: { ...arr(ref('SKUInput'), 8), minItems: 1 }, on_sale: bool, reorder_point: int() }),
    ProductUpdate: obj({ ...product, on_sale: bool, reorder_point: int(), expected_version: version, skus: arr(ref('SKU'), 8) }, [...Object.keys(product), 'on_sale', 'reorder_point', 'skus']),
    AdminProduct: obj({ id, ...product, skus: arr(ref('SKU'), 8), on_sale: bool, version, inventory: ref('Inventory'), promotion: { oneOf: [ref('Promotion'), { type: 'null' }] } }),
    PromotionTier: obj({ min_qty: qty, discount_bps: int(0, 10000) }),
    Promotion: obj({ starts_at: time, ends_at: time, tiers: arr(ref('PromotionTier'), 20) }),
    PromotionUpdate: obj({ expected_version: version, promotion: { oneOf: [ref('Promotion'), { type: 'null' }] } }),
    Inventory: obj({ id, on_hand: int(), reserved: int(), reorder_point: int(), version }),
    InventoryAdjustment: obj({ inventory_id: id, delta: int(-999999, 999999), expected_version: version, reason_code: reason }),
    Slot: obj({ id, ...slot }), AdminSlot: obj({ id, ...slot, capacity: int(), used: int(), version, updated_at: time }),
    SlotUpdate: obj({ expected_version: version, capacity: int(), accepting: bool }),
    SlotBatch: obj({ local_date: date, opens_at: { type: 'string', pattern: '^([01][0-9]|2[0-3]):[0-5][0-9]$' }, closes_at: { type: 'string', pattern: '^([01][0-9]|2[0-3]):[0-5][0-9]$' }, lead_minutes: int(0, 120), capacity: int(1, 9999) }),
    PaymentOption: obj({ code: ref('PaymentMethod'), enabled: bool, label: str(80) }), PaymentMethods: obj({ methods: arr(ref('PaymentOption'), 3) }),
    Wallet: obj({ balance_fen: fen, currency: { const: 'CNY', type: 'string' } }),
    CODReceipt: obj({ id, order_id: id, rider_id: id, amount_fen: fen, collected_at: time, remittance_status: ref('RemittanceStatus') }),
    CODBatchInput: obj({ rider_id: id, local_date: date, receipt_ids: { ...arr(id), minItems: 1, uniqueItems: true } }),
    CODVerify: obj({ actual_fen: fen, reason_code: reason }),
    CODBatch: obj({ id, rider_id: id, local_date: date, expected_fen: fen, actual_fen: { oneOf: [fen, {type:'null'}] }, difference_fen: { oneOf: [signedFen, {type:'null'}] }, status: ref('RemittanceStatus') }),
    Refund: obj({ id, order_id: id, amount_fen: fen, payment_method: ref('PaymentMethod'), status: ref('SettlementStatus'), requested_at: time }),
    Reason: obj({ reason_code: reason }), PayoutInput: obj({ receipt_reference: id }),
    OrderLineInput: obj(line), PricedLine: obj(pricedLine),
    QuoteInput: { ...obj(orderInput, ['slot_id']), if: { properties: { delivery_mode: { const: 'SCHEDULED' } } }, then: { required: ['slot_id'] }, else: { not: { required: ['slot_id'] } } },
    Quote: obj({ quote_id: id, valid_until: time, slot_id: id, lines: arr(ref('PricedLine'), 50), total_fen: fen, payment_method: ref('PaymentMethod') }),
    OrderInput: obj({ quote_id: id, note: str(200) }, ['note']), Order: obj(order),
    AdminOrder: obj({ ...order, address: ref('OrderAddress'), rider_id: id, overdue: bool }, ['rider_id']),
    OrderAddress: obj({ ...address, building_name: text }),
    PrepareInput: obj({ sorted: { type: 'boolean', const: true } }),
    DamagedStock: obj({ inventory_id: id, damaged_units: int(1, 99999) }),
    SellableReturn: obj({ inventory_id: id, sellable_units: int(1, 99999) }),
    RestockInput: obj({ reason_code: reason, damaged: arr(ref('DamagedStock'), 50) }),
    ReturnInspect: obj({ reason_code: reason, sellable: arr(ref('SellableReturn'), 50) }),
    Dashboard: obj({ local_date: date, order_count: int(), delivered_count: int(), net_collected_fen: int(-Number.MAX_SAFE_INTEGER), overdue_count: int() }),
    Availability: obj({ available: bool }), Acknowledgment: obj({ accepted: { type: 'boolean', const: true } }),
    LobbyTask: obj(task), RiderTask: obj({ ...task, address: ref('OrderAddress') }, ['address']),
    DeliverInput: obj({ cod_received: bool }, ['cod_received']),
    Earning: obj({ id, task_id: id, amount_fen: fen, recorded_at: time }),
    Message: obj({ id, kind: str(64), task_id: id, created_at: time, read_at: time }, ['read_at']),
    MarkRead: obj({ ids: { ...arr(id), minItems: 1, uniqueItems: true } }),
  };
  for (const name of ['Address', 'Rider', 'Product', 'AdminProduct', 'Slot', 'AdminSlot', 'CODReceipt', 'Refund', 'Order', 'AdminOrder', 'LobbyTask', 'Earning', 'Message']) {
    s[`${name}List`] = obj({ items: arr(ref(name)), next_cursor: { oneOf: [id, { type:'null' }] } });
  }
  return s;
}
