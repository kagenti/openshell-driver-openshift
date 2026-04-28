package driver

type Config struct {
	Namespace            string
	Tenant               string // openshell.ai/tenant and kagenti.io/team label value; defaults to Namespace if empty
	SupervisorImage      string
	SupervisorBinaryPath string
	DtachBinaryPath      string
	SupervisorMountPath  string
	GatewayEndpoint      string
}

func DefaultConfig() Config {
	return Config{
		Namespace:            "openshell-system",
		SupervisorImage:      "quay.io/azaalouk/openshell-supervisor:latest",
		SupervisorBinaryPath: "/usr/local/bin/openshell-sandbox",
		DtachBinaryPath:      "/usr/local/bin/dtach",
		SupervisorMountPath:  "/opt/openshell/bin",
	}
}
