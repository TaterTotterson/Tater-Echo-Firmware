package pcm

type Info struct {
	// Mask that represents the type of read or write methods available
	Access uint
	// Mask that represents the PCM_FORMAT available (e.g. PCM_FORMAT_32_LE)
	Format0 uint
	// Mask that represents the PCM_FORMAT available (e.g. PCM_FORMAT_32_LE)
	Format1 uint
	// Pcm formats available
	FormatNames []string
	// Mask that represents the subformat available
	Subformat uint
	// Minimum available rate
	RateMin uint
	// Maximum available rate
	RateMax uint
	// Minimum available channels
	ChannelsMin uint
	// Maximum available channels
	ChannelsMax uint
	// Minimum available sample bits
	SampleBitsMin uint
	// Maximum available sample bits
	SampleBitsMax uint
	// Minimum available period size
	PeriodSizeMin uint
	// Maximum available period size
	PeriodSizeMax uint
	// Minimum available period count
	PeriodCountMin uint
	// Maximum available period count
	PeriodCountMax uint
}
