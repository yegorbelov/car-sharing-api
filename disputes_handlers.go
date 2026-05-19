package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

const (
	dealDisputed     = "disputed"
	disputeOpen      = "open"
	disputeResolved  = "resolved"
)

var allowedDisputeReasons = map[string]string{
	"vehicle_damage":   "Vehicle damage",
	"cleanliness":      "Cleanliness issue",
	"wrong_condition":  "Not as described",
	"late_return":      "Late return",
	"billing":          "Billing disagreement",
	"other":            "Other",
}

var allowedDisputeResolutions = map[string]string{
	"favor_renter": "Full refund to renter",
	"favor_owner":  "Full payout to owner",
	"split":        "Custom split",
}

type disputeEvidence struct {
	ID              int64  `json:"id"`
	AttachmentURL   string `json:"attachmentUrl"`
	AttachmentType  string `json:"attachmentType"`
	Caption         string `json:"caption"`
	UploadedBy      int64  `json:"uploadedBy"`
	CreatedAt       string `json:"createdAt"`
}

type disputeView struct {
	ID                 int64             `json:"id"`
	DealID             int64             `json:"dealId"`
	Status             string            `json:"status"`
	ReasonCode         string            `json:"reasonCode"`
	ReasonLabel        string            `json:"reasonLabel"`
	Description        string            `json:"description"`
	OpenedByUserID     int64             `json:"openedByUserId"`
	OpenedByName       string            `json:"openedByName"`
	RenterRefundCents  int64             `json:"renterRefundCents"`
	OwnerPayoutCents   int64             `json:"ownerPayoutCents"`
	ResolutionCode     string            `json:"resolutionCode,omitempty"`
	ResolutionNote     string            `json:"resolutionNote,omitempty"`
	ArbitratorUserID   *int64            `json:"arbitratorUserId,omitempty"`
	ArbitratorName     string            `json:"arbitratorName,omitempty"`
	ResolvedAt         string            `json:"resolvedAt,omitempty"`
	CreatedAt          string            `json:"createdAt"`
	VehicleTitle       string            `json:"vehicleTitle"`
	RenterName         string            `json:"renterName"`
	OwnerName          string            `json:"ownerName"`
	HoldAmountCents    int64             `json:"holdAmountCents"`
	DealStatus         string            `json:"dealStatus"`
	Evidence           []disputeEvidence `json:"evidence"`
}

type openDisputeRequest struct {
	ReasonCode  string `json:"reasonCode"`
	Description string `json:"description"`
}

type resolveDisputeRequest struct {
	Resolution        string `json:"resolution"`
	RenterRefundCents *int64 `json:"renterRefundCents"`
	OwnerPayoutCents  *int64 `json:"ownerPayoutCents"`
	Note              string `json:"note"`
}

func disputeReasonLabel(code string) string {
	if l, ok := allowedDisputeReasons[code]; ok {
		return l
	}
	return code
}

func (a *api) dealPartyConflict(ctx context.Context, dealID, uid int64) (renterID, ownerID int64, hold int64, dealSt string, err error) {
	err = a.db.QueryRow(ctx, `
		SELECT renter_id, owner_id, hold_amount_cents, status
		FROM rental_deals WHERE id = $1
	`, dealID).Scan(&renterID, &ownerID, &hold, &dealSt)
	return
}

func (a *api) loadDisputeByID(ctx context.Context, disputeID int64) (disputeView, error) {
	return a.scanDisputeQuery(ctx, `
		SELECT ds.id, ds.deal_id, ds.status, ds.reason_code, ds.description,
			ds.opened_by_user_id, opener.full_name,
			ds.renter_refund_cents, ds.owner_payout_cents,
			ds.resolution_code, ds.resolution_note,
			ds.arbitrator_user_id, arb.full_name,
			COALESCE(ds.resolved_at::text, ''),
			ds.created_at::text,
			v.title, ru.full_name, ou.full_name,
			d.hold_amount_cents, d.status
		FROM rental_disputes ds
		JOIN rental_deals d ON d.id = ds.deal_id
		JOIN vehicles v ON v.id = d.vehicle_id
		JOIN app_users ru ON ru.id = d.renter_id
		JOIN app_users ou ON ou.id = d.owner_id
		JOIN app_users opener ON opener.id = ds.opened_by_user_id
		LEFT JOIN app_users arb ON arb.id = ds.arbitrator_user_id
		WHERE ds.id = $1
	`, disputeID)
}

func (a *api) loadDisputeByDealID(ctx context.Context, dealID int64) (disputeView, error) {
	return a.scanDisputeQuery(ctx, `
		SELECT ds.id, ds.deal_id, ds.status, ds.reason_code, ds.description,
			ds.opened_by_user_id, opener.full_name,
			ds.renter_refund_cents, ds.owner_payout_cents,
			ds.resolution_code, ds.resolution_note,
			ds.arbitrator_user_id, arb.full_name,
			COALESCE(ds.resolved_at::text, ''),
			ds.created_at::text,
			v.title, ru.full_name, ou.full_name,
			d.hold_amount_cents, d.status
		FROM rental_disputes ds
		JOIN rental_deals d ON d.id = ds.deal_id
		JOIN vehicles v ON v.id = d.vehicle_id
		JOIN app_users ru ON ru.id = d.renter_id
		JOIN app_users ou ON ou.id = d.owner_id
		JOIN app_users opener ON opener.id = ds.opened_by_user_id
		LEFT JOIN app_users arb ON arb.id = ds.arbitrator_user_id
		WHERE ds.deal_id = $1
	`, dealID)
}

func (a *api) scanDisputeQuery(ctx context.Context, q string, id int64) (disputeView, error) {
	var d disputeView
	var arbID sql.NullInt64
	var arbName sql.NullString
	err := a.db.QueryRow(ctx, q, id).Scan(
		&d.ID, &d.DealID, &d.Status, &d.ReasonCode, &d.Description,
		&d.OpenedByUserID, &d.OpenedByName,
		&d.RenterRefundCents, &d.OwnerPayoutCents,
		&d.ResolutionCode, &d.ResolutionNote,
		&arbID, &arbName,
		&d.ResolvedAt,
		&d.CreatedAt,
		&d.VehicleTitle, &d.RenterName, &d.OwnerName,
		&d.HoldAmountCents, &d.DealStatus,
	)
	if err != nil {
		return d, err
	}
	d.ReasonLabel = disputeReasonLabel(d.ReasonCode)
	if arbID.Valid {
		v := arbID.Int64
		d.ArbitratorUserID = &v
	}
	if arbName.Valid {
		d.ArbitratorName = arbName.String
	}
	ev, err := a.loadDisputeEvidence(ctx, d.ID)
	if err != nil {
		return d, err
	}
	d.Evidence = ev
	return d, nil
}

func (a *api) loadDisputeEvidence(ctx context.Context, disputeID int64) ([]disputeEvidence, error) {
	rows, err := a.db.Query(ctx, `
		SELECT id, attachment_url, attachment_type, caption, uploaded_by_user_id, created_at::text
		FROM dispute_evidence WHERE dispute_id = $1 ORDER BY id
	`, disputeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []disputeEvidence
	for rows.Next() {
		var e disputeEvidence
		if err := rows.Scan(&e.ID, &e.AttachmentURL, &e.AttachmentType, &e.Caption, &e.UploadedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	if list == nil {
		list = []disputeEvidence{}
	}
	return list, rows.Err()
}

func (a *api) listDisputeReasons(c *echo.Context) error {
	type reason struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	out := make([]reason, 0, len(allowedDisputeReasons))
	for code, label := range allowedDisputeReasons {
		out = append(out, reason{Code: code, Label: label})
	}
	return c.JSON(http.StatusOK, out)
}

func (a *api) getDealDispute(c *echo.Context) error {
	uid := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	renterID, ownerID, _, _, err := a.dealPartyConflict(ctx, dealID, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if uid != renterID && uid != ownerID {
		roles, _ := a.loadUserRoles(ctx, uid)
		if !roles.IsAdmin && !roles.IsArbitrator {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
		}
	}
	d, err := a.loadDisputeByDealID(ctx, dealID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "no_dispute"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, d)
}

func (a *api) postDealDispute(c *echo.Context) error {
	uid := authUserID(c)
	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	var req openDisputeRequest
	if isMultipartContentType(c.Request().Header.Get("Content-Type")) {
		req.ReasonCode = strings.TrimSpace(c.FormValue("reasonCode"))
		req.Description = strings.TrimSpace(c.FormValue("description"))
	} else if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	req.ReasonCode = strings.ToLower(strings.TrimSpace(req.ReasonCode))
	if _, ok := allowedDisputeReasons[req.ReasonCode]; !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_reason_code"})
	}
	req.Description = strings.TrimSpace(req.Description)
	if len(req.Description) < 10 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "description_too_short"})
	}
	if len(req.Description) > 2000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "description_too_long"})
	}

	ctx := c.Request().Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer tx.Rollback(ctx)

	var renterID, ownerID int64
	var hold int64
	var st string
	err = tx.QueryRow(ctx, `
		SELECT renter_id, owner_id, hold_amount_cents, status
		FROM rental_deals WHERE id = $1 FOR UPDATE
	`, dealID).Scan(&renterID, &ownerID, &hold, &st)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if uid != renterID && uid != ownerID {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
	if st != dealActive && st != dealCompleted {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot_dispute_status"})
	}
	var openCount int
	if err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM rental_disputes WHERE deal_id = $1 AND status = $2
	`, dealID, disputeOpen).Scan(&openCount); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if openCount > 0 {
		return c.JSON(http.StatusConflict, map[string]string{"error": "dispute_already_open"})
	}

	var disputeID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO rental_disputes (deal_id, opened_by_user_id, reason_code, description, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, dealID, uid, req.ReasonCode, req.Description, disputeOpen).Scan(&disputeID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	_, err = tx.Exec(ctx, `
		UPDATE rental_deals SET status = $1, updated_at = NOW() WHERE id = $2
	`, dealDisputed, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if isMultipartContentType(c.Request().Header.Get("Content-Type")) {
		url, attType, _, saveErr := saveMessageAttachment(c, "photo")
		if saveErr == nil && url != "" {
			caption := strings.TrimSpace(c.FormValue("caption"))
			_, _ = tx.Exec(ctx, `
				INSERT INTO dispute_evidence (dispute_id, uploaded_by_user_id, attachment_url, attachment_type, caption)
				VALUES ($1, $2, $3, $4, $5)
			`, disputeID, uid, url, attType, caption)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	d, err := a.loadDisputeByID(ctx, disputeID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, d)
}

func (a *api) listArbitrationDisputes(c *echo.Context) error {
	uid := authUserID(c)
	ctx := c.Request().Context()
	status := strings.TrimSpace(c.QueryParam("status"))
	if status == "" {
		status = disputeOpen
	}
	rows, err := a.db.Query(ctx, `
		SELECT ds.id, ds.deal_id, ds.status, ds.reason_code, ds.description,
			ds.opened_by_user_id, opener.full_name,
			ds.renter_refund_cents, ds.owner_payout_cents,
			ds.resolution_code, ds.resolution_note,
			ds.arbitrator_user_id, arb.full_name,
			COALESCE(ds.resolved_at::text, ''),
			ds.created_at::text,
			v.title, ru.full_name, ou.full_name,
			d.hold_amount_cents, d.status
		FROM rental_disputes ds
		JOIN rental_deals d ON d.id = ds.deal_id
		JOIN vehicles v ON v.id = d.vehicle_id
		JOIN app_users ru ON ru.id = d.renter_id
		JOIN app_users ou ON ou.id = d.owner_id
		JOIN app_users opener ON opener.id = ds.opened_by_user_id
		LEFT JOIN app_users arb ON arb.id = ds.arbitrator_user_id
		WHERE ds.status = $1
			AND d.renter_id <> $2 AND d.owner_id <> $2
		ORDER BY ds.created_at ASC
	`, status, uid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []disputeView
	for rows.Next() {
		var d disputeView
		var arbID sql.NullInt64
		var arbName sql.NullString
		if err := rows.Scan(
			&d.ID, &d.DealID, &d.Status, &d.ReasonCode, &d.Description,
			&d.OpenedByUserID, &d.OpenedByName,
			&d.RenterRefundCents, &d.OwnerPayoutCents,
			&d.ResolutionCode, &d.ResolutionNote,
			&arbID, &arbName,
			&d.ResolvedAt,
			&d.CreatedAt,
			&d.VehicleTitle, &d.RenterName, &d.OwnerName,
			&d.HoldAmountCents, &d.DealStatus,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		d.ReasonLabel = disputeReasonLabel(d.ReasonCode)
		if arbID.Valid {
			v := arbID.Int64
			d.ArbitratorUserID = &v
		}
		if arbName.Valid {
			d.ArbitratorName = arbName.String
		}
		ev, err := a.loadDisputeEvidence(ctx, d.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		d.Evidence = ev
		list = append(list, d)
	}
	if list == nil {
		list = []disputeView{}
	}
	return c.JSON(http.StatusOK, list)
}

func (a *api) getArbitrationDispute(c *echo.Context) error {
	uid := authUserID(c)
	disputeID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || disputeID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	d, err := a.loadDisputeByID(ctx, disputeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	var renterID, ownerID int64
	err = a.db.QueryRow(ctx, `SELECT renter_id, owner_id FROM rental_deals WHERE id = $1`, d.DealID).Scan(&renterID, &ownerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if uid == renterID || uid == ownerID {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "cannot_arbitrate_own_deal"})
	}
	return c.JSON(http.StatusOK, d)
}

func splitFromResolution(hold int64, res string, renterPtr, ownerPtr *int64) (renterRefund, ownerPayout int64, errKey string) {
	switch res {
	case "favor_renter":
		return hold, 0, ""
	case "favor_owner":
		return 0, hold, ""
	case "split":
		if renterPtr == nil || ownerPtr == nil {
			return 0, 0, "invalid_split"
		}
		r, o := *renterPtr, *ownerPtr
		if r < 0 || o < 0 || r+o != hold {
			return 0, 0, "invalid_split"
		}
		return r, o, ""
	default:
		return 0, 0, "invalid_resolution"
	}
}

func applyDisputeFunds(ctx context.Context, tx pgx.Tx, dealID, renterID, ownerID int64, hold, renterRefund, ownerPayout int64, paidToOwner bool) error {
	if renterRefund+ownerPayout != hold {
		return fmt.Errorf("invalid_split")
	}
	noteBase := "Dispute #" + strconv.FormatInt(dealID, 10) + " resolution"

	if paidToOwner {
		transfer := hold - ownerPayout
		if transfer > 0 {
			var ownerBal int64
			if err := tx.QueryRow(ctx, `SELECT balance_cents FROM app_users WHERE id = $1 FOR UPDATE`, ownerID).Scan(&ownerBal); err != nil {
				return err
			}
			if ownerBal < transfer {
				return fmt.Errorf("owner_insufficient")
			}
			if _, err := tx.Exec(ctx, `UPDATE app_users SET balance_cents = balance_cents - $1 WHERE id = $2`, transfer, ownerID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE app_users SET balance_cents = balance_cents + $1 WHERE id = $2`, transfer, renterID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
				VALUES ($1, $2, $3, 'dispute_adjustment', $4)
			`, ownerID, dealID, -transfer, noteBase+" (owner)"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
				VALUES ($1, $2, $3, 'dispute_refund', $4)
			`, renterID, dealID, transfer, noteBase+" (renter)"); err != nil {
				return err
			}
		}
		return nil
	}

	if renterRefund > 0 {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET balance_cents = balance_cents + $1 WHERE id = $2`, renterRefund, renterID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
			VALUES ($1, $2, $3, 'dispute_refund', $4)
		`, renterID, dealID, renterRefund, noteBase+" (renter)"); err != nil {
			return err
		}
	}
	if ownerPayout > 0 {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET balance_cents = balance_cents + $1 WHERE id = $2`, ownerPayout, ownerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
			VALUES ($1, $2, $3, 'payout_owner', $4)
		`, ownerID, dealID, ownerPayout, noteBase+" (owner)"); err != nil {
			return err
		}
	}
	return nil
}

func (a *api) postResolveDispute(c *echo.Context) error {
	arbID := authUserID(c)
	disputeID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || disputeID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	var req resolveDisputeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	req.Resolution = strings.ToLower(strings.TrimSpace(req.Resolution))
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 500 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "note_too_long"})
	}

	ctx := c.Request().Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer tx.Rollback(ctx)

	var dealID, renterID, ownerID int64
	var hold int64
	var disputeSt string
	err = tx.QueryRow(ctx, `
		SELECT ds.deal_id, d.renter_id, d.owner_id, d.hold_amount_cents, ds.status
		FROM rental_disputes ds
		JOIN rental_deals d ON d.id = ds.deal_id
		WHERE ds.id = $1 FOR UPDATE
	`, disputeID).Scan(&dealID, &renterID, &ownerID, &hold, &disputeSt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if arbID == renterID || arbID == ownerID {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "cannot_arbitrate_own_deal"})
	}
	if disputeSt != disputeOpen {
		return c.JSON(http.StatusConflict, map[string]string{"error": "dispute_not_open"})
	}

	renterRefund, ownerPayout, errKey := splitFromResolution(hold, req.Resolution, req.RenterRefundCents, req.OwnerPayoutCents)
	if errKey != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errKey})
	}

	paidToOwner := false
	var payoutCount int
	if err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM wallet_ledger
		WHERE deal_id = $1 AND entry_type = 'payout_owner'
	`, dealID).Scan(&payoutCount); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	paidToOwner = payoutCount > 0

	if err := applyDisputeFunds(ctx, tx, dealID, renterID, ownerID, hold, renterRefund, ownerPayout, paidToOwner); err != nil {
		if err.Error() == "owner_insufficient" {
			return c.JSON(http.StatusConflict, map[string]string{"error": "owner_insufficient_balance"})
		}
		if err.Error() == "invalid_split" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_split"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	newDealStatus := dealCompleted
	if renterRefund == hold {
		newDealStatus = dealCancelled
	}
	_, err = tx.Exec(ctx, `UPDATE rental_deals SET status = $1, updated_at = NOW() WHERE id = $2`, newDealStatus, dealID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	_, err = tx.Exec(ctx, `
		UPDATE rental_disputes SET
			status = $1,
			resolution_code = $2,
			resolution_note = $3,
			renter_refund_cents = $4,
			owner_payout_cents = $5,
			arbitrator_user_id = $6,
			resolved_at = NOW(),
			updated_at = NOW()
		WHERE id = $7
	`, disputeResolved, req.Resolution, req.Note, renterRefund, ownerPayout, arbID, disputeID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	details := map[string]any{
		"disputeId": disputeID, "dealId": dealID,
		"resolution": req.Resolution, "renterRefundCents": renterRefund, "ownerPayoutCents": ownerPayout,
	}
	if err := a.insertStaffAudit(ctx, arbID, "dispute_resolved", "dispute", disputeID, details); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	d, err := a.loadDisputeByID(ctx, disputeID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, d)
}

func registerDisputeRoutes(secured *echo.Group, h *api) {
	secured.GET("/disputes/reasons", h.listDisputeReasons)
	secured.GET("/deals/:id/dispute", h.getDealDispute)
	secured.POST("/deals/:id/dispute", h.postDealDispute)

	arb := secured.Group("", h.requireArbitrator)
	arb.GET("/arbitration/disputes", h.listArbitrationDisputes)
	arb.GET("/arbitration/disputes/:id", h.getArbitrationDispute)
	arb.POST("/arbitration/disputes/:id/resolve", h.postResolveDispute)
}
