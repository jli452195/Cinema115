package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"time"
)

var ErrLogin = errors.New("Emby 账号或密码不正确，或账号已停用")
var ErrAuthBusy = errors.New("登录操作繁忙，请稍后重试")
var ErrUserInput = errors.New("用户配置无效")

type PortalAuth struct {
	Members                          *MemberPlaybackService
	DB                               *storage.Store
	adminName, adminPassword, secret string
}
type portalClaims struct {
	Role    string `json:"role"`
	Version int64  `json:"version"`
	jwt.RegisteredClaims
}

func NewPortalAuth(db *storage.Store, name, password, secret string) *PortalAuth {
	return &PortalAuth{DB: db, adminName: name, adminPassword: password, secret: secret}
}
func equalSecret(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func embyProfileID(server, id string) string {
	v := sha256.Sum256([]byte(server + "\x00" + id))
	return hex.EncodeToString(v[:])
}
func (a *PortalAuth) sign(u model.PortalUser) (string, model.PortalUser, error) {
	now := time.Now()
	claims := portalClaims{Role: u.Role, Version: u.TokenVersion, RegisteredClaims: jwt.RegisteredClaims{Subject: u.ID, Issuer: "cinema115-portal", IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.secret))
	return token, u, err
}
func (a *PortalAuth) LoginAdmin(ctx context.Context, name, password string) (string, model.PortalUser, error) {
	if !equalSecret(name, a.adminName) || !equalSecret(password, a.adminPassword) {
		return "", model.PortalUser{}, ErrLogin
	}
	return a.sign(model.PortalUser{ID: "admin", Username: a.adminName, Role: "admin"})
}
func (a *PortalAuth) Login(ctx context.Context, name, password string) (string, model.PortalUser, error) {
	if a.Members == nil || strings.TrimSpace(name) == "" || len(name) > 128 || len(password) > 128 || password == "" {
		return "", model.PortalUser{}, ErrLogin
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c := a.Members.emby.snapshot()
	if c.ServerID == "" || c.APIKey == "" {
		return "", model.PortalUser{}, ErrLogin
	}
	remote, ok := a.Members.NewEmby(c.URL, c.APIKey).(interface {
		AuthenticateUser(context.Context, string, string) (emby.ManagedUser, string, error)
	})
	if !ok {
		return "", model.PortalUser{}, ErrLogin
	}
	user, server, err := remote.AuthenticateUser(ctx, name, password)
	if err != nil || server != c.ServerID || user.Policy["IsDisabled"] == true || user.Policy["IsAdministrator"] == true || strings.HasPrefix(strings.ToLower(user.Name), "c115_") {
		return "", model.PortalUser{}, ErrLogin
	}
	current := a.Members.emby.snapshot()
	if current.ServerID != c.ServerID || current.URL != c.URL {
		return "", model.PortalUser{}, ErrLogin
	}
	u, err := a.Members.ObserveUser(ctx, c, user)
	if err != nil {
		return "", model.PortalUser{}, err
	}
	if r := a.Members.record(u.ID); r.PlaybackOnly || !r.Provisioned {
		return "", model.PortalUser{}, ErrLogin
	}
	return a.sign(u)
}
func (a *PortalAuth) Authenticate(ctx context.Context, value string) (model.PortalUser, error) {
	var claims portalClaims
	token, err := jwt.ParseWithClaims(value, &claims, func(t *jwt.Token) (any, error) { return []byte(a.secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuer("cinema115-portal"))
	if err != nil || !token.Valid {
		return model.PortalUser{}, ErrLogin
	}
	if claims.Subject == "admin" && claims.Role == "admin" {
		return model.PortalUser{ID: "admin", Username: a.adminName, Role: "admin"}, nil
	}
	if claims.Role != "user" || claims.Version < 1 || a.Members == nil {
		return model.PortalUser{}, ErrLogin
	}
	u, err := a.Members.portalUser(ctx, claims.Subject)
	if err != nil || u.TokenVersion != claims.Version {
		return model.PortalUser{}, ErrLogin
	}
	return u, nil
}
func (a *PortalAuth) Register(ctx context.Context, name, password, code string) (model.PortalUser, error) {
	if a.Members == nil {
		return model.PortalUser{}, fmt.Errorf("%w：请先配置 Emby", ErrUserInput)
	}
	hash, err := inviteHash(code)
	if err != nil {
		return model.PortalUser{}, err
	}
	return a.Members.RegisterInvited(ctx, hash, strings.TrimSpace(name), password)
}
