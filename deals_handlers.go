package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

const (
	dealPendingOwner = "pending_owner"
	dealActive       = "active"
	dealCompleted    = "completed"
	dealCancelled    = "cancelled"
)

func computeHoldCents(dayCount int, pricePerDayCents int32) int64 {
	if dayCount < 1 {
		dayCount = 1
	}
	if dayCount > 14 {
		dayCount = 14
	}
	h := int64(pricePerDayCents) * int64(dayCount)
	if h < 5000 {
		h = 5000
	}
	if h > 200_000 {
		h = 200_000
	}
	return h
}

type createDealRequest struct {
	VehicleID int32 `json:"vehicleId"`
	DayCount  int   `json:"dayCount"`
}

type dealSummary struct {
	ID              int64  `json:"id"`
	VehicleID       int32  `json:"vehicleId"`
	VehicleTitle    string `json:"vehicleTitle"`
	RenterID        int64  `json:"renterId"`
	OwnerID         int64  `json:"ownerId"`
	RenterName      string `json:"renterName"`
	OwnerName       string `json:"ownerName"`
	Status          string `json:"status"`
	HoldAmountCents int64  `json:"holdAmountCents"`
	MyRole          string `json:"myRole"`
	DayCount        int32  `json:"dayCount"`
	StartDate       string `json:"startDate"`
	EndDate         string `json:"endDate"`
	CreatedAt       string `json:"createdAt"`
}

type dealMessageRow struct {
	ID        int64  `json:"id"`
	SenderID  int64  `json:"senderId"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

type postMessageRequest struct {
	Body string `json:"body"`
}

type ledgerRow struct {
	ID         int64   `json:"id"`
	DeltaCents int64   `json:"deltaCents"`
	EntryType  string  `json:"entryType"`
	Note       string  `json:"note"`
	CreatedAt  string  `json:"createdAt"`
	DealID     *int64  `json:"dealId,omitempty"`
}

type walletResponse struct {
	BalanceCents int64       `json:"balanceCents"`
	Balance      float64     `json:"balance"`
	Recent       []ledgerRow `json:"recent"`
}

func (a *api) postDeal(c *echo.Context) error {
	renterID := authUserID(c)
	var req createDealRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	if req.VehicleID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_vehicle"})
	}
	dayCount := req.DayCount
	if dayCount == 0 {
		dayCount = 3
	}
	if dayCount < 1 || dayCount > 14 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_day_count"})
	}

	ctx := c.Request().Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer tx.Rollback(ctx)

	var ownerID sql.NullInt64
	var price int32
	var title string
	err = tx.QueryRow(ctx, `
		SELECT owner_user_id, price_per_day_cents, title FROM vehicles WHERE id = $1 FOR UPDATE
	`, req.VehicleID).Scan(&ownerID, &price, &title)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "vehicle_not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if !ownerID.Valid {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "vehicle_has_no_owner"})
	}
	if ownerID.Int64 == renterID {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot_rent_own_car"})
	}

	// Prevent double-booking: one active or pending deal per vehicle at a time.
	var conflictCount int
	if err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM rental_deals
		WHERE vehicle_id = $1 AND status IN ($2, $3)
	`, req.VehicleID, dealPendingOwner, dealActive).Scan(&conflictCount); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if conflictCount > 0 {
		return c.JSON(http.StatusConflict, map[string]string{"error": "vehicle_unavailable"})
	}

	hold := computeHoldCents(dayCount, price)

	var balance int64
	err = tx.QueryRow(ctx, `SELECT balance_cents FROM app_users WHERE id = $1 FOR UPDATE`, renterID).Scan(&balance)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if balance < hold {
		return c.JSON(http.StatusPaymentRequired, map[string]string{"error": "insufficient_funds"})
	}

	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.AddDate(0, 0, dayCount)

	var dealID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO rental_deals (
			vehicle_id, renter_id, owner_id, status, hold_amount_cents, day_count, start_date, end_date
		) VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8::date)
		RETURNING id
	`, req.VehicleID, renterID, ownerID.Int64, dealPendingOwner, hold, dayCount, start, end).Scan(&dealID)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": pe.Message})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	_, err = tx.Exec(ctx, `
		UPDATE app_users SET balance_cents = balance_cents - $1 WHERE id = $2
	`, hold, renterID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
		VALUES ($1, $2, $3, 'hold', $4)
	`, renterID, dealID, -hold, "Security hold for rental request #"+strconv.FormatInt(dealID, 10))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"id":              dealID,
		"vehicleId":       req.VehicleID,
		"vehicleTitle":    title,
		"status":          dealPendingOwner,
		"holdAmountCents": hold,
		"dayCount":        dayCount,
		"startDate":       start.Format("2006-01-02"),
		"endDate":         end.Format("2006-01-02"),
	})
}

func (a *api) getMyDeals(c *echo.Context) error {
	uid := authUserID(c)
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT d.id, d.vehicle_id, v.title, d.renter_id, d.owner_id,
		       ru.full_name, ou.full_name, d.status, d.hold_amount_cents,
		       CASE WHEN d.renter_id = $1 THEN 'renter' ELSE 'owner' END,
		       d.day_count, d.start_date::text, d.end_date::text, d.created_at::text
		FROM rental_deals d
		JOIN vehicles v ON v.id = d.vehicle_id
		JOIN app_users ru ON ru.id = d.renter_id
		JOIN app_users ou ON ou.id = d.owner_id
		WHERE d.renter_id = $1 OR d.owner_id = $1
		ORDER BY d.created_at DESC
	`, uid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []dealSummary
	for rows.Next() {
		var d dealSummary
		if err := rows.Scan(
			&d.ID, &d.VehicleID, &d.VehicleTitle, &d.RenterID, &d.OwnerID,
			&d.RenterName, &d.OwnerName, &d.Status, &d.HoldAmountCents,
			&d.MyRole, &d.DayCount, &d.StartDate, &d.EndDate, &d.CreatedAt,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if list == nil {
		list = []dealSummary{}
	}
	return c.JSON(http.StatusOK, list)
}

func (a *api) getDeal(c *echo.Context) error {
	uid := authUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	var d dealSummary
	err = a.db.QueryRow(ctx, `
		SELECT d.id, d.vehicle_id, v.title, d.renter_id, d.owner_id,
		       ru.full_name, ou.full_name, d.status, d.hold_amount_cents,
		       CASE WHEN d.renter_id = $1 THEN 'renter' ELSE 'owner' END,
		       d.day_count, d.start_date::text, d.end_date::text, d.created_at::text
		FROM rental_deals d
		JOIN vehicles v ON v.id = d.vehicle_id
		JOIN app_users ru ON ru.id = d.renter_id
		JOIN app_users ou ON ou.id = d.owner_id
		WHERE d.id = $2 AND (d.renter_id = $1 OR d.owner_id = $1)
	`, uid, id).Scan(
		&d.ID, &d.VehicleID, &d.VehicleTitle, &d.RenterID, &d.OwnerID,
		&d.RenterName, &d.OwnerName, &d.Status, &d.HoldAmountCents,
		&d.MyRole, &d.DayCount, &d.StartDate, &d.EndDate, &d.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, d)
}

func (a *api) getDealMessages(c *echo.Context) error {
	uid := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	var allowed bool
	err = a.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM rental_deals WHERE id = $1 AND (renter_id = $2 OR owner_id = $2)
		)
	`, dealID, uid).Scan(&allowed)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if !allowed {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
	}

	rows, err := a.db.Query(ctx, `
		SELECT id, sender_id, body, created_at::text
		FROM deal_messages WHERE deal_id = $1 ORDER BY created_at ASC, id ASC
	`, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []dealMessageRow
	for rows.Next() {
		var m dealMessageRow
		if err := rows.Scan(&m.ID, &m.SenderID, &m.Body, &m.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if list == nil {
		list = []dealMessageRow{}
	}
	return c.JSON(http.StatusOK, list)
}

func (a *api) postDealMessage(c *echo.Context) error {
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
	if body == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "empty_body"})
	}
	if utf8.RuneCountInString(body) > 2000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body_too_long"})
	}

	ctx := c.Request().Context()
	var inserted dealMessageRow
	err = a.db.QueryRow(ctx, `
		INSERT INTO deal_messages (deal_id, sender_id, body)
		SELECT $1, $2, $3
		WHERE EXISTS (
			SELECT 1 FROM rental_deals d WHERE d.id = $1 AND (d.renter_id = $2 OR d.owner_id = $2)
		)
		RETURNING id, sender_id, body, created_at::text
	`, dealID, uid, body).Scan(&inserted.ID, &inserted.SenderID, &inserted.Body, &inserted.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, inserted)
}

func (a *api) postAcceptDeal(c *echo.Context) error {
	ownerID := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	cmd, err := a.db.Exec(ctx, `
		UPDATE rental_deals
		SET status = $1, updated_at = NOW()
		WHERE id = $2 AND owner_id = $3 AND status = $4
	`, dealActive, dealID, ownerID, dealPendingOwner)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if cmd.RowsAffected() == 0 {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot_accept"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": dealActive})
}

func refundRenterHold(ctx context.Context, tx pgx.Tx, dealID, renterID, hold int64, note string) error {
	_, err := tx.Exec(ctx, `
		UPDATE app_users SET balance_cents = balance_cents + $1 WHERE id = $2
	`, hold, renterID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
		VALUES ($1, $2, $3, 'release_hold', $4)
	`, renterID, dealID, hold, note)
	return err
}

func (a *api) postDeclineDeal(c *echo.Context) error {
	ownerID := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer tx.Rollback(ctx)

	var renterID int64
	var hold int64
	var st string
	err = tx.QueryRow(ctx, `
		SELECT renter_id, hold_amount_cents, status FROM rental_deals
		WHERE id = $1 AND owner_id = $2 FOR UPDATE
	`, dealID, ownerID).Scan(&renterID, &hold, &st)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if st != dealPendingOwner {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot_decline"})
	}

	_, err = tx.Exec(ctx, `
		UPDATE rental_deals SET status = $1, updated_at = NOW() WHERE id = $2
	`, dealCancelled, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := refundRenterHold(ctx, tx, dealID, renterID, hold, "Owner declined deal #"+strconv.FormatInt(dealID, 10)); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": dealCancelled})
}

func (a *api) postRenterCancel(c *echo.Context) error {
	renterID := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer tx.Rollback(ctx)

	var hold int64
	var st string
	err = tx.QueryRow(ctx, `
		SELECT hold_amount_cents, status FROM rental_deals
		WHERE id = $1 AND renter_id = $2 FOR UPDATE
	`, dealID, renterID).Scan(&hold, &st)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if st != dealPendingOwner {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot_cancel"})
	}
	_, err = tx.Exec(ctx, `UPDATE rental_deals SET status = $1, updated_at = NOW() WHERE id = $2`, dealCancelled, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := refundRenterHold(ctx, tx, dealID, renterID, hold, "Renter cancelled deal #"+strconv.FormatInt(dealID, 10)); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": dealCancelled})
}

func (a *api) postCompleteDeal(c *echo.Context) error {
	ownerID := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer tx.Rollback(ctx)

	var renterID int64
	var hold int64
	var st string
	err = tx.QueryRow(ctx, `
		SELECT renter_id, hold_amount_cents, status FROM rental_deals
		WHERE id = $1 AND owner_id = $2 FOR UPDATE
	`, dealID, ownerID).Scan(&renterID, &hold, &st)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if st != dealActive {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot_complete"})
	}

	_, err = tx.Exec(ctx, `UPDATE rental_deals SET status = $1, updated_at = NOW() WHERE id = $2`, dealCompleted, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	_, err = tx.Exec(ctx, `
		UPDATE app_users SET balance_cents = balance_cents + $1 WHERE id = $2
	`, hold, ownerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
		VALUES ($1, $2, $3, 'payout_owner', $4)
	`, ownerID, dealID, hold, "Rental payout for deal #"+strconv.FormatInt(dealID, 10))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": dealCompleted})
}

func (a *api) getWallet(c *echo.Context) error {
	uid := authUserID(c)
	ctx := c.Request().Context()
	var bal int64
	if err := a.db.QueryRow(ctx, `SELECT balance_cents FROM app_users WHERE id = $1`, uid).Scan(&bal); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	rows, err := a.db.Query(ctx, `
		SELECT id, delta_cents, entry_type, note, created_at::text, deal_id
		FROM wallet_ledger WHERE user_id = $1 ORDER BY id DESC LIMIT 40
	`, uid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var recent []ledgerRow
	for rows.Next() {
		var r ledgerRow
		var deal sql.NullInt64
		if err := rows.Scan(&r.ID, &r.DeltaCents, &r.EntryType, &r.Note, &r.CreatedAt, &deal); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if deal.Valid {
			d := deal.Int64
			r.DealID = &d
		}
		recent = append(recent, r)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if recent == nil {
		recent = []ledgerRow{}
	}
	return c.JSON(http.StatusOK, walletResponse{
		BalanceCents: bal,
		Balance:      float64(bal) / 100,
		Recent:       recent,
	})
}

func registerDealRoutes(g *echo.Group, h *api) {
	g.POST("/deals", h.postDeal)
	g.GET("/deals/mine", h.getMyDeals)
	g.GET("/deals/:id", h.getDeal)
	g.GET("/deals/:id/messages", h.getDealMessages)
	g.POST("/deals/:id/messages", h.postDealMessage)
	g.POST("/deals/:id/accept", h.postAcceptDeal)
	g.POST("/deals/:id/decline", h.postDeclineDeal)
	g.POST("/deals/:id/cancel", h.postRenterCancel)
	g.POST("/deals/:id/complete", h.postCompleteDeal)
	g.GET("/wallet", h.getWallet)
}
