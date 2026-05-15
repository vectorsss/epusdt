package data

import (
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// TestDailyOrderStatsAggregatesCarbonStoredCreatedAt is the regression
// test for the silent-empty-chart bug: carbon.Time stores created_at as
// "YYYY-MM-DD HH:MM:SS.ffffff +HHMM TZ" which SQLite's DATE()/strftime()
// can't parse — both return "" and every row collapses into the ""
// bucket. fillDailyStats then drops the "" bucket and the chart shows
// zeros for every day in the range. SUBSTR() is the only date extractor
// that ignores the trailing timezone junk.
func TestDailyOrderStatsAggregatesCarbonStoredCreatedAt(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// Insert three paid orders today via Create() so carbon.Time sets
	// created_at in its native format (the production code path).
	for i, amt := range []float64{1.77, 1.63, 4.42} {
		o := mdb.Orders{
			TradeId:        "trade_chart_" + string(rune('A'+i)),
			OrderId:        "merchant_chart_" + string(rune('A'+i)),
			Amount:         10,
			Currency:       "CNY",
			ActualAmount:   amt,
			ReceiveAddress: "UQAddrX",
			Network:        mdb.NetworkTon,
			Token:          "USDT",
			Status:         mdb.StatusPaySuccess,
			PayProvider:    mdb.PaymentProviderOnChain,
		}
		if err := dao.Mdb.Create(&o).Error; err != nil {
			t.Fatalf("create order: %v", err)
		}
	}

	now := time.Now()
	rows, err := DailyOrderStats(now.Add(-7*24*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("DailyOrderStats: %v", err)
	}

	today := now.Format("2006-01-02")
	var todayRow *DailyStat
	for i := range rows {
		if rows[i].Day == today {
			todayRow = &rows[i]
			break
		}
	}
	if todayRow == nil {
		t.Fatalf("DailyOrderStats has no bucket for today (%s); all %d returned rows are zero-filled — DATE()/strftime() regression?", today, len(rows))
	}
	if todayRow.SuccessCount != 3 {
		t.Errorf("today success_count = %d, want 3", todayRow.SuccessCount)
	}
	// 1.77 + 1.63 + 4.42 = 7.82
	if got := todayRow.ActualAmount; got < 7.81 || got > 7.83 {
		t.Errorf("today actual_amount = %v, want ~7.82", got)
	}
}

// TestHourlyOrderStatsAggregatesCarbonStoredCreatedAt mirrors the daily
// test for the hourly path — strftime('%Y-%m-%d %H:00') has the same
// failure mode as DATE() on carbon's format.
func TestHourlyOrderStatsAggregatesCarbonStoredCreatedAt(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	o := mdb.Orders{
		TradeId:        "trade_hour_1",
		OrderId:        "merchant_hour_1",
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.50,
		ReceiveAddress: "UQAddrH",
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		Status:         mdb.StatusPaySuccess,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&o).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	now := time.Now()
	// Hourly range: within last hour so isHourlyRange's caller would pick this path.
	rows, err := HourlyOrderStats(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("HourlyOrderStats: %v", err)
	}

	hasRealAmount := false
	for _, r := range rows {
		if r.ActualAmount > 0 {
			hasRealAmount = true
			break
		}
	}
	if !hasRealAmount {
		t.Fatalf("HourlyOrderStats returned %d rows, all zero — strftime() regression on carbon format?", len(rows))
	}
}

// TestDailyAssetByAddressAggregatesCarbonStoredCreatedAt ensures the
// per-address stacked chart query also handles carbon's storage format.
func TestDailyAssetByAddressAggregatesCarbonStoredCreatedAt(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	o := mdb.Orders{
		TradeId:        "trade_addr_1",
		OrderId:        "merchant_addr_1",
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   2.00,
		ReceiveAddress: "UQAddrStacked",
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		Status:         mdb.StatusPaySuccess,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(&o).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	now := time.Now()
	rows, err := DailyAssetByAddress(now.Add(-7*24*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("DailyAssetByAddress: %v", err)
	}

	// We should have at least one row with the inserted address.
	found := false
	for _, r := range rows {
		if r.Address == "UQAddrStacked" && r.ActualAmount > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("DailyAssetByAddress returned %d rows, none with the inserted address+amount — date extraction regression?", len(rows))
	}
}

// TestPaidStatsInRangeCountsParentsOnly verifies Step 2's filter: with
// parent_trade_id='' applied, each Plan E payment is counted once
// (as the parent) instead of twice (parent + sub).
func TestPaidStatsInRangeCountsParentsOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	parent := mdb.Orders{
		TradeId:        "stats_p",
		OrderId:        "merchant_stats",
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.63,
		ReceiveAddress: "UQ",
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		Status:         mdb.StatusPaySuccess,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	dao.Mdb.Create(&parent)
	sub := mdb.Orders{
		TradeId:        "stats_s",
		OrderId:        "merchant_stats_usdt_ton",
		ParentTradeId:  parent.TradeId,
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.63,
		ReceiveAddress: "UQ",
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		Status:         mdb.StatusPaySuccess,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	dao.Mdb.Create(&sub)

	now := time.Now()
	orderCount, successCount, actualSum, err := PaidStatsInRange(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("PaidStatsInRange: %v", err)
	}
	if orderCount != 1 {
		t.Errorf("order_count = %d, want 1 (parent only, sub filtered)", orderCount)
	}
	if successCount != 1 {
		t.Errorf("success_count = %d, want 1", successCount)
	}
	if actualSum < 1.62 || actualSum > 1.64 {
		t.Errorf("actual_sum = %v, want ~1.63 (parent contributes once)", actualSum)
	}
}
