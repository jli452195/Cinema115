package mediafacts

import (
	"testing"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
)

func TestValuesRecognizesExtendedNamingFields(t *testing.T) {
	values := Values("Title.S01E05.2160p.BluRay.REMUX.x265.10-bit.DV.P8.1.FEL.HDR10.RPU.23.976p.Progressive.CFR.MKV.TrueHD.7.1.Atmos.strm", releaseparse.Spec{}, "")
	want := map[naming.Field]string{
		naming.SourceMedium:   "BluRay",
		naming.SourceModifier: "REMUX",
		naming.VideoCodec:     "HEVC",
		naming.Encoder:        "x265",
		naming.BitDepth:       "10bit",
		naming.DynamicRange:   "DV HDR10",
		naming.DVProfile:      "P8.1",
		naming.DVLayer:        "FEL",
		naming.HDRBaseLayer:   "HDR10",
		naming.DVMetadata:     "RPU",
		naming.FrameRate:      "23.976fps",
		naming.ScanType:       "Progressive",
		naming.FrameRateMode:  "CFR",
		naming.Container:      "MKV",
		naming.AudioCodec:     "TRUEHD",
		naming.Channels:       "7.1",
		naming.AudioEffect:    "Atmos",
	}
	for field, expected := range want {
		if got := values[field]; got != expected {
			t.Errorf("%s = %q, want %q", field, got, expected)
		}
	}
}

func TestValuesKeepsDVWithSDRBaseLayer(t *testing.T) {
	values := Values("Title.2160p.HEVC.10bit.DV.P5.SDR.24fps.mkv", releaseparse.Spec{}, "")
	if values[naming.DynamicRange] != "DV" || values[naming.HDRBaseLayer] != "SDR" || values[naming.DVProfile] != "P5" {
		t.Fatalf("DV SDR fields = %+v", values)
	}
}

func TestValuesRejectsConflictingScalarFields(t *testing.T) {
	values := Values("Title.1080p.720p.H264.HEVC.10bit.12bit.SDR.HDR10.AAC.FLAC.mkv", releaseparse.Spec{}, "")
	for _, field := range []naming.Field{naming.Resolution, naming.VideoCodec, naming.BitDepth, naming.DynamicRange, naming.HDRBaseLayer, naming.AudioCodec} {
		if values[field] != "" {
			t.Errorf("conflicting %s = %q", field, values[field])
		}
	}
}

func TestValuesPreservesCombinedSourceAndDoesNotReadExtensionAsContainer(t *testing.T) {
	values := Values("Title.2160p.BluRay.REMUX.HEVC.mkv", releaseparse.Spec{}, "")
	if values[naming.Source] != "BluRay REMUX" || values[naming.Container] != "" {
		t.Fatalf("source/container = %+v", values)
	}
}

func TestValuesDoesNotTreatEpisodeResolutionAsFrameRate(t *testing.T) {
	values := Values("Title.S01E05.098.2160p.WEB-DL.HEVC.strm", releaseparse.Spec{}, "")
	if values[naming.FrameRate] != "" {
		t.Fatalf("frame rate = %q", values[naming.FrameRate])
	}
}

func TestValuesKeepsReleaseTagsWithoutKnownExtension(t *testing.T) {
	values := Values("Title.2160p.23.976fps", releaseparse.Spec{}, "")
	if values[naming.Resolution] != "2160p" || values[naming.FrameRate] != "23.976fps" {
		t.Fatalf("release tags without extension = %+v", values)
	}
}

func TestValuesNormalizesLegacyVideoCodecNames(t *testing.T) {
	values := Values("Title.1080p.AVC.8bit.23.976fps.strm", releaseparse.Spec{}, "")
	if values[naming.VideoCodec] != "H264" || values[naming.BitDepth] != "8bit" || values[naming.FrameRate] != "23.976fps" {
		t.Fatalf("normalized codec fields = %+v", values)
	}
}

func TestValuesUsesParsedReleaseSpecification(t *testing.T) {
	values := Values("Title.2160p.strm", releaseparse.Spec{Codec: "h265", BitDepth: "10", Container: "mkv"}, "")
	if values[naming.VideoCodec] != "HEVC" || values[naming.BitDepth] != "10bit" || values[naming.Container] != "MKV" {
		t.Fatalf("release specification fields = %+v", values)
	}
}

func TestValuesRejectsConflictingReleaseSpecification(t *testing.T) {
	values := Values("Title.H264.12bit.MP4.strm", releaseparse.Spec{Codec: "h265", BitDepth: "10", Container: "mkv"}, "")
	for _, field := range []naming.Field{naming.VideoCodec, naming.BitDepth, naming.Container} {
		if values[field] != "" {
			t.Errorf("conflicting release specification %s = %q", field, values[field])
		}
	}
}

func TestValuesRequiresDVMarkerForBareEnhancementLayer(t *testing.T) {
	if values := Values("Title.1080p.EL.HEVC.strm", releaseparse.Spec{}, ""); values[naming.DVLayer] != "" {
		t.Fatalf("bare enhancement layer = %q", values[naming.DVLayer])
	}
	if values := Values("Title.1080p.DV.EL.HEVC.strm", releaseparse.Spec{}, ""); values[naming.DVLayer] != "EL" {
		t.Fatalf("DV enhancement layer = %q", values[naming.DVLayer])
	}
}

func TestValuesRecognizesDolbyVisionAliases(t *testing.T) {
	for _, name := range []string{"Title.Dolby.Vision.P8.1.strm", "Title.DoVi.HDR10Plus.strm"} {
		if values := Values(name, releaseparse.Spec{}, ""); values[naming.DynamicRange] == "" {
			t.Fatalf("dynamic range missing for %q: %+v", name, values)
		}
	}
}
