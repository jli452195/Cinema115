package service

import (
	"context"
	"encoding/json"
	"strconv"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type AnimeEnablePreviewInput struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	SettingsRevision int64  `json:"settingsRevision"`
	SourceID         string `json:"sourceId"`
}

type AnimeEnableInput struct {
	ExpectedRevision int64                           `json:"expectedRevision"`
	SettingsRevision int64                           `json:"settingsRevision"`
	Sources          []storage.AnimeActivationSource `json:"sources"`
}

func (s *AnimeAdminService) EnablePreview(ctx context.Context, actor, id string, in AnimeEnablePreviewInput) (AnimePreviewResult, error) {
	var out AnimePreviewResult
	if !model.ValidAnimeID(id) || !model.ValidAnimeID(in.SourceID) || s.Previews == nil {
		return out, storage.ErrAnimeInvalid
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, id, false)
	if err != nil {
		return out, err
	}
	if sub.Revision != in.ExpectedRevision {
		return out, storage.ErrAnimeConflict
	}
	if sub.FirstEnabledAt != nil || sub.Status != "draft" && sub.Status != "paused" {
		return out, storage.ErrAnimeTransition
	}
	found := false
	for _, source := range sub.Sources {
		if source.ID == in.SourceID && source.RunWhenEnabled && source.Status == "paused" {
			found = true
		}
	}
	if !found {
		return out, storage.ErrAnimeReconfirm
	}
	if _, err := s.PrepareDownloadTarget(ctx, in.SettingsRevision); err != nil {
		return out, err
	}
	entry, err := s.Store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		return out, err
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return out, err
	}
	if settings.Revision != in.SettingsRevision {
		return out, storage.ErrAnimeSettingsChanged
	}
	out, err = s.Previews.Generate(ctx, actor, AnimePreviewInput{Config: storage.ActivationConfig(sub, entry, settings), SourceRef: in.SourceID})
	if err != nil || out.Batch == nil {
		return out, err
	}
	// Carry saved form choices into the fresh preview, rather than silently
	// replacing an explicit small selection with every default history match.
	for _, form := range sub.Form.FormSelections {
		if form.SourceRef != in.SourceID {
			continue
		}
		for _, preview := range out.Batch.Sources {
			if preview.Ref != in.SourceID {
				continue
			}
			loaded := make(map[string]bool, len(preview.Items))
			for _, item := range preview.Items {
				loaded[item.Release.ID] = item.Selectable
			}
			selected, dismissed := []string{}, []string{}
			missing := 0
			for _, rid := range form.SelectedReleaseIDs {
				if loaded[rid] {
					selected = append(selected, rid)
				} else {
					missing++
				}
			}
			missing += len(form.UnresolvedReleaseIDs)
			for _, rid := range form.DismissedReleaseIDs {
				if _, ok := loaded[rid]; ok {
					dismissed = append(dismissed, rid)
				}
			}
			b, err := s.Previews.Select(ctx, actor, storage.AnimePreviewRef{BatchID: out.Batch.ID, SourceRef: preview.Ref, PreviewID: preview.ID, LoadRevision: preview.LoadRevision}, selected, dismissed)
			if err != nil {
				return out, err
			}
			out = s.Previews.result(b, out.Issues)
			out.SelectionNotice = "已带回当前范围内 " + strconv.Itoa(len(selected)) + " 条已保存预选，请重新核对后启用。"
			if missing > 0 {
				out.SelectionNotice += "另有 " + strconv.Itoa(missing) + " 条旧预选未能确认，不会自动下载；可展开历史重新勾选。"
			}
			return out, nil
		}
	}
	return out, nil
}

func (s *AnimeAdminService) EnableSubscription(ctx context.Context, actor, key, id string, in AnimeEnableInput) (out model.AnimeControlReceipt, resultErr error) {
	if !model.ValidAnimeID(id) {
		return out, storage.ErrAnimeInvalid
	}
	k, err := animeRequestKey(actor, "POST /api/admin/anime/subscriptions/"+id+"/enable", key, in)
	if err != nil {
		return out, err
	}
	if receipt, err := s.Store.LookupAnimeReceipt(ctx, k); err != nil {
		return out, err
	} else if receipt != nil {
		return out, json.Unmarshal(receipt.Body, &out)
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	session, err := s.PrepareDownloadTarget(ctx, in.SettingsRevision)
	if err != nil {
		return out, err
	}
	return s.Store.EnableAnimeSubscription(ctx, id, in.ExpectedRevision, session.Target(), in.Sources, k)
}
