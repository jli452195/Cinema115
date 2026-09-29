package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

var ErrLogin = errors.New("账号或密码不正确，或账号已停用")
var ErrAuthBusy = errors.New("登录或密码操作繁忙，请稍后重试")
var ErrUserInput = errors.New("用户配置无效")

type WebsiteAuth struct {
	DB                               *storage.Store
	adminName, adminPassword, secret string
	passwordSlots                    chan struct{}
}
type websiteClaims struct {
	Role    string `json:"role,omitempty"`
	Version int64  `json:"version,omitempty"`
	jwt.RegisteredClaims
}

func NewWebsiteAuth(db *storage.Store, name, password, secret string) *WebsiteAuth {
	return &WebsiteAuth{DB: db, adminName: name, adminPassword: password, secret: secret, passwordSlots: make(chan struct{}, 2)}
}
func equalSecret(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (a *WebsiteAuth) passwordWork(ctx context.Context, fn func() (string, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// 限制同时进行的 Argon2 运算，避免匿名请求耗尽 NAS 内存。
	select {
	case a.passwordSlots <- struct{}{}:
		defer func() { <-a.passwordSlots }()
	default:
		return "", ErrAuthBusy
	}
	value, err := fn()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return value, err
}

const passwordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

func passwordDigest(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
}
func (a *WebsiteAuth) hashPassword(ctx context.Context, password string) (string, error) {
	if len(password) < 8 || len(password) > 128 {
		return "", fmt.Errorf("%w：密码需要 8–128 字节", ErrUserInput)
	}
	return a.passwordWork(ctx, func() (string, error) {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return "", fmt.Errorf("生成密码盐: %w", err)
		}
		return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(passwordDigest(password, salt)), nil
	})
}
func (a *WebsiteAuth) verifyPassword(ctx context.Context, password, hash string) error {
	_, err := a.passwordWork(ctx, func() (string, error) {
		parts := strings.Split(strings.TrimPrefix(hash, passwordPrefix), "$")
		salt := make([]byte, 16)
		expected := make([]byte, 32)
		valid := false
		if strings.HasPrefix(hash, passwordPrefix) && len(parts) == 2 {
			s, e1 := base64.RawStdEncoding.DecodeString(parts[0])
			h, e2 := base64.RawStdEncoding.DecodeString(parts[1])
			if e1 == nil && e2 == nil && len(s) == 16 && len(h) == 32 {
				salt = s
				expected = h
				valid = true
			}
		}
		// 不存在的账号也执行一次哈希，避免快速失败泄露用户名。
		match := subtle.ConstantTimeCompare(passwordDigest(password, salt), expected) == 1
		if !valid || !match {
			return "", ErrLogin
		}
		return "", nil
	})
	return err
}
func (a *WebsiteAuth) Login(ctx context.Context, name, password string) (string, model.WebsiteUser, error) {
	var u model.WebsiteUser
	if len(name) > 64 || len(password) > 128 || name == "" || password == "" {
		return "", u, ErrLogin
	}
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, a.adminName) {
		if !equalSecret(password, a.adminPassword) {
			return "", u, ErrLogin
		}
		u = model.WebsiteUser{ID: "admin", Username: a.adminName, Role: "admin"}
	} else {
		var err error
		u, err = a.DB.WebsiteUserByName(ctx, name)
		if err != nil && !errors.Is(err, storage.ErrWebsiteUserNotFound) {
			return "", u, err
		}
		if err = a.verifyPassword(ctx, password, u.PasswordHash); err != nil {
			return "", model.WebsiteUser{}, err
		}
		if u.Disabled {
			return "", model.WebsiteUser{}, ErrLogin
		}
	}
	now := time.Now()
	subject := "user:" + u.ID
	if u.Role == "admin" {
		subject = a.adminName
	}
	claims := websiteClaims{Role: u.Role, Version: u.TokenVersion, RegisteredClaims: jwt.RegisteredClaims{Subject: subject, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.secret))
	if err != nil {
		return "", model.WebsiteUser{}, fmt.Errorf("签发登录凭证: %w", err)
	}
	return token, u, nil
}
func (a *WebsiteAuth) Authenticate(ctx context.Context, value string) (model.WebsiteUser, error) {
	var claims websiteClaims
	token, err := jwt.ParseWithClaims(value, &claims, func(t *jwt.Token) (any, error) { return []byte(a.secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return model.WebsiteUser{}, ErrLogin
	}
	// 仅兼容原管理员用户名签发的无角色令牌，不把未知角色或普通用户提升为管理员。
	if claims.Subject == a.adminName && (claims.Role == "admin" || claims.Role == "") {
		return model.WebsiteUser{ID: "admin", Username: a.adminName, Role: "admin"}, nil
	}
	if claims.Role != "user" || !strings.HasPrefix(claims.Subject, "user:") || claims.Version < 1 {
		return model.WebsiteUser{}, ErrLogin
	}
	u, err := a.DB.WebsiteUser(ctx, strings.TrimPrefix(claims.Subject, "user:"))
	if errors.Is(err, storage.ErrWebsiteUserNotFound) {
		return model.WebsiteUser{}, ErrLogin
	}
	if err != nil {
		return model.WebsiteUser{}, err
	}
	if u.Disabled || u.Role != "user" || u.TokenVersion != claims.Version {
		return model.WebsiteUser{}, ErrLogin
	}
	return u, nil
}

var websiteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,31}$`)

func (a *WebsiteAuth) Create(ctx context.Context, name, password string) (model.WebsiteUser, error) {
	u, err := a.prepareUser(ctx, name, password)
	if err != nil {
		return model.WebsiteUser{}, err
	}
	if err := a.DB.CreateWebsiteUser(ctx, u); err != nil {
		return model.WebsiteUser{}, err
	}
	return u, nil
}

func (a *WebsiteAuth) prepareUser(ctx context.Context, name, password string) (model.WebsiteUser, error) {
	name = strings.TrimSpace(name)
	if !websiteNamePattern.MatchString(name) || strings.EqualFold(name, a.adminName) {
		return model.WebsiteUser{}, fmt.Errorf("%w：用户名需为 3–32 位字母、数字、下划线、点或短横线，且不能与管理员重名", ErrUserInput)
	}
	hash, err := a.hashPassword(ctx, password)
	if err != nil {
		return model.WebsiteUser{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return model.WebsiteUser{}, fmt.Errorf("生成用户编号: %w", err)
	}
	u := model.WebsiteUser{ID: hex.EncodeToString(id[:]), Username: name, Role: "user", PasswordHash: hash, TokenVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	return u, nil
}
func (a *WebsiteAuth) Update(ctx context.Context, id, password string, disabled *bool) error {
	if password == "" && disabled == nil {
		return fmt.Errorf("%w：请选择停用状态或填写新密码", ErrUserInput)
	}
	hash := ""
	if password != "" {
		var err error
		hash, err = a.hashPassword(ctx, password)
		if err != nil {
			return err
		}
	}
	return a.DB.UpdateWebsiteUser(ctx, id, hash, disabled)
}
