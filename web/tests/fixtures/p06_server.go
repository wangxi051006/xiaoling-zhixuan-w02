// Test-only real P01/P05 adapter. Stage under backend/cmd in a disposable copy.
// Never compile or deploy this program as the production API.
package main

import (
 "context"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "log/slog"
 "net"
 "net/http"
 "os"
 "strconv"
 "strings"
 "sync"
 "time"

 "campus.local/xiaoling/internal/bootstrap"
 "campus.local/xiaoling/internal/catalog"
 "campus.local/xiaoling/internal/fulfillment"
 "campus.local/xiaoling/internal/identity"
 "campus.local/xiaoling/internal/platform"
 "campus.local/xiaoling/internal/schedule"
 "campus.local/xiaoling/internal/settlement"
 "campus.local/xiaoling/migrations"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
)
type clock struct { mu sync.Mutex; at time.Time }
func(c *clock)Now()time.Time{c.mu.Lock();defer c.mu.Unlock();return c.at}
func(c *clock)advance(d time.Duration){c.mu.Lock();c.at=c.at.Add(d);c.mu.Unlock()}
func must(e error){if e!=nil{panic(e)}}
func main(){
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 dsn:=os.Getenv("TEST_DATABASE_URL");if !strings.Contains(dsn,"127.0.0.1:55497/"){panic("requires isolated P06 loopback PostgreSQL on 55497")}
 admin,e:=pgxpool.New(ctx,dsn);must(e);defer admin.Close()
 var raw[8]byte;_,e=rand.Read(raw[:]);must(e);schema:="p06_"+hex.EncodeToString(raw[:]);ident:=pgx.Identifier{schema}.Sanitize()
 _,e=admin.Exec(ctx,"CREATE SCHEMA "+ident);must(e)
 cfg,e:=pgxpool.ParseConfig(dsn);must(e);cfg.ConnConfig.RuntimeParams["search_path"]=schema;cfg.ConnConfig.RuntimeParams["timezone"]="UTC";cfg.MaxConns=10
 pool,e:=pgxpool.NewWithConfig(ctx,cfg);must(e)
 defer func(){pool.Close();_,e=admin.Exec(context.Background(),"DROP SCHEMA "+ident+" CASCADE");must(e)}()
 must(platform.Migrate(ctx,pool,migrations.SQL))
 c:=&clock{at:time.Date(2026,11,19,3,0,0,0,time.UTC)}
 user,rider,adm:=os.Getenv("P06_USER_ORIGIN"),os.Getenv("P06_RIDER_ORIGIN"),os.Getenv("P06_ADMIN_ORIGIN")
 var mu sync.Mutex;var app http.Handler;var svc *fulfillment.Service
 configure:=func(balance bool){
  policy:=settlement.PaymentPolicy{CODEnabled:true,BalanceEnabled:balance};directory:=identity.NewDirectory(pool)
  svc=&fulfillment.Service{Pool:pool,Clock:c,Identity:directory,Catalog:catalog.Store{Clock:c},Schedule:schedule.Store{},Settlement:settlement.Store{Policy:policy},RiderState:directory,Policy:policy,AcceptNewOrders:true,AllowDispatch:true}
  app,e=bootstrap.NewApplication(bootstrap.ApplicationOptions{Pool:pool,Clock:c,UserOrigin:user,RiderOrigin:rider,AdminOrigin:adm,Payments:policy,Orders:svc,Logger:slog.New(slog.DiscardHandler)});must(e)
 };configure(true)
 // Seed master data only; never update order/settlement/task state directly.
 _,e=pool.Exec(ctx,`INSERT INTO stations(id,name)VALUES('s','P06 合成测试站');INSERT INTO service_buildings(id,station_id,name)VALUES('b','s','测试一号楼');
 INSERT INTO accounts(id,station_id,issuer,subject,display_name)VALUES('r1','s','synthetic','r1','测试骑手一'),('r2','s','synthetic','r2','测试骑手二'),('op','s','synthetic','op','测试运营'),('fin','s','synthetic','fin','财务制单'),('fin2','s','synthetic','fin2','财务复点');
 INSERT INTO role_bindings(account_id,role)VALUES('r1','RIDER'),('r2','RIDER'),('op','OPERATOR'),('fin','FINANCE'),('fin2','FINANCE');INSERT INTO rider_profiles(account_id,available)VALUES('r1',false),('r2',true);`);must(e)
 for i:=1;i<=16;i++{id:="buyer"+strconv.Itoa(i);_,e=pool.Exec(ctx,`INSERT INTO accounts(id,station_id,phone)VALUES($1,'s',$2);`,id,fmt.Sprintf("+1555000%04d",i));must(e);_,e=pool.Exec(ctx,`INSERT INTO role_bindings(account_id,role)VALUES($1,'CUSTOMER');`,id);must(e);_,e=pool.Exec(ctx,`INSERT INTO addresses(id,account_id,building_id,room,contact_name,contact_phone)VALUES($1,$2,'b','测试房间','测试联系人',$3)`,"a"+strconv.Itoa(i),id,fmt.Sprintf("+1555000%04d",i));must(e);balance:=int64(10000);if i==4{balance=0};_,e=pool.Exec(ctx,`INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('s',$1,$2,$2)`,id,balance);must(e)}
 // Windows distinguish ASAP from future dispatch windows. Slots are master data.
 _,e=pool.Exec(ctx,`INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity)VALUES('asap','s',$1,$2,$3,100),('later','s',$4,$5,$6,100),('tomorrow','s',$7,$8,$9,100)`,c.Now().Add(10*time.Minute),c.Now().Add(25*time.Minute),c.Now().Add(-5*time.Minute),c.Now().Add(time.Hour),c.Now().Add(75*time.Minute),c.Now().Add(45*time.Minute),c.Now().Add(2*time.Hour),c.Now().Add(135*time.Minute),c.Now().Add(105*time.Minute));must(e)
 _,e=pool.Exec(ctx,`INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity)VALUES('next-day','s',$1,$2,$3,100)`,c.Now().Add(24*time.Hour),c.Now().Add(24*time.Hour+15*time.Minute),c.Now().Add(24*time.Hour-15*time.Minute));must(e)
 // Catalog types are discovered through the owning store, preserving migrations.
 var sku,pack,inventory,productID string
 must(platform.InTransaction(ctx,pool,func(tx pgx.Tx)error{p,err:= (catalog.Store{Clock:c}).CreateProduct(ctx,tx,"s",catalog.ProductInput{Name:"手账燕麦饼干",Description:"课间的一点甜 · 合成测试商品",OnSale:true,SKUs:[]catalog.SKUInput{{Label:"单袋",UnitsPerPack:1,UnitPriceFen:500},{Label:"三袋装",UnitsPerPack:3,UnitPriceFen:1350}}});if err!=nil{return err};productID=p.ID;inventory=p.Inventory.ID;for _,s:=range p.SKUs{if s.UnitsPerPack==1{sku=s.ID}else{pack=s.ID}};_,err=tx.Exec(ctx,`UPDATE inventory SET on_hand=200 WHERE id=$1`,inventory);return err}))
 sessions:=identity.NewSessions(pool,c)
 control:=http.NewServeMux();stop:=make(chan struct{})
 control.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){if r.Header.Get("Authorization")!="Bearer "+os.Getenv("P06_CONTROL_SECRET"){http.Error(w,"forbidden",403);return};mu.Lock();defer mu.Unlock();var in struct{Command,Actor,OrderID string;Minutes int;Enabled bool};if json.NewDecoder(r.Body).Decode(&in)!=nil{http.Error(w,"bad input",400);return};out:=map[string]any{};switch in.Command{
 case "info":out=map[string]any{"sku":sku,"pack":pack,"inventory":inventory,"product":productID,"now":c.Now(),"schema":schema,"migrations":"000-050"}
 case "session":entry,origin,mfa:=identity.UserEntry,user,time.Time{};if strings.HasPrefix(in.Actor,"r"){entry,origin,mfa=identity.RiderEntry,rider,c.Now()}else if in.Actor=="op"||strings.HasPrefix(in.Actor,"fin"){entry,origin,mfa=identity.AdminEntry,adm,c.Now()};token,s,err:=sessions.Issue(ctx,in.Actor,entry,mfa);if err!=nil{http.Error(w,"issue failed",500);return};out=map[string]any{"token":token,"origin":origin,"expires":s.ExpiresAt}
 case "advance":c.advance(time.Duration(in.Minutes)*time.Minute);n,err:=svc.RunDue(ctx,100);must(err);out["now"]=c.Now();out["worker_processed"]=n
 case "balance":configure(in.Enabled);out["enabled"]=in.Enabled
 case "expire":_,e=pool.Exec(ctx,`DELETE FROM sessions WHERE account_id=$1`,in.Actor);must(e);out["expired"]=true
 case "cod-ledger-row":
  var orderID, receiptID, batchID, receiptAmount, expected, status string
  var actual, difference *string
  must(pool.QueryRow(ctx, `SELECT r.order_id, m.receipt_id, b.id, r.amount_fen::text,
    b.expected_fen::text, v.actual_fen::text, v.difference_fen::text,
    CASE WHEN v.batch_id IS NULL THEN 'IN_BATCH' WHEN v.difference_fen=0 THEN 'VERIFIED' ELSE 'DISCREPANCY' END
    FROM cod_receipts r
    JOIN cod_batch_receipts m ON m.receipt_id=r.order_id
    JOIN cod_batches b ON b.id=m.batch_id
    LEFT JOIN cod_batch_verifications v ON v.batch_id=b.id
    WHERE r.order_id=$1`, in.OrderID).Scan(&orderID,&receiptID,&batchID,&receiptAmount,&expected,&actual,&difference,&status))
  out=map[string]any{"order_id":orderID,"receipt_id":receiptID,"batch_id":batchID,"receipt_amount_fen":receiptAmount,
   "expected_fen":expected,"actual_fen":actual,"difference_fen":difference,"status":status}
 case "snapshot":var n,earnings,receipts,paid,refunds,verified int;must(pool.QueryRow(ctx,`SELECT count(*) FROM orders`).Scan(&n));must(pool.QueryRow(ctx,`SELECT count(*) FROM rider_earnings`).Scan(&earnings));must(pool.QueryRow(ctx,`SELECT count(*) FROM cod_receipts`).Scan(&receipts));must(pool.QueryRow(ctx,`SELECT count(*) FROM cod_refund_payouts`).Scan(&paid));must(pool.QueryRow(ctx,`SELECT count(*) FROM wallet_entries WHERE kind='REFUND'`).Scan(&refunds));must(pool.QueryRow(ctx,`SELECT count(*) FROM cod_batch_verifications`).Scan(&verified));var wallet settlement.WalletReconciliation;var cod settlement.CODDayReconciliation;must(platform.InTransaction(ctx,pool,func(tx pgx.Tx)error{var err error;wallet,err=(settlement.Store{}).ReconcileWallets(ctx,tx,"s");if err!=nil{return err};cod,err=(settlement.Store{}).ReconcileCODDay(ctx,tx,"s","2026-11-19");return err}));batchRows:=[]map[string]any{};brows,berr:=pool.Query(ctx,`SELECT b.id,to_char(b.local_date,'YYYY-MM-DD'),b.expected_fen::text,coalesce(v.actual_fen::text,''),coalesce(v.difference_fen::text,''),CASE WHEN v.batch_id IS NULL THEN 'IN_BATCH' WHEN v.difference_fen=0 THEN 'VERIFIED' ELSE 'DISCREPANCY' END,coalesce(v.creator_id,''),coalesce(v.verifier_id,''),coalesce(v.reason_code,'') FROM cod_batches b LEFT JOIN cod_batch_verifications v ON v.batch_id=b.id ORDER BY b.id`);must(berr);for brows.Next(){var bid,ld,ex,ac,diff,st,cr,ve,rc string;must(brows.Scan(&bid,&ld,&ex,&ac,&diff,&st,&cr,&ve,&rc));batchRows=append(batchRows,map[string]any{"batch_id":bid,"local_date":ld,"expected_fen":ex,"actual_fen":ac,"difference_fen":diff,"expected_status":st,"creator_id":cr,"verifier_id":ve,"reason_code":rc})};brows.Close();out=map[string]any{"orders":n,"earnings":earnings,"receipts":receipts,"payouts":paid,"wallet_refunds":refunds,"verified_batches":verified,"wallet":wallet,"cod":cod,"batches":batchRows}
 case "shutdown":out["stopping"]=true;defer func(){close(stop)}()
 default:http.Error(w,"unknown",400);return};w.Header().Set("Content-Type","application/json");json.NewEncoder(w).Encode(out)
 })
 apiListener,e:=net.Listen("tcp","127.0.0.1:0");must(e);ctlListener,e:=net.Listen("tcp","127.0.0.1:0");must(e)
 apiServer:=&http.Server{Handler:http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){mu.Lock();defer mu.Unlock();app.ServeHTTP(w,r)}),ReadHeaderTimeout:5*time.Second}
 ctlServer:=&http.Server{Handler:control,ReadHeaderTimeout:5*time.Second}
 go apiServer.Serve(apiListener);go ctlServer.Serve(ctlListener)
 json.NewEncoder(os.Stdout).Encode(map[string]any{"api":"http://"+apiListener.Addr().String(),"control":"http://"+ctlListener.Addr().String(),"schema":schema})
 <-stop
 apiServer.Close();ctlServer.Close()
}
