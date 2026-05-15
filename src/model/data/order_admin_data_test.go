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

// TestApplyDraftParentDisplayPrefersPaidSubOrder verifies B5's priority
// rule: a paid sub-order wins over a selected-but-unpaid one. This
// matters because the paid sub holds the authoritative block_tx_id;
// the admin row must show the tx hash that actually settled the parent.
func TestApplyDraftParentDisplayPrefersPaidSubOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	parent := mdb.Orders{
		TradeId:     "draft_parent_3",
		OrderId:     "merchant_order_draft_3",
		Amount:      10,
		Currency:    "CNY",
		Status:      mdb.StatusPaySuccess,
		PayProvider: mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Selected but unpaid (user committed; payment didn't land).
	selected := mdb.Orders{
		TradeId:            "sub_selected_unpaid",
		OrderId:            "merchant_order_draft_3_usdt_tron",
		ParentTradeId:      parent.TradeId,
		ReceiveAddress:     "TSEL",
		Network:            mdb.NetworkTron,
		Token:              "USDT",
		Status:             mdb.StatusWaitPay,
		PayProvider:        mdb.PaymentProviderOnChain,
		IsSelected:         true,
		BlockTransactionId: "",
	}
	if err := dao.Mdb.Create(&selected).Error; err != nil {
		t.Fatalf("create selected sub: %v", err)
	}
	// Paid sub (the one that actually settled the parent).
	paid := mdb.Orders{
		TradeId:            "sub_paid",
		OrderId:            "merchant_order_draft_3_ton_ton",
		ParentTradeId:      parent.TradeId,
		ReceiveAddress:     "UPAID",
		Network:            mdb.NetworkTon,
		Token:              "TON",
		ActualAmount:       1.63,
		Status:             mdb.StatusPaySuccess,
		PayProvider:        mdb.PaymentProviderOnChain,
		IsSelected:         false, // not selected anymore — paid takes over
		BlockTransactionId: "f53b1748149026d9d0e945c92e82e9f1938812a5cbe2b517bf12d4ea8920265c",
	}
	if err := dao.Mdb.Create(&paid).Error; err != nil {
		t.Fatalf("create paid sub: %v", err)
	}

	out := applyDraftParentDisplay([]mdb.Orders{parent})
	if out[0].Network != mdb.NetworkTon {
		t.Fatalf("overlay network = %q, want ton (paid sub wins over selected)", out[0].Network)
	}
	if out[0].BlockTransactionId != paid.BlockTransactionId {
		t.Fatalf("overlay block_transaction_id = %q, want %q",
			out[0].BlockTransactionId, paid.BlockTransactionId)
	}
	if out[0].ActualAmount != 1.63 {
		t.Fatalf("overlay actual_amount = %v, want 1.63 (from paid sub)", out[0].ActualAmount)
	}
}

// TestApplyDraftParentDisplayOverlaysBlockTransactionId is a focused B5
// regression test: the block_transaction_id field must come through the
// overlay so admin can click into the chain explorer from the parent row.
func TestApplyDraftParentDisplayOverlaysBlockTransactionId(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	parent := mdb.Orders{
		TradeId:     "draft_parent_b5",
		OrderId:     "merchant_order_b5",
		Amount:      10,
		Currency:    "CNY",
		Status:      mdb.StatusPaySuccess,
		PayProvider: mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	sub := mdb.Orders{
		TradeId:            "sub_b5",
		OrderId:            "merchant_order_b5_usdt_ton",
		ParentTradeId:      parent.TradeId,
		ReceiveAddress:     "UQAddr",
		Network:            mdb.NetworkTon,
		Token:              "USDT",
		ActualAmount:       1.62,
		Status:             mdb.StatusPaySuccess,
		PayProvider:        mdb.PaymentProviderOnChain,
		IsSelected:         true,
		BlockTransactionId: "abc123tx",
	}
	if err := dao.Mdb.Create(&sub).Error; err != nil {
		t.Fatalf("create sub: %v", err)
	}

	out := applyDraftParentDisplay([]mdb.Orders{parent})
	if out[0].BlockTransactionId != "abc123tx" {
		t.Fatalf("B5 overlay missing: got %q, want abc123tx", out[0].BlockTransactionId)
	}

	// DB row must stay empty (no mutation).
	reloaded := mdb.Orders{}
	if err := dao.Mdb.Where("trade_id = ?", parent.TradeId).First(&reloaded).Error; err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if reloaded.BlockTransactionId != "" {
		t.Fatalf("DB parent block_transaction_id mutated to %q, must stay empty",
			reloaded.BlockTransactionId)
	}
}

// TestListOrdersDefaultsToParentOnly verifies Y1: by default the admin
// list query excludes sub-orders so each payment shows as a single row.
// Set ParentOnly=false to opt back in to the full hierarchical view.
func TestListOrdersDefaultsToParentOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	parent := mdb.Orders{
		TradeId:        "y1_parent",
		OrderId:        "y1_merchant",
		Amount:         10,
		Currency:       "CNY",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		ReceiveAddress: "UQAddr",
	}
	if err := dao.Mdb.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	sub := mdb.Orders{
		TradeId:        "y1_sub",
		OrderId:        "y1_merchant_usdt_ton",
		ParentTradeId:  parent.TradeId,
		Amount:         10,
		Currency:       "CNY",
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		ReceiveAddress: "UQAddrSub",
		IsSelected:     true,
	}
	if err := dao.Mdb.Create(&sub).Error; err != nil {
		t.Fatalf("create sub: %v", err)
	}

	// ParentOnly=true (Y1 default): sub-order hidden.
	parents, _, err := ListOrders(OrderListFilter{ParentOnly: true, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListOrders ParentOnly: %v", err)
	}
	if len(parents) != 1 {
		t.Fatalf("ParentOnly=true returned %d rows, want 1 (sub should be hidden)", len(parents))
	}
	if parents[0].TradeId != parent.TradeId {
		t.Fatalf("ParentOnly returned wrong row: %s, want %s", parents[0].TradeId, parent.TradeId)
	}

	// ParentOnly=false (escape hatch): both rows.
	all, _, err := ListOrders(OrderListFilter{ParentOnly: false, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListOrders ParentOnly=false: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ParentOnly=false returned %d rows, want 2 (parent + sub)", len(all))
	}
}
