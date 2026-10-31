package tguser

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"

	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

// Only enabled source channels have durable pts or restored difference workers.
// An outbox write must succeed before gotd can save the corresponding pts.
// gotd logs some handler/storage failures, so a failed callback also cancels the
// connection and blocks every subsequent checkpoint until it has been restarted.
type relayState struct {
	mu       sync.Mutex
	store    RelayStateStore
	cancel   context.CancelFunc
	owner    int64
	channels map[int64]relayPeer
	err      error
}

type relayUpdatesAPI struct {
	*tg.Client
	state  *relayState
	handle func(context.Context, int64, *tg.Message) error
}

// A difference snapshot can contain the latest available messages even when
// older history has expired. Persist those cards before gotd resets channel pts.
func (api relayUpdatesAPI) UpdatesGetChannelDifference(ctx context.Context, request *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	response, err := api.Client.UpdatesGetChannelDifference(ctx, request)
	if err != nil {
		return nil, err
	}
	if snapshot, ok := response.(*tg.UpdatesChannelDifferenceTooLong); ok {
		messages := make([]*tg.Message, 0, len(snapshot.Messages))
		for _, raw := range snapshot.Messages {
			if message, ok := raw.(*tg.Message); ok {
				messages = append(messages, message)
			}
		}
		sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
		values := make([]tg.UpdateClass, 0, len(messages))
		for _, message := range messages {
			values = append(values, &tg.UpdateNewChannelMessage{Message: message})
		}
		if err := api.state.handle(ctx, &tg.Updates{Updates: values, Users: snapshot.Users, Chats: snapshot.Chats}, api.handle); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func (s *relayState) fail(err error) error {
	if err == nil {
		return nil
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.cancel()
	return err
}

func (s *relayState) failure() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *relayState) check(ctx context.Context, owner int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	if owner != s.owner || owner == 0 {
		return ErrLogin
	}
	return nil
}

func (s *relayState) GetState(ctx context.Context, owner int64) (updates.State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx, owner); err != nil {
		return updates.State{}, false, err
	}
	value, err := s.store.LoadTelegramRelayState(ctx, owner, "global")
	if err != nil {
		return updates.State{}, false, err
	}
	if value == "" {
		return updates.State{}, false, nil
	}
	var state updates.State
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return state, false, errors.New("Telegram 转发同步进度损坏，请检查存储")
	}
	return state, true, nil
}

func (s *relayState) writeState(ctx context.Context, owner int64, change func(*updates.State), initial *updates.State) error {
	s.mu.Lock()
	if err := s.check(ctx, owner); err != nil {
		s.mu.Unlock()
		return err
	}
	var state updates.State
	if initial != nil {
		state = *initial
	} else {
		value, err := s.store.LoadTelegramRelayState(ctx, owner, "global")
		if err != nil {
			s.mu.Unlock()
			return s.fail(err)
		}
		if value == "" {
			s.mu.Unlock()
			return s.fail(errors.New("Telegram 转发同步进度不存在"))
		}
		if err := json.Unmarshal([]byte(value), &state); err != nil {
			s.mu.Unlock()
			return s.fail(errors.New("Telegram 转发同步进度损坏"))
		}
	}
	if change != nil {
		change(&state)
	}
	raw, err := json.Marshal(state)
	if err == nil {
		err = s.store.CommitTelegramRelayUpdate(ctx, owner, "global", string(raw), nil)
	}
	s.mu.Unlock()
	if err != nil {
		return s.fail(err)
	}
	return nil
}

func (s *relayState) SetState(ctx context.Context, owner int64, state updates.State) error {
	return s.writeState(ctx, owner, nil, &state)
}
func (s *relayState) SetPts(ctx context.Context, owner int64, value int) error {
	return s.writeState(ctx, owner, func(state *updates.State) { state.Pts = value }, nil)
}
func (s *relayState) SetQts(ctx context.Context, owner int64, value int) error {
	return s.writeState(ctx, owner, func(state *updates.State) { state.Qts = value }, nil)
}
func (s *relayState) SetDate(ctx context.Context, owner int64, value int) error {
	return s.writeState(ctx, owner, func(state *updates.State) { state.Date = value }, nil)
}
func (s *relayState) SetSeq(ctx context.Context, owner int64, value int) error {
	return s.writeState(ctx, owner, func(state *updates.State) { state.Seq = value }, nil)
}
func (s *relayState) SetDateSeq(ctx context.Context, owner int64, date, seq int) error {
	return s.writeState(ctx, owner, func(state *updates.State) { state.Date = date; state.Seq = seq }, nil)
}

func (s *relayState) GetChannelPts(ctx context.Context, owner, channel int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx, owner); err != nil {
		return 0, false, err
	}
	entry, selected := s.channels[channel]
	if !selected {
		return 0, false, nil
	}
	value, err := s.store.LoadTelegramRelayState(ctx, owner, entry.ID)
	if err != nil {
		return 0, false, err
	}
	if value == "" {
		return entry.Pts, entry.Pts > 0, nil
	}
	pts, err := strconv.Atoi(value)
	if err != nil || pts <= 0 {
		return 0, false, errors.New("Telegram 频道同步进度损坏")
	}
	return pts, true, nil
}

func (s *relayState) SetChannelPts(ctx context.Context, owner, channel int64, pts int) error {
	s.mu.Lock()
	if err := s.check(ctx, owner); err != nil {
		s.mu.Unlock()
		return err
	}
	entry, selected := s.channels[channel]
	if !selected {
		s.mu.Unlock()
		return nil
	}
	err := s.store.CommitTelegramRelayUpdate(ctx, owner, entry.ID, strconv.Itoa(pts), nil)
	s.mu.Unlock()
	if err != nil {
		return s.fail(err)
	}
	return nil
}

func (s *relayState) ForEachChannels(ctx context.Context, owner int64, f func(context.Context, int64, int) error) error {
	s.mu.Lock()
	if err := s.check(ctx, owner); err != nil {
		s.mu.Unlock()
		return err
	}
	ids := make([]int64, 0, len(s.channels))
	for id := range s.channels {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		pts, found, err := s.GetChannelPts(ctx, owner, id)
		if err != nil {
			return err
		}
		if found {
			if err := f(ctx, id, pts); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *relayState) SetChannelAccessHash(ctx context.Context, owner, channel, hash int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx, owner); err != nil {
		return err
	}
	if entry, ok := s.channels[channel]; ok {
		entry.AccessHash = hash
		s.channels[channel] = entry
	}
	return nil
}
func (s *relayState) GetChannelAccessHash(ctx context.Context, owner, channel int64) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx, owner); err != nil {
		return 0, false, err
	}
	entry, ok := s.channels[channel]
	return entry.AccessHash, ok, nil
}

func (s *relayState) selected(update tg.UpdateClass) bool {
	var id int64
	if tooLong, ok := update.(*tg.UpdateChannelTooLong); ok {
		id = tooLong.ChannelID
	} else {
		var valid bool
		id, _, _, valid, _ = tg.IsChannelPtsUpdate(update)
		if !valid {
			return false
		}
	}
	s.mu.Lock()
	_, ok := s.channels[id]
	s.mu.Unlock()
	return ok
}

func (s *relayState) filter(raw tg.UpdatesClass) tg.UpdatesClass {
	filter := func(values []tg.UpdateClass) []tg.UpdateClass {
		result := make([]tg.UpdateClass, 0, len(values))
		for _, value := range values {
			if s.selected(value) {
				if long, ok := value.(*tg.UpdateChannelTooLong); ok {
					// Omitting the optional hint asks gotd for an actual difference
					// rather than cancelling forever on a large PTS-distance hint.
					value = &tg.UpdateChannelTooLong{ChannelID: long.ChannelID}
				}
				result = append(result, value)
			}
		}
		return result
	}
	switch value := raw.(type) {
	case *tg.Updates:
		copy := *value
		copy.Updates = filter(value.Updates)
		return &copy
	case *tg.UpdatesCombined:
		copy := *value
		copy.Updates = filter(value.Updates)
		return &copy
	case *tg.UpdateShort:
		if selected := filter([]tg.UpdateClass{value.Update}); len(selected) != 0 {
			return &tg.UpdateShort{Date: value.Date, Update: selected[0]}
		}
		return &tg.Updates{Date: value.Date}
	case *tg.UpdatesTooLong:
		return raw
	default:
		return &tg.Updates{}
	}
}

func (s *relayState) handle(ctx context.Context, raw tg.UpdatesClass, handle func(context.Context, int64, *tg.Message) error) error {
	s.mu.Lock()
	owner, failed := s.owner, s.err
	s.mu.Unlock()
	if failed != nil {
		return failed
	}
	var list []tg.UpdateClass
	switch value := raw.(type) {
	case *tg.Updates:
		list = value.Updates
	case *tg.UpdatesCombined:
		list = value.Updates
	case *tg.UpdateShort:
		list = []tg.UpdateClass{value.Update}
	}
	for _, value := range list {
		var message *tg.Message
		switch update := value.(type) {
		case *tg.UpdateNewChannelMessage:
			message, _ = update.Message.(*tg.Message)
		case *tg.UpdateEditChannelMessage:
			message, _ = update.Message.(*tg.Message)
		}
		if message == nil {
			continue
		}
		peer, ok := message.PeerID.(*tg.PeerChannel)
		if !ok {
			continue
		}
		s.mu.Lock()
		_, selected := s.channels[peer.ChannelID]
		s.mu.Unlock()
		if !selected {
			continue
		}
		if err := handle(ctx, owner, message); err != nil {
			return s.fail(err)
		}
	}
	return nil
}
