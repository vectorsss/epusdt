package task

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/util/log"

	"github.com/ethereum/go-ethereum/common"
)

// chainEnabledWatchdog returns a cancellable context whose cancel() is
// invoked when either:
//  1. IsChainEnabled(network) returns false — admin disabled the chain
//  2. The enabled-token fingerprint changes — admin added/removed/
//     toggled a chain_tokens row for this network
//
// Both cases need the listener to exit so the outer loop can reconnect
// with the fresh token set (EVM WebSocket subscriptions are fixed at
// connect time; to pick up a new contract we must re-subscribe).
//
// initialFingerprint is the fingerprint computed BEFORE connecting; the
// watchdog compares every 10s tick against this baseline. Caller must
// defer the returned cancel func to release the goroutine.
func chainEnabledWatchdog(network, logPrefix, initialFingerprint string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !data.IsChainEnabled(network) {
					log.Sugar.Infof("%s chain disabled, stopping listener", logPrefix)
					cancel()
					return
				}
				if fp := chainTokenFingerprint(network); fp != initialFingerprint {
					log.Sugar.Infof("%s chain_tokens changed (was %q → now %q), reconnecting", logPrefix, initialFingerprint, fp)
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

// chainTokenFingerprint returns a stable string representing the
// enabled-token set for a network. Used by chainEnabledWatchdog to
// detect admin changes between polls.
func chainTokenFingerprint(network string) string {
	tokens, err := data.ListEnabledChainTokensByNetwork(network)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, strings.ToLower(strings.TrimSpace(t.ContractAddress))+"|"+strings.ToUpper(strings.TrimSpace(t.Symbol)))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// loadChainTokenContracts reads enabled tokens for a network and returns
// their contract addresses as ethereum-go common.Address values. Rows
// with blank contract_address (e.g. Solana native SOL marker) are
// skipped. Callers use the length to decide whether to connect or idle.
func loadChainTokenContracts(network, logPrefix string) []common.Address {
	tokens, err := data.ListEnabledChainTokensByNetwork(network)
	if err != nil {
		log.Sugar.Errorf("%s load chain_tokens err=%v", logPrefix, err)
		return nil
	}
	addrs := make([]common.Address, 0, len(tokens))
	for _, t := range tokens {
		c := strings.TrimSpace(t.ContractAddress)
		if c == "" {
			continue
		}
		addrs = append(addrs, common.HexToAddress(c))
	}
	return addrs
}

// resolveChainWsURL picks a healthy WS endpoint from rpc_nodes for the
// given network, falling back to the provided default (usually the
// public node URL) when no row is configured.
func resolveChainWsURL(network, fallback string) string {
	node, err := data.SelectRpcNode(network, mdb.RpcNodeTypeWs)
	if err == nil && node != nil && node.ID > 0 {
		return node.Url
	}
	return fallback
}
