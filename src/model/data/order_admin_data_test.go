package data

import (
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// TestApplyDraftParentDisplayOverlaysSubOrderFields verifies B2: when
// the admin list contains a draft parent (parent_trade_id='' and
// network=''), the empty network/token/receive_address/actual_amount
// columns are filled in from the active sub-order so the row reads
// naturally. The underlying DB row is not modified.
func TestApplyDraftParentDisplayOverlaysSubOrderFields(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// Draft parent: stored with empty chain fields, no wallet allocated.
	draftParent := mdb.Orders{
		TradeId:        "draft_parent_1",
		OrderId:        "merchant_order_draft_1",
		Amount:         10,
		Currency:       "CNY",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
		IsSelected:     false,
		ActualAmount:   0,
		ReceiveAddress: "",
		Network:        "",
		Token:          "",
	}
	if err := dao.Mdb.Create(&draftParent).Error; err != nil {
		t.Fatalf("create draft parent: %v", err)
	}

	// Sub-order: full chain identity, marked selected (this is the one
	// the user committed to in cashier).
	selectedSub := mdb.Orders{
		TradeId:        "sub_selected_1",
		OrderId:        "merchant_order_draft_1_usdt_tron",
		ParentTradeId:  draftParent.TradeId,
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.47,
		ReceiveAddress: "TWalletTron001",
		Network:        mdb.NetworkTron,
		Token:          "USDT",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
		IsSelected:     true,
	}
	if err := dao.Mdb.Create(&selectedSub).Error; err != nil {
		t.Fatalf("create sub-order: %v", err)
	}

	// Non-draft parent (no overlay should happen).
	regularParent := mdb.Orders{
		TradeId:        "regular_parent_1",
		OrderId:        "merchant_order_regular_1",
		Amount:         5,
		Currency:       "CNY",
		ActualAmount:   0.71,
		ReceiveAddress: "UQTonAddress01",
		Network:        mdb.NetworkTon,
		Token:          "TON",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
		IsSelected:     true,
	}
	if err := dao.Mdb.Create(&regularParent).Error; err != nil {
		t.Fatalf("create regular parent: %v", err)
	}

	rows := []mdb.Orders{draftParent, selectedSub, regularParent}
	out := applyDraftParentDisplay(rows)

	// Draft parent row should now show the sub-order's fields.
	if out[0].Network != mdb.NetworkTron {
		t.Fatalf("draft parent overlay network = %q, want tron", out[0].Network)
	}
	if out[0].Token != "USDT" {
		t.Fatalf("draft parent overlay token = %q, want USDT", out[0].Token)
	}
	if out[0].ReceiveAddress != "TWalletTron001" {
		t.Fatalf("draft parent overlay address = %q, want TWalletTron001", out[0].ReceiveAddress)
	}
	if got := out[0].ActualAmount; got != 1.47 {
		t.Fatalf("draft parent overlay actual_amount = %v, want 1.47", got)
	}

	// Sub-order row must be untouched.
	if out[1].Network != mdb.NetworkTron {
		t.Fatalf("sub-order network changed: %q", out[1].Network)
	}

	// Regular parent must be untouched.
	if out[2].Network != mdb.NetworkTon {
		t.Fatalf("regular parent network changed: %q", out[2].Network)
	}
	if out[2].ReceiveAddress != "UQTonAddress01" {
		t.Fatalf("regular parent address changed: %q", out[2].ReceiveAddress)
	}

	// DB row must NOT be modified.
	reloaded := mdb.Orders{}
	if err := dao.Mdb.Where("trade_id = ?", draftParent.TradeId).First(&reloaded).Error; err != nil {
		t.Fatalf("reload draft parent: %v", err)
	}
	if reloaded.Network != "" {
		t.Fatalf("DB draft parent network mutated to %q, must stay empty", reloaded.Network)
	}
}

// TestApplyDraftParentDisplayPrefersSelectedSubOrder verifies the
// selection rule: when multiple sub-orders exist for a draft parent,
// the one with is_selected=true wins over a more recent unselected one.
func TestApplyDraftParentDisplayPrefersSelectedSubOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	parent := mdb.Orders{
		TradeId:     "draft_parent_2",
		OrderId:     "merchant_order_draft_2",
		Amount:      10,
		Currency:    "CNY",
		Status:      mdb.StatusWaitPay,
		PayProvider: mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Older sub, marked selected.
	older := mdb.Orders{
		TradeId:        "sub_older",
		OrderId:        "merchant_order_draft_2_usdt_tron",
		ParentTradeId:  parent.TradeId,
		ReceiveAddress: "TOLD",
		Network:        mdb.NetworkTron,
		Token:          "USDT",
		Status:         mdb.StatusExpired,
		PayProvider:    mdb.PaymentProviderOnChain,
		IsSelected:     true,
	}
	if err := dao.Mdb.Create(&older).Error; err != nil {
		t.Fatalf("create older sub: %v", err)
	}
	// Newer sub, NOT selected.
	newer := mdb.Orders{
		TradeId:        "sub_newer",
		OrderId:        "merchant_order_draft_2_ton_ton",
		ParentTradeId:  parent.TradeId,
		ReceiveAddress: "UNEW",
		Network:        mdb.NetworkTon,
		Token:          "TON",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
		IsSelected:     false,
	}
	if err := dao.Mdb.Create(&newer).Error; err != nil {
		t.Fatalf("create newer sub: %v", err)
	}

	out := applyDraftParentDisplay([]mdb.Orders{parent})
	if out[0].Network != mdb.NetworkTron {
		t.Fatalf("overlay network = %q, want tron (the selected one, not the newer)", out[0].Network)
	}
}
