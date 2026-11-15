package hardwarebundle

import "os"

// ConfigureEnvironment lets an application update use its own qualified
// sidecars even when started by an older immutable supervisor. Explicit
// operator overrides (including "0") are retained. Hardware packages still
// independently validate targets, manifests, hashes and device metadata.
func ConfigureEnvironment(target string) {
	for key, value := range runtimeEnvironmentDefaults(target, func(path string) bool {
		info, err := os.Lstat(path)
		return err == nil && info.Mode().IsRegular()
	}) {
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

func runtimeEnvironmentDefaults(target string, available func(string) bool) map[string]string {
	values := map[string]string{}
	if target == "athena" {
		values["TATER_FIRMWARE_TARGET"] = target
		if available("/data/tater-mesa/runtime.json") || available("/usr/lib/tater-mesa/runtime.json") {
			values["TATER_GPU_ENABLE"] = "1"
		}
		// Only a matched system boot can create this ephemeral readiness
		// marker, after loading the target/kernel-pinned original adapters.
		// Runtime manifests by themselves must never enable AP hardware.
		if available("/run/tater-athena-ap.ready") && available("/data/tater-athena-codec/runtime.json") &&
			available("/data/tater-athena-codec/omx-lib/runtime.json") {
			values["TATER_ATHENA_AP_ENABLE"], values["TATER_VDEC_ENABLE"] = "1", "1"
		}
		return values
	}
	if target != "checkers" && target != "cronos" && target != "crown" && target != "rook" {
		return values
	}
	values["TATER_FIRMWARE_TARGET"] = target
	for key, path := range map[string]string{"TATER_GPU_ENABLE": "/data/tater-gpu/runtime.json", "TATER_VDEC_ENABLE": "/data/tater-vdec/runtime.json"} {
		if available(path) {
			values[key] = "1"
		}
	}
	return values
}
