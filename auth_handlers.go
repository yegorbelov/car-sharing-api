package main

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 10

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"fullName"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userPublic struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	FullName  string `json:"fullName"`
	AvatarURL string `json:"avatarUrl"`
}

type authResponse struct {
	AccessToken string     `json:"accessToken"`
	User        userPublic `json:"user"`
}

type jwtClaims struct {
	UserID int64 `json:"uid"`
	jwt.RegisteredClaims
}

func jwtSecret() []byte {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		s = "dev-only-change-JWT_SECRET-in-production"
	}
	return []byte(s)
}

func signAccessToken(userID int64) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret())
}

func parseAccessToken(token string) (userID int64, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, errors.New("missing token")
	}
	var claims jwtClaims
	_, err = jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return jwtSecret(), nil
	})
	if err != nil {
		return 0, err
	}
	if claims.UserID == 0 {
		return 0, errors.New("invalid claims")
	}
	return claims.UserID, nil
}

func bearerUserID(c *echo.Context) (int64, error) {
	h := c.Request().Header.Get("Authorization")
	if h == "" {
		return 0, errors.New("missing authorization")
	}
	const p = "Bearer "
	if !strings.HasPrefix(strings.TrimSpace(h), p) {
		return 0, errors.New("invalid authorization scheme")
	}
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), p))
	return parseAccessToken(raw)
}

func isStrongPassword(pw string) bool {
	if len(pw) < 8 {
		return false
	}
	var letter, digit bool
	for _, r := range pw {
		if unicode.IsLetter(r) {
			letter = true
		}
		if unicode.IsDigit(r) {
			digit = true
		}
	}
	return letter && digit
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (a *api) postRegister(c *echo.Context) error {
	var req registerRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	email := normalizeEmail(req.Email)
	if email == "" || !strings.Contains(email, "@") {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_email"})
	}
	if !isStrongPassword(req.Password) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "weak_password"})
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "hash_failed"})
	}
	fullName := strings.TrimSpace(req.FullName)
	if fullName == "" {
		fullName = email
	}
	ctx := c.Request().Context()
	var id int64
	err = a.db.QueryRow(ctx, `
		INSERT INTO app_users (email, password_hash, full_name)
		VALUES ($1, $2, $3)
		RETURNING id
	`, email, string(hash), fullName).Scan(&id)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return c.JSON(http.StatusConflict, map[string]string{"error": "email_taken"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	token, err := signAccessToken(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "token_failed"})
	}
	return c.JSON(http.StatusCreated, authResponse{
		AccessToken: token,
		User: userPublic{
			ID:       id,
			Email:    email,
			FullName: fullName,
		},
	})
}

func (a *api) postLogin(c *echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_email"})
	}
	ctx := c.Request().Context()
	var id int64
	var hash string
	var fullName string
	var avatarURL string
	err := a.db.QueryRow(ctx, `
		SELECT id, password_hash, full_name, avatar_url FROM app_users WHERE lower(email) = lower($1)
	`, email).Scan(&id, &hash, &fullName, &avatarURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
	}
	token, err := signAccessToken(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "token_failed"})
	}
	return c.JSON(http.StatusOK, authResponse{
		AccessToken: token,
		User: userPublic{
			ID:        id,
			Email:     email,
			FullName:  fullName,
			AvatarURL: avatarURL,
		},
	})
}

func (a *api) getMe(c *echo.Context) error {
	uid, err := bearerUserID(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	ctx := c.Request().Context()
	var u userPublic
	err = a.db.QueryRow(ctx, `
		SELECT id, email, full_name, avatar_url FROM app_users WHERE id = $1
	`, uid).Scan(&u.ID, &u.Email, &u.FullName, &u.AvatarURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "server_error"})
	}
	return c.JSON(http.StatusOK, map[string]userPublic{"user": u})
}

const echoUserIDKey = "authUserID"

func (a *api) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		uid, err := bearerUserID(c)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}
		// Verify the user still exists in the DB (protects against DB resets / deleted accounts).
		var exists bool
		if err := a.db.QueryRow(c.Request().Context(),
			`SELECT EXISTS(SELECT 1 FROM app_users WHERE id = $1)`, uid,
		).Scan(&exists); err != nil || !exists {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "session_invalid"})
		}
		c.Set(echoUserIDKey, uid)
		return next(c)
	}
}

func authUserID(c *echo.Context) int64 {
	v, _ := c.Get(echoUserIDKey).(int64)
	return v
}
