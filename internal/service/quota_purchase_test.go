package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
)

// stubPaymentClient records the last CreateOrder request and echoes the
// requested amount/currency back; all other DyPaymentServiceClient methods are
// unused by quota purchase.
type stubPaymentClient struct {
	lastReq *gen.DyCreateOrderRequest
}

func (s *stubPaymentClient) CreateOrder(ctx context.Context, in *gen.DyCreateOrderRequest, opts ...grpc.CallOption) (*gen.DyOrder, error) {
	s.lastReq = in
	return &gen.DyOrder{Id: "order-1", Amount: in.Amount, Currency: &wrapperspb.StringValue{Value: in.Currency}}, nil
}

func (s *stubPaymentClient) CreateTransactionWithAccount(context.Context, *gen.DyCreateTransactionWithAccountRequest, ...grpc.CallOption) (*gen.DyTransaction, error) {
	panic("unused")
}

func (s *stubPaymentClient) CreateTransaction(context.Context, *gen.DyCreateTransactionRequest, ...grpc.CallOption) (*gen.DyTransaction, error) {
	panic("unused")
}

func (s *stubPaymentClient) CancelOrder(context.Context, *gen.DyCancelOrderRequest, ...grpc.CallOption) (*gen.DyOrder, error) {
	panic("unused")
}

func (s *stubPaymentClient) RefundOrder(context.Context, *gen.DyRefundOrderRequest, ...grpc.CallOption) (*gen.DyRefundOrderResponse, error) {
	panic("unused")
}

func (s *stubPaymentClient) Transfer(context.Context, *gen.DyTransferRequest, ...grpc.CallOption) (*gen.DyTransaction, error) {
	panic("unused")
}

func (s *stubPaymentClient) GetWalletFund(context.Context, *gen.DyGetWalletFundRequest, ...grpc.CallOption) (*gen.DyWalletFund, error) {
	panic("unused")
}

func (s *stubPaymentClient) RegisterAppSubscriptionDefinition(context.Context, *gen.DyRegisterAppSubscriptionDefinitionRequest, ...grpc.CallOption) (*gen.DyRegisterAppSubscriptionDefinitionResponse, error) {
	panic("unused")
}

// defaultPurchaseConfig mirrors the viper defaults: currency defaults to golds,
// min 1 GB, max 100 GB for the tests.
func defaultPurchaseConfig() config.QuotaPurchaseConfig {
	return config.QuotaPurchaseConfig{PricePerGB: "0.05", MinGB: 1, MaxGB: 100}
}

func newPurchaseTestService(t *testing.T, purchase config.QuotaPurchaseConfig) (*QuotaService, *gorm.DB) {
	t.Helper()
	db := openTestDB(t, &database.QuotaRecord{})
	svc := NewQuotaService(&database.DB{DB: db})
	svc.SetPurchaseConfig(purchase)
	svc.SetWalletConfig(config.WalletConfig{Target: "wallet:9090"})
	return svc, db
}

func TestCreatePurchaseOrder(t *testing.T) {
	svc, _ := newPurchaseTestService(t, defaultPurchaseConfig())
	stub := &stubPaymentClient{}
	svc.SetPaymentClient(stub)

	accountID := uuid.New().String()
	order, err := svc.CreatePurchaseOrder(context.Background(), accountID, 10)
	if err != nil {
		t.Fatalf("CreatePurchaseOrder() error = %v", err)
	}
	if order.GetId() != "order-1" {
		t.Fatalf("order id = %q, want order-1", order.GetId())
	}
	if order.GetAmount() != "0.5" {
		t.Errorf("order amount = %q, want 0.5", order.GetAmount())
	}
	req := stub.lastReq
	if req == nil {
		t.Fatal("CreateOrder was not called")
	}
	if req.Currency != "golds" {
		t.Errorf("Currency = %q, want golds (default)", req.Currency)
	}
	if req.Amount != "0.5" {
		t.Errorf("Amount = %q, want 0.5", req.Amount)
	}
	if req.GetProductIdentifier() != "dysonfs.quota" {
		t.Errorf("ProductIdentifier = %q, want dysonfs.quota", req.GetProductIdentifier())
	}
	if req.PayeeWalletId != nil {
		t.Errorf("PayeeWalletId = %v, want nil (system payee)", *req.PayeeWalletId)
	}
	if req.AppIdentifier != nil {
		t.Errorf("AppIdentifier = %v, want nil", *req.AppIdentifier)
	}
	var meta map[string]any
	if err := json.Unmarshal(req.Meta, &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta["account_id"] != accountID {
		t.Errorf("meta account_id = %v, want %q", meta["account_id"], accountID)
	}
	if meta["quantity_gb"] != float64(10) {
		t.Errorf("meta quantity_gb = %v, want 10", meta["quantity_gb"])
	}
	if meta["quota_mb"] != float64(10*1024) {
		t.Errorf("meta quota_mb = %v, want %d", meta["quota_mb"], 10*1024)
	}
}

func TestCreatePurchaseOrderWholeGoldAmount(t *testing.T) {
	svc, _ := newPurchaseTestService(t, config.QuotaPurchaseConfig{PricePerGB: "120", MinGB: 1, MaxGB: 100})
	stub := &stubPaymentClient{}
	svc.SetPaymentClient(stub)

	if _, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), 3); err != nil {
		t.Fatalf("CreatePurchaseOrder() error = %v", err)
	}
	if stub.lastReq.Amount != "360" {
		t.Errorf("Amount = %q, want 360", stub.lastReq.Amount)
	}
}

func TestCreatePurchaseOrderCustomCurrency(t *testing.T) {
	svc, _ := newPurchaseTestService(t, config.QuotaPurchaseConfig{PricePerGB: "0.05", Currency: "credits", MinGB: 1, MaxGB: 100})
	stub := &stubPaymentClient{}
	svc.SetPaymentClient(stub)

	if _, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), 10); err != nil {
		t.Fatalf("CreatePurchaseOrder() error = %v", err)
	}
	if stub.lastReq.Currency != "credits" {
		t.Errorf("Currency = %q, want credits", stub.lastReq.Currency)
	}
}

func TestCreatePurchaseOrderNotConfigured(t *testing.T) {
	db := openTestDB(t, &database.QuotaRecord{})
	svc := NewQuotaService(&database.DB{DB: db})
	svc.SetPurchaseConfig(defaultPurchaseConfig())
	// No wallet target and no payment client injected.

	_, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), 10)
	if !errors.Is(err, ErrPurchaseNotConfigured) {
		t.Fatalf("error = %v, want ErrPurchaseNotConfigured", err)
	}
}

func TestCreatePurchaseOrderQuantityTooLow(t *testing.T) {
	svc, _ := newPurchaseTestService(t, defaultPurchaseConfig())
	svc.SetPaymentClient(&stubPaymentClient{})

	_, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), 0)
	if !errors.Is(err, ErrPurchaseQuantityTooLow) {
		t.Fatalf("quantity 0 error = %v, want ErrPurchaseQuantityTooLow", err)
	}

	strict, _ := newPurchaseTestService(t, config.QuotaPurchaseConfig{PricePerGB: "0.05", MinGB: 5, MaxGB: 100})
	strict.SetPaymentClient(&stubPaymentClient{})
	_, err = strict.CreatePurchaseOrder(context.Background(), uuid.New().String(), 3)
	if !errors.Is(err, ErrPurchaseQuantityTooLow) {
		t.Fatalf("quantity 3 with min 5 error = %v, want ErrPurchaseQuantityTooLow", err)
	}
}

func TestCreatePurchaseOrderQuantityTooHigh(t *testing.T) {
	svc, _ := newPurchaseTestService(t, defaultPurchaseConfig())
	svc.SetPaymentClient(&stubPaymentClient{})

	_, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), 101)
	if !errors.Is(err, ErrPurchaseQuantityTooHigh) {
		t.Fatalf("error = %v, want ErrPurchaseQuantityTooHigh", err)
	}
}

func TestCreatePurchaseOrderRespectsTotalCap(t *testing.T) {
	svc, db := newPurchaseTestService(t, config.QuotaPurchaseConfig{PricePerGB: "0.05", MinGB: 1, MaxGB: 100})
	svc.SetPaymentClient(&stubPaymentClient{})
	accountID := uuid.New()

	// Admin-assigned extra quota (no order id) counts toward the cap.
	if err := db.Create(&database.QuotaRecord{ID: database.NewID(), AccountID: accountID, Name: "admin grant", Description: "manual", Quota: 80 * 1024}).Error; err != nil {
		t.Fatalf("create admin record: %v", err)
	}

	// 80 + 21 > 100 → rejected.
	if _, err := svc.CreatePurchaseOrder(context.Background(), accountID.String(), 21); !errors.Is(err, ErrPurchaseQuantityTooHigh) {
		t.Fatalf("error = %v, want ErrPurchaseQuantityTooHigh", err)
	}
	// 80 + 20 = 100 → allowed (boundary).
	if _, err := svc.CreatePurchaseOrder(context.Background(), accountID.String(), 20); err != nil {
		t.Fatalf("CreatePurchaseOrder(20) error = %v", err)
	}
}

func TestCreatePurchaseOrderCapIgnoresExpiredQuota(t *testing.T) {
	svc, db := newPurchaseTestService(t, config.QuotaPurchaseConfig{PricePerGB: "0.05", MinGB: 1, MaxGB: 100})
	svc.SetPaymentClient(&stubPaymentClient{})
	accountID := uuid.New()

	past := time.Now().Add(-time.Hour)
	if err := db.Create(&database.QuotaRecord{ID: database.NewID(), AccountID: accountID, Name: "expired", Description: "expired", Quota: 80 * 1024, ExpiredAt: &past}).Error; err != nil {
		t.Fatalf("create expired record: %v", err)
	}

	// Expired quota does not count: the full 100 GB is purchasable.
	if _, err := svc.CreatePurchaseOrder(context.Background(), accountID.String(), 100); err != nil {
		t.Fatalf("CreatePurchaseOrder(100) error = %v", err)
	}
}

func TestPurchaseInfo(t *testing.T) {
	svc, _ := newPurchaseTestService(t, defaultPurchaseConfig())
	info := svc.PurchaseInfo()
	if info.PricePerGB != "0.05" || info.Currency != "golds" || info.MinGB != 1 || info.MaxGB != 100 {
		t.Errorf("info = %+v, want price 0.05 / golds / min 1 / max 100", info)
	}
}

func purchaseEventJSON(orderID string, status int, accountID uuid.UUID, meta map[string]any) string {
	metaJSON := "null"
	if meta != nil {
		b, _ := json.Marshal(meta)
		metaJSON = string(b)
	}
	return fmt.Sprintf(`{"event_id":"evt-%s","timestamp":"2026-01-01T00:00:00Z","event_type":"payment_orders","stream_name":"payment_events","order_id":%q,"wallet_id":"w-1","account_id":%q,"app_identifier":null,"product_identifier":"dysonfs.quota","status":%d,"meta":%s}`,
		orderID, orderID, accountID.String(), status, metaJSON)
}

func ownOrderMeta(accountID uuid.UUID, quantityGB int64) map[string]any {
	return map[string]any{
		"account_id":  accountID.String(),
		"quantity_gb": quantityGB,
		"quota_mb":    quantityGB * 1024,
	}
}

func TestHandlePaymentOrderEventGrants(t *testing.T) {
	svc, db := newPurchaseTestService(t, defaultPurchaseConfig())
	accountID := uuid.New()
	payload := purchaseEventJSON("order-1", 1, accountID, ownOrderMeta(accountID, 5))

	if err := svc.handlePaymentOrderEvent([]byte(payload)); err != nil {
		t.Fatalf("handlePaymentOrderEvent() error = %v", err)
	}
	var records []database.QuotaRecord
	if err := db.Find(&records).Error; err != nil {
		t.Fatalf("find records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.AccountID != accountID {
		t.Errorf("AccountID = %v, want %v", rec.AccountID, accountID)
	}
	if rec.Quota != 5*1024 {
		t.Errorf("Quota = %d, want %d", rec.Quota, 5*1024)
	}
	if rec.Name != "5 GB Extra Quota" {
		t.Errorf("Name = %q, want %q", rec.Name, "5 GB Extra Quota")
	}
	if rec.Description == "" {
		t.Error("Description is empty")
	}
	if rec.OrderID == nil || *rec.OrderID != "order-1" {
		t.Errorf("OrderID = %v, want order-1", rec.OrderID)
	}
	if rec.ExpiredAt != nil {
		t.Errorf("ExpiredAt = %v, want nil (permanent)", rec.ExpiredAt)
	}
}

func TestHandlePaymentOrderEventIdempotent(t *testing.T) {
	svc, db := newPurchaseTestService(t, defaultPurchaseConfig())
	accountID := uuid.New()
	payload := purchaseEventJSON("order-1", 1, accountID, ownOrderMeta(accountID, 5))

	if err := svc.handlePaymentOrderEvent([]byte(payload)); err != nil {
		t.Fatalf("first handle error = %v", err)
	}
	if err := svc.handlePaymentOrderEvent([]byte(payload)); err != nil {
		t.Fatalf("second handle error = %v", err)
	}
	var count int64
	if err := db.Model(&database.QuotaRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("records = %d, want 1 (redelivery must not double-grant)", count)
	}
}

func TestHandlePaymentOrderEventIgnoresForeignProduct(t *testing.T) {
	svc, db := newPurchaseTestService(t, defaultPurchaseConfig())
	accountID := uuid.New()
	// Sphere-style sponsor event on the shared subject: meta without the
	// DysonFS quota snapshot.
	payload := purchaseEventJSON("order-sponsor", 1, accountID, map[string]any{"post_id": "post-9"})

	if err := svc.handlePaymentOrderEvent([]byte(payload)); err != nil {
		t.Fatalf("handlePaymentOrderEvent() error = %v", err)
	}
	var count int64
	if err := db.Model(&database.QuotaRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("records = %d, want 0", count)
	}
}

func TestHandlePaymentOrderEventIgnoresUnpaid(t *testing.T) {
	svc, db := newPurchaseTestService(t, defaultPurchaseConfig())
	accountID := uuid.New()
	payload := purchaseEventJSON("order-1", 0, accountID, ownOrderMeta(accountID, 5))

	if err := svc.handlePaymentOrderEvent([]byte(payload)); err != nil {
		t.Fatalf("handlePaymentOrderEvent() error = %v", err)
	}
	var count int64
	if err := db.Model(&database.QuotaRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("records = %d, want 0", count)
	}
}
