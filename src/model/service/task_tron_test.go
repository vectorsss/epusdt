package service

import (
	"testing"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/internal/testutil"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
)

// TestResolveTronNode_NoRow verifies that resolveTronNode falls back to the
// hard-coded default when the rpc_nodes table has no TRON row.
func TestResolveTronNode_NoRow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	config.TRON_GRID_API_KEY = "fallback-key"

	gotURL, gotKey := resolveTronNode()
	if gotURL != tronNodeDefault {
		t.Errorf("url = %q, want %q", gotURL, tronNodeDefault)
	}
	if gotKey != "fallback-key" {
		t.Errorf("apiKey = %q, want \"fallback-key\"", gotKey)
	}
}

// TestResolveTronNode_WithRow verifies that resolveTronNode uses the DB row
// when an enabled TRON HTTP node is present.
func TestResolveTronNode_WithRow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	config.TRON_GRID_API_KEY = "fallback-key"

	// Insert an enabled TRON http node.
	node := &mdb.RpcNode{
		Network: mdb.NetworkTron,
		Url:     "https://custom-tron.example.com",
		Type:    mdb.RpcNodeTypeHttp,
		ApiKey:  "db-api-key",
		Weight:  1,
		Enabled: true,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		t.Fatalf("seed rpc_node: %v", err)
	}

	gotURL, gotKey := resolveTronNode()
	if gotURL != "https://custom-tron.example.com" {
		t.Errorf("url = %q, want https://custom-tron.example.com", gotURL)
	}
	if gotKey != "db-api-key" {
		t.Errorf("apiKey = %q, want \"db-api-key\"", gotKey)
	}
}

// TestResolveTronNode_DisabledRow verifies that a disabled row is ignored and
// the fallback is used.
func TestResolveTronNode_DisabledRow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	config.TRON_GRID_API_KEY = "fallback-key"

	node := &mdb.RpcNode{
		Network: mdb.NetworkTron,
		Url:     "https://disabled-tron.example.com",
		Type:    mdb.RpcNodeTypeHttp,
		ApiKey:  "disabled-key",
		Weight:  1,
		Enabled: true, // start enabled, then disable below
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		t.Fatalf("seed rpc_node: %v", err)
	}
	// Explicitly disable — GORM does not save bool zero-value on Create.
	if err := dao.Mdb.Model(node).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable rpc_node: %v", err)
	}

	gotURL, gotKey := resolveTronNode()
	if gotURL != tronNodeDefault {
		t.Errorf("url = %q, want %q (disabled row should be ignored)", gotURL, tronNodeDefault)
	}
	if gotKey != "fallback-key" {
		t.Errorf("apiKey = %q, want \"fallback-key\"", gotKey)
	}
}
