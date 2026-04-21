package driver

// Config holds driver configuration parsed from CLI flags.
type Config struct {
	Namespace            string
	SupervisorImage      string
	SupervisorBinaryPath string
	SupervisorMountPath  string

	// Gateway connection config. The driver injects these as env vars into
	// sandbox pods so the supervisor can connect back to the gateway.
	GatewayEndpoint    string
	SSHListenAddr      string
	SSHHandshakeSecret string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Namespace:            "openshell-system",
		SupervisorImage:      "quay.io/azaalouk/openshell-supervisor:latest",
		SupervisorBinaryPath: "/usr/local/bin/openshell-sandbox",
		SupervisorMountPath:  "/opt/openshell/bin",
	}
}
