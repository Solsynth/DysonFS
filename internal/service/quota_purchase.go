package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
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
// payment client was never dialed (missing [wallet] target).
var ErrPurchaseNotConfigured = errors.New("quota purchase is not configured")

// ErrPurchaseQuantityTooLow is returned when the requested GB is below the
// configured minimum (or not positive).
var ErrPurchaseQuantityTooLow = errors.New("quota purchase quantity is too low")

// ErrPurchaseQuantityTooHigh is returned when the purchase would push the
// account's total extra quota (purchases + admin grants) above the configured
// maximum.
var ErrPurchaseQuantityTooHigh = errors.New("quota purchase would exceed the maximum extra quota")

// quotaOrderProductIdentifier is the Wallet order label for DysonFS quota
// purchases (product_identifier field).
const quotaOrderProductIdentifier = "dysonfs.quota"

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

// QuotaPurchaseInfo is the read model served by GET /api/billing/quota/purchase.
type QuotaPurchaseInfo struct {
	PricePerGB string `json:"price_per_gb"`
	Currency   string `json:"currency"`
	MinGB      int64  `json:"min_gb"`
	MaxGB      int64  `json:"max_gb"`               // 0 = unlimited; cap on total extra quota (purchases + admin grants)
	ExpiresIn  string `json:"expires_in,omitempty"` // Go duration string, omitted when permanent
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

// SetPurchaseConfig stores the [quota.purchase] purchase terms. Called
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

// PurchaseInfo returns the configured purchase terms.
func (s *QuotaService) PurchaseInfo() QuotaPurchaseInfo {
	info := QuotaPurchaseInfo{
		PricePerGB: s.purchaseCfg.PricePerGB,
		Currency:   s.purchaseCurrency(),
		MinGB:      s.purchaseCfg.MinGB,
		MaxGB:      s.purchaseCfg.MaxGB,
	}
	if s.purchaseCfg.ExpiresIn > 0 {
		info.ExpiresIn = s.purchaseCfg.ExpiresIn.String()
	}
	return info
}

// purchaseCurrency returns the configured currency, defaulting to golds.
func (s *QuotaService) purchaseCurrency() string {
	if c := strings.TrimSpace(s.purchaseCfg.Currency); c != "" {
		return c
	}
	return "golds"
}

// CreatePurchaseOrder creates a Wallet order (system payee) for quantityGB GB
// of extra quota at the configured per-GB price, mirroring the
// DysonNetwork.Sphere sponsor flow. Quantity must be within [MinGB, ∞) and the
// account's total extra quota (purchases + admin grants) must stay within
// MaxGB. The grant target account, quantity and quota amount are frozen into
// the order meta at creation time so later config edits cannot change what a
// paid order grants.
func (s *QuotaService) CreatePurchaseOrder(ctx context.Context, accountID string, quantityGB int64) (*gen.DyOrder, error) {
	if !s.PurchaseEnabled() || s.paymentClient == nil {
		return nil, ErrPurchaseNotConfigured
	}
	if quantityGB <= 0 {
		return nil, ErrPurchaseQuantityTooLow
	}
	if minGB := s.purchaseCfg.MinGB; minGB > 0 && quantityGB < minGB {
		return nil, ErrPurchaseQuantityTooLow
	}
	// maxGB caps the account's total extra quota (purchased + admin-assigned),
	// not a single order. Compare in MB against the same non-expired sum that
	// QuotaSummary.ExtraQuota surfaces.
	if maxGB := s.purchaseCfg.MaxGB; maxGB > 0 {
		currentMB, err := s.extraQuotaMB(accountID)
		if err != nil {
			return nil, fmt.Errorf("check extra quota cap: %w", err)
		}
		if currentMB+quantityGB*1024 > maxGB*1024 {
			return nil, ErrPurchaseQuantityTooHigh
		}
	}
	amount, err := computeOrderAmount(s.purchaseCfg.PricePerGB, quantityGB)
	if err != nil {
		return nil, err
	}
	metaMap := map[string]any{
		"account_id":  accountID,
		"quantity_gb": quantityGB,
		"quota_mb":    quantityGB * 1024,
	}
	if expiresIn := s.purchaseCfg.ExpiresIn; expiresIn > 0 {
		// Freeze the granted lifetime at order time like the amount and
		// quantity, so later config edits cannot change a paid order.
		metaMap["expires_in_seconds"] = int64(expiresIn.Seconds())
	}
	meta, err := json.Marshal(metaMap)
	if err != nil {
		return nil, fmt.Errorf("encode order meta: %w", err)
	}
	currency := s.purchaseCurrency()
	remarks := fmt.Sprintf("DysonFS quota purchase: %d GB extra storage", quantityGB)
	productIdentifier := quotaOrderProductIdentifier
	order, err := s.paymentClient.CreateOrder(ctx, &gen.DyCreateOrderRequest{
		Currency:          currency,
		Amount:            amount,
		ProductIdentifier: &productIdentifier,
		Meta:              meta,
		Remarks:           &remarks,
	})
	if err != nil {
		return nil, fmt.Errorf("create wallet order: %w", err)
	}
	return order, nil
}

// computeOrderAmount multiplies the per-GB decimal price by the quantity,
// returning a decimal string with the same scale as the price (trailing zeros
// trimmed). big.Rat keeps the arithmetic exact.
func computeOrderAmount(pricePerGB string, quantityGB int64) (string, error) {
	price, ok := new(big.Rat).SetString(strings.TrimSpace(pricePerGB))
	if !ok || price.Sign() < 0 {
		return "", fmt.Errorf("compute order amount: invalid price per GB %q", pricePerGB)
	}
	amount := new(big.Rat).Mul(price, new(big.Rat).SetInt64(quantityGB))
	scale := decimalScale(pricePerGB)
	s := amount.FloatString(scale)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s, nil
}

func decimalScale(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
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
	// Only events whose meta carries the snapshot frozen at order creation
	// (account_id, quantity_gb, quota_mb) are ours.
	if !isOwnQuotaOrder(evt) {
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
	quotaMB, ok := metaInt64(evt.Meta, "quota_mb")
	if !ok || quotaMB <= 0 {
		return fmt.Errorf("payment order event %s: no valid quota_mb in meta", evt.OrderID)
	}
	quantityGB, ok := metaInt64(evt.Meta, "quantity_gb")
	if !ok || quantityGB <= 0 {
		return fmt.Errorf("payment order event %s: no valid quantity_gb in meta", evt.OrderID)
	}
	// Lifetime frozen in the meta at order creation wins; fall back to the
	// current config (covers orders created before expiry was snapshotted);
	// none = permanent.
	var expiredAt *time.Time
	if seconds, ok := metaInt64(evt.Meta, "expires_in_seconds"); ok && seconds > 0 {
		t := time.Now().Add(time.Duration(seconds) * time.Second)
		expiredAt = &t
	} else if expiresIn := s.purchaseCfg.ExpiresIn; expiresIn > 0 {
		t := time.Now().Add(expiresIn)
		expiredAt = &t
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
	record := database.QuotaRecord{
		ID:          database.NewID(),
		AccountID:   accountID,
		Name:        fmt.Sprintf("%d GB Extra Quota", quantityGB),
		Description: "Extra storage purchased via Wallet order",
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

// isOwnQuotaOrder reports whether the event's meta carries the snapshot this
// service froze at order creation (account_id, quantity_gb, quota_mb). The
// snapshot also survives config drift: a paid grant is never dropped because
// purchase terms changed before fulfillment.
func isOwnQuotaOrder(evt paymentOrderEvent) bool {
	accountID, _ := evt.Meta["account_id"].(string)
	if accountID == "" {
		return false
	}
	quotaMB, ok := metaInt64(evt.Meta, "quota_mb")
	if !ok || quotaMB <= 0 {
		return false
	}
	quantityGB, ok := metaInt64(evt.Meta, "quantity_gb")
	return ok && quantityGB > 0
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

// metaInt64 extracts an int64 value from the meta map, accepting the float64
// produced by json.Unmarshal and the json.Number produced by a decoder with
// UseNumber.
func metaInt64(meta map[string]any, key string) (int64, bool) {
	v, ok := meta[key]
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
