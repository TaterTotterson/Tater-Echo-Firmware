//go:build !releaseassets

package microwakeword

// Developer and ordinary CI builds deliberately omit the large target-native
// wake companions. Release builds replace these stubs with verified embedded
// assets by enabling the releaseassets build tag.
var (
	embeddedReleaseRuntime        []byte
	embeddedReleaseORTRuntime     []byte
	embeddedReleaseMelspectrogram []byte
	embeddedReleaseEmbedding      []byte
)
