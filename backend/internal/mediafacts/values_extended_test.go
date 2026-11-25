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

func TestValuesPreservesCombinedSource(t *testing.T) {
	values := Values("Title.2160p.BluRay.REMUX.HEVC.mkv", releaseparse.Spec{}, "")
	if values[naming.Source] != "BluRay REMUX" {
		t.Fatalf("source = %q", values[naming.Source])
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
	values := Values("Title.2160p.MKV.strm", releaseparse.Spec{Codec: "h265", BitDepth: "10", Container: "mkv"}, "")
	if _, exists := values[naming.Field("container")]; values[naming.VideoCodec] != "HEVC" || values[naming.BitDepth] != "10bit" || exists {
		t.Fatalf("release specification fields = %+v", values)
	}
}

func TestValuesRejectsConflictingReleaseSpecification(t *testing.T) {
	values := Values("Title.H264.12bit.MP4.strm", releaseparse.Spec{Codec: "h265", BitDepth: "10", Container: "mkv"}, "")
	for _, field := range []naming.Field{naming.VideoCodec, naming.BitDepth} {
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
	for _, tt := range []struct{ name, dynamicRange, profile string }{
		{"Title.Dolby.Vision.P8.1.strm", "DV", "P8.1"},
		{"Title.DoVi.HDR10Plus.strm", "DV HDR10+", ""},
		{"Title.2160p.HDR10 Plus.strm", "HDR10+", ""},
		{"Title.2160p.HDR10+.strm", "HDR10+", ""},
		{"Title.DV.P8.4.HLG.strm", "DV HLG", "P8.4"},
		{"Title.DV.Profile 8.4.mkv", "DV", "P8.4"},
		{"Title.2160p.WEB-DL.strm", "", ""},
		{"Title.P7.RPU.FEL.strm", "", ""},
		{"Title.HDR10.RPU.strm", "HDR10", ""},
	} {
		values := Values(tt.name, releaseparse.Spec{}, "")
		if values[naming.DynamicRange] != tt.dynamicRange || values[naming.DVProfile] != tt.profile ||
			tt.profile == "" && (values[naming.DVLayer] != "" || values[naming.DVMetadata] != "") {
			t.Errorf("%q: 动态范围或 DV 字段错误：%+v", tt.name, values)
		}
	}
}
