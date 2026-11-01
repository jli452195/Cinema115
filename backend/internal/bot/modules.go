package bot

import (
	"context"
	"errors"
	"fmt"
)

var ErrModuleChanged = errors.New("功能模块分配已变化，请刷新后重试")
var ErrModuleAssigned = errors.New("功能模块已分配给其他 Bot，请先解除分配")

type TelegramModuleView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	AssignedBotID string `json:"assignedBotId"`
}

var telegramModules = []TelegramModuleView{
	{ID: "share_notifications", Name: "115 分享通知", Description: "接收 115 分享链接并生成分享 STRM。"},
	{ID: "cloud_download", Name: "磁力转存", Description: "接收磁力或 ED2K 链接并转存到 115。"},
	{ID: "follow_notifications", Name: "追更通知", Description: "发送追更申请和订阅生成结果。"},
	{ID: "pool_notifications", Name: "播放池通知", Description: "发送动态播放池健康状态通知；解除分配即可停用。"},
}

func (h *Hub) Modules(ctx context.Context) ([]TelegramModuleView, error) {
	modules := append([]TelegramModuleView(nil), telegramModules...)
	if h.settings != nil {
		settings := h.settings.Value()
		modules[0].AssignedBotID = settings.BotID
	}
	if h.Playback != nil && h.Playback.Pool != nil {
		settings := h.Playback.Pool.Settings()
		modules[3].AssignedBotID = settings.NotificationBotID
	}
	if h.Follows != nil {
		settings, err := h.Follows.Store.FollowSettings(ctx)
		if err != nil {
			return nil, err
		}
		modules[2].AssignedBotID = settings.BotID
	}
	for _, profile := range h.profiles.List() {
		if profile.CloudDownload.Enabled {
			modules[1].AssignedBotID = profile.ID
			break
		}
	}
	return modules, nil
}

func (h *Hub) SetModule(ctx context.Context, moduleID, expectedBotID, botID string) error {
	h.moduleMu.Lock()
	defer h.moduleMu.Unlock()
	changed, err := h.setModuleLocked(ctx, moduleID, expectedBotID, botID)
	if err != nil {
		return err
	}
	if changed {
		h.Refresh()
	}
	return nil
}

func (h *Hub) setModuleLocked(ctx context.Context, moduleID, expectedBotID, botID string) (bool, error) {
	modules, err := h.Modules(ctx)
	if err != nil {
		return false, err
	}
	var current *TelegramModuleView
	for i := range modules {
		if modules[i].ID == moduleID {
			current = &modules[i]
			break
		}
	}
	if current == nil {
		return false, fmt.Errorf("%w：未知功能模块", ErrConfig)
	}
	if current.AssignedBotID != expectedBotID {
		return false, ErrModuleChanged
	}
	if current.AssignedBotID == botID {
		return false, nil
	}
	if botID != "" {
		_, err = h.profiles.Get(botID)
		if err != nil {
			return false, err
		}
	}

	var bindErr error
	switch moduleID {
	case "share_notifications":
		if h.settings == nil {
			return false, fmt.Errorf("%w：分享设置尚未初始化", ErrConfig)
		}
		bindErr = h.settings.BindTelegramBot(expectedBotID, botID)
	case "cloud_download":
		bindErr = h.profiles.BindCloudDownload(expectedBotID, botID)
	case "follow_notifications":
		if h.Follows == nil {
			return false, fmt.Errorf("%w：追更设置尚未初始化", ErrConfig)
		}
		bindErr = h.Follows.BindTelegramBot(ctx, expectedBotID, botID)
	case "pool_notifications":
		if h.Playback == nil || h.Playback.Pool == nil {
			return false, fmt.Errorf("%w：播放池设置尚未初始化", ErrConfig)
		}
		bindErr = h.Playback.Pool.BindNotificationBot(ctx, expectedBotID, botID)
	default:
		return false, fmt.Errorf("%w：未知功能模块", ErrConfig)
	}
	if bindErr != nil {
		return false, bindErr
	}
	return true, nil
}

func (h *Hub) DeleteWithModules(ctx context.Context, id string, revision uint64, releaseModules bool) error {
	h.moduleMu.Lock()
	defer h.moduleMu.Unlock()
	profile, err := h.profiles.Get(id)
	if err != nil {
		return err
	}
	if profile.Revision != revision {
		return ErrBotChanged
	}
	modules, err := h.Modules(ctx)
	if err != nil {
		return err
	}
	bound := make([]TelegramModuleView, 0, len(modules))
	for _, module := range modules {
		if module.AssignedBotID == id {
			bound = append(bound, module)
		}
	}
	if len(bound) > 0 && !releaseModules {
		return fmt.Errorf("%w：仍分配有 %d 个功能模块", ErrModuleAssigned, len(bound))
	}
	expectedRevision := revision
	changed := false
	defer func() {
		if changed {
			h.Refresh()
		}
	}()
	for _, module := range bound {
		if module.ID == "cloud_download" {
			expectedRevision++
		}
		moduleChanged, err := h.setModuleLocked(ctx, module.ID, id, "")
		if err != nil {
			return err
		}
		changed = changed || moduleChanged
	}
	profile, err = h.profiles.Get(id)
	if err != nil {
		return err
	}
	if profile.Revision != expectedRevision {
		return ErrBotChanged
	}
	if err := h.profiles.Delete(id, profile.Revision); err != nil {
		return err
	}
	changed = true
	return nil
}
