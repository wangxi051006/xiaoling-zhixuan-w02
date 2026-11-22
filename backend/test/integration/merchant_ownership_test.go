//go:build integration

package integration

import (
	"fmt"
	"testing"

	"campus.local/xiaoling/internal/identity"
)

func TestMerchantOwnershipAtCheckout(t *testing.T) {
	f := setup(t)
	legacyQuote := f.quote("buyer", "ASAP", "COD")
	f.exec(`INSERT INTO merchants(id, station_id, name) VALUES ('shop-a','s','Shop A'), ('shop-b','s','Shop B');
		INSERT INTO products(id, station_id, merchant_id, name, on_sale) VALUES ('product-b','s','shop-b','Shop B product',true);
		INSERT INTO inventory(id,product_id,on_hand) VALUES ('inventory-b','product-b',10);
		INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen) VALUES ('sku-b','product-b','single',1,100)`)
	f.exec(`UPDATE products SET merchant_id='shop-a' WHERE id=(SELECT product_id FROM skus WHERE id=$1)`, f.sku)

	mixed := fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1},{"sku_id":"sku-b","qty":1}],"address_id":"a","delivery_mode":"ASAP","payment_method":"COD"}`, f.sku)
	if res := f.call("buyer", "POST", "/checkout/quote", mixed, 400); res["code"] != "MIXED_MERCHANTS" {
		t.Fatalf("mixed-merchant quote: %v", res)
	}
	if res := f.call("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, legacyQuote), 409); res["code"] != "QUOTE_CHANGED" {
		t.Fatalf("quote with changed merchant: %v", res)
	}
	id := f.order("buyer", "ASAP", "COD")
	if f.n(`SELECT count(*) FROM orders o JOIN quotes q ON q.id=o.quote_id
		WHERE o.id=$1 AND o.merchant_id='shop-a' AND q.merchant_id=o.merchant_id`, id) != 1 {
		t.Fatal("order did not retain the merchant from its quote")
	}
	f.exec(`UPDATE products SET merchant_id='shop-b' WHERE id=(SELECT product_id FROM skus WHERE id=$1)`, f.sku)
	if f.n(`SELECT count(*) FROM orders WHERE id=$1 AND merchant_id='shop-a'`, id) != 1 {
		t.Fatal("historical order moved with the product")
	}
}

func TestMerchantOrderScope(t *testing.T) {
	f := setup(t)
	legacy := f.order("buyer", "ASAP", "COD")
	f.exec(`INSERT INTO merchants(id, station_id, name) VALUES ('shop-a','s','Shop A'), ('shop-b','s','Shop B');
		INSERT INTO accounts(id,station_id,issuer,subject) VALUES ('staff-a','s','synthetic','staff-a');
		INSERT INTO role_bindings(account_id,role) VALUES ('staff-a','MERCHANT');
		INSERT INTO merchant_staff(account_id,station_id,merchant_id) VALUES ('staff-a','s','shop-a')`)
	token, session, err := identity.NewSessions(f.pool, f.clock).Issue(f.ctx, "staff-a", identity.MerchantEntry, f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.credentials["staff-a"] = credential{token, session.CSRFToken, "https://merchant.synthetic.test"}
	f.exec(`UPDATE products SET merchant_id='shop-a'`)
	owned := f.order("buyer", "ASAP", "COD")
	f.exec(`UPDATE products SET merchant_id='shop-b'`)
	foreign := f.order("buyer", "ASAP", "COD")
	f.exec(`INSERT INTO products(id,station_id,merchant_id,name) VALUES ('own-product','s','shop-a','Own product');
		INSERT INTO inventory(id,product_id) VALUES ('own-inventory','own-product')`)

	items := f.call("staff-a", "GET", "/merchant/orders", "", 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != owned {
		t.Fatalf("unexpected merchant orders: %v", items)
	}
	products := f.call("staff-a", "GET", "/merchant/products", "", 200)["items"].([]any)
	if len(products) != 1 || products[0].(map[string]any)["id"] != "own-product" {
		t.Fatalf("unexpected merchant products: %v", products)
	}
	for _, id := range []string{legacy, foreign} {
		f.call("staff-a", "GET", "/merchant/orders/"+id, "", 404)
		f.call("staff-a", "POST", "/merchant/order-batches/accept", fmt.Sprintf(`{"ids":[%q,%q]}`, owned, id), 404)
	}
	if f.n(`SELECT count(*) FROM orders WHERE id=$1 AND merchant_accepted_at IS NOT NULL`, owned) != 0 {
		t.Fatal("partial merchant batch committed")
	}
	f.call("staff-a", "POST", "/merchant/order-batches/accept", fmt.Sprintf(`{"ids":[%q]}`, owned), 200)
	f.call("staff-a", "POST", "/merchant/order-batches/ready", fmt.Sprintf(`{"ids":[%q]}`, owned), 200)
	var task string
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM delivery_tasks WHERE order_id=$1`, owned).Scan(&task); err != nil {
		t.Fatal(err)
	}
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	f.call("r1", "POST", "/rider/tasks/"+task+"/pickup", "", 200)
	f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
	foreignTask := f.pickup(foreign)
	f.call("r1", "POST", "/rider/tasks/"+foreignTask+"/deliver", `{"cod_received":true}`, 200)
	payouts := f.call("staff-a", "GET", "/merchant/settlements", "", 200)["items"].([]any)
	if len(payouts) != 1 || payouts[0].(map[string]any)["order_id"] != owned {
		t.Fatalf("unexpected merchant settlements: %v", payouts)
	}
	f.call("staff-a", "GET", "/admin/orders", "", 404)
	f.exec(`DELETE FROM merchant_staff WHERE account_id='staff-a'`)
	f.call("staff-a", "GET", "/merchant/orders", "", 404)
}
