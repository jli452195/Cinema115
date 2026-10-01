package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"testing"
)

type summaryChecker struct {
	calls, checks int
	before        func()
}

func (s *summaryChecker) CheckCookie(context.Context, string) (bool, error) {
	s.checks++
	return true, nil
}
func (s *summaryChecker) QuerySummary(context.Context, string, string) (model.P115Summary, bool, error) {
	s.calls++
	if s.before != nil {
		s.before()
	}
	return model.P115Summary{VIP: "normal", SpaceKnown: true, RemainingBytes: 0, CheckedAt: "2026-10-01T00:00:00Z"}, true, nil
}

func TestAccountSummaryStatusAndLoginReuseVerification(t *testing.T) {
	_, accounts, _ := setupAccountsAPI(t, nil, nil)
	checker := &summaryChecker{}
	r := gin.New()
	RegisterP115Accounts(r.Group("/api/admin"), accounts, checker, nil, nil)
	accountRequest(t, r, "GET", "/accounts/default/status", "", 200)
	a, _ := accounts.Get("default")
	if checker.checks != 0 || checker.calls != 1 || a.Summary.VIP != "normal" || !a.Summary.SpaceKnown || a.Revision != 2 {
		t.Fatal("status duplicated verification or lost metadata")
	}
	accountRequest(t, r, "GET", "/accounts", "", 200)
	if checker.calls != 1 {
		t.Fatal("list caused upstream request")
	}
	accountRequest(t, r, "PUT", "/accounts/default", `{"revision":2,"cookie":"UID=111_B; SEID=new"}`, 200)
	a, _ = accounts.Get("default")
	if checker.checks != 0 || checker.calls != 2 || a.Summary.VIP != "normal" || a.Revision != 3 {
		t.Fatal("login failed to atomically save queried summary")
	}
	checker.before = func() {
		cookie := "UID=111_C; SEID=next"
		if err := accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
			t.Fatal(err)
		}
	}
	accountRequest(t, r, "GET", "/accounts/default/status", "", 409)
	a, _ = accounts.Get("default")
	if a.Summary.CheckedAt != "" {
		t.Fatal("stale summary saved across credential change")
	}
}
