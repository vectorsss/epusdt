package data

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestEvmTransactionLockAddressIsCaseInsensitive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	tradeID := "trade-evm-case"
	address := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"

	if err := LockTransaction("Ethereum", address, "usdt", tradeID, 1.23, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, strings.ToLower(address), "USDT", 1.23)
	if err != nil {
		t.Fatalf("lookup transaction lock: %v", err)
	}
	if gotTradeID != tradeID {
		t.Fatalf("trade id = %q, want %q", gotTradeID, tradeID)
	}

	if err := UnLockTransaction(mdb.NetworkEthereum, strings.ToUpper(address), "USDT", 1.23); err != nil {
		t.Fatalf("unlock transaction: %v", err)
	}

	gotTradeID, err = GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, address, "USDT", 1.23)
	if err != nil {
		t.Fatalf("lookup after unlock: %v", err)
	}
	if gotTradeID != "" {
		t.Fatalf("expected lock to be released, got trade id %q", gotTradeID)
	}
}

func TestTransactionLockPrecisionPreventsEquivalentAmountsOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "2", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 2: %v", err)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress001", "USDT", "trade-old", 1.23, time.Hour); err != nil {
		t.Fatalf("lock old transaction: %v", err)
	}

	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 4: %v", err)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress001", "USDT", "trade-equivalent", 1.2300, time.Hour); !errors.Is(err, ErrTransactionLocked) {
		t.Fatalf("equivalent lock error = %v, want %v", err, ErrTransactionLocked)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress001", "USDT", "trade-new", 1.2301, time.Hour); err != nil {
		t.Fatalf("distinct precision lock: %v", err)
	}
}

func TestTransactionLockLookupUsesStoredPrecision(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 4: %v", err)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress002", "USDT", "trade-precise", 1.2345, time.Hour); err != nil {
		t.Fatalf("lock precise transaction: %v", err)
	}
	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "2", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 2: %v", err)
	}

	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, "TPrecisionAddress002", "USDT", 1.2345)
	if err != nil {
		t.Fatalf("lookup transaction lock: %v", err)
	}
	if gotTradeID != "trade-precise" {
		t.Fatalf("trade id = %q, want trade-precise", gotTradeID)
	}
}

func TestNonEvmTransactionLockAddressRemainsCaseSensitive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	tradeID := "trade-tron-case"
	address := "TCaseSensitiveAddress001"

	if err := LockTransaction(mdb.NetworkTron, address, "USDT", tradeID, 1.00, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, strings.ToLower(address), "USDT", 1.00)
	if err != nil {
		t.Fatalf("lookup transaction lock: %v", err)
	}
	if gotTradeID != "" {
		t.Fatalf("tron address lookup should remain case-sensitive, got trade id %q", gotTradeID)
	}
}

// TestMarkParentOrderSuccessOverlaysSubFieldsOnDraftParent verifies the
// Plan E fix: when a draft parent (network='') is settled by its sub,
// the sub's chain fields are copied onto the parent so dashboard queries
// can use a single parent_trade_id='' filter without losing amount data.
func TestMarkParentOrderSuccessOverlaysSubFieldsOnDraftParent(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	draftParent := mdb.Orders{
		TradeId:     "draft_overlay_p",
		OrderId:     "merchant_draft_overlay",
		Amount:      10,
		Currency:    "CNY",
		Status:      mdb.StatusWaitPay,
		PayProvider: mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&draftParent).Error; err != nil {
		t.Fatalf("create draft parent: %v", err)
	}

	sub := mdb.Orders{
		TradeId:            "draft_overlay_s",
		OrderId:            "merchant_draft_overlay_usdt_ton",
		ParentTradeId:      draftParent.TradeId,
		Amount:             10,
		Currency:           "CNY",
		ActualAmount:       1.63,
		ReceiveAddress:     "UQAddrPaid",
		Network:            mdb.NetworkTon,
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		PayProvider:        mdb.PaymentProviderOnChain,
		BlockTransactionId: "block_tx_abc",
	}
	if err := dao.Mdb.Create(&sub).Error; err != nil {
		t.Fatalf("create sub: %v", err)
	}

	updated, err := MarkParentOrderSuccess(draftParent.TradeId, &sub)
	if err != nil {
		t.Fatalf("MarkParentOrderSuccess: %v", err)
	}
	if !updated {
		t.Fatal("MarkParentOrderSuccess returned not-updated")
	}

	reloaded := mdb.Orders{}
	if err := dao.Mdb.Where("trade_id = ?", draftParent.TradeId).First(&reloaded).Error; err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if reloaded.Status != mdb.StatusPaySuccess {
		t.Errorf("parent status = %d, want %d", reloaded.Status, mdb.StatusPaySuccess)
	}
	if reloaded.Network != mdb.NetworkTon {
		t.Errorf("parent network = %q, want ton (copied from sub)", reloaded.Network)
	}
	if reloaded.Token != "USDT" {
		t.Errorf("parent token = %q, want USDT (copied from sub)", reloaded.Token)
	}
	if reloaded.ReceiveAddress != "UQAddrPaid" {
		t.Errorf("parent receive_address = %q, want UQAddrPaid (copied)", reloaded.ReceiveAddress)
	}
	if reloaded.ActualAmount != 1.63 {
		t.Errorf("parent actual_amount = %v, want 1.63 (copied)", reloaded.ActualAmount)
	}
	if reloaded.BlockTransactionId != "block_tx_abc" {
		t.Errorf("parent block_transaction_id = %q, want block_tx_abc (copied)", reloaded.BlockTransactionId)
	}
}

// TestMarkParentOrderSuccessKeepsBoundParentFields verifies the WHERE
// network='' guard: when a parent was bound to a chain at creation
// (the legacy direct-QR flow), the original quote is preserved even
// after a sub on a different chain settles the payment.
func TestMarkParentOrderSuccessKeepsBoundParentFields(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	boundParent := mdb.Orders{
		TradeId:        "bound_p",
		OrderId:        "merchant_bound",
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.47,
		ReceiveAddress: "TQuoted",
		Network:        mdb.NetworkTron,
		Token:          "USDT",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&boundParent).Error; err != nil {
		t.Fatalf("create bound parent: %v", err)
	}

	sub := mdb.Orders{
		TradeId:            "bound_s",
		OrderId:            "merchant_bound_usdt_ton",
		ParentTradeId:      boundParent.TradeId,
		Amount:             10,
		Currency:           "CNY",
		ActualAmount:       1.63,
		ReceiveAddress:     "UQPaid",
		Network:            mdb.NetworkTon,
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		PayProvider:        mdb.PaymentProviderOnChain,
		BlockTransactionId: "block_tx_ton",
	}
	dao.Mdb.Create(&sub)

	if _, err := MarkParentOrderSuccess(boundParent.TradeId, &sub); err != nil {
		t.Fatalf("MarkParentOrderSuccess: %v", err)
	}

	reloaded := mdb.Orders{}
	if err := dao.Mdb.Where("trade_id = ?", boundParent.TradeId).First(&reloaded).Error; err != nil {
		t.Fatalf("reload bound parent: %v", err)
	}
	if reloaded.Network != mdb.NetworkTron {
		t.Errorf("bound parent network changed to %q, must stay tron (original quote)", reloaded.Network)
	}
	if reloaded.ReceiveAddress != "TQuoted" {
		t.Errorf("bound parent receive_address changed to %q, must stay TQuoted", reloaded.ReceiveAddress)
	}
	if reloaded.ActualAmount != 1.47 {
		t.Errorf("bound parent actual_amount = %v, must stay 1.47 (original quote)", reloaded.ActualAmount)
	}
}

// TestBackfillDraftParentFields verifies the one-time migration: paid
// draft parents from before the inline overlay get backfilled from
// their pay_by_sub_id sub-order. Idempotent on second run.
func TestBackfillDraftParentFields(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	sub := mdb.Orders{
		TradeId:            "backfill_s",
		OrderId:            "merchant_backfill_usdt_ton",
		ParentTradeId:      "backfill_p",
		Amount:             10,
		Currency:           "CNY",
		ActualAmount:       2.50,
		ReceiveAddress:     "UQBackfillPaid",
		Network:            mdb.NetworkTon,
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		PayProvider:        mdb.PaymentProviderOnChain,
		BlockTransactionId: "block_backfill",
	}
	if err := dao.Mdb.Create(&sub).Error; err != nil {
		t.Fatalf("create sub: %v", err)
	}
	parent := mdb.Orders{
		TradeId:     "backfill_p",
		OrderId:     "merchant_backfill",
		Amount:      10,
		Currency:    "CNY",
		Status:      mdb.StatusPaySuccess,
		PayProvider: mdb.PaymentProviderOnChain,
		PayBySubId:  sub.ID,
	}
	if err := dao.Mdb.Create(&parent).Error; err != nil {
		t.Fatalf("create draft parent: %v", err)
	}

	affected, err := BackfillDraftParentFields()
	if err != nil {
		t.Fatalf("BackfillDraftParentFields: %v", err)
	}
	if affected != 1 {
		t.Errorf("backfill affected %d rows, want 1", affected)
	}

	reloaded := mdb.Orders{}
	dao.Mdb.Where("trade_id = ?", "backfill_p").First(&reloaded)
	if reloaded.Network != mdb.NetworkTon {
		t.Errorf("after backfill, network = %q, want ton", reloaded.Network)
	}
	if reloaded.ActualAmount != 2.50 {
		t.Errorf("after backfill, actual_amount = %v, want 2.50", reloaded.ActualAmount)
	}
	if reloaded.BlockTransactionId != "block_backfill" {
		t.Errorf("after backfill, block_transaction_id = %q, want block_backfill", reloaded.BlockTransactionId)
	}

	affected2, _ := BackfillDraftParentFields()
	if affected2 != 0 {
		t.Errorf("second backfill affected %d rows, want 0 (idempotent)", affected2)
	}
}
