package mdb

const (
	StatusWaitPay     = 1
	StatusPaySuccess  = 2
	StatusExpired     = 3
	CallBackConfirmOk = 1
	CallBackConfirmNo = 2
)

const (
	PaymentTypeEpay = "Epay"
)

type Orders struct {
	TradeId            string  `gorm:"column:trade_id;uniqueIndex:orders_trade_id_uindex" json:"trade_id" example:"T2026041612345678"`
	OrderId            string  `gorm:"column:order_id;uniqueIndex:orders_order_id_uindex" json:"order_id" example:"ORD20260416001"`
	ParentTradeId      string  `gorm:"column:parent_trade_id;index:idx_orders_parent_trade_id;default:''" json:"parent_trade_id"`
	BlockTransactionId string  `gorm:"index:orders_block_transaction_id_index;column:block_transaction_id" json:"block_transaction_id" example:"0xabc123..."`
	Amount             float64 `gorm:"column:amount" json:"amount" example:"100.0000"`
	Currency           string  `gorm:"column:currency" json:"currency" example:"CNY"`
	ActualAmount       float64 `gorm:"column:actual_amount" json:"actual_amount" example:"14.2857"`
	ReceiveAddress     string  `gorm:"column:receive_address" json:"receive_address" example:"TTestTronAddress001"`
	Token              string  `gorm:"column:token" json:"token" example:"USDT"`
	Network            string  `gorm:"column:network" json:"network" example:"tron"`
	// 订单状态 1=等待支付 2=支付成功 3=已过期
	Status      int    `gorm:"column:status;default:1" json:"status" enums:"1,2,3" example:"1"`
	NotifyUrl   string `gorm:"column:notify_url" json:"notify_url" example:"https://example.com/notify"`
	RedirectUrl string `gorm:"column:redirect_url" json:"redirect_url" example:"https://example.com/success"`
	Name        string `gorm:"column:name" json:"name" example:"VIP月卡"`
	CallbackNum int    `gorm:"column:callback_num;default:0" json:"callback_num" example:"0"`
	// 回调确认状态 1=回调成功 2=未回调/回调失败
	CallBackConfirm int    `gorm:"column:callback_confirm;default:2" json:"callback_confirm" enums:"1,2" example:"2"`
	IsSelected      bool   `gorm:"column:is_selected;default:false" json:"is_selected" example:"false"`
	PaymentType     string `gorm:"column:payment_type" json:"payment_type" example:"Epay"`
	ApiKeyID        uint64 `gorm:"column:api_key_id;default:0;index:orders_api_key_id_index" json:"api_key_id" example:"1"`
	// PayBySubId holds the primary-key ID of the sub-order that settled this parent order.
	// Zero when the parent order was paid directly (no sub-order involved).
	PayBySubId uint64 `gorm:"column:pay_by_sub_id;default:0" json:"pay_by_sub_id" example:"0"`
	BaseModel
}

func (o *Orders) TableName() string {
	return "orders"
}
