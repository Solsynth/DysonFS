package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	dyauth "src.solsynth.dev/sosys/go/pkg/auth"
	gen "src.solsynth.dev/sosys/go/proto"
)

// ErrPurchaseNotConfigured is returned when quota purchase is disabled or the
// payment client was never dialed (missing or empty [quota.purchase] target).
var ErrPurchaseNotConfigured = errors.New("quota purchase is not configured")

// ErrPurchaseProductNotFound is returned when the requested product identifier
// does not match any configured [quota.purchase.products] entry.
var ErrPurchaseProductNotFound = errors.New("quota purchase product not found")

// paymentOrderEvent mirrors the Wallet PaymentOrderEvent payload published on
// the shared payment_orders subject (snake_case JSON, as serialized by
// DysonNetwork's EventBus with SnakeCaseLower).
type paymentOrderEvent struct {
	EventID           string         `json:"event_id"`
	Timestamp         string         `json:"timestamp"`
	EventType         string         `json:"event_type"`
	StreamName        string         `json:"stream_name"`
	OrderID           string         `json:"order_id"`
	WalletID          string         `json:"wallet_id"`
	AccountID         string         `json:"account_id"`
	AppIdentifier     *string        `json:"app_identifier"`
	ProductIdentifier string         `json:"product_identifier"`
	Status            int            `json:"status"`
	Meta              map[string]any `json:"meta"`
}

// QuotaProduct is the read model served by GET /api/billing/quota/products.
type QuotaProduct struct {
	ProductIdentifier string `json:"product_identifier"`
	DisplayName       string `json:"display_name"`
	Description       string `json:"description"`
	QuotaMB           int64  `json:"quota_mb"`
	Price             string `json:"price"`
	Currency          string `json:"currency"`             // always "golds"
	ExpiresIn         string `json:"expires_in,omitempty"` // Go duration string, omitted when permanent
}

// NewPaymentClient dials the Wallet DyPaymentService gRPC endpoint using the
// [wallet] config, mirroring NewWorkspaceClient.
func NewPaymentClient(cfg config.WalletConfig) (gen.DyPaymentServiceClient, *grpc.ClientConn, error) {
	target, useTLS := dyauth.NormalizeAuthGRPCTarget(cfg.Target, cfg.UseTLS)
	if strings.TrimSpace(target) == "" {
		return nil, nil, errors.New("payment gRPC target is empty")
	}
	var transportCredentials credentials.TransportCredentials
	if useTLS {
		transportCredentials = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSSkipVerify})
	} else {
		transportCredentials = insecure.NewCredentials()
	}
	conn, err := grpc.Dial(target, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("dial payment service: %w", err)
	}
	return gen.NewDyPaymentServiceClient(conn), conn, nil
}

// SetPaymentClient injects the Wallet payment client (nil when the wallet
// target is unset).
func (s *QuotaService) SetPaymentClient(client gen.DyPaymentServiceClient) {
	s.paymentClient = client
}

// SetPurchaseConfig stores the [quota.purchase] product packs. Called
// unconditionally from app wiring.
func (s *QuotaService) SetPurchaseConfig(cfg config.QuotaPurchaseConfig) {
	s.purchaseCfg = cfg
}

// SetWalletConfig stores the [wallet] gRPC endpoint config. Called
// unconditionally from app wiring; a non-empty target enables the purchase
// flow.
func (s *QuotaService) SetWalletConfig(cfg config.WalletConfig) {
	s.walletCfg = cfg
}

// PurchaseEnabled reports whether the purchase flow is active, which is
// implied by a configured [wallet] gRPC target.
func (s *QuotaService) PurchaseEnabled() bool {
	return strings.TrimSpace(s.walletCfg.Target) != ""
}

// ListPurchaseProducts returns the configured products in config order.
func (s *QuotaService) ListPurchaseProducts() []QuotaProduct {
	products := make([]QuotaProduct, 0, len(s.purchaseCfg.Products))
	for _, p := range s.purchaseCfg.Products {
		item := QuotaProduct{
			ProductIdentifier: p.Identifier,
			DisplayName:       p.DisplayName,
			Description:       p.Description,
			QuotaMB:           p.QuotaMB,
			Price:             p.Price,
			Currency:          "golds",
		}
		if p.ExpiresIn > 0 {
			item.ExpiresIn = p.ExpiresIn.String()
		}
		products = append(products, item)
	}
	return products
}

// CreatePurchaseOrder creates a Wallet order (currency golds, system payee)
// for the configured product, mirroring the DysonNetwork.Sphere sponsor flow.
// The grant target account and quota amount are frozen into the order meta at
// creation time so later config edits cannot change what a paid order grants.
func (s *QuotaService) CreatePurchaseOrder(ctx context.Context, accountID, productIdentifier string) (*gen.DyOrder, error) {
	if !s.PurchaseEnabled() || s.paymentClient == nil {
		return nil, ErrPurchaseNotConfigured
	}
	product := s.product(productIdentifier)
	if product == nil {
		return nil, ErrPurchaseProductNotFound
	}
	meta, err := json.Marshal(map[string]any{
		"account_id":         accountID,
		"quota_mb":           product.QuotaMB,
		"product_identifier": product.Identifier,
	})
	if err != nil {
		return nil, fmt.Errorf("encode order meta: %w", err)
	}
	remarks := "DysonFS quota purchase: " + product.DisplayName
	order, err := s.paymentClient.CreateOrder(ctx, &gen.DyCreateOrderRequest{
		Currency:          "golds",
		Amount:            product.Price,
		ProductIdentifier: &product.Identifier,
		Meta:              meta,
		Remarks:           &remarks,
	})
	if err != nil {
		return nil, fmt.Errorf("create wallet order: %w", err)
	}
	return order, nil
}

// ConsumePaymentOrders runs the durable JetStream consumer for Wallet payment
// events (stream payment_events, subject payment_orders) and blocks until ctx
// is cancelled. Redelivered events are made idempotent by the order_id unique
// index on quota_records.
func (s *QuotaService) ConsumePaymentOrders(ctx context.Context, bus *eventbus.Bus) error {
	return bus.Consume(ctx, "payment_events", "payment_orders", "dysonfs_quota_purchase", s.handlePaymentOrderEvent)
}

func (s *QuotaService) handlePaymentOrderEvent(data []byte) error {
	var evt paymentOrderEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		// The payload was written by this service's own order flow; a parse
		// failure means a real bug and the event must not be dropped.
		return fmt.Errorf("decode payment order event: %w", err)
	}
	// Other services' orders share this subject (Sphere sponsors, awards, …).
	// Recognize our own orders by config membership or, when the product was
	// removed from config before fulfillment, by the meta snapshot frozen at
	// order creation — a paid grant must never be dropped because of config
	// drift.
	if evt.ProductIdentifier == "" || (!s.productConfigured(evt.ProductIdentifier) && !isOwnQuotaOrder(evt)) {
		return nil
	}
	// Wallet publishes on pay; be explicit anyway (1 = paid).
	if evt.Status != 1 {
		return nil
	}
	accountID, err := resolveGrantAccount(evt)
	if err != nil {
		return err
	}
	quotaMB, ok := metaQuotaMB(evt.Meta)
	if !ok || quotaMB <= 0 {
		if product := s.product(evt.ProductIdentifier); product != nil {
			quotaMB = product.QuotaMB
		} else {
			return fmt.Errorf("payment order event %s: no quota_mb in meta and product %q not configured", evt.OrderID, evt.ProductIdentifier)
		}
	}
	// Idempotency: NATS delivers at-least-once; a redelivered event must not
	// double-grant. The unique index on order_id is the backstop for the race
	// between this check and the insert.
	var existing database.QuotaRecord
	err = s.db.Where("order_id = ?", evt.OrderID).First(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("lookup quota record by order id %s: %w", evt.OrderID, err)
	}
	product := s.product(evt.ProductIdentifier)
	name, description := evt.ProductIdentifier, evt.ProductIdentifier
	var expiredAt *time.Time
	if product != nil {
		name, description = product.DisplayName, product.Description
		if product.ExpiresIn > 0 {
			t := time.Now().Add(product.ExpiresIn)
			expiredAt = &t
		}
	}
	record := database.QuotaRecord{
		ID:          database.NewID(),
		AccountID:   accountID,
		Name:        name,
		Description: description,
		Quota:       quotaMB,
		ExpiredAt:   expiredAt,
		OrderID:     &evt.OrderID,
	}
	if err := s.db.Create(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil // raced with a redelivery; already granted
		}
		return fmt.Errorf("grant quota record for order %s: %w", evt.OrderID, err)
	}
	return nil
}

func (s *QuotaService) product(identifier string) *config.QuotaProductConfig {
	for i := range s.purchaseCfg.Products {
		if s.purchaseCfg.Products[i].Identifier == identifier {
			return &s.purchaseCfg.Products[i]
		}
	}
	return nil
}

func (s *QuotaService) productConfigured(identifier string) bool {
	return s.product(identifier) != nil
}

// isOwnQuotaOrder reports whether the event's meta carries the snapshot this
// service froze at order creation (account_id, quota_mb, product_identifier
// matching the event). Used to keep granting a paid order whose product was
// removed from config before fulfillment.
func isOwnQuotaOrder(evt paymentOrderEvent) bool {
	accountID, _ := evt.Meta["account_id"].(string)
	productID, _ := evt.Meta["product_identifier"].(string)
	if accountID == "" || productID != evt.ProductIdentifier {
		return false
	}
	quotaMB, ok := metaQuotaMB(evt.Meta)
	return ok && quotaMB > 0
}

// resolveGrantAccount prefers the creator frozen in the order meta, falling
// back to the payer on the event. Invalid both ways is a hard error (Nak).
func resolveGrantAccount(evt paymentOrderEvent) (uuid.UUID, error) {
	if s, ok := evt.Meta["account_id"].(string); ok {
		if id, err := uuid.Parse(s); err == nil {
			return id, nil
		}
	}
	if id, err := uuid.Parse(evt.AccountID); err == nil {
		return id, nil
	}
	return uuid.Nil, fmt.Errorf("payment order event %s: no valid account_id in meta or event", evt.OrderID)
}

// metaQuotaMB extracts the int64 quota value from the meta map, accepting the
// float64 produced by json.Unmarshal and the json.Number produced by a decoder
// with UseNumber.
func metaQuotaMB(meta map[string]any) (int64, bool) {
	v, ok := meta["quota_mb"]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
