package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

var ErrModuleChanged = errors.New("功能模块分配已变化，请刷新后重试")
var ErrModuleAssigned = errors.New("功能模块已分配给其他 Bot，请先解除分配")

type TelegramModuleView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	AssignedBotID string `json:"assignedBotId"`
	SetupHint     string `json:"setupHint,omitempty"`
}

var telegramModules = []TelegramModuleView{
	{ID: "share_notifications", Name: "115 分享 STRM", Description: "接收 115 分享链接，生成 STRM 并发送处理结果。"},
	{ID: "follow_notifications", Name: "追更通知", Description: "发送追更申请和订阅生成结果。"},
	{ID: "pool_notifications", Name: "播放池通知", Description: "发送动态播放池健康状态通知；解除分配即可停用。"},
}

func (h *Hub) Modules(ctx context.Context) ([]TelegramModuleView, error) {
	modules := append([]TelegramModuleView(nil), telegramModules...)
	if h.Anime != nil && h.Anime.Store != nil {
		settings, err := h.Anime.Store.GetAnimeNotificationSettings(ctx)
		if err != nil {
			return nil, err
		}
		modules = append(modules, TelegramModuleView{ID: "anime_notifications", Name: "动漫订阅通知", Description: "推送动漫下载完成、整理完成及任务异常，支持自定义通知模板。", AssignedBotID: settings.BotID})
	}
	if h.settings != nil {
		settings := h.settings.Value()
		setStaticModuleOwner(modules, "share_notifications", settings.BotID)
	}
	if h.Playback != nil && h.Playback.Pool != nil {
		settings := h.Playback.Pool.Settings()
		setStaticModuleOwner(modules, "pool_notifications", settings.NotificationBotID)
	}
	if h.Follows != nil {
		settings, err := h.Follows.Store.FollowSettings(ctx)
		if err != nil {
			return nil, err
		}
		setStaticModuleOwner(modules, "follow_notifications", settings.BotID)
	}
	if h.CloudDownloadSettings != nil {
		for _, target := range h.CloudDownloadSettings.Targets() {
			module := TelegramModuleView{ID: cloudDownloadModuleID(target.ID), Name: "磁力/ED2K 转存：" + target.Name, Description: "转存目录：" + target.TargetPath, AssignedBotID: target.AssignedBotID}
			if !h.CloudDownloadSettings.TargetReady(target) {
				module.SetupHint = "主账号或下载目录已变化，请到磁力/ED2K 转存页面重新选择目录"
			}
			modules = append(modules, module)
		}
	}
	if h.ShareTransferSettings != nil {
		for _, target := range h.ShareTransferSettings.Targets() {
			module := TelegramModuleView{ID: shareTransferModuleID(target.ID), Name: "115 链接转存：" + target.Name, Description: "接收目录：" + target.TargetPath, AssignedBotID: target.AssignedBotID}
			if !h.ShareTransferSettings.TargetReady(target) {
				module.SetupHint = "主账号或接收目录已变化，请到 115 链接转存页面重新选择目录"
			}
			modules = append(modules, module)
		}
	}
	return modules, nil
}

func setStaticModuleOwner(modules []TelegramModuleView, id, owner string) {
	for i := range modules {
		if modules[i].ID == id {
			modules[i].AssignedBotID = owner
			return
		}
	}
}

func cloudDownloadModuleID(targetID string) string { return "cloud_download:" + targetID }

func shareTransferModuleID(targetID string) string { return "share_transfer:" + targetID }

func cloudDownloadTargetID(moduleID string) (string, bool) {
	id, ok := strings.CutPrefix(moduleID, "cloud_download:")
	return id, ok && id != ""
}

func shareTransferTargetID(moduleID string) (string, bool) {
	id, ok := strings.CutPrefix(moduleID, "share_transfer:")
	return id, ok && id != ""
}

func (h *Hub) shareNotificationBotID() string {
	if h.settings == nil {
		return ""
	}
	return h.settings.Value().BotID
}

func (h *Hub) hasShareTransferTarget(botID string) bool {
	if h.ShareTransferSettings == nil {
		return false
	}
	for _, target := range h.ShareTransferSettings.Targets() {
		if target.AssignedBotID == botID {
			return true
		}
	}
	return false
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
		_, isShareTransfer := shareTransferTargetID(moduleID)
		if moduleID == "share_notifications" && h.hasShareTransferTarget(botID) || isShareTransfer && h.shareNotificationBotID() == botID {
			return false, service.ErrTelegramModuleConflict
		}
	}

	var bindErr error
	switch moduleID {
	case "share_notifications":
		if h.settings == nil {
			return false, fmt.Errorf("%w：分享设置尚未初始化", ErrConfig)
		}
		bindErr = h.settings.BindTelegramBot(expectedBotID, botID)
	case "follow_notifications":
		if h.Follows == nil {
			return false, fmt.Errorf("%w：追更设置尚未初始化", ErrConfig)
		}
		bindErr = h.Follows.BindTelegramBot(ctx, expectedBotID, botID)
	case "anime_notifications":
		if h.Anime == nil || h.Anime.Store == nil {
			return false, fmt.Errorf("%w：动漫订阅尚未初始化", ErrConfig)
		}
		bindErr = h.Anime.Store.BindAnimeNotificationBot(ctx, expectedBotID, botID)
		if errors.Is(bindErr, storage.ErrAnimeConflict) {
			return false, ErrModuleChanged
		}
	case "pool_notifications":
		if h.Playback == nil || h.Playback.Pool == nil {
			return false, fmt.Errorf("%w：播放池设置尚未初始化", ErrConfig)
		}
		bindErr = h.Playback.Pool.BindNotificationBot(ctx, expectedBotID, botID)
	default:
		if targetID, ok := shareTransferTargetID(moduleID); ok {
			if h.ShareTransferSettings == nil {
				return false, fmt.Errorf("%w：115 分享转存设置尚未初始化", ErrConfig)
			}
			bindErr = h.ShareTransferSettings.BindTarget(targetID, expectedBotID, botID)
			if errors.Is(bindErr, service.ErrShareTransferTargetChanged) {
				return false, ErrModuleChanged
			}
			if errors.Is(bindErr, service.ErrShareTransferTargetMissing) {
				return false, fmt.Errorf("%w：分享转存目标不存在", ErrConfig)
			}
		} else {
			targetID, ok := cloudDownloadTargetID(moduleID)
			if !ok || h.CloudDownloadSettings == nil {
				return false, fmt.Errorf("%w：未知功能模块", ErrConfig)
			}
			bindErr = h.CloudDownloadSettings.BindTarget(targetID, expectedBotID, botID)
			if errors.Is(bindErr, service.ErrCloudTargetChanged) {
				return false, ErrModuleChanged
			}
			if errors.Is(bindErr, service.ErrCloudTargetMissing) {
				return false, fmt.Errorf("%w：转存目标不存在", ErrConfig)
			}
		}
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
