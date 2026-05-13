package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/shopspring/decimal"
	tonaddress "github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
)

// TON listener uses the same cron-driven polling shape as the Solana
// listener. Each tick: connect (cached), walk an owner's recent
// transactions backwards from the chain tip, decode incoming TON or
// Jetton transfers, and match against active transaction_lock rows.
//
// LiteServer connection setup is expensive (ADNL handshake + chain proof
// bootstrapping) so the API client is built once and reused. The client
// is rebuilt only when the configured rpc_nodes URL changes.
const (
	tonListLimit         = 32
	tonRequestTimeout    = 30 * time.Second
	tonProcessedCacheTTL = 1 * time.Hour
	// TON native uses 9 decimals; Jetton master entries carry their own decimals.
	tonNativeDecimals = 9
)

var (
	gTonAPIMu        sync.Mutex
	gTonAPI          ton.APIClientWrapped
	gTonAPIConfigURL string

	gProcessedTonTx    sync.Map // hex(hash) -> unix ts
	gTonJettonWalletMu sync.Mutex
	gTonJettonWallet   = map[string]*tonaddress.Address{} // owner.StringRaw()+"|"+master.StringRaw() -> jetton wallet addr
)

// TonCallBack scans a single owner address for inbound payments and
// confirms any matching pending orders. Errors are logged and
// swallowed; the next tick will retry.
func TonCallBack(ownerAddrStr string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TON][%s] panic recovered: %v", ownerAddrStr, err)
		}
	}()

	api, err := ensureTonAPI()
	if err != nil {
		log.Sugar.Errorf("[TON][%s] init api err=%v", ownerAddrStr, err)
		return
	}

	ownerAddr, err := tonaddress.ParseAddr(ownerAddrStr)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] parse owner addr err=%v", ownerAddrStr, err)
		return
	}

	tokens, err := data.ListEnabledChainTokensByNetwork(mdb.NetworkTon)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] load chain_tokens err=%v", ownerAddrStr, err)
		return
	}
	if len(tokens) == 0 {
		log.Sugar.Debugf("[TON][%s] no enabled chain_tokens, skipping", ownerAddrStr)
		return
	}

	// Partition tokens: a row with symbol=TON and empty contract_address
	// enables native TON acceptance; rows with a non-empty contract_address
	// are Jetton masters. Anything else is ignored.
	var nativeToken *mdb.ChainToken
	jettonTokens := make(map[string]*mdb.ChainToken, len(tokens))
	for i := range tokens {
		sym := strings.ToUpper(strings.TrimSpace(tokens[i].Symbol))
		contract := strings.TrimSpace(tokens[i].ContractAddress)
		if sym == "TON" && contract == "" {
			nativeToken = &tokens[i]
			continue
		}
		if contract == "" {
			continue
		}
		jettonTokens[contract] = &tokens[i]
	}

	cleanupTonProcessedCache()

	ctx, cancel := context.WithTimeout(context.Background(), tonRequestTimeout)
	defer cancel()

	master, err := api.CurrentMasterchainInfo(ctx)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] masterchain info err=%v", ownerAddrStr, err)
		return
	}

	// Resolve each enabled Jetton's wallet contract address for this owner.
	// The address is deterministic from (master_contract, owner) so we cache
	// across ticks to avoid repeated get-method calls.
	jettonWalletToToken := make(map[string]*mdb.ChainToken, len(jettonTokens))
	for masterStr, tk := range jettonTokens {
		jwAddr, err := resolveJettonWalletAddress(ctx, api, master, ownerAddr, masterStr)
		if err != nil {
			log.Sugar.Errorf("[TON][%s] resolve jetton wallet master=%s err=%v", ownerAddrStr, masterStr, err)
			continue
		}
		jettonWalletToToken[jwAddr.StringRaw()] = tk
	}

	acc, err := api.GetAccount(ctx, master, ownerAddr)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] get account err=%v", ownerAddrStr, err)
		return
	}
	if !acc.IsActive || acc.LastTxLT == 0 {
		log.Sugar.Debugf("[TON][%s] account not active or no transactions yet", ownerAddrStr)
		return
	}

	cutoff := time.Now().Add(-config.GetOrderExpirationTimeDuration() - 5*time.Minute).Unix()
	lt := acc.LastTxLT
	txHash := acc.LastTxHash
	scanned := 0

	for lt > 0 && len(txHash) > 0 {
		txs, err := api.ListTransactions(ctx, ownerAddr, tonListLimit, lt, txHash)
		if err != nil {
			if errors.Is(err, ton.ErrNoTransactionsWereFound) {
				break
			}
			log.Sugar.Errorf("[TON][%s] list transactions err=%v", ownerAddrStr, err)
			return
		}
		if len(txs) == 0 {
			break
		}

		// ListTransactions returns oldest-first; walk newest-first.
		for i := len(txs) - 1; i >= 0; i-- {
			tx := txs[i]
			scanned++

			if int64(tx.Now) < cutoff {
				log.Sugar.Debugf("[TON][%s] tx now=%d before cutoff=%d, stopping scan", ownerAddrStr, tx.Now, cutoff)
				return
			}

			hashKey := hex.EncodeToString(tx.Hash)
			if _, dup := gProcessedTonTx.Load(hashKey); dup {
				continue
			}

			processTonTransaction(tx, ownerAddrStr, nativeToken, jettonWalletToToken)
			gProcessedTonTx.Store(hashKey, time.Now().Unix())
		}

		// Step to the next page; the oldest tx in this batch carries the
		// pointer to its predecessor.
		oldest := txs[0]
		if oldest.PrevTxLT == 0 || len(oldest.PrevTxHash) == 0 {
			break
		}
		lt = oldest.PrevTxLT
		txHash = oldest.PrevTxHash
	}

	log.Sugar.Debugf("[TON][%s] scan complete, processed %d transactions", ownerAddrStr, scanned)
}

// processTonTransaction inspects a single transaction's incoming
// internal message and, if it represents a TON or Jetton transfer to one
// of our enabled tokens, attempts to match a pending order.
func processTonTransaction(tx *tlb.Transaction, ownerAddrStr string,
	nativeToken *mdb.ChainToken, jettonWalletToToken map[string]*mdb.ChainToken) {
	if tx.IO.In == nil || tx.IO.In.MsgType != tlb.MsgTypeInternal {
		return
	}
	ti := tx.IO.In.AsInternal()
	if ti == nil {
		return
	}

	// Skip bounced messages — these are refunds, not new income.
	if dsc, ok := tx.Description.(tlb.TransactionDescriptionOrdinary); ok && dsc.BouncePhase != nil {
		if _, bounced := dsc.BouncePhase.Phase.(tlb.BouncePhaseOk); bounced {
			return
		}
	}

	txHashHex := hex.EncodeToString(tx.Hash)
	blockTime := int64(ti.CreatedAt)
	if blockTime <= 0 {
		blockTime = int64(tx.Now)
	}

	// Jetton transfer arrives as an internal message from our own jetton
	// wallet contract carrying a transfer_notification body. Match the
	// source against the cached jetton wallets first.
	if ti.SrcAddr != nil {
		if tk := jettonWalletToToken[ti.SrcAddr.StringRaw()]; tk != nil {
			var notif jetton.TransferNotification
			if err := tlb.LoadFromCell(&notif, ti.Body.BeginParse()); err != nil {
				log.Sugar.Warnf("[TON][%s] tx=%s decode jetton transfer err=%v", ownerAddrStr, txHashHex, err)
				return
			}
			amount := adjustTonAmount(notif.Amount.Nano(), tk.Decimals)
			senderStr := ""
			if notif.Sender != nil {
				senderStr = notif.Sender.String()
			}
			matchAndConfirmTonPayment(ownerAddrStr, txHashHex, blockTime, tk, amount, senderStr)
			return
		}
	}

	// Native TON transfer — the message carries non-zero value and the
	// admin enabled a TON-symbol row with no contract_address.
	if nativeToken == nil {
		return
	}
	nano := ti.Amount.Nano()
	if nano == nil || nano.Sign() <= 0 {
		return
	}
	amount := adjustTonAmount(nano, tonNativeDecimals)
	senderStr := ""
	if ti.SrcAddr != nil {
		senderStr = ti.SrcAddr.String()
	}
	matchAndConfirmTonPayment(ownerAddrStr, txHashHex, blockTime, nativeToken, amount, senderStr)
}

func matchAndConfirmTonPayment(ownerAddrStr, txHashHex string, blockTime int64,
	token *mdb.ChainToken, amount float64, sender string) {
	symbol := strings.ToUpper(strings.TrimSpace(token.Symbol))
	if amount <= 0 {
		return
	}
	if token.MinAmount > 0 && amount < token.MinAmount {
		return
	}

	log.Sugar.Infof("[TON][%s] tx=%s incoming %s amount=%.6f from=%s -> matching",
		ownerAddrStr, txHashHex, symbol, amount, sender)

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, ownerAddrStr, symbol, amount)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] tx=%s lock lookup err=%v", ownerAddrStr, txHashHex, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Infof("[TON][%s] tx=%s no active transaction_lock: token=%s amount=%.6f",
			ownerAddrStr, txHashHex, symbol, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] tx=%s load order err=%v", ownerAddrStr, txHashHex, err)
		return
	}

	// Reject payments older than the order itself.
	createdMs := order.CreatedAt.TimestampMilli()
	blockMs := blockTime * 1000
	if blockMs < createdMs {
		log.Sugar.Warnf("[TON][%s] tx=%s skipped: block_time_ms=%d before order_created_ms=%d",
			ownerAddrStr, txHashHex, blockMs, createdMs)
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     ownerAddrStr,
		Token:              symbol,
		Network:            mdb.NetworkTon,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHashHex,
	}
	if err := OrderProcessing(req); err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[TON][%s] tx=%s already resolved: trade_id=%s reason=%v",
				ownerAddrStr, txHashHex, tradeID, err)
			return
		}
		log.Sugar.Errorf("[TON][%s] tx=%s OrderProcessing failed trade_id=%s err=%v",
			ownerAddrStr, txHashHex, tradeID, err)
		return
	}
	log.Sugar.Infof("[TON][%s] order marked paid: trade_id=%s tx=%s token=%s amount=%.6f",
		ownerAddrStr, tradeID, txHashHex, symbol, amount)
	sendPaymentNotification(order)
}

func adjustTonAmount(nano *big.Int, decimals int) float64 {
	if nano == nil || nano.Sign() == 0 {
		return 0
	}
	if decimals <= 0 {
		decimals = tonNativeDecimals
	}
	d := decimal.NewFromBigInt(nano, 0)
	divisor := decimal.New(1, int32(decimals))
	adjusted := d.Div(divisor)
	return math.MustParsePrecFloat64(adjusted.InexactFloat64(), data.MaxAmountPrecision)
}

func cleanupTonProcessedCache() {
	cutoff := time.Now().Add(-tonProcessedCacheTTL).Unix()
	gProcessedTonTx.Range(func(k, v interface{}) bool {
		if ts, ok := v.(int64); ok && ts < cutoff {
			gProcessedTonTx.Delete(k)
		}
		return true
	})
}

// resolveJettonWalletAddress derives the owner's jetton wallet contract
// address for a given Jetton master. The result is cached across ticks
// because the derivation is deterministic and a get-method call costs
// one round-trip to a lite server.
func resolveJettonWalletAddress(ctx context.Context, api ton.APIClientWrapped,
	master *ton.BlockIDExt, owner *tonaddress.Address, masterStr string) (*tonaddress.Address, error) {
	masterAddr, err := tonaddress.ParseAddr(masterStr)
	if err != nil {
		if masterAddr, err = tonaddress.ParseRawAddr(masterStr); err != nil {
			return nil, fmt.Errorf("parse master addr %q: %w", masterStr, err)
		}
	}
	key := owner.StringRaw() + "|" + masterAddr.StringRaw()

	gTonJettonWalletMu.Lock()
	cached, ok := gTonJettonWallet[key]
	gTonJettonWalletMu.Unlock()
	if ok && cached != nil {
		return cached, nil
	}

	jc := jetton.NewJettonMasterClient(api, masterAddr)
	wallet, err := jc.GetJettonWalletAtBlock(ctx, owner, master)
	if err != nil {
		return nil, err
	}
	addr := wallet.Address()

	gTonJettonWalletMu.Lock()
	gTonJettonWallet[key] = addr
	gTonJettonWalletMu.Unlock()
	return addr, nil
}

func ensureTonAPI() (ton.APIClientWrapped, error) {
	cfgURL, err := resolveTonConfigURL()
	if err != nil {
		return nil, err
	}

	gTonAPIMu.Lock()
	defer gTonAPIMu.Unlock()
	if gTonAPI != nil && gTonAPIConfigURL == cfgURL {
		return gTonAPI, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), tonRequestTimeout)
	defer cancel()

	cfg, err := liteclient.GetConfigFromUrl(ctx, cfgURL)
	if err != nil {
		return nil, fmt.Errorf("fetch ton config %q: %w", cfgURL, err)
	}

	pool := liteclient.NewConnectionPool()
	if err := pool.AddConnectionsFromConfig(ctx, cfg); err != nil {
		return nil, fmt.Errorf("connect to lite servers: %w", err)
	}

	api := ton.NewAPIClient(pool, ton.ProofCheckPolicyFast).WithRetry()
	api.SetTrustedBlockFromConfig(cfg)

	gTonAPI = api
	gTonAPIConfigURL = cfgURL

	// Bust the jetton-wallet cache: a new config could imply a network
	// switch (mainnet <-> testnet) where derivations stay the same per
	// owner+master, but cached pointers reference the old pool.
	gTonJettonWalletMu.Lock()
	gTonJettonWallet = map[string]*tonaddress.Address{}
	gTonJettonWalletMu.Unlock()

	log.Sugar.Infof("[TON] initialized API client from config %s", cfgURL)
	return api, nil
}

func resolveTonConfigURL() (string, error) {
	node, err := data.SelectRpcNode(mdb.NetworkTon, mdb.RpcNodeTypeHttp)
	if err != nil {
		return "", err
	}
	if node == nil || node.ID == 0 {
		return "", fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes (set the URL of the TON global.config.json)",
			mdb.NetworkTon, mdb.RpcNodeTypeHttp)
	}
	url := strings.TrimSpace(node.Url)
	if url == "" {
		return "", fmt.Errorf("rpc_nodes id=%d has empty url", node.ID)
	}
	return url, nil
}
