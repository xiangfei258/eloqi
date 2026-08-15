package windows

import (
	"testing"

	"github.com/xiangchang24/eloqi/internal/platform"
)

func TestDefaultWindowsWaveFormat(t *testing.T) {
	t.Parallel()
	format := defaultWindowsWaveFormat()
	if format.formatTag != waveFormatPCM {
		t.Fatalf("format tag = %d, want PCM", format.formatTag)
	}
	if format.samplesPerSec != platform.DefaultSampleRate ||
		format.channels != platform.DefaultChannels ||
		format.bitsPerSample != platform.DefaultBitDepth {
		t.Fatalf("unexpected capture format: %#v", format)
	}
	if format.blockAlign != 2 {
		t.Fatalf("block align = %d, want 2", format.blockAlign)
	}
	if format.avgBytesPerSec != 32000 {
		t.Fatalf("average bytes/sec = %d, want 32000", format.avgBytesPerSec)
	}
	if format.extraSize != 0 {
		t.Fatalf("PCM extra size = %d, want 0", format.extraSize)
	}
	if windowsRecorderBufferLimit != 1<<20 {
		t.Fatalf("buffer limit = %d, want 1 MiB", windowsRecorderBufferLimit)
	}
	if windowsRecorderChunkBytes%int(format.blockAlign) != 0 {
		t.Fatalf("chunk size %d is not frame aligned", windowsRecorderChunkBytes)
	}
}
