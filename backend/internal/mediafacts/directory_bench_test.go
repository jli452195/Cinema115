package mediafacts

import "testing"

func BenchmarkDirectoryContext(b *testing.B) {
	for _, directory := range []string{"S*******", "Season 00", "Unknown Work"} {
		b.Run(directory, func(b *testing.B) {
			const root = "/strm/待整理"
			source := "五十公里桃花坞 (2021) {tmdb-126398}/" + directory + "/Wonderland.2021.S00E169.2160p.WEB-DL.60fps.HEVC.DDP 2.0-WF.strm"
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := ParseDirectoryContext(root, source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
