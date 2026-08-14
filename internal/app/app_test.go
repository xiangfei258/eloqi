package app

import (
	"testing"

	"github.com/xiangchang24/eloqi/internal/config"
)

func TestASRConfigFromConfigCarriesDiarizationSetting(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.Endpoint = "https://asr.example.test"
	cfg.ASR.APIKey = "k"
	cfg.ASR.Model = "m"
	cfg.ASR.StripDiarization = true

	got := asrConfigFromConfig(cfg)
	if !got.StripDiarization {
		t.Fatal("StripDiarization = false, want true")
	}
	if got.Endpoint != cfg.ASR.Endpoint || got.APIKey != cfg.ASR.APIKey || got.Model != cfg.ASR.Model {
		t.Fatalf("ASR mapping = %+v", got)
	}
}
