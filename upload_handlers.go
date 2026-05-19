package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
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

// saveMessageAttachment stores a chat image or document; returns public URL path, type, and original name.
func saveMessageAttachment(c *echo.Context, field string) (publicURL, attType, originalName string, err error) {
	candidates := []string{field, "attachment", "photo"}
	var (
		file   multipart.File
		header *multipart.FileHeader
	)
	seen := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		f, h, ferr := c.Request().FormFile(name)
		if ferr == nil {
			file, header = f, h
			break
		}
	}
	if file == nil || header == nil {
		return "", "", "", fmt.Errorf("missing_file")
	}
	defer file.Close()

	if header.Size > maxUploadSize {
		return "", "", "", fmt.Errorf("file_too_large")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	attType = "file"
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".heic":
		attType = "image"
	case ".pdf", ".doc", ".docx", ".txt", ".zip", ".xls", ".xlsx":
	default:
		return "", "", "", fmt.Errorf("invalid_file_type")
	}

	uniqueName := fmt.Sprintf("%d_%d%s", time.Now().UnixNano(), header.Size, ext)
	dst := filepath.Join(uploadDir, uniqueName)
	f, err := os.Create(dst)
	if err != nil {
		return "", "", "", fmt.Errorf("save_failed")
	}
	defer f.Close()
	if _, err = io.Copy(f, file); err != nil {
		return "", "", "", fmt.Errorf("save_failed")
	}
	name := strings.TrimSpace(header.Filename)
	if name == "" {
		name = uniqueName
	}
	return "/uploads/" + uniqueName, attType, name, nil
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
		_ = os.Remove(filepath.Join(uploadDir, strings.TrimPrefix(old, "/uploads/")))
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

// POST /api/v1/vehicles/:id/photo  — append a photo (owner only, max 10 per vehicle).
func (a *api) postVehiclePhoto(c *echo.Context) error {
	uid := authUserID(c)
	vid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || vid <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var ownerID sql.NullInt64
	var legacyPhoto, photoURLsJSON string
	err = a.db.QueryRow(ctx,
		`SELECT owner_user_id, photo_url, COALESCE(photo_urls, '[]') FROM vehicles WHERE id = $1`, vid,
	).Scan(&ownerID, &legacyPhoto, &photoURLsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if !ownerID.Valid || ownerID.Int64 != uid {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}

	urls := effectiveVehiclePhotoList(photoURLsJSON, legacyPhoto)
	if len(urls) >= maxVehiclePhotos {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "too_many_photos"})
	}

	name, err := saveUpload(c, "photo")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	newURL := "/uploads/" + name
	urls = append(urls, newURL)
	jsonStr, err := marshalVehiclePhotoURLs(urls)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	cover := primaryVehiclePhoto(urls)
	_, err = a.db.Exec(ctx, `UPDATE vehicles SET photo_urls = $1, photo_url = $2 WHERE id = $3`, jsonStr, cover, vid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"photoUrl":  cover,
		"photoUrls": urls,
	})
}
