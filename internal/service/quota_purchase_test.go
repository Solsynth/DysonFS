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

// stubPaymentClient records the last CreateOrder request and returns a canned
// order; all other DyPaymentServiceClient methods are unused by quota purchase.
type stubPaymentClient struct {
	lastReq *gen.DyCreateOrderRequest
}

func (s *stubPaymentClient) CreateOrder(ctx context.Context, in *gen.DyCreateOrderRequest, opts ...grpc.CallOption) (*gen.DyOrder, error) {
	s.lastReq = in
	return &gen.DyOrder{Id: "order-1", Amount: "120", Currency: &wrapperspb.StringValue{Value: "golds"}}, nil
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

const (
	testProductIdentifier  = "dysonfs.quota.10gb"
	testProductDisplayName = "10 GB Extra Quota"
	testProductDescription = "One-time extra storage, +10 GB for 30 days"
	testProductPrice       = "120"
	testProductQuotaMB     = int64(10240)
)

func testPurchaseProducts() []config.QuotaProductConfig {
	return []config.QuotaProductConfig{
		{Identifier: testProductIdentifier, DisplayName: testProductDisplayName, Description: testProductDescription, QuotaMB: testProductQuotaMB, Price: testProductPrice},
	}
}

func newPurchaseTestService(t *testing.T, products []config.QuotaProductConfig) (*QuotaService, *gorm.DB) {
	t.Helper()
	db := openTestDB(t, &database.QuotaRecord{})
	svc := NewQuotaService(&database.DB{DB: db})
	svc.SetPurchaseConfig(config.QuotaPurchaseConfig{Products: products})
	svc.SetWalletConfig(config.WalletConfig{Target: "wallet:9090"})
	return svc, db
}

func TestCreatePurchaseOrder(t *testing.T) {
	svc, _ := newPurchaseTestService(t, testPurchaseProducts())
	stub := &stubPaymentClient{}
	svc.SetPaymentClient(stub)

	accountID := uuid.New().String()
	order, err := svc.CreatePurchaseOrder(context.Background(), accountID, testProductIdentifier)
	if err != nil {
		t.Fatalf("CreatePurchaseOrder() error = %v", err)
	}
	if order.GetId() != "order-1" {
		t.Fatalf("order id = %q, want order-1", order.GetId())
	}
	req := stub.lastReq
	if req == nil {
		t.Fatal("CreateOrder was not called")
	}
	if req.Currency != "golds" {
		t.Errorf("Currency = %q, want golds", req.Currency)
	}
	if req.Amount != testProductPrice {
		t.Errorf("Amount = %q, want %q", req.Amount, testProductPrice)
	}
	if req.GetProductIdentifier() != testProductIdentifier {
		t.Errorf("ProductIdentifier = %q, want %q", req.GetProductIdentifier(), testProductIdentifier)
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
	if meta["quota_mb"] != float64(testProductQuotaMB) {
		t.Errorf("meta quota_mb = %v, want %d", meta["quota_mb"], testProductQuotaMB)
	}
	if meta["product_identifier"] != testProductIdentifier {
		t.Errorf("meta product_identifier = %v, want %q", meta["product_identifier"], testProductIdentifier)
	}
}

func TestCreatePurchaseOrderUnknownProduct(t *testing.T) {
	svc, _ := newPurchaseTestService(t, testPurchaseProducts())
	svc.SetPaymentClient(&stubPaymentClient{})

	_, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), "dysonfs.quota.nope")
	if !errors.Is(err, ErrPurchaseProductNotFound) {
		t.Fatalf("error = %v, want ErrPurchaseProductNotFound", err)
	}
}

func TestCreatePurchaseOrderNotConfigured(t *testing.T) {
	db := openTestDB(t, &database.QuotaRecord{})
	svc := NewQuotaService(&database.DB{DB: db})
	svc.SetPurchaseConfig(config.QuotaPurchaseConfig{Products: testPurchaseProducts()})
	// No wallet target and no payment client injected.

	_, err := svc.CreatePurchaseOrder(context.Background(), uuid.New().String(), testProductIdentifier)
	if !errors.Is(err, ErrPurchaseNotConfigured) {
		t.Fatalf("error = %v, want ErrPurchaseNotConfigured", err)
	}
}

func purchaseEventJSON(orderID, productID string, status int, accountID uuid.UUID, meta map[string]any) string {
	metaJSON := "null"
	if meta != nil {
		b, _ := json.Marshal(meta)
		metaJSON = string(b)
	}
	return fmt.Sprintf(`{"event_id":"evt-%s","timestamp":"2026-01-01T00:00:00Z","event_type":"payment_orders","stream_name":"payment_events","order_id":%q,"wallet_id":"w-1","account_id":%q,"app_identifier":null,"product_identifier":%q,"status":%d,"meta":%s}`,
		orderID, orderID, accountID.String(), productID, status, metaJSON)
}

func TestHandlePaymentOrderEventGrants(t *testing.T) {
	svc, db := newPurchaseTestService(t, testPurchaseProducts())
	accountID := uuid.New()
	payload := purchaseEventJSON("order-1", testProductIdentifier, 1, accountID, map[string]any{
		"account_id":         accountID.String(),
		"quota_mb":           testProductQuotaMB,
		"product_identifier": testProductIdentifier,
	})

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
	if rec.Quota != testProductQuotaMB {
		t.Errorf("Quota = %d, want %d", rec.Quota, testProductQuotaMB)
	}
	if rec.Name != testProductDisplayName {
		t.Errorf("Name = %q, want %q", rec.Name, testProductDisplayName)
	}
	if rec.Description != testProductDescription {
		t.Errorf("Description = %q, want %q", rec.Description, testProductDescription)
	}
	if rec.OrderID == nil || *rec.OrderID != "order-1" {
		t.Errorf("OrderID = %v, want order-1", rec.OrderID)
	}
	if rec.ExpiredAt != nil {
		t.Errorf("ExpiredAt = %v, want nil for permanent product", rec.ExpiredAt)
	}
}

func TestHandlePaymentOrderEventExpiring(t *testing.T) {
	products := []config.QuotaProductConfig{
		{Identifier: testProductIdentifier, DisplayName: testProductDisplayName, Description: testProductDescription, QuotaMB: testProductQuotaMB, Price: testProductPrice, ExpiresIn: 24 * time.Hour},
	}
	svc, db := newPurchaseTestService(t, products)
	accountID := uuid.New()
	payload := purchaseEventJSON("order-expiring", testProductIdentifier, 1, accountID, map[string]any{
		"account_id":         accountID.String(),
		"quota_mb":           testProductQuotaMB,
		"product_identifier": testProductIdentifier,
	})

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
	if rec.ExpiredAt == nil {
		t.Fatal("ExpiredAt = nil, want now+24h")
	}
	diff := rec.ExpiredAt.Sub(time.Now())
	if diff < 23*time.Hour || diff > 25*time.Hour {
		t.Errorf("ExpiredAt diff = %v, want ≈24h", diff)
	}
}

func TestHandlePaymentOrderEventIdempotent(t *testing.T) {
	svc, db := newPurchaseTestService(t, testPurchaseProducts())
	accountID := uuid.New()
	payload := purchaseEventJSON("order-1", testProductIdentifier, 1, accountID, map[string]any{
		"account_id":         accountID.String(),
		"quota_mb":           testProductQuotaMB,
		"product_identifier": testProductIdentifier,
	})

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
	svc, db := newPurchaseTestService(t, testPurchaseProducts())
	accountID := uuid.New()
	// Sphere-style sponsor event on the shared subject: different product and
	// meta without the DysonFS quota snapshot.
	payload := purchaseEventJSON("order-sponsor", "ads.sponsor", 1, accountID, map[string]any{"post_id": "post-9"})

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
	svc, db := newPurchaseTestService(t, testPurchaseProducts())
	accountID := uuid.New()
	payload := purchaseEventJSON("order-1", testProductIdentifier, 0, accountID, map[string]any{
		"account_id":         accountID.String(),
		"quota_mb":           testProductQuotaMB,
		"product_identifier": testProductIdentifier,
	})

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

func TestListPurchaseProducts(t *testing.T) {
	svc, _ := newPurchaseTestService(t, testPurchaseProducts())
	products := svc.ListPurchaseProducts()
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}
	p := products[0]
	if p.ProductIdentifier != testProductIdentifier || p.DisplayName != testProductDisplayName || p.QuotaMB != testProductQuotaMB || p.Price != testProductPrice {
		t.Errorf("product = %+v, want configured values", p)
	}
	if p.Currency != "golds" {
		t.Errorf("Currency = %q, want golds", p.Currency)
	}
	if p.ExpiresIn != "" {
		t.Errorf("ExpiresIn = %q, want empty for permanent product", p.ExpiresIn)
	}
}
