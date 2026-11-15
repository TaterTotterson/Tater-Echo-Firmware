package hardwarebundle

import (
	"os"
	"testing"
)

func TestRuntimeDefaultsRemainTargetAndPresenceGated(t *testing.T) {
	for _, target := range []string{"checkers", "cronos", "crown", "athena", "rook", "crown-extra", ""} {
		values := runtimeEnvironmentDefaults(target, func(string) bool { return true })
		qualified := target == "checkers" || target == "cronos" || target == "crown" || target == "athena" || target == "rook"
		if (values["TATER_GPU_ENABLE"] == "1") != qualified || (values["TATER_VDEC_ENABLE"] == "1") != qualified {
			t.Fatal(target, values)
		}
	}
	values := runtimeEnvironmentDefaults("crown", func(string) bool { return false })
	if len(values) != 1 || values["TATER_FIRMWARE_TARGET"] != "crown" {
		t.Fatal(values)
	}
	t.Setenv("TATER_GPU_ENABLE", "0")
	t.Setenv("TATER_FIRMWARE_TARGET", "checkers")
	ConfigureEnvironment("crown")
	if os.Getenv("TATER_GPU_ENABLE") != "0" || os.Getenv("TATER_FIRMWARE_TARGET") != "checkers" {
		t.Fatal("explicit environment changed")
	}
}

func TestAthenaCodecsRequireMatchedBootMarkerAndBothPrivateRuntimes(t *testing.T) {
	paths := []string{"/run/tater-athena-ap.ready", "/data/tater-athena-codec/runtime.json", "/data/tater-athena-codec/omx-lib/runtime.json"}
	for _, missing := range append(paths, "") {
		values := runtimeEnvironmentDefaults("athena", func(path string) bool { return path != missing })
		if (values["TATER_VDEC_ENABLE"] == "1") != (missing == "") || (values["TATER_ATHENA_AP_ENABLE"] == "1") != (missing == "") {
			t.Fatal(missing, values)
		}
	}
}

func TestAthenaNeverEnablesVendorCodecAndNeedsItsOwnMesaRuntime(t *testing.T) {
	values := runtimeEnvironmentDefaults("athena", func(path string) bool { return path == "/data/tater-gpu/runtime.json" })
	if values["TATER_GPU_ENABLE"] != "" || values["TATER_VDEC_ENABLE"] != "" {
		t.Fatal("Athena accepted the Android runtime", values)
	}
}
