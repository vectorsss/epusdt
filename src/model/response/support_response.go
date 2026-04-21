package response

type NetworkTokenSupport struct {
	Network string   `json:"network" example:"tron"`
	Tokens  []string `json:"tokens" example:"USDT,USDC"`
}

type SupportedAssetsResponse struct {
	Supports []NetworkTokenSupport `json:"supports"`
}
