package windows

import "github.com/xiangchang24/eloqi/internal/platform"

const (
	waveFormatPCM = 1

	windowsRecorderBufferLimit = 1 << 20
	windowsRecorderBuffers     = 4
	windowsRecorderChunkBytes  = 4096
)

type waveFormat struct {
	formatTag      uint16
	channels       uint16
	samplesPerSec  uint32
	avgBytesPerSec uint32
	blockAlign     uint16
	bitsPerSample  uint16
	extraSize      uint16
}

func defaultWindowsWaveFormat() waveFormat {
	blockAlign := uint16(platform.DefaultChannels * platform.DefaultBitDepth / 8)
	return waveFormat{
		formatTag:      waveFormatPCM,
		channels:       platform.DefaultChannels,
		samplesPerSec:  platform.DefaultSampleRate,
		avgBytesPerSec: platform.DefaultSampleRate * uint32(blockAlign),
		blockAlign:     blockAlign,
		bitsPerSample:  platform.DefaultBitDepth,
	}
}
