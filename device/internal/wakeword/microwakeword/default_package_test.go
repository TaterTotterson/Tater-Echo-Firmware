package microwakeword

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureEmbeddedDefaultPackageInstallsPairedModelsAndRepairsDamage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TATER_MWW_DIR", dir)

	updated, err := EnsureEmbeddedDefaultPackage()
	if err != nil || !updated {
		t.Fatalf("first install updated=%v err=%v", updated, err)
	}
	for _, name := range []string{
		defaultManifestFilename,
		defaultModelFilename,
		defaultOWWMetadataFilename,
		defaultOWWONNXFilename,
		defaultBundleFilename,
	} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			t.Fatalf("installed %s info=%v err=%v", name, info, err)
		}
	}
	if paired, err := PairedCompanionPackage(DefaultPackage); err != nil || paired != DefaultPackage {
		t.Fatalf("paired=%q err=%v", paired, err)
	}
	updated, err = EnsureEmbeddedDefaultPackage()
	if err != nil || updated {
		t.Fatalf("cached install updated=%v err=%v", updated, err)
	}
	if err := os.WriteFile(filepath.Join(dir, defaultOWWONNXFilename), []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err = EnsureEmbeddedDefaultPackage()
	if err != nil || !updated || !CompanionReady(DefaultPackage) {
		t.Fatalf("repair updated=%v ready=%v err=%v", updated, CompanionReady(DefaultPackage), err)
	}
}
