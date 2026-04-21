package driver

// Config holds driver configuration parsed from CLI flags.
type Config struct {
	Namespace            string
	SupervisorImage      string
	SupervisorBinaryPath string
	SupervisorMountPath  string
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
