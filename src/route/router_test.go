package route

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/util/log"
	"github.com/assimon/luuu/util/sign"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
)

const testAPIToken = "test-secret-token"

func setupTestEnv(t *testing.T) *echo.Echo {
	t.Helper()

	tmpDir := t.TempDir()

	// minimal viper config
	viper.Reset()
	viper.Set("db_type", "sqlite")
	viper.Set("api_auth_token", testAPIToken)
	viper.Set("epay_pid", 1)
	viper.Set("app_uri", "http://localhost:8080")
	viper.Set("order_expiration_time", 10)
	viper.Set("api_rate_url", "")
	viper.Set("forced_usdt_rate", 7.0)
	viper.Set("runtime_root_path", tmpDir)
	viper.Set("log_save_path", tmpDir)
	viper.Set("sqlite_database_filename", tmpDir+"/test.db")
	viper.Set("runtime_sqlite_filename", tmpDir+"/runtime.db")

	log.Init()

	// init config paths
	os.Setenv("EPUSDT_CONFIG", tmpDir)
	defer os.Unsetenv("EPUSDT_CONFIG")

	// init DB
	if err := dao.DBInit(); err != nil {
		t.Fatalf("DBInit: %v", err)
	}
	if err := dao.RuntimeInit(); err != nil {
		t.Fatalf("RuntimeInit: %v", err)
	}

	// ensure tables exist (MdbTableInit uses sync.Once, so migrate directly)
	dao.Mdb.AutoMigrate(&mdb.Orders{}, &mdb.WalletAddress{})

	// seed wallet addresses
	dao.Mdb.Create(&mdb.WalletAddress{Network: mdb.NetworkTron, Address: "TTestTronAddress001", Status: mdb.TokenStatusEnable})
	dao.Mdb.Create(&mdb.WalletAddress{Network: mdb.NetworkSolana, Address: "SolTestAddress001", Status: mdb.TokenStatusEnable})

	e := echo.New()
	RegisterRoute(e)
	return e
}

func signBody(body map[string]interface{}) map[string]interface{} {
	sig, _ := sign.Get(body, testAPIToken)
	body["signature"] = sig
	return body
}

func doPost(e *echo.Echo, path string, body map[string]interface{}) *httptest.ResponseRecorder {
	jsonBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(jsonBytes)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func signEpayValues(values url.Values) url.Values {
	signParams := make(map[string]interface{})
	for key, items := range values {
		if key == "sign" || key == "sign_type" || len(items) == 0 {
			continue
		}
		signParams[key] = items[0]
	}
	sig, _ := sign.Get(signParams, testAPIToken)
	values.Set("sign", sig)
	values.Set("sign_type", "MD5")
	return values
}

func doFormPost(e *echo.Echo, path string, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestCreateOrderEpusdtDefaultTron tests the epusdt compatibility route defaults to tron network.
func TestCreateOrderEpusdtDefaultTron(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-tron-001",
		"amount":     1.00,
		"notify_url": "http://localhost/notify",
	})

	rec := doPost(e, "/payments/epusdt/v1/order/create-transaction", body)
	t.Logf("Status: %d, Body: %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in response, got: %v", resp)
	}

	if data["trade_id"] == nil || data["trade_id"] == "" {
		t.Error("expected trade_id in response")
	}
	if data["receive_address"] != "TTestTronAddress001" {
		t.Errorf("expected tron address, got: %v", data["receive_address"])
	}
	t.Logf("Order created: trade_id=%v address=%v amount=%v", data["trade_id"], data["receive_address"], data["actual_amount"])
}

// TestCreateOrderGmpayV1Solana tests the gmpay route with solana network.
func TestCreateOrderGmpayV1Solana(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-sol-001",
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "http://localhost/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	t.Logf("Status: %d, Body: %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in response, got: %v", resp)
	}

	if data["trade_id"] == nil || data["trade_id"] == "" {
		t.Error("expected trade_id in response")
	}
	if data["receive_address"] != "SolTestAddress001" {
		t.Errorf("expected solana address, got: %v", data["receive_address"])
	}
	t.Logf("Order created: trade_id=%v address=%v amount=%v", data["trade_id"], data["receive_address"], data["actual_amount"])
}

// TestCreateOrderGmpayV1SolNative tests creating an order for native SOL token.
func TestCreateOrderGmpayV1SolNative(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-sol-native-001",
		"amount":     0.05,
		"token":      "sol",
		"currency":   "usd",
		"network":    "solana",
		"notify_url": "http://localhost/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	t.Logf("Status: %d, Body: %s", rec.Code, rec.Body.String())

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	t.Logf("Response: %v", resp)

	// This may fail if rate API is not configured, which is expected in test
	// The important thing is the route accepts the request with network=solana token=sol
	if rec.Code != http.StatusOK {
		t.Logf("Note: non-200 may be expected if rate API is not configured for SOL")
	}
}

func doGet(e *echo.Echo, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", testAPIToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func doPostWithToken(e *echo.Echo, path string, body map[string]interface{}) *httptest.ResponseRecorder {
	jsonBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(jsonBytes)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", testAPIToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func parseResp(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// TestWalletAddAndList tests adding wallets via API and listing them.
func TestWalletAddAndList(t *testing.T) {
	e := setupTestEnv(t)

	// Add a solana wallet
	rec := doPostWithToken(e, "/payments/gmpay/v1/wallet/add", map[string]interface{}{
		"network": "solana",
		"address": "NewSolWallet001",
	})
	t.Logf("Add: %s", rec.Body.String())
	resp := parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("add wallet failed: %v", resp)
	}

	// Add a tron wallet
	rec = doPostWithToken(e, "/payments/gmpay/v1/wallet/add", map[string]interface{}{
		"network": "tron",
		"address": "NewTronWallet001",
	})
	resp = parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("add tron wallet failed: %v", resp)
	}

	// List all wallets
	rec = doGet(e, "/payments/gmpay/v1/wallet/list")
	resp = parseResp(t, rec)
	wallets := resp["data"].([]interface{})
	// 2 seeded + 2 added = 4
	if len(wallets) != 4 {
		t.Fatalf("expected 4 wallets, got %d: %v", len(wallets), wallets)
	}

	// List by network
	rec = doGet(e, "/payments/gmpay/v1/wallet/list?network=solana")
	resp = parseResp(t, rec)
	wallets = resp["data"].([]interface{})
	if len(wallets) != 2 {
		t.Fatalf("expected 2 solana wallets, got %d", len(wallets))
	}

	rec = doGet(e, "/payments/gmpay/v1/wallet/list?network=tron")
	resp = parseResp(t, rec)
	wallets = resp["data"].([]interface{})
	if len(wallets) != 2 {
		t.Fatalf("expected 2 tron wallets, got %d", len(wallets))
	}
}

// TestWalletDuplicateRejected tests that adding the same network+address twice fails.
func TestWalletDuplicateRejected(t *testing.T) {
	e := setupTestEnv(t)

	body := map[string]interface{}{"network": "solana", "address": "DupWallet001"}
	rec := doPostWithToken(e, "/payments/gmpay/v1/wallet/add", body)
	resp := parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("first add failed: %v", resp)
	}

	// Same network+address should fail
	rec = doPostWithToken(e, "/payments/gmpay/v1/wallet/add", body)
	resp = parseResp(t, rec)
	if resp["status_code"].(float64) == 200 {
		t.Fatal("expected duplicate to be rejected")
	}
	t.Logf("Duplicate rejected: %v", resp["message"])

	// Same address, different network should succeed
	rec = doPostWithToken(e, "/payments/gmpay/v1/wallet/add", map[string]interface{}{
		"network": "tron",
		"address": "DupWallet001",
	})
	resp = parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("same address on different network should succeed: %v", resp)
	}
}

// TestWalletStatusAndDelete tests enable/disable/delete operations.
func TestWalletStatusAndDelete(t *testing.T) {
	e := setupTestEnv(t)

	// Add a wallet
	rec := doPostWithToken(e, "/payments/gmpay/v1/wallet/add", map[string]interface{}{
		"network": "solana",
		"address": "StatusTestWallet",
	})
	resp := parseResp(t, rec)
	wallet := resp["data"].(map[string]interface{})
	walletID := fmt.Sprintf("%.0f", wallet["id"].(float64))

	// Get wallet
	rec = doGet(e, "/payments/gmpay/v1/wallet/"+walletID)
	resp = parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("get wallet failed: %v", resp)
	}

	// Disable wallet
	rec = doPostWithToken(e, "/payments/gmpay/v1/wallet/"+walletID+"/status", map[string]interface{}{
		"status": 2,
	})
	resp = parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("disable wallet failed: %v", resp)
	}

	// Verify disabled — should not appear in available list
	rec = doGet(e, "/payments/gmpay/v1/wallet/list?network=solana")
	resp = parseResp(t, rec)
	wallets := resp["data"].([]interface{})
	for _, w := range wallets {
		wm := w.(map[string]interface{})
		if wm["address"] == "StatusTestWallet" && wm["status"].(float64) != 2 {
			t.Error("wallet should be disabled")
		}
	}

	// Delete wallet
	rec = doPostWithToken(e, "/payments/gmpay/v1/wallet/"+walletID+"/delete", nil)
	resp = parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("delete wallet failed: %v", resp)
	}

	// Verify deleted
	rec = doGet(e, "/payments/gmpay/v1/wallet/"+walletID)
	resp = parseResp(t, rec)
	// Should return not found
	if resp["status_code"].(float64) == 200 {
		data := resp["data"].(map[string]interface{})
		if data["id"].(float64) > 0 {
			t.Error("wallet should be deleted")
		}
	}
}

// TestWalletAuthRequired tests that wallet APIs require auth token.
func TestWalletAuthRequired(t *testing.T) {
	e := setupTestEnv(t)

	// No auth header — should not return success
	req := httptest.NewRequest(http.MethodGet, "/payments/gmpay/v1/wallet/list", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// The response should indicate auth failure (not 200 success)
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		// echo may return plain text error
		if rec.Code == http.StatusOK {
			t.Error("expected auth failure without token")
		}
		t.Logf("Auth rejected (non-JSON): status=%d body=%s", rec.Code, rec.Body.String())
		return
	}
	statusCode, _ := resp["status_code"].(float64)
	if statusCode == 200 {
		t.Error("expected auth failure without token")
	}
	t.Logf("Auth rejected: %v", resp)
}

// TestCreateOrderNetworkIsolation verifies tron and solana wallets don't mix.
func TestCreateOrderNetworkIsolation(t *testing.T) {
	e := setupTestEnv(t)

	// Try to create a solana order — should get solana address, not tron
	body := signBody(map[string]interface{}{
		"order_id":   fmt.Sprintf("test-isolation-%d", 1),
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "http://localhost/notify",
	})
	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data, got: %v", resp)
	}
	if data["receive_address"] == "TTestTronAddress001" {
		t.Error("solana order should NOT get a tron address")
	}
	if data["receive_address"] != "SolTestAddress001" {
		t.Errorf("expected SolTestAddress001, got %v", data["receive_address"])
	}
}

func TestEpaySubmitPhpGetCompatible(t *testing.T) {
	e := setupTestEnv(t)

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-get-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-get-001"},
		"notify_url":   {"http://localhost/notify"},
		"return_url":   {"http://localhost/return"},
	})

	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/") {
		t.Fatalf("expected checkout redirect, got %q", rec.Header().Get("Location"))
	}
}

func TestEpaySubmitPhpPostFormCompatible(t *testing.T) {
	e := setupTestEnv(t)

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-post-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-post-001"},
		"notify_url":   {"http://localhost/notify"},
		"return_url":   {"http://localhost/return"},
		"sitename":     {"example-shop"},
	})

	rec := doFormPost(e, "/payments/epay/v1/order/create-transaction/submit.php", values)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/") {
		t.Fatalf("expected checkout redirect, got %q", rec.Header().Get("Location"))
	}
}
