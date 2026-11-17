package integration

type testVaultDaemonBootstrap struct {
	StateRoot        string `json:"state_root"`
	Password         []byte `json:"password"`
	ControlToken     []byte `json:"control_token"`
	APIListenAddress string `json:"api_listen_address,omitempty"`
}
