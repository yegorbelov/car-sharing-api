package main

import (
	"context"
	"database/sql"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type dealMessageReplyPreview struct {
	ID             int64  `json:"id"`
	SenderID       int64  `json:"senderId"`
	Body           string `json:"body"`
	AttachmentType string `json:"attachmentType,omitempty"`
}

type dealMessageRow struct {
	ID             int64                    `json:"id"`
	SenderID       int64                    `json:"senderId"`
	Body           string                   `json:"body"`
	CreatedAt      string                   `json:"createdAt"`
	AttachmentURL  string                   `json:"attachmentUrl,omitempty"`
	AttachmentType string                   `json:"attachmentType,omitempty"`
	AttachmentName string                   `json:"attachmentName,omitempty"`
	ReplyToID      *int64                   `json:"replyToId,omitempty"`
	ReplyTo        *dealMessageReplyPreview `json:"replyTo,omitempty"`
}

type postMessageRequest struct {
	Body           string `json:"body"`
	ReplyToID      *int64 `json:"replyToId,omitempty"`
	AttachmentURL  string `json:"attachmentUrl,omitempty"`
	AttachmentType string `json:"attachmentType,omitempty"`
	AttachmentName string `json:"attachmentName,omitempty"`
}

func scanDealMessageRow(
	id, senderID int64,
	body, createdAt, attURL, attType, attName string,
	replyToID sql.NullInt64,
	rID, rSender sql.NullInt64,
	rBody, rAttType sql.NullString,
) dealMessageRow {
	m := dealMessageRow{
		ID:             id,
		SenderID:       senderID,
		Body:           body,
		CreatedAt:      createdAt,
		AttachmentURL:  attURL,
		AttachmentType: attType,
		AttachmentName: attName,
	}
	if replyToID.Valid {
		v := replyToID.Int64
		m.ReplyToID = &v
	}
	if rID.Valid {
		preview := dealMessageReplyPreview{
			ID:       rID.Int64,
			SenderID: rSender.Int64,
			Body:     rBody.String,
		}
		if rAttType.Valid {
			preview.AttachmentType = rAttType.String
		}
		m.ReplyTo = &preview
	}
	return m
}

func userCanAccessDeal(ctx context.Context, db *pgxpool.Pool, dealID, uid int64) (bool, error) {
	var allowed bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM rental_deals WHERE id = $1 AND (renter_id = $2 OR owner_id = $2)
		)
	`, dealID, uid).Scan(&allowed)
	return allowed, err
}

func validateReplyTo(ctx context.Context, db *pgxpool.Pool, dealID int64, replyToID *int64) error {
	if replyToID == nil || *replyToID <= 0 {
		return nil
	}
	var ok bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM deal_messages WHERE id = $1 AND deal_id = $2)
	`, *replyToID, dealID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("invalid_reply")
	}
	return nil
}

func (a *api) getDealMessages(c *echo.Context) error {
	uid := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	allowed, err := userCanAccessDeal(ctx, a.db, dealID, uid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if !allowed {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
	}

	rows, err := a.db.Query(ctx, `
		SELECT m.id, m.sender_id, m.body, m.created_at::text,
			NULLIF(m.attachment_url, ''), NULLIF(m.attachment_type, ''), NULLIF(m.attachment_name, ''),
			m.reply_to_id,
			r.id, r.sender_id, r.body, NULLIF(r.attachment_type, '')
		FROM deal_messages m
		LEFT JOIN deal_messages r ON r.id = m.reply_to_id AND r.deal_id = m.deal_id
		WHERE m.deal_id = $1
		ORDER BY m.created_at ASC, m.id ASC
	`, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []dealMessageRow
	for rows.Next() {
		var (
			id, senderID                                    int64
			body, createdAt                                 string
			attURL, attType, attName                        sql.NullString
			replyToID                                       sql.NullInt64
			rID, rSender                                    sql.NullInt64
			rBody, rAttType                                 sql.NullString
		)
		if err := rows.Scan(
			&id, &senderID, &body, &createdAt,
			&attURL, &attType, &attName,
			&replyToID,
			&rID, &rSender, &rBody, &rAttType,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		list = append(list, scanDealMessageRow(
			id, senderID, body, createdAt,
			nullString(attURL), nullString(attType), nullString(attName),
			replyToID, rID, rSender, rBody, rAttType,
		))
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if list == nil {
		list = []dealMessageRow{}
	}
	return c.JSON(http.StatusOK, list)
}

func nullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func isMultipartContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "multipart/form-data"
}

func (a *api) postDealMessage(c *echo.Context) error {
	if isMultipartContentType(c.Request().Header.Get("Content-Type")) {
		return a.postDealMessageMultipart(c)
	}
	uid := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	var req postMessageRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	body := strings.TrimSpace(req.Body)
	attURL := strings.TrimSpace(req.AttachmentURL)
	attType := strings.TrimSpace(req.AttachmentType)
	attName := strings.TrimSpace(req.AttachmentName)
	if body == "" && attURL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "empty_message"})
	}
	if body != "" && utf8.RuneCountInString(body) > 2000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body_too_long"})
	}
	ctx := c.Request().Context()
	if err := validateReplyTo(ctx, a.db, dealID, req.ReplyToID); err != nil {
		if err.Error() == "invalid_reply" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_reply"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	inserted, err := a.insertDealMessage(ctx, dealID, uid, body, attURL, attType, attName, req.ReplyToID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, inserted)
}

func (a *api) postDealMessageMultipart(c *echo.Context) error {
	if err := c.Request().ParseMultipartForm(maxUploadSize); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_multipart"})
	}

	uid := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	body := strings.TrimSpace(c.FormValue("body"))
	var replyToID *int64
	if raw := strings.TrimSpace(c.FormValue("replyToId")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_reply"})
		}
		replyToID = &id
	}
	attURL, attType, attName, err := saveMessageAttachment(c, "attachment")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if body != "" && utf8.RuneCountInString(body) > 2000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body_too_long"})
	}
	ctx := c.Request().Context()
	if err := validateReplyTo(ctx, a.db, dealID, replyToID); err != nil {
		if err.Error() == "invalid_reply" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_reply"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	inserted, err := a.insertDealMessage(ctx, dealID, uid, body, attURL, attType, attName, replyToID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, inserted)
}

func (a *api) insertDealMessage(
	ctx context.Context,
	dealID, uid int64,
	body, attURL, attType, attName string,
	replyToID *int64,
) (dealMessageRow, error) {
	var (
		id, senderID     int64
		outBody          string
		createdAt        string
		outAttURL        sql.NullString
		outAttType       sql.NullString
		outAttName       sql.NullString
		outReplyTo       sql.NullInt64
		rID, rSender     sql.NullInt64
		rBody, rAttType  sql.NullString
	)
	err := a.db.QueryRow(ctx, `
		INSERT INTO deal_messages (deal_id, sender_id, body, attachment_url, attachment_type, attachment_name, reply_to_id)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE EXISTS (
			SELECT 1 FROM rental_deals d WHERE d.id = $1 AND (d.renter_id = $2 OR d.owner_id = $2)
		)
		RETURNING id, sender_id, body, created_at::text,
			NULLIF(attachment_url, ''), NULLIF(attachment_type, ''), NULLIF(attachment_name, ''),
			reply_to_id
	`, dealID, uid, body, attURL, attType, attName, replyToID).Scan(
		&id, &senderID, &outBody, &createdAt,
		&outAttURL, &outAttType, &outAttName, &outReplyTo,
	)
	if err != nil {
		return dealMessageRow{}, err
	}
	if outReplyTo.Valid {
		_ = a.db.QueryRow(ctx, `
			SELECT id, sender_id, body, NULLIF(attachment_type, '')
			FROM deal_messages WHERE id = $1
		`, outReplyTo.Int64).Scan(&rID, &rSender, &rBody, &rAttType)
	}
	return scanDealMessageRow(
		id, senderID, outBody, createdAt,
		nullString(outAttURL), nullString(outAttType), nullString(outAttName),
		outReplyTo, rID, rSender, rBody, rAttType,
	), nil
}
