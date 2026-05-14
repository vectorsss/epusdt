package data

import (
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm"
)

// OrderListFilter bundles every filter supported by the admin orders
// list page. Zero values are ignored so callers can pass only what the
// user actually filtered on.
type OrderListFilter struct {
	Status   int
	Network  string
	Token    string
	Address  string
	Keyword  string // matches trade_id / order_id / block_transaction_id
	StartAt  *time.Time
	EndAt    *time.Time
	Page     int
	PageSize int
	// ParentOnly restricts the result to top-level orders only
	// (parent_trade_id = ''). Sub-orders are excluded from the listing.
	ParentOnly bool
}

// ListOrders returns a paginated order slice plus the total count under
// the same filter (for the UI pagination bar).
func ListOrders(f OrderListFilter) ([]mdb.Orders, int64, error) {
	tx := buildOrderListQuery(f)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	size := f.PageSize
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	var rows []mdb.Orders
	err := tx.Order("id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&rows).Error
	if err != nil {
		return rows, total, err
	}
	rows = applyDraftParentDisplay(rows)
	return rows, total, nil
}

// applyDraftParentDisplay overlays sub-order display fields onto draft
// parent rows (parent_trade_id='' and network=='') so the admin list
// reads naturally instead of showing blank network/token/address cells.
// Only the in-memory view is changed; the underlying DB row is untouched.
//
// Selection rule for the overlay source:
//   1. Prefer the sub-order with is_selected=true (the one the user
//      committed to in cashier).
//   2. Fall back to the most recent sub-order (highest id) when none
//      are selected.
func applyDraftParentDisplay(rows []mdb.Orders) []mdb.Orders {
	if len(rows) == 0 {
		return rows
	}
	var draftTradeIDs []string
	for _, r := range rows {
		if r.ParentTradeId == "" && r.Network == "" {
			draftTradeIDs = append(draftTradeIDs, r.TradeId)
		}
	}
	if len(draftTradeIDs) == 0 {
		return rows
	}

	var subs []mdb.Orders
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id IN ?", draftTradeIDs).
		Order("id DESC"). // most recent first
		Find(&subs).Error; err != nil || len(subs) == 0 {
		return rows
	}

	picks := make(map[string]mdb.Orders, len(draftTradeIDs))
	for _, sub := range subs {
		existing, ok := picks[sub.ParentTradeId]
		if !ok {
			picks[sub.ParentTradeId] = sub
			continue
		}
		if !existing.IsSelected && sub.IsSelected {
			picks[sub.ParentTradeId] = sub
		}
	}

	for i := range rows {
		if rows[i].ParentTradeId != "" || rows[i].Network != "" {
			continue
		}
		chosen, ok := picks[rows[i].TradeId]
		if !ok {
			continue
		}
		rows[i].Network = chosen.Network
		rows[i].Token = chosen.Token
		rows[i].ReceiveAddress = chosen.ReceiveAddress
		rows[i].ActualAmount = chosen.ActualAmount
	}
	return rows
}

func buildOrderListQuery(f OrderListFilter) *gorm.DB {
	tx := dao.Mdb.Model(&mdb.Orders{})
	if f.ParentOnly {
		tx = tx.Where("parent_trade_id = ?", "")
	}
	if f.Status > 0 {
		tx = tx.Where("status = ?", f.Status)
	}
	if f.Network != "" {
		tx = tx.Where("network = ?", strings.ToLower(f.Network))
	}
	if f.Token != "" {
		tx = tx.Where("token = ?", strings.ToUpper(f.Token))
	}
	if f.Address != "" {
		tx = tx.Where("receive_address = ?", f.Address)
	}
	if f.Keyword != "" {
		kw := "%" + strings.TrimSpace(f.Keyword) + "%"
		tx = tx.Where("trade_id LIKE ? OR order_id LIKE ? OR block_transaction_id LIKE ?", kw, kw, kw)
	}
	if f.StartAt != nil {
		tx = tx.Where("created_at >= ?", f.StartAt.Format("2006-01-02 15:04:05"))
	}
	if f.EndAt != nil {
		tx = tx.Where("created_at <= ?", f.EndAt.Format("2006-01-02 15:04:05"))
	}
	return tx
}

// CountOrdersByStatus returns how many orders exist in each status.
// Used by the dashboard overview card.
func CountOrdersByStatus() (map[int]int64, error) {
	type row struct {
		Status int
		Total  int64
	}
	var rows []row
	err := dao.Mdb.Model(&mdb.Orders{}).
		Select("status, COUNT(*) AS total").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[int]int64{}
	for _, r := range rows {
		out[r.Status] = r.Total
	}
	return out, nil
}

// CloseOrderManually transitions a pending order to expired. Only
// touches rows currently in StatusWaitPay so idempotent / safe.
func CloseOrderManually(tradeID string) (bool, error) {
	result := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", mdb.StatusWaitPay).
		Update("status", mdb.StatusExpired)
	return result.RowsAffected > 0, result.Error
}

// ReopenOrderCallback flips callback_confirm back to NO so the mq
// worker picks it up on the next tick. Used by "resend callback".
func ReopenOrderCallback(tradeID string) (bool, error) {
	result := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", mdb.StatusPaySuccess).
		Updates(map[string]interface{}{
			"callback_confirm": mdb.CallBackConfirmNo,
			"callback_num":     0,
		})
	return result.RowsAffected > 0, result.Error
}

// CountOrdersByAddress returns order counts grouped by receive_address.
// The admin wallet list annotates each wallet row with this number.
func CountOrdersByAddress() (map[string]int64, error) {
	type row struct {
		Address string `gorm:"column:receive_address"`
		Total   int64
	}
	var rows []row
	err := dao.Mdb.Model(&mdb.Orders{}).
		Select("receive_address, COUNT(*) AS total").
		Group("receive_address").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Address] = r.Total
	}
	return out, nil
}
