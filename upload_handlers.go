package main

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

const uploadDir = "./uploads"
const maxUploadSize = 8 << 20 // 8 MB

func ensureUploadDir() {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		panic("cannot create upload dir: " + err.Error())
	}
}

func saveUpload(c *echo.Context, field string) (filename string, err error) {
	file, header, err := c.Request().FormFile(field)
	if err != nil {
		return "", fmt.Errorf("missing_file")
	}
	defer file.Close()

	if header.Size > maxUploadSize {
		return "", fmt.Errorf("file_too_large")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".heic":
	default:
		return "", fmt.Errorf("invalid_file_type")
	}

	uniqueName := fmt.Sprintf("%d_%d%s", time.Now().UnixNano(), header.Size, ext)
	dst := filepath.Join(uploadDir, uniqueName)
	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("save_failed")
	}
	defer f.Close()
	if _, err = io.Copy(f, file); err != nil {
		return "", fmt.Errorf("save_failed")
	}
	return uniqueName, nil
}

// POST /api/v1/auth/avatar  — upload or replace the authenticated user's avatar.
func (a *api) postAvatar(c *echo.Context) error {
	uid := authUserID(c)

	name, err := saveUpload(c, "photo")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Delete the old avatar file if it exists.
	var old string
	_ = a.db.QueryRow(c.Request().Context(),
		`SELECT avatar_url FROM app_users WHERE id = $1`, uid,
	).Scan(&old)
	if old != "" {
		_ = os.Remove(filepath.Join(uploadDir, old))
	}

	url := "/uploads/" + name
	_, err = a.db.Exec(c.Request().Context(),
		`UPDATE app_users SET avatar_url = $1 WHERE id = $2`, url, uid,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"avatarUrl": url})
}

// POST /api/v1/vehicles/:id/photo  — upload or replace a vehicle's cover photo (owner only).
func (a *api) postVehiclePhoto(c *echo.Context) error {
	uid := authUserID(c)
	vid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || vid <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var ownerID sql.NullInt64
	var oldPhoto string
	err = a.db.QueryRow(ctx,
		`SELECT owner_user_id, photo_url FROM vehicles WHERE id = $1`, vid,
	).Scan(&ownerID, &oldPhoto)
	if err != nil {
		if pgx.ErrNoRows == err {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if !ownerID.Valid || ownerID.Int64 != uid {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}

	name, err := saveUpload(c, "photo")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	if oldPhoto != "" {
		_ = os.Remove(filepath.Join(uploadDir, strings.TrimPrefix(oldPhoto, "/uploads/")))
	}

	url := "/uploads/" + name
	_, err = a.db.Exec(ctx, `UPDATE vehicles SET photo_url = $1 WHERE id = $2`, url, vid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"photoUrl": url})
}
