package main

// Runtime identities only; owner-extracted binaries never enter this repository.
import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const athenaEncoderRoot = "/data/tater-athena-codec"
const athenaVencHelperHash = "ecba5f5291797fb57b9930da757324de9b25973672fd3afa6b50783e79145de5"

var athenaEncoderFiles = map[string]string{
	"/lib/libbase.so":           "ce5af18fea3001895a079e74dc712f9c6473e19c7b7657dc3b5a2733c4f27b3e",
	"/lib/libbwc.so":            "d007c563b78b3ad8e7f552780f6b6adb9f4fdd906d426a169a3ee2bc1bf81d41",
	"/lib/libc.so":              "7585ba7ce44cb14d3e7bbaa40aca4a3e7e8c97b8c76e2b9514c1c53c5bf97ca2",
	"/lib/libc++.so":            "812b7edee3e7ebed369a9dac66921c93098e15617c995e3f58e7143e3aac522e",
	"/lib/libcutils.so":         "04e41f96159bbcf30255e1c69b8b70218efc20ccc301ab27e7a07248964f0ebe",
	"/lib/libdl.so":             "01ff90be229ce00bf68bc25447a2ce5162853d041c0e4e6a7492de4546446b81",
	"/lib/libion_mtk.so":        "ba52be04219104bcaa280f623d217e7dc3234e242784cbb86827103fd9f57c84",
	"/lib/libion_ulit.so":       "375023230fbe32e5d6e9c722f53bf6cba2c210e90f5e6ceef15b6181a6341c0f",
	"/lib/libion.so":            "de3e597edc16f5a8de1a025a4ef31777a582545cd81979bcbf4aeff63afa8626",
	"/lib/libladder.so":         "4afda9301105380f693c2b8d7c2ee4a5e5a80d1d4cc8bee8543daa3a34f3de6e",
	"/lib/liblog.so":            "f7974d3f1843fc2d9a708ae79d0a16d7c7e3a1cf7b620b6c5ec7e54813a3f9b9",
	"/lib/liblzma.so":           "2f8e3b417bb82d94b6893046b6329a1fcfb27f53f451212868cc30247152ac70",
	"/lib/libm.so":              "ac4d03ac3ebde8520cf933079c210ae07e0d3eff2a35549fb0ab22409f8ab00e",
	"/lib/libmp4enc_sa.ca7.so":  "4f720a6708072ffea7891d3a3399886e408059ca13900d626cfd2ce32679743e",
	"/lib/libmtk_drvb.so":       "06f3a8bdcdef0183f6794b245d5cce0faec07c5ae0c30f38c343abb8c6527a9b",
	"/lib/libunwindstack.so":    "87861c713ae6fcee92aa59c8ff8b5022e075a43a9fbc50533baf5f5164ca2a44",
	"/lib/libutils.so":          "aa897d4311500863882b4901837902baae3a5f837ae6f67ebb972afba339a4ca",
	"/lib/libvc1dec_sa.ca7.so":  "9d04bfa2ec630ab0e0a0483c8c7bf3dd5b13ca3a61beb2a2ca22610cfbde299e",
	"/lib/libvcodec_oal.so":     "d8e0d6f8ad1f752756a8c18975afc7071dea9d022f2b67caf1e1db1211c7c2e1",
	"/lib/libvcodec_utility.so": "47c6979ab704b3885ad522b83b68b492e69eb341741814cd28b24d238ca83218",
	"/lib/libvcodecdrv.so":      "8f3eff348d8254a846695985905a63f8d93dc7fb9dccc1f2712b8935172d4e7f",
	"/lib/libvndksupport.so":    "d415f8ae4ab630bfe6f809e72f14317a727156b8c607a1e9fe49515661294096",
	"/lib/libvp8dec_sa.ca7.so":  "61d352aae0886412ac2e7ed155d1d62c9b0612bb6b9869973aca00fc464060a2",
	"/linker":                   "6a9d2fd130c8f4c89ae55ded7095308a023922b2f0777e41a7e71d438e4f50f8",
	"/omx-lib/android.hardware.configstore-utils.so":        "d9fefc36020ae3972240cf24ec377e6da4090d1d7ff0aec4acadb3a2659a5767",
	"/omx-lib/android.hardware.configstore@1.0.so":          "1acaf54d3437b8157b0a99a59ead525cd25e91708ae34747deb2d4d3461e62c2",
	"/omx-lib/android.hardware.configstore@1.1.so":          "72bcb810262e9995677e3b8845c67f05f2a69c38ce80761167bd9207ee506e49",
	"/omx-lib/android.hardware.graphics.allocator@2.0.so":   "c67f209c86205eeb8031d5b99e3725e3c7d24f140aaf3867ca879968d3daa415",
	"/omx-lib/android.hardware.graphics.bufferqueue@1.0.so": "5266bdc0214306ad9ad38dabe3f581d5914bfdceb666fa61a6b6960a9e796899",
	"/omx-lib/android.hardware.graphics.common@1.0.so":      "4099e2bf1c1550391b9e20347bd5c9c93e35bfc98cb3922fb7bcb3544073e259",
	"/omx-lib/android.hardware.graphics.common@1.1.so":      "89470538aeb9be676e86610bd1b8cfe33f2b36dfa72d1c305eb6ee880dca3342",
	"/omx-lib/android.hardware.graphics.mapper@2.0.so":      "d7466dc628b2b073e09125a07ad4bd0b06d40f76e9cd5ff37408d3a228f7b00b",
	"/omx-lib/android.hardware.graphics.mapper@2.1.so":      "b770834c656fcb858843353e649eb205c0d1a23feba06ec9b1b85d36f43c1097",
	"/omx-lib/android.hardware.media.omx@1.0.so":            "b96a78c523b13df4bd8bc484ae91ff0b2c38bacd00b48a2630d241dca1b810a8",
	"/omx-lib/android.hardware.media@1.0.so":                "b42b176aede0e0fa564b766733b2fb02985b912d7c90bc747ae99163bfd94151",
	"/omx-lib/android.hidl.memory.token@1.0.so":             "16341d0831541c88184fa3eb26a00c7900321d41b5d8cb103bf0be92547b6e50",
	"/omx-lib/android.hidl.memory@1.0.so":                   "6d953d868c26093a2c78fde5aae25e6d767c291e00f87cb6d59313797b1ded1c",
	"/omx-lib/libbacktrace.so":                              "bf4e6b3ecf7d14db8e5e62e2f72b3bd42cf58a57deb4e34b1835e750a6b9af54",
	"/omx-lib/libbase.so":                                   "ce5af18fea3001895a079e74dc712f9c6473e19c7b7657dc3b5a2733c4f27b3e",
	"/omx-lib/libbinder.so":                                 "cbeaa880507356957b7955bec7ed7b7f4489270bc17dc532c00baabc6693c549",
	"/omx-lib/libbwc.so":                                    "d007c563b78b3ad8e7f552780f6b6adb9f4fdd906d426a169a3ee2bc1bf81d41",
	"/omx-lib/libc.so":                                      "7585ba7ce44cb14d3e7bbaa40aca4a3e7e8c97b8c76e2b9514c1c53c5bf97ca2",
	"/omx-lib/libc++.so":                                    "812b7edee3e7ebed369a9dac66921c93098e15617c995e3f58e7143e3aac522e",
	"/omx-lib/libcutils.so":                                 "04e41f96159bbcf30255e1c69b8b70218efc20ccc301ab27e7a07248964f0ebe",
	"/omx-lib/libdl.so":                                     "01ff90be229ce00bf68bc25447a2ce5162853d041c0e4e6a7492de4546446b81",
	"/omx-lib/libdpframework.so":                            "47ee0ff8dc25c677e5393abee3ac5cdd6eba99de520018714bcecd4b769f0970",
	"/omx-lib/libged.so":                                    "c90b7cca279be016ef630d967116efe82aa6dc0c1d73401a61afed1d06a8ec8d",
	"/omx-lib/libgralloc_extra.so":                          "bac21d2ac1e69d8463208f4d2bc66f700791d46955fc023003a8c4f436f8ca54",
	"/omx-lib/libhardware.so":                               "ea5327d9d001719810cd2e637a3df0d96f5d155ad812091557381a073a8a8a06",
	"/omx-lib/libhidlbase.so":                               "734e7ee68c2b9ba5afb8dcf52d0b1ba35fff66437a6ad134ac4d8db9e26db9b0",
	"/omx-lib/libhidlmemory.so":                             "70e189e762975e679eb7c0548cc4d49555cfa3968db7f931d9922074a7292800",
	"/omx-lib/libhidltransport.so":                          "c1c763dd2ff14173e9c5dd48ffa195cfb63bd2fd82df7116540146f83059a00d",
	"/omx-lib/libhwbinder.so":                               "91488942832b48b5550bd3f6515412e0fdb88c6b8cc6fe34ba1f4e364a7b9515",
	"/omx-lib/libion_mtk.so":                                "ba52be04219104bcaa280f623d217e7dc3234e242784cbb86827103fd9f57c84",
	"/omx-lib/libion_ulit.so":                               "375023230fbe32e5d6e9c722f53bf6cba2c210e90f5e6ceef15b6181a6341c0f",
	"/omx-lib/libion.so":                                    "de3e597edc16f5a8de1a025a4ef31777a582545cd81979bcbf4aeff63afa8626",
	"/omx-lib/libladder.so":                                 "4afda9301105380f693c2b8d7c2ee4a5e5a80d1d4cc8bee8543daa3a34f3de6e",
	"/omx-lib/liblog.so":                                    "f7974d3f1843fc2d9a708ae79d0a16d7c7e3a1cf7b620b6c5ec7e54813a3f9b9",
	"/omx-lib/liblzma.so":                                   "2f8e3b417bb82d94b6893046b6329a1fcfb27f53f451212868cc30247152ac70",
	"/omx-lib/libm.so":                                      "ac4d03ac3ebde8520cf933079c210ae07e0d3eff2a35549fb0ab22409f8ab00e",
	"/omx-lib/libmp4enc_sa.ca7.so":                          "4f720a6708072ffea7891d3a3399886e408059ca13900d626cfd2ce32679743e",
	"/omx-lib/libmtk_drvb.so":                               "06f3a8bdcdef0183f6794b245d5cce0faec07c5ae0c30f38c343abb8c6527a9b",
	"/omx-lib/libMtkOmxVenc.so":                             "852f82e3dd4d05d1b63435b258811f89b6f5b6466ff1d52cf577e85108cb5ec5",
	"/omx-lib/libnativewindow.so":                           "212fe8c6fb70dfbe0edc60b83c86945a401f0335fdd12c6d73b66c6a4b0c4f72",
	"/omx-lib/libpq_prot.so":                                "bbd23f94ceeddad6f1440134d8ef212a3b4e90704d8f341f4bfe6e6892be4b5d",
	"/omx-lib/libsync.so":                                   "74336925a40e086da6992351aa8449e0a69f502e80597667ab12af50ae71a7fe",
	"/omx-lib/libtz_uree.so":                                "d05cf7adf62f7f75758043c03639dca68d0e7dc387eeac7303a88f57284ba432",
	"/omx-lib/libui.so":                                     "92b7a431b065e6fc34ed068802f709972a18d9eab532dc4406fce1969022e641",
	"/omx-lib/libunwind.so":                                 "368c10030c6dd29ac2cc445373362f988330aa56192739db72320b545e79f7bc",
	"/omx-lib/libunwindstack.so":                            "87861c713ae6fcee92aa59c8ff8b5022e075a43a9fbc50533baf5f5164ca2a44",
	"/omx-lib/libutils.so":                                  "aa897d4311500863882b4901837902baae3a5f837ae6f67ebb972afba339a4ca",
	"/omx-lib/libutilscallstack.so":                         "4f8b0500560a97da85884d0d5e5a8caf89d3c3121ccb66c08a9ff9e281bdf6e3",
	"/omx-lib/libvc1dec_sa.ca7.so":                          "9d04bfa2ec630ab0e0a0483c8c7bf3dd5b13ca3a61beb2a2ca22610cfbde299e",
	"/omx-lib/libvcodec_oal.so":                             "d8e0d6f8ad1f752756a8c18975afc7071dea9d022f2b67caf1e1db1211c7c2e1",
	"/omx-lib/libvcodec_utility.so":                         "47c6979ab704b3885ad522b83b68b492e69eb341741814cd28b24d238ca83218",
	"/omx-lib/libvcodecdrv.so":                              "8f3eff348d8254a846695985905a63f8d93dc7fb9dccc1f2712b8935172d4e7f",
	"/omx-lib/libvndksupport.so":                            "d415f8ae4ab630bfe6f809e72f14317a727156b8c607a1e9fe49515661294096",
	"/omx-lib/libvp8dec_sa.ca7.so":                          "61d352aae0886412ac2e7ed155d1d62c9b0612bb6b9869973aca00fc464060a2",
	"/omx-lib/vendor.mediatek.hardware.pq@2.0.so":           "d581346d48e48be057241d68d1b403a6debcee9fb1de8e23a57b2579246e01d3",
}

func validateAthenaEncoderManifest(value []byte, encoder bool) error {
	var m struct {
		Schema       int    `json:"schema"`
		Target       string `json:"target"`
		RuntimeID    string `json:"runtime_id"`
		Experimental bool   `json:"experimental"`
		Tree         string `json:"tree_sha256"`
		Count        int    `json:"file_count"`
		Bytes        int64  `json:"runtime_bytes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if len(value) > 4096 || decoder.Decode(&m) != nil {
		return errors.New("invalid Athena encoder runtime manifest")
	}
	var trailing any
	id, tree, count, size := "athena-mt8183-ap-vdec-1", "efcfe0d53924554af190c7e9f8068f190e1af3620c0346efceed3a81890b23bc", 24, int64(6042836)
	if encoder {
		id, tree, count, size = "athena-mt8183-ap-venc-1", "de1daf2574d4d4d4d3c5ba8605852f782a9f170d35934888290e1d52d9a91db3", 55, 8510652
	}
	if decoder.Decode(&trailing) != io.EOF || m.Schema != 1 || m.Target != "athena" ||
		!m.Experimental || m.RuntimeID != id || m.Tree != tree || m.Count != count || m.Bytes != size {
		return errors.New("Athena encoder runtime identity is not qualified")
	}
	return nil
}

func athenaEncoderPaths() (helper, component, libraries string, err error) {
	if os.Getenv("TATER_ATHENA_AP_ENABLE") != "1" {
		return "", "", "", errors.New("Athena AP capture is not enabled")
	}
	info, nodeErr := os.Stat("/dev/tater-athena-ap-venc")
	if nodeErr != nil || info.Mode()&os.ModeCharDevice == 0 {
		return "", "", "", errors.New("Athena AP encoder device is unavailable")
	}
	for _, directory := range []string{athenaEncoderRoot, athenaEncoderRoot + "/lib", athenaEncoderRoot + "/omx-lib"} {
		info, statErr := os.Lstat(directory)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", "", errors.New("Athena encoder runtime must remain isolated")
		}
	}
	for _, encoder := range []bool{false, true} {
		manifest := athenaEncoderRoot + "/runtime.json"
		if encoder {
			manifest = athenaEncoderRoot + "/omx-lib/runtime.json"
		}
		info, statErr := os.Lstat(manifest)
		if statErr != nil || !info.Mode().IsRegular() {
			return "", "", "", errors.New("invalid Athena runtime manifest file")
		}
		value, readErr := readEncoderManifest(manifest)
		if readErr != nil || validateAthenaEncoderManifest(value, encoder) != nil {
			return "", "", "", errors.New("Athena encoder runtime manifest is unavailable")
		}
	}
	executable, executableErr := os.Executable()
	if executableErr != nil {
		return "", "", "", executableErr
	}
	helper = filepath.Join(filepath.Dir(executable), "tater-hw-venc")
	if err = checkedAthenaEncoderFile(helper, athenaVencHelperHash); err != nil {
		return
	}
	for name, expected := range athenaEncoderFiles {
		if err = checkedAthenaEncoderFile(athenaEncoderRoot+name, expected); err != nil {
			return
		}
	}
	component = athenaEncoderRoot + "/omx-lib/libMtkOmxVenc.so"
	libraries = athenaEncoderRoot + "/omx-lib:" + athenaEncoderRoot + "/lib"
	return
}

func checkedAthenaEncoderFile(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		return errors.New("Athena encoder dependency must be an ordinary file")
	}
	return checkedEncoderFile(path, expected)
}

func encoderProcessArguments(profile encoderProfile, component string) []string {
	if profile.target == "athena" {
		return []string{"--stream-nv12-h264-v1", component, "640", "480", "30"}
	}
	format := "--stream-bayer10"
	if profile.target == "rook" {
		format = "--stream-bayer8"
	}
	return []string{format, component, "--target", profile.target,
		"bounded-private-buffers", strconv.Itoa(profile.width), strconv.Itoa(profile.height), "30", "-"}
}
