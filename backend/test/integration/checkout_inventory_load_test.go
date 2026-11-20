//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func checkoutQuoteBody(sku, address, method string) string {
	return fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1}],"address_id":%q,"delivery_mode":"ASAP","payment_method":%q}`, sku, address, method)
}

func checkoutOrderBody(quote string) string {
	return fmt.Sprintf(`{"quote_id":%q}`, quote)
}

func assertMerchantInventory(f *fixture, onHand, reserved int64) {
	f.t.Helper()
	response := f.call("op", "GET", "/admin/products?limit=100", "", 200)
	for _, raw := range response["items"].([]any) {
		inventory := raw.(map[string]any)["inventory"].(map[string]any)
		if inventory["id"] != f.inventory {
			continue
		}
		gotOnHand := int64(inventory["on_hand"].(float64))
		gotReserved := int64(inventory["reserved"].(float64))
		f.t.Logf("merchant inventory: on_hand=%d reserved=%d sellable=%d", gotOnHand, gotReserved, gotOnHand-gotReserved)
		if gotOnHand != onHand || gotReserved != reserved {
			f.t.Fatalf("merchant inventory (%d, %d), want (%d, %d)", gotOnHand, gotReserved, onHand, reserved)
		}
		return
	}
	f.t.Fatal("inventory missing from the merchant's product list")
}

func TestCheckoutInventoryWith100Customers(t *testing.T) {
	const customers = 100
	cases := []struct {
		name       string
		method     string
		stock      int64
		sharedIP   bool
		wantOrders int64
		wantStock  int64
		wantRate   int64
	}{
		{"COD_full_stock_shared_IP", "COD", customers, true, 30, 0, 70},
		{"COD_full_stock_distinct_IPs", "COD", customers, false, 100, 0, 0},
		{"COD_limited_stock_distinct_IPs", "COD", 30, false, 30, 70, 0},
		{"Balance_limited_stock_distinct_IPs", "BALANCE", 30, false, 30, 70, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			f.exec(`UPDATE inventory SET on_hand=$1 WHERE id=$2`, tc.stock, f.inventory)
			f.exec(`UPDATE delivery_slots SET capacity=$1 WHERE id='asap'`, customers+10)
			f.exec(`INSERT INTO accounts(id,station_id,phone)
				SELECT 'load-'||n,'s','+86139000'||lpad(n::text,5,'0') FROM generate_series(1,100) n;
				INSERT INTO role_bindings(account_id,role) SELECT id,'CUSTOMER' FROM accounts WHERE id LIKE 'load-%';
				INSERT INTO addresses(id,account_id,building_id,room,contact_name,contact_phone)
				SELECT id,id,'b','101','Load test',phone FROM accounts WHERE id LIKE 'load-%';
				INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)
				SELECT station_id,id,10000,10000 FROM accounts WHERE id LIKE 'load-%';`)

			actors := make([]string, customers)
			quotes := make([]string, customers)
			for i := range actors {
				actor := fmt.Sprintf("load-%d", i+1)
				f.issue(actor)
				actors[i] = actor
				quote := f.call(actor, "POST", "/checkout/quote", checkoutQuoteBody(f.sku, actor, tc.method), 201)
				quotes[i] = quote["quote_id"].(string)
			}
			assertMerchantInventory(f, tc.stock, 0)

			start := make(chan struct{})
			responses := make(chan *httptest.ResponseRecorder, customers)
			var ready sync.WaitGroup
			ready.Add(customers)
			for i, actor := range actors {
				go func() {
					ready.Done()
					<-start
					source := "192.0.2.1:4000"
					if !tc.sharedIP {
						source = fmt.Sprintf("192.0.2.%d:4000", i+1)
					}
					responses <- f.requestFrom(f.ctx, actor, "POST", "/orders", checkoutOrderBody(quotes[i]), "stock-load", source)
				}()
			}
			ready.Wait()
			close(start)

			var created, outOfStock, rateLimited int64
			for range customers {
				response := <-responses
				switch response.Code {
				case 201:
					created++
				case 409:
					assertCheckoutError(t, response, 409, "OUT_OF_STOCK")
					outOfStock++
				case 429:
					assertCheckoutError(t, response, 429, "RATE_LIMITED")
					rateLimited++
				default:
					t.Errorf("unexpected response %d: %s", response.Code, response.Body)
				}
			}
			t.Logf("requests=%d created=%d out_of_stock=%d rate_limited=%d", customers, created, outOfStock, rateLimited)
			if created != tc.wantOrders || outOfStock != tc.wantStock || rateLimited != tc.wantRate {
				t.Fatalf("want %d created, %d out of stock, and %d rate limited", tc.wantOrders, tc.wantStock, tc.wantRate)
			}
			assertMerchantInventory(f, tc.stock, created)
			if orders := f.n(`SELECT count(*) FROM orders`); orders != created {
				t.Errorf("orders=%d, want %d", orders, created)
			}
			if orders := f.n(`SELECT count(DISTINCT actor_id) FROM orders`); orders != created {
				t.Errorf("orders from distinct customers=%d, want %d", orders, created)
			}
			if reservations := f.n(`SELECT count(*) FROM stock_reservations WHERE inventory_id=$1 AND state='RESERVED' AND units=1`, f.inventory); reservations != created {
				t.Errorf("one-unit reservations=%d, want %d", reservations, created)
			}
			if slots := f.n(`SELECT used FROM delivery_slots WHERE id='asap'`); slots != created {
				t.Errorf("used delivery slots=%d, want %d", slots, created)
			}
			wantState := "COD_UNCOLLECTED"
			if tc.method == "BALANCE" {
				wantState = "BALANCE_PAID"
			}
			if settlements := f.n(`SELECT count(*) FROM settlements WHERE state=$1`, wantState); settlements != created {
				t.Errorf("settlements in state %s=%d, want %d", wantState, settlements, created)
			}
		})
	}
}

func assertCheckoutError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != status || body.Code != code {
		t.Errorf("want HTTP %d %s, got %d %s", status, code, response.Code, response.Body)
	}
}

func TestCheckoutLastUnitPaymentAndRelease(t *testing.T) {
	t.Run("UnsubmittedQuoteDoesNotReserve", func(t *testing.T) {
		f := setup(t)
		f.exec(`UPDATE inventory SET on_hand=1 WHERE id=$1`, f.inventory)
		f.quote("buyer", "ASAP", "BALANCE")
		assertMerchantInventory(f, 1, 0)
		f.order("other", "ASAP", "BALANCE")
		assertMerchantInventory(f, 1, 1)
	})

	t.Run("InsufficientBalanceDoesNotReserve", func(t *testing.T) {
		f := setup(t)
		f.exec(`UPDATE inventory SET on_hand=1 WHERE id=$1`, f.inventory)
		f.exec(`UPDATE wallets SET opening_fen=0,balance_fen=0 WHERE actor_id='buyer'`)
		quote := f.quote("buyer", "ASAP", "BALANCE")
		assertCheckoutError(t, f.request("buyer", "POST", "/orders", checkoutOrderBody(quote), "unfunded"), 409, "INSUFFICIENT_BALANCE")
		assertMerchantInventory(f, 1, 0)
		if orders := f.n(`SELECT count(*) FROM orders`); orders != 0 {
			t.Fatalf("orders after failed payment=%d, want 0", orders)
		}
		f.order("other", "ASAP", "BALANCE")
		assertMerchantInventory(f, 1, 1)
	})

	t.Run("UncollectedCODReservesUntilCanceled", func(t *testing.T) {
		f := setup(t)
		f.exec(`UPDATE inventory SET on_hand=1 WHERE id=$1`, f.inventory)
		id := f.order("buyer", "ASAP", "COD")
		order := f.call("op", "GET", "/admin/orders/"+id, "", 200)
		if order["settlement_status"] != "COD_UNCOLLECTED" {
			t.Fatal(order)
		}
		assertMerchantInventory(f, 1, 1)
		quote := f.quote("other", "ASAP", "BALANCE")
		assertCheckoutError(t, f.request("other", "POST", "/orders", checkoutOrderBody(quote), "while-reserved"), 409, "OUT_OF_STOCK")

		f.clock.Set(f.clock.Now().Add(30 * time.Minute))
		if done, err := f.service.RunDue(f.ctx, 10); err != nil || done == 0 {
			t.Fatalf("processing overdue order: done=%d err=%v", done, err)
		}
		order = f.call("op", "GET", "/admin/orders/"+id, "", 200)
		if order["overdue"] != true || order["settlement_status"] != "COD_UNCOLLECTED" {
			t.Fatal(order)
		}
		assertMerchantInventory(f, 1, 1)
		f.exec(`UPDATE delivery_slots SET starts_at=$1,ends_at=$2,dispatch_at=$3 WHERE id='asap'`,
			f.clock.Now().Add(10*time.Minute), f.clock.Now().Add(25*time.Minute), f.clock.Now())
		quote = f.quote("other", "ASAP", "BALANCE")
		assertCheckoutError(t, f.request("other", "POST", "/orders", checkoutOrderBody(quote), "after-overdue"), 409, "OUT_OF_STOCK")
		f.call("op", "POST", "/admin/orders/"+id+"/cancel", `{"reason_code":"TEST_CANCEL"}`, 200)
		assertMerchantInventory(f, 1, 0)
		f.call("other", "POST", "/orders", checkoutOrderBody(quote), 201)
		assertMerchantInventory(f, 1, 1)
	})
}
