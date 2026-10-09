//go:build releaseassets

package microwakeword

import _ "embed"

// The emOS targets keep their existing single-ELF A/B OTA contract so every older
// installation can consume the update. Release builds embed the target-native
// companions in that ELF; startup verifies and materializes them before either
// OWW lane is allowed to run.

//go:embed release_assets/libtater_microwakeword.so
var embeddedReleaseRuntime []byte

//go:embed release_assets/libonnxruntime.so
var embeddedReleaseORTRuntime []byte

//go:embed release_assets/melspectrogram.onnx
var embeddedReleaseMelspectrogram []byte

//go:embed release_assets/embedding_model.onnx
var embeddedReleaseEmbedding []byte
