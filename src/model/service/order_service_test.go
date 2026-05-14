package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/go-resty/resty/v2"
)

func newCreateTransactionRequest(orderID string, amount float64) *request.CreateTransactionRequest {
	return &request.CreateTransactionRequest{
		OrderId:   orderID,
		Currency:  "CNY",
		Token:     "USDT",
		Network:   "tron",
		Amount:    amount,
		NotifyUrl: "https://merchant.example/callback",
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func installMockHTTPClient(t *testing.T, handler roundTripFunc) {
	t.Helper()

	oldFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		client := resty.NewWithClient(&http.Client{Transport: handler})
		client.SetTimeout(10 * time.Second)
		return client
	}
	t.Cleanup(func() {
		http_client.ClientFactory = oldFactory
	})
}

func TestCreateTransactionAssignsIncrementedAmountsAndLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_2", 1), nil)
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	if got := fmt.Sprintf("%.2f", resp1.ActualAmount); got != "1.00" {
		t.Fatalf("first actual amount = %s, want 1.00", got)
	}
	if got := fmt.Sprintf("%.2f", resp2.ActualAmount); got != "1.01" {
		t.Fatalf("second actual amount = %s, want 1.01", got)
	}
	if resp1.ReceiveAddress != "wallet_1" || resp2.ReceiveAddress != "wallet_1" {
		t.Fatalf("unexpected receive addresses: %s, %s", resp1.ReceiveAddress, resp2.ReceiveAddress)
	}
	if resp1.Token != "USDT" || resp2.Token != "USDT" {
		t.Fatalf("unexpected tokens: %s, %s", resp1.Token, resp2.Token)
	}

	tradeID1, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", resp1.ReceiveAddress, resp1.Token, resp1.ActualAmount)
	if err != nil {
		t.Fatalf("get first runtime lock: %v", err)
	}
	if tradeID1 != resp1.TradeId {
		t.Fatalf("first runtime lock = %s, want %s", tradeID1, resp1.TradeId)
	}

	tradeID2, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", resp2.ReceiveAddress, resp2.Token, resp2.ActualAmount)
	if err != nil {
		t.Fatalf("get second runtime lock: %v", err)
	}
	if tradeID2 != resp2.TradeId {
		t.Fatalf("second runtime lock = %s, want %s", tradeID2, resp2.TradeId)
	}
}

func TestCreateTransactionUsesConfiguredAmountPrecision(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set amount precision: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_precision_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_precision_1", 1), nil)
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_precision_2", 1), nil)
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	if got := fmt.Sprintf("%.4f", resp1.ActualAmount); got != "1.0000" {
		t.Fatalf("first actual amount = %s, want 1.0000", got)
	}
	if got := fmt.Sprintf("%.4f", resp2.ActualAmount); got != "1.0001" {
		t.Fatalf("second actual amount = %s, want 1.0001", got)
	}
}

func TestCreateTransactionStoresNormalizedMerchantAmount(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_normalized_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_normalized_1", 100.129), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if got := fmt.Sprintf("%.2f", resp.Amount); got != "100.13" {
		t.Fatalf("response amount = %s, want 100.13", got)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if got := fmt.Sprintf("%.2f", order.Amount); got != "100.13" {
		t.Fatalf("stored amount = %s, want 100.13", got)
	}
}

func TestCreateTransactionUsesRateAPIWhenForcedSettingIsNotPositive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/cny.json" {
			t.Fatalf("rate api path = %s, want /cny.json", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.14635}}`)),
			Request:    r,
		}, nil
	})

	if err := data.SetSetting("rate", "rate.forced_usdt_rate", "0", "string"); err != nil {
		t.Fatalf("set rate.forced_usdt_rate: %v", err)
	}
	if err := data.SetSetting("rate", "rate.api_url", "https://rate.example.test", "string"); err != nil {
		t.Fatalf("set rate.api_url: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_api_rate_1", 10), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if got := fmt.Sprintf("%.2f", resp.ActualAmount); got != "1.46" {
		t.Fatalf("actual amount = %s, want 1.46", got)
	}
}

func TestCreateTransactionFailsWhenRateAPIUnavailableAndForcedSettingIsNotPositive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_usdt_rate", "0", "string"); err != nil {
		t.Fatalf("set rate.forced_usdt_rate: %v", err)
	}
	if err := data.SetSetting("rate", "rate.api_url", "", "string"); err != nil {
		t.Fatalf("clear rate.api_url: %v", err)
	}

	_, err := CreateTransaction(newCreateTransactionRequest("order_missing_rate_1", 10), nil)
	if err != constant.RateAmountErr {
		t.Fatalf("create transaction error = %v, want %v", err, constant.RateAmountErr)
	}
}

func TestCreateTransactionNormalizesEvmReceiveAddressToLowercase(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	mixedAddress := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"
	if err := dao.Mdb.Create(&mdb.WalletAddress{
		Network: mdb.NetworkEthereum,
		Address: mixedAddress,
		Status:  mdb.TokenStatusEnable,
	}).Error; err != nil {
		t.Fatalf("seed mixed-case wallet: %v", err)
	}

	req := newCreateTransactionRequest("order_evm_1", 1)
	req.Network = mdb.NetworkEthereum

	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	expectedAddress := strings.ToLower(mixedAddress)
	if resp.ReceiveAddress != expectedAddress {
		t.Fatalf("receive address = %q, want %q", resp.ReceiveAddress, expectedAddress)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if order.ReceiveAddress != expectedAddress {
		t.Fatalf("stored order address = %q, want %q", order.ReceiveAddress, expectedAddress)
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, strings.ToUpper(mixedAddress), resp.Token, resp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup runtime lock: %v", err)
	}
	if tradeID != resp.TradeId {
		t.Fatalf("runtime lock trade_id = %q, want %q", tradeID, resp.TradeId)
	}
}

func TestOrderProcessingMarksPaidAndReleasesLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     resp.ReceiveAddress,
		Token:              resp.Token,
		Network:            "tron",
		TradeId:            resp.TradeId,
		Amount:             resp.ActualAmount,
		BlockTransactionId: "block_1",
	})
	if err != nil {
		t.Fatalf("order processing: %v", err)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get order by trade id: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	if order.CallBackConfirm != mdb.CallBackConfirmNo {
		t.Fatalf("callback confirm = %d, want %d", order.CallBackConfirm, mdb.CallBackConfirmNo)
	}
	if order.BlockTransactionId != "block_1" {
		t.Fatalf("block transaction id = %s, want block_1", order.BlockTransactionId)
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", resp.ReceiveAddress, resp.Token, resp.ActualAmount)
	if err != nil {
		t.Fatalf("get runtime lock after processing: %v", err)
	}
	if tradeID != "" {
		t.Fatalf("runtime lock still exists: %s", tradeID)
	}
}

func TestOrderProcessingRejectsDuplicateBlockForSameOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     resp.ReceiveAddress,
		Token:              resp.Token,
		Network:            "tron",
		TradeId:            resp.TradeId,
		Amount:             resp.ActualAmount,
		BlockTransactionId: "block_1",
	}
	if err = OrderProcessing(req); err != nil {
		t.Fatalf("first order processing: %v", err)
	}

	err = OrderProcessing(req)
	if err != constant.OrderBlockAlreadyProcess {
		t.Fatalf("second order processing error = %v, want %v", err, constant.OrderBlockAlreadyProcess)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order after duplicate block: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status after duplicate block = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	if order.BlockTransactionId != "block_1" {
		t.Fatalf("order block transaction id after duplicate block = %s, want block_1", order.BlockTransactionId)
	}
}

func TestOrderProcessingDoesNotReviveExpiredOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	if err = dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", resp.TradeId).
		Update("status", mdb.StatusExpired).Error; err != nil {
		t.Fatalf("force order expired: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     resp.ReceiveAddress,
		Token:              resp.Token,
		Network:            "tron",
		TradeId:            resp.TradeId,
		Amount:             resp.ActualAmount,
		BlockTransactionId: "block_expired",
	})
	if err != constant.OrderStatusConflict {
		t.Fatalf("order processing error = %v, want %v", err, constant.OrderStatusConflict)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload expired order: %v", err)
	}
	if order.Status != mdb.StatusExpired {
		t.Fatalf("expired order status = %d, want %d", order.Status, mdb.StatusExpired)
	}
	if order.BlockTransactionId != "" {
		t.Fatalf("expired order block transaction id = %s, want empty", order.BlockTransactionId)
	}
}

func TestOrderProcessingOnlyOneOrderClaimsABlockTransaction(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_2"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_2", 2), nil)
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		address string
		token   string
		tradeID string
		amount  float64
	}{
		{address: resp1.ReceiveAddress, token: resp1.Token, tradeID: resp1.TradeId, amount: resp1.ActualAmount},
		{address: resp2.ReceiveAddress, token: resp2.Token, tradeID: resp2.TradeId, amount: resp2.ActualAmount},
	} {
		wg.Add(1)
		go func(address, token, tradeID string, amount float64) {
			defer wg.Done()
			<-start
			errs <- OrderProcessing(&request.OrderProcessingRequest{
				ReceiveAddress:     address,
				Token:              token,
				Network:            "tron",
				TradeId:            tradeID,
				Amount:             amount,
				BlockTransactionId: "shared_block",
			})
		}(tc.address, tc.token, tc.tradeID, tc.amount)
	}

	close(start)
	wg.Wait()
	close(errs)

	var successCount int
	var duplicateCount int
	for err := range errs {
		switch err {
		case nil:
			successCount++
		case constant.OrderBlockAlreadyProcess:
			duplicateCount++
		default:
			t.Fatalf("unexpected order processing error: %v", err)
		}
	}
	if successCount != 1 || duplicateCount != 1 {
		t.Fatalf("success=%d duplicate=%d, want 1 and 1", successCount, duplicateCount)
	}

	orders := []struct {
		tradeID string
	}{
		{tradeID: resp1.TradeId},
		{tradeID: resp2.TradeId},
	}
	var paidCount int
	var pendingCount int
	for _, item := range orders {
		order, err := data.GetOrderInfoByTradeId(item.tradeID)
		if err != nil {
			t.Fatalf("reload order %s: %v", item.tradeID, err)
		}
		switch order.Status {
		case mdb.StatusPaySuccess:
			paidCount++
			if order.BlockTransactionId != "shared_block" {
				t.Fatalf("paid order block transaction id = %s, want shared_block", order.BlockTransactionId)
			}
		case mdb.StatusWaitPay:
			pendingCount++
			if order.BlockTransactionId != "" {
				t.Fatalf("pending order block transaction id = %s, want empty", order.BlockTransactionId)
			}
		default:
			t.Fatalf("unexpected order status for %s: %d", item.tradeID, order.Status)
		}
	}
	if paidCount != 1 || pendingCount != 1 {
		t.Fatalf("paid=%d pending=%d, want 1 and 1", paidCount, pendingCount)
	}
}

func TestOrderProcessingSubOrderReturnsErrorWhenParentNotWaitPay(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_parent_for_sub", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch network create sub-order: %v", err)
	}

	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", parentResp.TradeId).
		Update("status", mdb.StatusExpired).Error; err != nil {
		t.Fatalf("force parent expired: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_sub_parent_not_wait",
	})
	if err == nil {
		t.Fatal("expected error when parent order is not wait-pay")
	}
}

func TestOrderProcessingSubOrderReturnsErrorWhenParentMissing(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_parent_missing", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch network create sub-order: %v", err)
	}

	if err := dao.Mdb.Where("trade_id = ?", parentResp.TradeId).Delete(&mdb.Orders{}).Error; err != nil {
		t.Fatalf("delete parent order: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_sub_parent_missing",
	})
	if err == nil {
		t.Fatal("expected error when parent order is missing")
	}
}

// TestOrderProcessingSubOrderPaidParentKeepsOwnFields verifies the new behavior:
// when a sub-order is paid, the parent order is marked as paid but its own
// block_transaction_id, actual_amount, and receive_address are NOT overwritten.
// The sub-order's primary-key ID is recorded in the parent's pay_by_sub_id field.
// Also verifies: parent callback_confirm=No (callback queued), sub-order callback_confirm=Ok.
func TestOrderProcessingSubOrderPaidParentKeepsOwnFields(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_sub_pay_test", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	// Snapshot the parent's original fields before any payment.
	originalParent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("load original parent: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch network create sub-order: %v", err)
	}

	// Load the sub-order to get its DB primary-key ID.
	subOrder, err := data.GetOrderInfoByTradeId(subResp.TradeId)
	if err != nil {
		t.Fatalf("load sub-order: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_sub_paid",
	})
	if err != nil {
		t.Fatalf("order processing sub-order: %v", err)
	}

	// Sub-order must be paid with the block hash and no pending callback.
	sub, err := data.GetOrderInfoByTradeId(subResp.TradeId)
	if err != nil {
		t.Fatalf("reload sub-order: %v", err)
	}
	if sub.Status != mdb.StatusPaySuccess {
		t.Fatalf("sub-order status = %d, want %d", sub.Status, mdb.StatusPaySuccess)
	}
	if sub.BlockTransactionId != "block_sub_paid" {
		t.Fatalf("sub-order block_transaction_id = %q, want %q", sub.BlockTransactionId, "block_sub_paid")
	}
	if sub.CallBackConfirm != mdb.CallBackConfirmOk {
		t.Fatalf("sub-order callback_confirm = %d, want %d (no callback for sub-order)", sub.CallBackConfirm, mdb.CallBackConfirmOk)
	}

	// Parent must be paid but its own fields must be unchanged.
	parent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("reload parent order: %v", err)
	}
	if parent.Status != mdb.StatusPaySuccess {
		t.Fatalf("parent status = %d, want %d", parent.Status, mdb.StatusPaySuccess)
	}
	if parent.BlockTransactionId != "" {
		t.Fatalf("parent block_transaction_id = %q, want empty (parent was not directly paid)", parent.BlockTransactionId)
	}
	if parent.ReceiveAddress != originalParent.ReceiveAddress {
		t.Fatalf("parent receive_address changed: got %q, want %q", parent.ReceiveAddress, originalParent.ReceiveAddress)
	}
	if parent.ActualAmount != originalParent.ActualAmount {
		t.Fatalf("parent actual_amount changed: got %v, want %v", parent.ActualAmount, originalParent.ActualAmount)
	}
	if parent.PayBySubId != subOrder.ID {
		t.Fatalf("parent pay_by_sub_id = %d, want %d (sub-order ID)", parent.PayBySubId, subOrder.ID)
	}
	if parent.CallBackConfirm != mdb.CallBackConfirmNo {
		t.Fatalf("parent callback_confirm = %d, want %d (callback must be queued)", parent.CallBackConfirm, mdb.CallBackConfirmNo)
	}
}

func TestOrderProcessingParentDirectPayExpiresOkPayProviderOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_parent_direct_okpay_expire", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subOrder := &mdb.Orders{
		TradeId:         "okpay_sub_parent_direct_expire",
		OrderId:         "okpay_sub_parent_direct_expire",
		ParentTradeId:   parentResp.TradeId,
		Amount:          parentResp.Amount,
		Currency:        parentResp.Currency,
		ActualAmount:    0.15,
		ReceiveAddress:  "OKPAY",
		Token:           "USDT",
		Network:         mdb.NetworkTron,
		Status:          mdb.StatusWaitPay,
		NotifyUrl:       "",
		CallBackConfirm: mdb.CallBackConfirmOk,
		PayProvider:     mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(subOrder).Error; err != nil {
		t.Fatalf("create okpay sub-order: %v", err)
	}
	providerRow := &mdb.ProviderOrder{
		TradeId:         subOrder.TradeId,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: "okp-parent-direct-expire",
		PayURL:          "https://t.me/ExampleWalletBot?start=shop_deposit--okpay-order-parent-direct-expire",
		Amount:          subOrder.ActualAmount,
		Coin:            subOrder.Token,
		Status:          mdb.ProviderOrderStatusPending,
	}
	if err := dao.Mdb.Create(providerRow).Error; err != nil {
		t.Fatalf("create provider row: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     parentResp.ReceiveAddress,
		Token:              strings.ToUpper(parentResp.Token),
		Network:            mdb.NetworkTron,
		TradeId:            parentResp.TradeId,
		Amount:             parentResp.ActualAmount,
		BlockTransactionId: "block_parent_direct_okpay_expire",
	})
	if err != nil {
		t.Fatalf("order processing parent direct pay: %v", err)
	}

	expiredSub, err := data.GetOrderInfoByTradeId(subOrder.TradeId)
	if err != nil {
		t.Fatalf("reload sub-order: %v", err)
	}
	if expiredSub.Status != mdb.StatusExpired {
		t.Fatalf("sub-order status = %d, want %d", expiredSub.Status, mdb.StatusExpired)
	}

	expiredProviderRow, err := data.GetProviderOrderByTradeIDAndProvider(subOrder.TradeId, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("reload provider row: %v", err)
	}
	if expiredProviderRow.Status != mdb.ProviderOrderStatusExpired {
		t.Fatalf("provider row status = %q, want %q", expiredProviderRow.Status, mdb.ProviderOrderStatusExpired)
	}
}

// TestOrderProcessingSubOrderExpiresSiblingsAndReleasesLocks verifies that when one
// sub-order is paid all sibling sub-orders are expired and their runtime locks (as
// well as the parent's lock) are released.
func TestOrderProcessingSubOrderExpiresSiblingsAndReleasesLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkBsc, "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"); err != nil {
		t.Fatalf("add bsc wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_sib_expiry_test", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	// Create two sub-orders on different networks.
	subEthResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch to ethereum sub-order: %v", err)
	}

	subBscResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkBsc,
	})
	if err != nil {
		t.Fatalf("switch to bsc sub-order: %v", err)
	}

	// Pay the Ethereum sub-order.
	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subEthResp.ReceiveAddress,
		Token:              strings.ToUpper(subEthResp.Token),
		Network:            strings.ToLower(subEthResp.Network),
		TradeId:            subEthResp.TradeId,
		Amount:             subEthResp.ActualAmount,
		BlockTransactionId: "block_sib_eth",
	})
	if err != nil {
		t.Fatalf("order processing eth sub-order: %v", err)
	}

	// BSC sibling must be expired.
	subBsc, err := data.GetOrderInfoByTradeId(subBscResp.TradeId)
	if err != nil {
		t.Fatalf("reload bsc sub-order: %v", err)
	}
	if subBsc.Status != mdb.StatusExpired {
		t.Fatalf("bsc sibling status = %d, want %d (expired)", subBsc.Status, mdb.StatusExpired)
	}

	// Parent runtime lock must be released.
	parentLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkTron, parentResp.ReceiveAddress, parentResp.Token, parentResp.ActualAmount)
	if err != nil {
		t.Fatalf("check parent runtime lock: %v", err)
	}
	if parentLock != "" {
		t.Fatalf("parent runtime lock still held: trade_id=%s", parentLock)
	}

	// BSC sibling runtime lock must be released.
	sibLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkBsc, subBscResp.ReceiveAddress, subBscResp.Token, subBscResp.ActualAmount)
	if err != nil {
		t.Fatalf("check bsc sibling runtime lock: %v", err)
	}
	if sibLock != "" {
		t.Fatalf("bsc sibling runtime lock still held: trade_id=%s", sibLock)
	}

	// Ethereum sub-order runtime lock must also be released.
	ethLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkEthereum, subEthResp.ReceiveAddress, subEthResp.Token, subEthResp.ActualAmount)
	if err != nil {
		t.Fatalf("check eth sub-order runtime lock: %v", err)
	}
	if ethLock != "" {
		t.Fatalf("eth sub-order runtime lock still held: trade_id=%s", ethLock)
	}
}

// TestCreateTransactionDraftMode verifies Plan E: when merchant sends
// empty network (or token), CreateTransaction stores a parent row with
// no chain identity / no locked wallet, and is_selected=false so the
// cashier renders the network selector.
func TestCreateTransactionDraftMode(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// Note: no wallet allocation needed for draft mode — that's the
	// point. If the service tried to allocate one, this test would
	// fail with NotAvailableWalletAddress.

	req := newCreateTransactionRequest("order_draft_1", 10)
	req.Network = ""
	req.Token = ""
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create draft transaction: %v", err)
	}

	if resp.ReceiveAddress != "" {
		t.Fatalf("draft response receive_address = %q, want empty", resp.ReceiveAddress)
	}
	if resp.Token != "" {
		t.Fatalf("draft response token = %q, want empty", resp.Token)
	}
	if resp.ActualAmount != 0 {
		t.Fatalf("draft response actual_amount = %v, want 0", resp.ActualAmount)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load draft order: %v", err)
	}
	if order.Network != "" || order.Token != "" || order.ReceiveAddress != "" {
		t.Fatalf("draft order should have empty chain fields, got network=%q token=%q address=%q",
			order.Network, order.Token, order.ReceiveAddress)
	}
	if order.ActualAmount != 0 {
		t.Fatalf("draft order actual_amount = %v, want 0", order.ActualAmount)
	}
	if order.IsSelected {
		t.Fatalf("draft order is_selected = true, want false (cashier should render selector)")
	}
	if got := fmt.Sprintf("%.2f", order.Amount); got != "10.00" {
		t.Fatalf("draft order amount = %s, want 10.00 (fiat amount must persist)", got)
	}
	if order.OrderId != "order_draft_1" {
		t.Fatalf("draft order merchant_order_id = %q, want order_draft_1", order.OrderId)
	}
	if order.NotifyUrl == "" {
		t.Fatalf("draft order notify_url should persist for webhook routing")
	}
}

// TestCreateTransactionDraftModePartialInputCollapses verifies that if
// the merchant accidentally sends only one of network/token, the row is
// normalized to fully-draft (both empty) instead of half-filled.
func TestCreateTransactionDraftModePartialInputCollapses(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	req := newCreateTransactionRequest("order_partial_1", 10)
	req.Network = "tron" // network set but...
	req.Token = ""       // ...token empty
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create partial draft: %v", err)
	}
	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load partial draft order: %v", err)
	}
	if order.Network != "" {
		t.Fatalf("partial draft order network = %q, want empty (should collapse)", order.Network)
	}
}

// TestSwitchNetworkFromDraftParentUsesDerivedOrderID verifies B3: the
// sub-order created from a draft parent gets an order_id derived from
// the parent's order_id plus token+network suffix, so admin list
// sorting visually clusters the parent with its sub-orders.
func TestSwitchNetworkFromDraftParentUsesDerivedOrderID(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	// 1. Create draft parent.
	parentReq := newCreateTransactionRequest("order_draft_sw", 10)
	parentReq.Network = ""
	parentReq.Token = ""
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create draft parent: %v", err)
	}

	// 2. User picks USDT on tron in cashier.
	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkTron,
	})
	if err != nil {
		t.Fatalf("switch network from draft: %v", err)
	}
	sub, err := data.GetOrderInfoByTradeId(subResp.TradeId)
	if err != nil {
		t.Fatalf("load sub-order: %v", err)
	}

	// 3. Sub-order's OrderId must be parent.OrderId + "_usdt_tron".
	wantOrderID := "order_draft_sw_usdt_tron"
	if sub.OrderId != wantOrderID {
		t.Fatalf("sub-order order_id = %q, want %q", sub.OrderId, wantOrderID)
	}
	if sub.Network != mdb.NetworkTron {
		t.Fatalf("sub-order network = %q, want tron", sub.Network)
	}
	if sub.Token != "USDT" {
		t.Fatalf("sub-order token = %q, want USDT", sub.Token)
	}
	if sub.ReceiveAddress == "" {
		t.Fatalf("sub-order receive_address must be allocated")
	}
	if !sub.IsSelected {
		t.Fatalf("sub-order is_selected = false, want true")
	}

	// 4. Parent should now be is_selected=true (refreshed by SwitchNetwork)
	//    but its chain fields stay empty (parent stays draft on disk).
	parent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if !parent.IsSelected {
		t.Fatalf("parent is_selected = false after SwitchNetwork, want true")
	}
	if parent.Network != "" {
		t.Fatalf("parent network should remain empty (draft), got %q", parent.Network)
	}
}

// TestOrderProcessingDraftParentPaidViaSubOrder is the Plan E end-to-end
// happy path: draft parent → SwitchNetwork → user pays sub-order →
// parent gets marked paid → webhook can route by parent.OrderId.
//
// Critically also verifies E3: OrderProcessing must not crash or warn
// loudly when releasing the locks of a draft parent that never had any.
func TestOrderProcessingDraftParentPaidViaSubOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	// 1. Create draft parent.
	parentReq := newCreateTransactionRequest("order_e2e_draft", 10)
	parentReq.Network = ""
	parentReq.Token = ""
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create draft parent: %v", err)
	}

	// 2. SwitchNetwork to tron.
	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkTron,
	})
	if err != nil {
		t.Fatalf("switch network: %v", err)
	}

	// 3. Pay the sub-order on chain.
	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_e2e_draft",
	})
	if err != nil {
		t.Fatalf("order processing: %v", err)
	}

	// 4. Parent must be paid and queued for callback (notify_url is what
	//    TokenPilot relies on for webhook routing).
	parent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if parent.Status != mdb.StatusPaySuccess {
		t.Fatalf("parent status = %d, want %d (paid)", parent.Status, mdb.StatusPaySuccess)
	}
	if parent.CallBackConfirm != mdb.CallBackConfirmNo {
		t.Fatalf("parent callback_confirm = %d, want %d (queued for webhook)", parent.CallBackConfirm, mdb.CallBackConfirmNo)
	}
	if parent.OrderId != "order_e2e_draft" {
		t.Fatalf("parent merchant order_id changed to %q (must stay for webhook routing)", parent.OrderId)
	}
}
