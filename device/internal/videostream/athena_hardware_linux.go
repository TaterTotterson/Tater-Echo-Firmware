//go:build linux

package videostream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/hardwarebundle"
)

const (
	athenaCodecRoot        = "/data/tater-athena-codec"
	athenaVDECHelperSHA256 = "2a342b28b72ce7096df006afb1785d9c18c53924697a12c366084775e6ff51a9"
	athenaVDECTreeSHA256   = "efcfe0d53924554af190c7e9f8068f190e1af3620c0346efceed3a81890b23bc"
)

// Every dependency is target-owned and checked, not only the component.
// The original helper receives one local component and framed bytes, never URLs.
var athenaVDECRuntimeFiles = map[string]string{
	"lib/libbase.so":           "ce5af18fea3001895a079e74dc712f9c6473e19c7b7657dc3b5a2733c4f27b3e",
	"lib/libbwc.so":            "d007c563b78b3ad8e7f552780f6b6adb9f4fdd906d426a169a3ee2bc1bf81d41",
	"lib/libc++.so":            "812b7edee3e7ebed369a9dac66921c93098e15617c995e3f58e7143e3aac522e",
	"lib/libc.so":              "7585ba7ce44cb14d3e7bbaa40aca4a3e7e8c97b8c76e2b9514c1c53c5bf97ca2",
	"lib/libcutils.so":         "04e41f96159bbcf30255e1c69b8b70218efc20ccc301ab27e7a07248964f0ebe",
	"lib/libdl.so":             "01ff90be229ce00bf68bc25447a2ce5162853d041c0e4e6a7492de4546446b81",
	"lib/libion.so":            "de3e597edc16f5a8de1a025a4ef31777a582545cd81979bcbf4aeff63afa8626",
	"lib/libion_mtk.so":        "ba52be04219104bcaa280f623d217e7dc3234e242784cbb86827103fd9f57c84",
	"lib/libion_ulit.so":       "375023230fbe32e5d6e9c722f53bf6cba2c210e90f5e6ceef15b6181a6341c0f",
	"lib/libladder.so":         "4afda9301105380f693c2b8d7c2ee4a5e5a80d1d4cc8bee8543daa3a34f3de6e",
	"lib/liblog.so":            "f7974d3f1843fc2d9a708ae79d0a16d7c7e3a1cf7b620b6c5ec7e54813a3f9b9",
	"lib/liblzma.so":           "2f8e3b417bb82d94b6893046b6329a1fcfb27f53f451212868cc30247152ac70",
	"lib/libm.so":              "ac4d03ac3ebde8520cf933079c210ae07e0d3eff2a35549fb0ab22409f8ab00e",
	"lib/libmp4enc_sa.ca7.so":  "4f720a6708072ffea7891d3a3399886e408059ca13900d626cfd2ce32679743e",
	"lib/libmtk_drvb.so":       "06f3a8bdcdef0183f6794b245d5cce0faec07c5ae0c30f38c343abb8c6527a9b",
	"lib/libunwindstack.so":    "87861c713ae6fcee92aa59c8ff8b5022e075a43a9fbc50533baf5f5164ca2a44",
	"lib/libutils.so":          "aa897d4311500863882b4901837902baae3a5f837ae6f67ebb972afba339a4ca",
	"lib/libvc1dec_sa.ca7.so":  "9d04bfa2ec630ab0e0a0483c8c7bf3dd5b13ca3a61beb2a2ca22610cfbde299e",
	"lib/libvcodec_oal.so":     "d8e0d6f8ad1f752756a8c18975afc7071dea9d022f2b67caf1e1db1211c7c2e1",
	"lib/libvcodec_utility.so": "47c6979ab704b3885ad522b83b68b492e69eb341741814cd28b24d238ca83218",
	"lib/libvcodecdrv.so":      "8f3eff348d8254a846695985905a63f8d93dc7fb9dccc1f2712b8935172d4e7f",
	"lib/libvndksupport.so":    "d415f8ae4ab630bfe6f809e72f14317a727156b8c607a1e9fe49515661294096",
	"lib/libvp8dec_sa.ca7.so":  "61d352aae0886412ac2e7ed155d1d62c9b0612bb6b9869973aca00fc464060a2",
	"linker":                   "6a9d2fd130c8f4c89ae55ded7095308a023922b2f0777e41a7e71d438e4f50f8",
}

func athenaAPDecoderConfig() hardwareDecoderConfig {
	return hardwareDecoderConfig{
		helper:      environmentValue("TATER_VDEC_HELPER", hardwarebundle.Helper("tater-hw-vdec")),
		component:   athenaCodecRoot + "/lib/libvcodecdrv.so",
		libraryPath: athenaCodecRoot + "/lib",
		manifest:    athenaCodecRoot + "/runtime.json",
		target:      "athena",
	}
}

type athenaVDECManifest struct {
	Schema       int    `json:"schema"`
	Target       string `json:"target"`
	RuntimeID    string `json:"runtime_id"`
	Experimental bool   `json:"experimental"`
	TreeSHA256   string `json:"tree_sha256"`
	FileCount    int    `json:"file_count"`
	RuntimeBytes int64  `json:"runtime_bytes"`
}

func validateAthenaVDECManifest(value []byte) error {
	var manifest athenaVDECManifest
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return errors.New("invalid Athena AP runtime manifest")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing Athena AP runtime manifest")
	}
	if manifest.Schema != 1 || manifest.Target != "athena" || manifest.RuntimeID != "athena-mt8183-ap-vdec-1" ||
		!manifest.Experimental || manifest.TreeSHA256 != athenaVDECTreeSHA256 ||
		manifest.FileCount != 24 || manifest.RuntimeBytes != 6042836 {
		return errors.New("Athena AP decoder runtime identity is not allowlisted")
	}
	return nil
}

func validateAthenaVDECRuntime(config hardwareDecoderConfig) error {
	if config.component != athenaCodecRoot+"/lib/libvcodecdrv.so" || config.libraryPath != athenaCodecRoot+"/lib" ||
		config.manifest != athenaCodecRoot+"/runtime.json" {
		return errors.New("Athena AP runtime paths must remain isolated")
	}
	if !athenaOrdinaryFile(config.manifest, vdecManifestMaxBytes) {
		return errors.New("Athena AP runtime manifest must be an ordinary bounded file")
	}
	manifest, err := boundedFile(config.manifest, vdecManifestMaxBytes)
	if err != nil {
		return err
	}
	if err = validateAthenaVDECManifest(manifest); err != nil {
		return err
	}
	if !athenaPinnedFile(config.helper, athenaVDECHelperSHA256) {
		return errors.New("Athena AP decoder helper is not allowlisted")
	}
	for name, expected := range athenaVDECRuntimeFiles {
		if !athenaPinnedFile(filepath.Join(athenaCodecRoot, name), expected) {
			return errors.New("Athena AP decoder dependency is not allowlisted: " + name)
		}
	}
	return nil
}

func athenaOrdinaryFile(path string, maximum int64) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= maximum
}

func athenaPinnedFile(path, expected string) bool {
	if !athenaOrdinaryFile(path, 8*1024*1024) {
		return false
	}
	hash, err := fileSHA256(path)
	return err == nil && hash == expected
}

func validateVDECCapabilitiesForTarget(value []byte, target string) error {
	if target != "athena" {
		return validateVDECCapabilities(value)
	}
	var capabilities struct {
		vdecCapabilities
		Profiles        []string `json:"profiles"`
		SingleSliceOnly bool     `json:"single_slice_only"`
		LiveOnly        bool     `json:"live_only"`
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if len(value) > 16*1024 || decoder.Decode(&capabilities) != nil {
		return errors.New("invalid Athena AP capabilities")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing Athena AP capabilities")
	}
	if capabilities.Adapter != "tater-athena-ap-vdec" || capabilities.Version != 1 || capabilities.Codec != "h264" ||
		len(capabilities.StreamVersions) != 1 || capabilities.StreamVersions[0] != 2 || capabilities.StreamV2Output != "rgba" ||
		capabilities.MaxWidth != 1280 || capabilities.MaxHeight != 960 || len(capabilities.Profiles) != 1 ||
		capabilities.Profiles[0] != "baseline" || !capabilities.SingleSliceOnly || !capabilities.LiveOnly {
		return errors.New("Athena AP decoder must remain Baseline single-slice live-only")
	}
	return nil
}
