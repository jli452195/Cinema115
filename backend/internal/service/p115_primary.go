package service

// PrimaryID is empty until a primary account is explicitly created.
func (s *P115Accounts) PrimaryID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.value.Accounts {
		if account.Primary {
			return account.ID
		}
	}
	return ""
}

// Primary resolves once and rejects stale directory/account ownership. The
// returned value is a credential snapshot, independent of subsequent edits.
func (s *P115Accounts) Primary(expected string) (P115Account, error) {
	if s == nil {
		return P115Account{}, generationInputError("115 账号服务尚未初始化")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.value.Accounts {
		if !account.Primary {
			continue
		}
		if expected != "" && account.ID != expected {
			return P115Account{}, generationInputError("主账号已变化，请刷新并重新配置目录")
		}
		if account.Cookie == "" {
			return P115Account{}, generationInputError("请先在 115 助手中登录主账号")
		}
		return account, nil
	}
	return P115Account{}, generationInputError("请先在 115 助手中配置主账号")
}

func (s *P115Accounts) ValidatePrimary(id string) error {
	_, err := s.Primary(id)
	return err
}

func (s *P115Accounts) ValidateSecondary(id string) error {
	if err := s.ValidateSelection(id); err != nil {
		return err
	}
	a, err := s.Get(id)
	if err != nil {
		return err
	}
	if a.Primary {
		return generationInputError("用户播放和秒传目标必须绑定小号")
	}
	return nil
}
