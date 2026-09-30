package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

type countedPlaybackDB struct {
	*storage.Store
	batches []storage.PlaybackBatch
	fail    bool
}

func (d *countedPlaybackDB) SavePlayback(ctx context.Context, b storage.PlaybackBatch) error {
	if d.fail {
		return errors.New("disk unavailable")
	}
	if err := d.Store.SavePlayback(ctx, b); err != nil {
		return err
	}
	d.batches = append(d.batches, b)
	return nil
}

func TestPlaybackSQLiteImportIncrementalWritesAndRestart(t *testing.T) {
	ctx := context.Background()
	old, f, selected := setupFastCache(t)
	if _, _, err := old.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "source-secret", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	legacy := old.store.(*SessionStore)
	legacyRaw := legacy.Value()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "playback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	counted := &countedPlaybackDB{Store: db}
	s, err := NewSQLitePlaybackCache(ctx, counted, legacy, old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Status().Materials) != 1 || len(s.Status().Entries) != 1 || len(s.Status().Config.Bindings) != 1 {
		t.Fatal("import lost records/bindings")
	}
	if len(counted.batches) != 1 {
		t.Fatal("import was not one transaction")
	}
	material := s.Status().Materials[0]
	material.Ranges = []PlaybackRange{{Start: 1, End: 3, SHA1: strings.Repeat("C", 40), VerifiedAt: time.Now().UTC()}}
	if err := s.putMaterial(material); err != nil {
		t.Fatal(err)
	}
	batch := counted.batches[len(counted.batches)-1]
	if len(batch.Materials) != 1 || len(batch.Copies) != 0 || batch.Settings != nil {
		t.Fatal("material update wrote unrelated records")
	}
	entry := s.Status().Entries[0]
	writes := len(counted.batches)
	entry.LastUsed = entry.LastUsed.Add(time.Second)
	if err := s.put(entry); err != nil {
		t.Fatal(err)
	}
	if len(counted.batches) != writes {
		t.Fatal("last-used touch not coalesced")
	}
	entry.Status = "uploading"
	if err := s.put(entry); err != nil {
		t.Fatal(err)
	}
	batch = counted.batches[len(counted.batches)-1]
	if len(batch.Copies) != 1 || len(batch.Materials) != 0 || batch.Settings != nil {
		t.Fatal("upload intent did not persist independently")
	}
	counted.fail = true
	entry.Status = "ready"
	if err := s.put(entry); err == nil {
		t.Fatal("database failure hidden")
	}
	if got, _ := s.entryByKey(entry.Key); got.Status != "uploading" {
		t.Fatal("failed write changed index")
	}
	material.Name = "changed"
	if err := s.putMaterial(material); err == nil {
		t.Fatal("material write failure hidden")
	}
	if got, _ := s.materialByID(material.ID); got.Name == "changed" {
		t.Fatal("failed material write changed index")
	}
	counted.fail = false
	restarted, err := NewSQLitePlaybackCache(ctx, counted, legacy, old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	got := restarted.Status()
	if got.Entries[0].Status != "uncertain" || len(got.Materials[0].Ranges) != 1 {
		t.Fatal("restart lost recovery state/ranges")
	}
	if _, ok := restarted.materialByID(material.ID); !ok {
		t.Fatal("restart did not rebuild indices")
	}
	if err := restarted.ForgetMaterial(ctx, material.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLitePlaybackCache(ctx, counted, legacy, old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Status().Materials) != 0 || len(reopened.Status().Entries) != 1 {
		t.Fatal("legacy file resurrected forgotten material")
	}
	if legacy.Value() != legacyRaw {
		t.Fatal("rewrote legacy document")
	}
	rows, err := db.LoadPlayback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range rows.Copies {
		for _, secret := range []string{"source-secret", "SEID=", "https://"} {
			if strings.Contains(e.Record, secret) {
				t.Fatal("persisted credential")
			}
		}
	}
}

func TestPlaybackSHA1HintSharesMaterialAcrossTargets(t *testing.T) {
	ctx := context.Background()
	s, f, first := setupFastCache(t)
	if _, _, err := s.ResolvePickCodeWithReport(ctx, first, "pickaaa", "source", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	second := *first
	second.binding.AccountID = "second-account"
	second.binding.ParentCID = "456"
	second.cookie, second.uid = "UID=789_A; SEID=second", "789"
	// The second account has no files, although the first account has its copy.
	f.files = nil
	if _, report, err := s.ResolvePickCodeWithReport(WithPlaybackSHA1Hint(ctx, strings.Repeat("a", 40)), &second, "differentpc", "", "player", "primary"); err != nil || report.UsedSource {
		t.Fatal("second target did not reuse verified material", err)
	}
	if f.reads != 1 || f.uploads != 2 || len(s.Status().Materials) != 1 {
		t.Fatal("material was reloaded or target copy skipped")
	}
	entries := s.Status().Entries
	if len(entries) != 2 {
		t.Fatal("target copies were not isolated")
	}
	for _, e := range entries {
		if e.AccountID == second.binding.AccountID && (e.UID != second.uid || e.ParentCID != "456") {
			t.Fatal("second target inherited first target identity or directory")
		}
	}
	if m, _ := s.lookupMaterial(DirectLinkKey("pickcode-material", "primary", "differentpc"), ""); m.ID != "" {
		t.Fatal("external hash created unverified source association")
	}
}

func TestPlaybackSHA1HintUsesOnlyUniqueVerifiedMaterial(t *testing.T) {
	ctx := context.Background()
	s, f, selected := setupFastCache(t)
	if _, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "source", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 40)
	// A different external pickcode needs no main credentials when SHA1 matches.
	if _, r, err := s.ResolvePickCodeWithReport(WithPlaybackSHA1Hint(ctx, hash), selected, "otherpc", "", "player", "primary"); err != nil || r.UsedSource {
		t.Fatal("hint did not reuse verified material", err)
	}
	if f.reads != 1 || f.uploads != 1 {
		t.Fatal("hint unnecessarily queried source/uploaded")
	}
	unknown := DirectLinkKey("pickcode-material", "primary", "otherpc")
	if m, _ := s.lookupMaterial(unknown, ""); m.ID != "" {
		t.Fatal("external hint poisoned origin index")
	}
	// Conflicting known origin must ignore the hint and use real metadata.
	if _, _, err := s.ResolvePickCodeWithReport(WithPlaybackSHA1Hint(ctx, strings.Repeat("B", 40)), selected, "pickaaa", "source", "player", "primary"); err != nil {
		t.Fatal(err)
	}
	if f.reads != 2 {
		t.Fatal("known-origin conflict not revalidated")
	}
	m := s.Status().Materials[0]
	m.Size++
	m.ID = playbackMaterialID(m.SHA1, m.Size)
	m.Origins = nil
	if err := s.putMaterial(m); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ResolvePickCodeWithReport(WithPlaybackSHA1Hint(ctx, hash), selected, "newpick", "", "player", "primary"); err == nil {
		t.Fatal("ambiguous SHA1 silently selected a size")
	}
	if m, _ := s.lookupMaterial("", hash); m.ID != "" {
		t.Fatal("ambiguous hash index resolved")
	}
	if err := s.ForgetMaterial(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if m, byHash := s.lookupMaterial("", hash); m.ID == "" || !byHash {
		t.Fatal("delete did not update hash index")
	}
	unverified := s.Status().Materials[0]
	unverified.VerifiedAt = time.Time{}
	if err := s.putMaterial(unverified); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.lookupMaterial("", hash); m.ID != "" {
		t.Fatal("unverified material accepted from external SHA1")
	}
}

func BenchmarkPlaybackMaterialLookup(b *testing.B) {
	s := &PlaybackCache{}
	for i := 0; i < 50000; i++ {
		hash := DirectLinkKey("hash", time.Unix(int64(i), 0).String())[:40]
		s.doc.Materials = append(s.doc.Materials, PlaybackMaterial{ID: playbackMaterialID(hash, 100), SHA1: hash, Size: 100, Name: "movie.mkv", VerifiedAt: time.Now()})
	}
	s.rebuildPlaybackIndexLocked()
	hash := s.doc.Materials[25000].SHA1
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if m, _ := s.lookupMaterial("", hash); m.ID == "" {
			b.Fatal("not found")
		}
	}
}
