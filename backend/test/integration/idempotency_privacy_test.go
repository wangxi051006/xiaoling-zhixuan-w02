//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

func TestP05RiderCommandReplayRevalidatesContactAndNeverCachesPII(t *testing.T) {
	for _, terminal := range []string{"reassigned", "delivered", "returned"} {
		t.Run(terminal, func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", "COD")
			task := f.dispatch(id)
			claimPath := "/rider/tasks/" + task + "/claim"
			w := f.request("r1", "POST", claimPath, "", "old-claim")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"address"`) {
				t.Fatal("initial assigned task lacks permitted contact", w.Code, w.Body)
			}
			if terminal == "reassigned" {
				f.call("op", "POST", "/admin/orders/"+id+"/reclaim", `{"reason_code":"OPS_REVIEW"}`, 200)
				f.call("r2", "POST", claimPath, "", 200)
				replay := f.request("r1", "POST", claimPath, "", "old-claim")
				if replay.Code != 404 {
					t.Fatalf("old rider replay after reassignment status=%d body=%s", replay.Code, replay.Body)
				}
			} else {
				pickupPath := "/rider/tasks/" + task + "/pickup"
				w = f.request("r1", "POST", pickupPath, "", "old-pickup")
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
				if terminal == "delivered" {
					f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
				} else {
					returnPath := "/rider/tasks/" + task + "/return"
					w = f.request("r1", "POST", returnPath, `{"reason_code":"REFUSED"}`, "old-return")
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body)
					}
					f.call("op", "POST", "/admin/orders/"+id+"/return-inspect", fmt.Sprintf(`{"reason_code":"ACCEPT","sellable":[{"inventory_id":%q,"sellable_units":1}]}`, f.inventory), 200)
					w = f.request("r1", "POST", returnPath, `{"reason_code":"REFUSED"}`, "old-return")
					if w.Code != 200 || strings.Contains(w.Body.String(), `"address"`) {
						t.Fatalf("return replay disclosed completed contact: %d %s", w.Code, w.Body)
					}
				}
				for _, c := range []struct{ path, key string }{{claimPath, "old-claim"}, {pickupPath, "old-pickup"}} {
					replay := f.request("r1", "POST", c.path, "", c.key)
					if replay.Code != 200 || strings.Contains(replay.Body.String(), `"address"`) {
						t.Fatalf("completed replay disclosed contact: %d %s", replay.Code, replay.Body)
					}
				}
			}
			if f.n(`SELECT count(*) FROM idempotency_keys WHERE command LIKE 'rider:%' AND response_body ? 'address'`) != 0 {
				t.Fatal("rider idempotency cache persisted contact PII")
			}
			if f.n(`SELECT count(*) FROM order_events WHERE kind='CLAIMED'`) != map[string]int64{"reassigned": 2, "delivered": 1, "returned": 1}[terminal] {
				t.Fatal("replay duplicated command")
			}
		})
	}
}
