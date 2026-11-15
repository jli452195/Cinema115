package recognition

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var recognitionBenchmarkResult Result

// 基准显式加载文档示例；生产不会自动加载这些词表。
func BenchmarkRecognitionFullNameScan(b *testing.B) {
	directory := filepath.Join("..", "..", "..", "docs", "examples", "recognition")
	groups, err := os.ReadFile(filepath.Join(directory, "release-groups.txt"))
	if err != nil {
		b.Fatal(err)
	}
	platforms, err := os.ReadFile(filepath.Join(directory, "platforms.txt"))
	if err != nil {
		b.Fatal(err)
	}
	baseRules := 0
	for _, line := range strings.Split(string(groups), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			baseRules++
		}
	}
	names := []string{
		"EPSiLON -dfaef",
		"[喵萌奶茶屋&LoliHouse] Show - 01 [1080p][HEVC][CHS].mkv",
		"[Nekomoe kissaten&VCB-Studio] Show - 02 [1080p][BDRip][FLAC].mkv",
		"Example.Show.S01E01.2026.1080p.NF.WEB-DL.DDP5.1.H.264-NTb.mkv",
		"[樱都字幕组] Show - 02 [1080p][CHS].mkv",
		"-樱都字幕组- Show - 02 [1080p].mkv",
		"Movie.Name.2026.2160p.BluRay.REMUX.DV.HDR.TrueHD.Atmos-FraMeSToR.mkv",
		"Unknown.Movie.2026.1080p.WEB-DL.HEVC.AAC-UnknownGroup.mkv",
	}
	for _, count := range []int{baseRules, 1000, 2000} {
		var source strings.Builder
		source.Write(groups)
		for i := baseRules; i < count; i++ {
			fmt.Fprintf(&source, "\nBenchRelease%04d", i)
		}
		snapshot, err := Compile(source.String(), string(platforms))
		if err != nil {
			b.Fatal(err)
		}
		for _, long := range []bool{false, true} {
			label, prefix := "Normal", ""
			if long {
				label, prefix = "Long1KiB", strings.Repeat("Series.Name.", 80)
			}
			b.Run(fmt.Sprintf("Rules%d/%s", count, label), func(b *testing.B) {
				inputs := make([]string, len(names))
				for i, name := range names {
					inputs[i] = prefix + name
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					result, err := snapshot.Recognize(inputs[i%len(inputs)])
					if err != nil {
						b.Fatal(err)
					}
					recognitionBenchmarkResult = result
				}
			})
		}
	}
}

// 复杂规则单独计量，避免用大量普通名称掩盖逐条正则扫描的开销。
func BenchmarkRecognitionComplexRules(b *testing.B) {
	for _, count := range []int{100, 1000, 2000} {
		var source strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&source, "Release%04d(?:-(?:Raws|Studio))?\n", i)
		}
		snapshot, err := Compile(source.String(), "NF ~= NF|Netflix\nIQ ~= IQ|iQIYI|爱奇艺")
		if err != nil {
			b.Fatal(err)
		}
		for _, long := range []bool{false, true} {
			length, prefix := "Normal", ""
			if long {
				length, prefix = "Long1KiB", strings.Repeat("Series.Name.", 80)
			}
			for _, matching := range []bool{false, true} {
				mode, name := "Miss", "Unknown.Series.S01E01.1080p.WEB-DL.mkv"
				if matching {
					mode = "SparseHit"
					name = fmt.Sprintf("[Release0000-Studio&Release%04d-Raws] Series.S01E01.NF.WEB-DL.1080p.mkv", count-1)
				}
				input := prefix + name
				b.Run(fmt.Sprintf("Rules%d/%s/%s", count, length, mode), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						result, err := snapshot.Recognize(input)
						if err != nil {
							b.Fatal(err)
						}
						recognitionBenchmarkResult = result
					}
				})
			}
		}
	}
}

// 无固定文字的规则应继续走通用正则路径，单独保留其退化场景基准。
func BenchmarkRecognitionUnfilterableRules(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&source, "(?:[A-Z]{4}|[0-9]{4})-[0-9]{%d}\n", i%12+1)
	}
	snapshot, err := Compile(source.String(), "")
	if err != nil {
		b.Fatal(err)
	}
	for _, long := range []bool{false, true} {
		label, name := "Normal", "Unknown.Series.1080p.mkv"
		if long {
			label, name = "Long1KiB", strings.Repeat("Series.Name.", 80)+name
		}
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				result, err := snapshot.Recognize(name)
				if err != nil {
					b.Fatal(err)
				}
				recognitionBenchmarkResult = result
			}
		})
	}
}
