package service

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/assimon/luuu/model/response"
	"github.com/assimon/luuu/util/constant"
	"github.com/assimon/luuu/util/log"
	"github.com/assimon/luuu/util/math"
	"github.com/dromara/carbon/v2"
	"github.com/shopspring/decimal"
)

const (
	CnyMinimumPaymentAmount  = 0.01
	UsdtMinimumPaymentAmount = 0.01
	UsdtAmountPerIncrement   = 0.01
	IncrementalMaximumNumber = 100
)

var (
	gCreateTransactionLock sync.Mutex
	gOrderProcessingLock   sync.Mutex
)

// CreateTransaction creates a new payment order.
func CreateTransaction(req *request.CreateTransactionRequest) (*response.CreateTransactionResponse, error) {
	gCreateTransactionLock.Lock()
	defer gCreateTransactionLock.Unlock()

	token := strings.ToUpper(strings.TrimSpace(req.Token))
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	network := strings.ToLower(strings.TrimSpace(req.Network))
	payAmount := math.MustParsePrecFloat64(req.Amount, 2)
	rate := config.GetRateForCoin(strings.ToLower(token), strings.ToLower(currency))
	if rate <= 0 {
		return nil, constant.RateAmountErr
	}

	decimalPayAmount := decimal.NewFromFloat(payAmount)
	decimalTokenAmount := decimalPayAmount.Mul(decimal.NewFromFloat(rate))
	if decimalPayAmount.Cmp(decimal.NewFromFloat(CnyMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}
	if decimalTokenAmount.Cmp(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}

	exist, err := data.GetOrderInfoByOrderId(req.OrderId)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 {
		return nil, constant.OrderAlreadyExists
	}

	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	tradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), 2)
	availableAddress, availableAmount, err := ReserveAvailableWalletAndAmount(tradeID, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if availableAddress == "" {
		return nil, constant.NotAvailableAmountErr
	}

	tx := dao.Mdb.Begin()
	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        req.OrderId,
		Amount:         req.Amount,
		Currency:       currency,
		ActualAmount:   availableAmount,
		ReceiveAddress: availableAddress,
		Token:          token,
		Network:        network,
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      req.NotifyUrl,
		RedirectUrl:    req.RedirectUrl,
		Name:           req.Name,
		PaymentType:    req.PaymentType,
	}
	if err = data.CreateOrderWithTransaction(tx, order); err != nil {
		tx.Rollback()
		_ = data.UnLockTransactionByTradeId(tradeID)
		return nil, err
	}
	if err = tx.Commit().Error; err != nil {
		tx.Rollback()
		_ = data.UnLockTransactionByTradeId(tradeID)
		return nil, err
	}

	expirationTime := carbon.Now().AddMinutes(config.GetOrderExpirationTime()).Timestamp()
	resp := &response.CreateTransactionResponse{
		TradeId:        order.TradeId,
		OrderId:        order.OrderId,
		Amount:         order.Amount,
		Currency:       order.Currency,
		ActualAmount:   order.ActualAmount,
		ReceiveAddress: order.ReceiveAddress,
		Token:          order.Token,
		ExpirationTime: expirationTime,
		PaymentUrl:     fmt.Sprintf("%s/pay/checkout-counter/%s", config.GetAppUri(), order.TradeId),
	}
	return resp, nil
}

// OrderProcessing marks an order as paid and releases its sqlite reservation.
func OrderProcessing(req *request.OrderProcessingRequest) error {
	gOrderProcessingLock.Lock()
	defer gOrderProcessingLock.Unlock()

	tx := dao.Mdb.Begin()
	exist, err := data.GetOrderByBlockIdWithTransaction(tx, req.BlockTransactionId)
	if err != nil {
		tx.Rollback()
		return err
	}
	if exist.ID > 0 {
		tx.Rollback()
		return constant.OrderBlockAlreadyProcess
	}

	updated, err := data.OrderSuccessWithTransaction(tx, req)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !updated {
		tx.Rollback()
		return constant.OrderStatusConflict
	}
	if err = tx.Commit().Error; err != nil {
		tx.Rollback()
		return err
	}

	if err = data.UnLockTransaction(req.Network, req.ReceiveAddress, req.Token, req.Amount); err != nil {
		log.Sugar.Warnf("[order] unlock transaction after pay success failed, trade_id=%s, err=%v", req.TradeId, err)
	}
	return nil
}

// ReserveAvailableWalletAndAmount finds and locks a network+address+token+amount pair.
func ReserveAvailableWalletAndAmount(tradeID string, network string, token string, amount float64, walletAddress []mdb.WalletAddress) (string, float64, error) {
	availableAddress := ""
	availableAmount := amount

	tryLockWalletFunc := func(targetAmount float64) (string, error) {
		for _, address := range walletAddress {
			err := data.LockTransaction(network, address.Address, token, tradeID, targetAmount, config.GetOrderExpirationTimeDuration())
			if err == nil {
				return address.Address, nil
			}
			if errors.Is(err, data.ErrTransactionLocked) {
				continue
			}
			return "", err
		}
		return "", nil
	}

	for i := 0; i < IncrementalMaximumNumber; i++ {
		address, err := tryLockWalletFunc(availableAmount)
		if err != nil {
			return "", 0, err
		}
		if address == "" {
			decimalOldAmount := decimal.NewFromFloat(availableAmount)
			decimalIncr := decimal.NewFromFloat(UsdtAmountPerIncrement)
			availableAmount = decimalOldAmount.Add(decimalIncr).InexactFloat64()
			continue
		}
		availableAddress = address
		break
	}
	return availableAddress, availableAmount, nil
}

// GenerateCode creates a unique trade id.
func GenerateCode() string {
	date := time.Now().Format("20060102")
	r := rand.Intn(1000)
	return fmt.Sprintf("%s%d%03d", date, time.Now().UnixNano()/1e6, r)
}

// GetOrderInfoByTradeId returns a validated order.
func GetOrderInfoByTradeId(tradeId string) (*mdb.Orders, error) {
	order, err := data.GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return nil, err
	}
	if order.ID <= 0 {
		return nil, constant.OrderNotExists
	}
	return order, nil
}
