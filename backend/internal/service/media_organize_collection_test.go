package service

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaOrganizerMixedCollection(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	metadata := newMediaCollectionMetadata()
	s.metadata = metadata
	const root = "【动漫】七大罪.2015-2022.TV+剧场版+OAD 大合集.1080P.日语中字"
	const mixed = "七大罪 戒律的复活+圣战的预兆 2017-2018.1080P.日语内封中字"
	type expected struct {
		id, season, episode int
	}
	want := make(map[string]expected, 113)
	add := func(parent, name string, expected expected) {
		relative := path.Join(root, parent, name)
		mediaTestFile(t, rule.Source, relative, name)
		want[relative] = expected
	}
	for episode := 1; episode <= 24; episode++ {
		add("七大罪 愤怒的审判 2021.全24集.1080P.日语中字", fmt.Sprintf("[BeanSub][Nanatsu_no_Taizai_Fundo_no_Shinpan][%02d][GB][1080P][x264_AAC].mp4", episode), expected{62104, 4, episode})
		// The filename's translated subtitle is not a verified TMDB alias.
		add("七大罪 众神的逆鳞.2019.全24集.1080P.日语中字", fmt.Sprintf("[BeanSub][Nanatsu_no_Taizai_Kamigami_no_Gekirin][%02d][GB][1080P][x264_AAC].mp4", episode), expected{0, 1, episode})
		add("七大罪.2017.全24集.1080P.日语内封中字", fmt.Sprintf("Nanatsu no Taizai %02d [BD 1920x1080 HEVC 10bit CHS].mkv", episode), expected{0, 1, episode})
		ending := ""
		if episode == 24 {
			ending = " END"
		}
		add(mixed, fmt.Sprintf("[SAIO-Raws] Nanatsu no Taizai Imashime no Fukkatsu %02d%s [BD 1920x1080 HEVC-10bit OPUS ASSx2].mkv", episode, ending), expected{0, 1, episode})
	}
	for episode := 1; episode <= 4; episode++ {
		ending := ""
		if episode == 4 {
			ending = " END"
		}
		add(mixed, fmt.Sprintf("[SAIO-Raws] Nanatsu no Taizai Seisen no Shirushi %02d%s [BD 1920x1080 HEVC-10bit OPUS ASSx2].mkv", episode, ending), expected{0, 1, episode})
		add(mixed, fmt.Sprintf("[SAIO-Raws] Nanatsu no Taizai Imashime no Fukkatsu NCOP %02d [BD 1920x1080 HEVC-10bit OPUS].mkv", episode), expected{0, 1, 0})
		if episode <= 2 {
			add(mixed, fmt.Sprintf("[SAIO-Raws] Nanatsu no Taizai Imashime no Fukkatsu NCED %02d [BD 1920x1080 HEVC-10bit OPUS].mkv", episode), expected{0, 1, 0})
		}
	}
	add(mixed, "[SAIO-Raws] Gekijouban Nanatsu no Taizai Tenkuu no Torawarebito [BD 1920x1080 HEVC-10bit OPUSx2 ASSx2].mkv", expected{101, 1, 0})
	add("七大罪剧场版 天空的囚人 2018.1080P.内封多国字幕多国音轨", "[DragsterPS] The Seven Deadly Sins the Movie - Prisoners of the Sky [1080p] [Multi-Audio] [Multi-Subs] [6FA5BD15].mkv", expected{101, 1, 0})
	add("七大罪剧场版 天空的囚人 2018.1080P.内封多国字幕多国音轨", "[Ohys-Raws] Gekijouban Nanatsu no Taizai Tenkuu no Torawarebito (BD 1920x1080 x264 FLACx2).mkv", expected{101, 1, 0})
	add("七大罪剧场版 光芒诅咒之人 2021.1080P.日语中字", "[BeanSub][The_Seven_Deadly_Sins-Hikari_ni_Norowareshi_Mono_tachi][MOVIE][GB][1080P][x264_AAC].mp4", expected{102, 1, 0})
	for _, name := range []string{
		"[BeanSub][Nanatsu no Taizai][OAD][01][GB][480P][MP4].mp4",
		"[BeanSub][Nanatsu no Taizai][OAD][02][GB][576P][MP4].mp4",
		"[BeanSub][Nanatsu_no_Taizai][OAD][03][GB][576P][x264_AAC].mp4",
	} {
		add("七大罪OAD3部.2015-2018.720P.日语中字", name, expected{0, 1, 0})
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	if len(want) != 113 || len(run.Entries) != len(want) {
		t.Fatalf("collection file count changed: want=%d got=%d", len(want), len(run.Entries))
	}
	groups := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, group := range run.Groups {
		groups[group.ID] = group
	}
	for _, entry := range run.Entries {
		expected := want[entry.File.ID]
		group := groups[entry.Group]
		if group.TMDBID != expected.id || entry.File.Season != expected.season || entry.File.Episode != expected.episode {
			t.Fatalf("unexpected identity/coordinates: %s group=%+v file=%+v", entry.File.Name, group, entry.File)
		}
		if expected.id == 0 && (entry.Status != "needs_match" || entry.Relative != "") {
			t.Fatalf("unconfirmed content became executable: %+v", entry)
		}
		if expected.id == 62104 && (entry.Status != "ready" || !strings.Contains(entry.Relative, fmt.Sprintf("S04E%02d", expected.episode))) {
			t.Fatalf("verified season did not reach naming: %+v", entry)
		}
		if expected.id == 101 || expected.id == 102 {
			if group.Kind != "movie" || entry.Relative == "" || strings.Contains(entry.Relative, "Season ") {
				t.Fatalf("movie inherited TV numbering: %+v", entry)
			}
		}
	}
	primaryQueries := 0
	for _, query := range metadata.queries {
		if strings.Contains(query, "1920") || strings.Contains(query, "NCOP") || strings.Contains(query, "NCED") || strings.Contains(query, "OAD") {
			t.Fatalf("metadata query contains dimensions or unconfirmed extras: %q", query)
		}
		if query == "Nanatsu no Taizai Fundo no Shinpan" {
			primaryQueries++
		}
	}
	if primaryQueries != 1 {
		t.Fatalf("one season was recognized repeatedly: %v", metadata.queries)
	}
	// Execute only the isolated temporary fixture. Uncertain identities remain
	// in place; verified files retain their exact original bytes.
	mediaTestProcess(t, s, &run, "execute")
	for _, entry := range run.Entries {
		if entry.Relative == "" {
			if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(entry.File.ID))); err != nil {
				t.Fatal("unconfirmed source was moved", entry.File.Name, err)
			}
			continue
		}
		if entry.Status != "done" && entry.Status != "conflict" {
			t.Fatal("verified media did not finish", entry)
		}
		if entry.Status == "done" {
			body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(entry.Relative)))
			if err != nil || string(body) != entry.File.Name {
				t.Fatal("organizing changed the original file", entry.File.Name, err)
			}
		}
	}
}

func TestMediaOrganizerBareEpisodeGroupReusesRecognition(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	for episode := 1; episode <= 24; episode++ {
		mediaTestFile(t, rule.Source, fmt.Sprintf("Work.2024.全24集/Work %02d [BD 1920x1080 HEVC].mkv", episode), "media")
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	if len(run.Groups) != 1 || metadata.browseCalls != 1 {
		t.Fatalf("one release generated redundant metadata lookups: groups=%d requests=%d", len(run.Groups), metadata.browseCalls)
	}
	for _, entry := range run.Entries {
		if entry.Status != "ready" || entry.File.Episode < 1 || entry.File.Episode > 24 {
			t.Fatal("ordinary bare episode numbering regressed", entry)
		}
	}
}
