package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"
)

type handlerStubPaymentClient struct {
	lastReq *gen.DyCreateOrderRequest
}

func (s *handlerStubPaymentClient) CreateOrder(ctx context.Context, in *gen.DyCreateOrderRequest, opts ...grpc.CallOption) (*gen.DyOrder, error) {
	s.lastReq = in
	return &gen.DyOrder{Id: "order-1", Amount: "120", Currency: &wrapperspb.StringValue{Value: "golds"}}, nil
}

func (s *handlerStubPaymentClient) CreateTransactionWithAccount(context.Context, *gen.DyCreateTransactionWithAccountRequest, ...grpc.CallOption) (*gen.DyTransaction, error) {
	panic("unused")
}

func (s *handlerStubPaymentClient) CreateTransaction(context.Context, *gen.DyCreateTransactionRequest, ...grpc.CallOption) (*gen.DyTransaction, error) {
	panic("unused")
}

func (s *handlerStubPaymentClient) CancelOrder(context.Context, *gen.DyCancelOrderRequest, ...grpc.CallOption) (*gen.DyOrder, error) {
	panic("unused")
}

func (s *handlerStubPaymentClient) RefundOrder(context.Context, *gen.DyRefundOrderRequest, ...grpc.CallOption) (*gen.DyRefundOrderResponse, error) {
	panic("unused")
}

func (s *handlerStubPaymentClient) Transfer(context.Context, *gen.DyTransferRequest, ...grpc.CallOption) (*gen.DyTransaction, error) {
	panic("unused")
}

func (s *handlerStubPaymentClient) GetWalletFund(context.Context, *gen.DyGetWalletFundRequest, ...grpc.CallOption) (*gen.DyWalletFund, error) {
	panic("unused")
}

func (s *handlerStubPaymentClient) RegisterAppSubscriptionDefinition(context.Context, *gen.DyRegisterAppSubscriptionDefinitionRequest, ...grpc.CallOption) (*gen.DyRegisterAppSubscriptionDefinitionResponse, error) {
	panic("unused")
}

func purchaseEnabledConfig() *config.Config {
	return &config.Config{
		Wallet: config.WalletConfig{Target: "wallet:9090"},
		Quota: config.QuotaConfig{Purchase: config.QuotaPurchaseConfig{
			Products: []config.QuotaProductConfig{
				{Identifier: "dysonfs.quota.10gb", DisplayName: "10 GB Extra Quota", Description: "One-time extra storage", QuotaMB: 10240, Price: "120"},
			},
		}},
	}
}

func newPurchaseHandlerRouter(t *testing.T, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t, &database.QuotaRecord{})
	quota := service.NewQuotaService(&database.DB{DB: db})
	if cfg != nil {
		quota.SetPurchaseConfig(cfg.Quota.Purchase)
		quota.SetWalletConfig(cfg.Wallet)
		if cfg.Wallet.Target != "" {
			quota.SetPaymentClient(&handlerStubPaymentClient{})
		}
	}
	r := gin.New()
	r.Use(testAuthMiddleware(uuid.New()))
	RegisterRoutes(r, cfg, service.NewFileService(&database.DB{DB: db}, nil), nil, service.NewTaskService(&database.DB{DB: db}), quota, nil, nil)
	return r
}

func TestListQuotaPurchaseProducts(t *testing.T) {
	r := newPurchaseHandlerRouter(t, purchaseEnabledConfig())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/billing/quota/products", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var products []service.QuotaProduct
	if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}
	if products[0].ProductIdentifier != "dysonfs.quota.10gb" || products[0].Currency != "golds" {
		t.Errorf("product = %+v, want dysonfs.quota.10gb / golds", products[0])
	}
}

func TestCreateQuotaPurchaseOrder(t *testing.T) {
	r := newPurchaseHandlerRouter(t, purchaseEnabledConfig())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/billing/quota/purchase", strings.NewReader(`{"product_identifier":"dysonfs.quota.10gb"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["order_id"] != "order-1" || resp["amount"] != "120" || resp["currency"] != "golds" {
		t.Errorf("response = %+v, want order_id/amount/currency", resp)
	}
}

func TestCreateQuotaPurchaseUnknownProduct(t *testing.T) {
	r := newPurchaseHandlerRouter(t, purchaseEnabledConfig())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/billing/quota/purchase", strings.NewReader(`{"product_identifier":"dysonfs.quota.nope"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestQuotaPurchaseRoutesMissingWhenDisabled(t *testing.T) {
	r := newPurchaseHandlerRouter(t, &config.Config{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/billing/quota/products", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET products status = %d, want 404", w.Code)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/billing/quota/purchase", strings.NewReader(`{"product_identifier":"dysonfs.quota.10gb"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST purchase status = %d, want 404", w.Code)
	}
}
