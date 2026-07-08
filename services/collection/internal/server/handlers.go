package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vg-collect/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vg-collect/services/collection/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vg-collect/services/collection/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

// maxBodyBytes caps request bodies; the largest legitimate body is an
// entry update with long notes, far under this.
const maxBodyBytes = 64 * 1024

var (
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

	regionVals    = map[string]bool{"ntsc_u": true, "ntsc_j": true, "pal": true, "region_free": true}
	packagingVals = map[string]bool{"sealed": true, "cib": true, "loose": true}
	conditionVals = map[string]bool{"mint": true, "near_mint": true, "very_good": true, "good": true, "acceptable": true, "poor": true}
	statusVals    = map[string]bool{"backlog": true, "playing": true, "beaten": true, "completed": true, "dropped": true, "shelved": true}
	pricingVals   = map[string]bool{"auto": true, "proxy": true, "disabled": true}
	itemTypeVals  = map[string]bool{"game": true, "console": true, "accessory": true}
	sortVals      = map[string]bool{"name": true, "release_date": true, "purchased_at": true, "created_at": true, "value": true, "paid": true, "rating": true, "backlog_rank": true}
	orderVals     = map[string]bool{"asc": true, "desc": true}
	groupVals     = map[string]bool{"platform": true, "status": true, "item_type": true, "location": true, "tag": true}
)

// entryInput is the shared mutable field set of the create and update
// bodies, unwrapped to plain values (defaults applied) so one
// validator serves both operations.
type entryInput struct {
	Region           string
	Edition          *string
	Packaging        string
	HasBox           bool
	HasManual        bool
	BoxCondition     *string
	ManualCondition  *string
	ItemCondition    *string
	PricePaidCents   *int64
	Currency         string
	PurchasedAt      *time.Time
	PurchasedFrom    *string
	PricingMode      string
	PricingProductID *uuid.UUID
	Status           string
	Rating           *int
	Notes            *string
	StorageLocation  *string
	Pinned           bool
	TagIDs           []uuid.UUID
}

func strDeref(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

func dateToTime(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time
	return &t
}

func uuidsFrom(ids *[]openapi_types.UUID) []uuid.UUID {
	if ids == nil {
		return nil
	}
	return *ids
}

// createInput unwraps an EntryCreate, applying the contract defaults.
func createInput(b api.EntryCreate) entryInput {
	return entryInput{
		Region:           string(b.Region),
		Edition:          b.Edition,
		Packaging:        string(b.Packaging),
		HasBox:           b.HasBox != nil && *b.HasBox,
		HasManual:        b.HasManual != nil && *b.HasManual,
		BoxCondition:     (*string)(b.BoxCondition),
		ManualCondition:  (*string)(b.ManualCondition),
		ItemCondition:    (*string)(b.ItemCondition),
		PricePaidCents:   b.PricePaidCents,
		Currency:         strDeref(b.Currency, "USD"),
		PurchasedAt:      dateToTime(b.PurchasedAt),
		PurchasedFrom:    b.PurchasedFrom,
		PricingMode:      strDeref((*string)(b.PricingMode), "auto"),
		PricingProductID: b.PricingProductId,
		Status:           strDeref((*string)(b.Status), "backlog"),
		Rating:           b.Rating,
		Notes:            b.Notes,
		StorageLocation:  b.StorageLocation,
		Pinned:           b.Pinned != nil && *b.Pinned,
		TagIDs:           uuidsFrom(b.TagIds),
	}
}

// updateInput unwraps an EntryUpdate (full replacement: absent
// optional fields clear).
func updateInput(b api.EntryUpdate) entryInput {
	return entryInput{
		Region:           string(b.Region),
		Edition:          b.Edition,
		Packaging:        string(b.Packaging),
		HasBox:           b.HasBox != nil && *b.HasBox,
		HasManual:        b.HasManual != nil && *b.HasManual,
		BoxCondition:     (*string)(b.BoxCondition),
		ManualCondition:  (*string)(b.ManualCondition),
		ItemCondition:    (*string)(b.ItemCondition),
		PricePaidCents:   b.PricePaidCents,
		Currency:         strDeref(b.Currency, "USD"),
		PurchasedAt:      dateToTime(b.PurchasedAt),
		PurchasedFrom:    b.PurchasedFrom,
		PricingMode:      string(b.PricingMode),
		PricingProductID: b.PricingProductId,
		Status:           string(b.Status),
		Rating:           b.Rating,
		Notes:            b.Notes,
		StorageLocation:  b.StorageLocation,
		Pinned:           b.Pinned,
		TagIDs:           uuidsFrom(b.TagIds),
	}
}

// validateEntryInput enforces the body rules the generated layer does
// not; a non-empty return is the 400 detail.
func validateEntryInput(in entryInput) string {
	if !regionVals[in.Region] {
		return "region must be one of ntsc_u, ntsc_j, pal, region_free"
	}
	if !packagingVals[in.Packaging] {
		return "packaging must be one of sealed, cib, loose"
	}
	if !statusVals[in.Status] {
		return "status is not a known value"
	}
	if !pricingVals[in.PricingMode] {
		return "pricing_mode must be one of auto, proxy, disabled"
	}
	for name, c := range map[string]*string{
		"box_condition": in.BoxCondition, "manual_condition": in.ManualCondition, "item_condition": in.ItemCondition,
	} {
		if c != nil && !conditionVals[*c] {
			return name + " is not a known grade"
		}
	}
	if in.BoxCondition != nil && !in.HasBox {
		return "box_condition requires has_box"
	}
	if in.ManualCondition != nil && !in.HasManual {
		return "manual_condition requires has_manual"
	}
	if !currencyRe.MatchString(in.Currency) {
		return "currency must be a three-letter uppercase code"
	}
	if in.PricePaidCents != nil && *in.PricePaidCents < 0 {
		return "price_paid_cents must not be negative"
	}
	if in.Rating != nil && (*in.Rating < 1 || *in.Rating > 10) {
		return "rating must be between 1 and 10"
	}
	if in.PricingMode == "proxy" && in.PricingProductID == nil {
		return "pricing_mode proxy requires pricing_product_id"
	}
	for name, lim := range map[string]struct {
		v   *string
		max int
	}{
		"edition":          {in.Edition, 200},
		"purchased_from":   {in.PurchasedFrom, 200},
		"storage_location": {in.StorageLocation, 200},
		"notes":            {in.Notes, 10000},
	} {
		if lim.v != nil && len(*lim.v) > lim.max {
			return name + " is too long"
		}
	}
	if len(in.TagIDs) > 50 {
		return "at most 50 tags per entry"
	}
	return ""
}

// applyInput writes the mutable fields onto a store entry.
func applyInput(e *store.Entry, in entryInput) {
	e.Region = in.Region
	e.Edition = in.Edition
	e.Packaging = in.Packaging
	e.HasBox = in.HasBox
	e.HasManual = in.HasManual
	e.BoxCondition = in.BoxCondition
	e.ManualCondition = in.ManualCondition
	e.ItemCondition = in.ItemCondition
	e.PricePaidCents = in.PricePaidCents
	e.Currency = in.Currency
	e.PurchasedAt = in.PurchasedAt
	e.PurchasedFrom = in.PurchasedFrom
	e.PricingMode = in.PricingMode
	e.PricingProductID = in.PricingProductID
	e.Status = in.Status
	e.Rating = in.Rating
	e.Notes = in.Notes
	e.StorageLocation = in.StorageLocation
	e.Pinned = in.Pinned
}

// effectiveProductID resolves which product prices an entry: its own
// (auto; product-backed entries only), the override (proxy), or none
// (disabled).
func effectiveProductID(mode string, productID *uuid.UUID, pricingProductID *uuid.UUID) *uuid.UUID {
	switch mode {
	case "auto":
		return productID
	case "proxy":
		return pricingProductID
	default:
		return nil
	}
}

// valueForPackaging picks the packaging-matched price field; nil when
// the product is unmatched or lists no price for that condition.
func valueForPackaging(packaging string, p enrichapi.ProductPrices) *int64 {
	if p.Unmatched {
		return nil
	}
	switch packaging {
	case "sealed":
		return p.NewCents
	case "cib":
		return p.CibCents
	default:
		return p.LooseCents
	}
}

// toAPIEntry maps a store entry (plus its composed value) onto the
// contract shape.
func toAPIEntry(e store.Entry, valueCents *int64) api.Entry {
	out := api.Entry{
		Id:               e.ID,
		ProductId:        e.ProductID,
		ItemType:         api.EntryItemType(e.ItemType),
		MediaType:        api.EntryMediaType(e.MediaType),
		DisplayName:      e.DisplayName,
		CoverUrl:         e.CoverURL,
		IgdbGameId:       e.IGDBGameID,
		Region:           api.EntryRegion(e.Region),
		Edition:          e.Edition,
		Packaging:        api.EntryPackaging(e.Packaging),
		HasBox:           e.HasBox,
		HasManual:        e.HasManual,
		BoxCondition:     (*api.EntryBoxCondition)(e.BoxCondition),
		ManualCondition:  (*api.EntryManualCondition)(e.ManualCondition),
		ItemCondition:    (*api.EntryItemCondition)(e.ItemCondition),
		PricePaidCents:   e.PricePaidCents,
		Currency:         e.Currency,
		PurchasedFrom:    e.PurchasedFrom,
		PricingMode:      api.EntryPricingMode(e.PricingMode),
		PricingProductId: e.PricingProductID,
		Status:           api.EntryStatus(e.Status),
		Rating:           e.Rating,
		Notes:            e.Notes,
		StorageLocation:  e.StorageLocation,
		Pinned:           e.Pinned,
		BacklogRank:      e.BacklogRank,
		Source:           api.EntrySource(e.Source),
		ExternalRef:      e.ExternalRef,
		ValueCents:       valueCents,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
	}
	if e.PlatformName != nil {
		out.Platform = &api.EntryPlatform{IgdbPlatformId: e.PlatformIGDBID, Name: *e.PlatformName}
	}
	if e.FirstReleaseDate != nil {
		out.FirstReleaseDate = &openapi_types.Date{Time: *e.FirstReleaseDate}
	}
	if e.PurchasedAt != nil {
		out.PurchasedAt = &openapi_types.Date{Time: *e.PurchasedAt}
	}
	tags := make([]api.TagRef, len(e.Tags))
	for i, t := range e.Tags {
		tags[i] = api.TagRef{Id: t.ID, Name: t.Name}
	}
	out.Tags = tags
	return out
}

// respondEntry writes a single entry, composing its current value
// best-effort (a pricing hiccup never fails an entry response).
func (h *Handlers) respondEntry(w http.ResponseWriter, r *http.Request, bearer string, e store.Entry, status int) {
	var value *int64
	if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, []uuid.UUID{*id})
		if err != nil {
			h.logger.WarnContext(r.Context(), "value composition unavailable", "err", err)
		} else if p, ok := prices[id.String()]; ok {
			value = valueForPackaging(e.Packaging, p)
		}
	}
	writeJSON(w, status, toAPIEntry(e, value))
}

// invalidateDashboard drops the user's cached dashboard after a
// mutation (fail-open: a cache hiccup only delays freshness to the TTL).
func (h *Handlers) invalidateDashboard(ctx context.Context, userID uuid.UUID) {
	if err := h.cache.InvalidateDashboard(ctx, userID.String()); err != nil {
		h.failOpen(ctx, "dashboard_invalidate", err)
	}
}

// validateCustomFields enforces the either/or between product-backed
// and custom creation bodies; a non-empty return is the 400 detail.
func validateCustomFields(body api.EntryCreate) string {
	if body.ProductId != nil {
		if body.DisplayName != nil || body.ItemType != nil || body.PlatformName != nil || body.FirstReleaseDate != nil {
			return "catalog fields are snapshotted from the product; omit display_name/item_type/platform_name/first_release_date when product_id is set"
		}
		return ""
	}
	if body.DisplayName == nil || strings.TrimSpace(*body.DisplayName) == "" {
		return "custom entries (no product_id) require display_name"
	}
	if len(*body.DisplayName) > 200 {
		return "display_name is too long"
	}
	if body.ItemType == nil || !itemTypeVals[string(*body.ItemType)] {
		return "custom entries (no product_id) require item_type (game, console, or accessory)"
	}
	if body.PlatformName != nil && (strings.TrimSpace(*body.PlatformName) == "" || len(*body.PlatformName) > 100) {
		return "platform_name must be 1-100 characters"
	}
	return ""
}

// CreateEntry adds an entry. Product-backed: catalog facts are
// snapshotted from the enrichment product (the source of truth; the
// client never supplies them) - the one operation with a hard
// enrichment dependency. Custom (no product_id): the user supplies
// the display facts for an off-catalog item; pricing defaults to
// disabled and may proxy any catalog product as its price source.
func (h *Handlers) CreateEntry(w http.ResponseWriter, r *http.Request) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.EntryCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.MediaType != nil && *body.MediaType != "physical" {
		problem(w, r, http.StatusBadRequest, "invalid_body", "media_type must be physical")
		return
	}
	if detail := validateCustomFields(body); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	custom := body.ProductId == nil
	in := createInput(body)
	if custom {
		if body.PricingMode == nil {
			in.PricingMode = "disabled" // no own product to price against
		} else if in.PricingMode == "auto" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "custom entries cannot use pricing_mode auto; choose proxy or disabled")
			return
		}
	}
	if detail := validateEntryInput(in); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}

	e := store.Entry{
		UserID:    userID,
		MediaType: "physical",
		Source:    "manual",
	}
	if custom {
		e.ItemType = string(*body.ItemType)
		e.DisplayName = *body.DisplayName
		e.PlatformName = body.PlatformName
		e.FirstReleaseDate = dateToTime(body.FirstReleaseDate)
	} else {
		product, err := h.enrichment.GetProduct(r.Context(), bearer, *body.ProductId)
		if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
			problem(w, r, http.StatusNotFound, "unknown_product", "no such product in the catalog")
			return
		}
		if err != nil {
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		e.ProductID = &product.Id
		e.ItemType = string(product.Type)
		e.DisplayName = product.Name
		if product.Platform != nil {
			e.PlatformIGDBID = &product.Platform.IgdbPlatformId
			e.PlatformName = &product.Platform.Name
		}
		if product.Igdb != nil {
			e.IGDBGameID = &product.Igdb.GameId
			e.FirstReleaseDate = dateToTime(product.Igdb.FirstReleaseDate)
			e.CoverURL = product.Igdb.CoverUrl
		}
		// Hardware has no igdb block and some games ship no cover;
		// the platform logo is the next-best entry image.
		if (e.CoverURL == nil || *e.CoverURL == "") && product.Platform != nil {
			e.CoverURL = product.Platform.LogoUrl
		}
	}
	// A NEW proxy reference must exist in the catalog (the entry's own
	// product was just fetched, so proxying to itself needs no check).
	if in.PricingMode == "proxy" && (custom || *in.PricingProductID != *body.ProductId) {
		target, err := h.enrichment.GetProduct(r.Context(), bearer, *in.PricingProductID)
		if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
			problem(w, r, http.StatusNotFound, "unknown_pricing_product", "no such pricing product in the catalog")
			return
		}
		if err != nil {
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		// A custom game's recommendation identity comes from its proxy
		// target: owning a reproduction of X means playing X.
		if custom && e.ItemType == "game" && target.Igdb != nil {
			e.IGDBGameID = &target.Igdb.GameId
		}
	}
	applyInput(&e, in)

	created, err := h.store.CreateEntry(r.Context(), e, in.TagIDs)
	if errors.Is(err, store.ErrTagNotFound) {
		problem(w, r, http.StatusNotFound, "tag_not_found", "a tag id does not exist")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	h.respondEntry(w, r, bearer, created, http.StatusCreated)
}

// GetEntry fetches one entry with its composed current value.
func (h *Handlers) GetEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	e, err := h.store.GetEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}
	h.respondEntry(w, r, bearer, e, http.StatusOK)
}

// UpdateEntry replaces the mutable state (the edit form submits the
// whole entry; absent optional fields clear). product_id, media_type,
// and the catalog snapshot are immutable.
func (h *Handlers) UpdateEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	current, err := h.store.GetEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "get failed")
		return
	}

	var body api.EntryUpdate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	in := updateInput(body)
	if detail := validateEntryInput(in); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}

	// Custom entries own their display fields (full replacement, like
	// every mutable field); product-backed entries must not touch the
	// catalog snapshot.
	custom := current.ProductID == nil
	if !custom && (body.DisplayName != nil || body.PlatformName != nil || body.FirstReleaseDate != nil) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "catalog fields are immutable on product-backed entries")
		return
	}
	if custom {
		if body.DisplayName == nil || strings.TrimSpace(*body.DisplayName) == "" || len(*body.DisplayName) > 200 {
			problem(w, r, http.StatusBadRequest, "invalid_body", "custom entries require display_name (1-200 characters)")
			return
		}
		if body.PlatformName != nil && (strings.TrimSpace(*body.PlatformName) == "" || len(*body.PlatformName) > 100) {
			problem(w, r, http.StatusBadRequest, "invalid_body", "platform_name must be 1-100 characters")
			return
		}
		if in.PricingMode == "auto" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "custom entries cannot use pricing_mode auto; choose proxy or disabled")
			return
		}
	}

	// A NEW proxy reference must exist in the catalog; proxying to the
	// entry's own product needs no round-trip (a product-backed entry's
	// product_id is already known-good). Otherwise, re-validate unless
	// the stored state is already an active proxy at this target
	// (validated when that was set) - switching INTO proxy mode always
	// validates, even if some earlier, never-validated
	// pricing_product_id happens to match the new one.
	var proxyTarget *enrichapi.Product
	if in.PricingMode == "proxy" && in.PricingProductID != nil &&
		(current.ProductID == nil || *in.PricingProductID != *current.ProductID) &&
		(current.PricingMode != "proxy" || current.PricingProductID == nil || *current.PricingProductID != *in.PricingProductID) {
		target, err := h.enrichment.GetProduct(r.Context(), bearer, *in.PricingProductID)
		if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
			problem(w, r, http.StatusNotFound, "unknown_pricing_product", "no such pricing product in the catalog")
			return
		}
		if err != nil {
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		proxyTarget = &target
	}

	e := current
	applyInput(&e, in)
	if custom {
		e.DisplayName = *body.DisplayName
		e.PlatformName = body.PlatformName
		e.FirstReleaseDate = dateToTime(body.FirstReleaseDate)
		// The recommendation identity follows the pricing proxy:
		// re-snapshot on a new target, keep it while the target is
		// unchanged, clear it when the proxy is removed.
		switch {
		case in.PricingMode != "proxy":
			e.IGDBGameID = nil
		case proxyTarget != nil:
			e.IGDBGameID = nil
			if e.ItemType == "game" && proxyTarget.Igdb != nil {
				e.IGDBGameID = &proxyTarget.Igdb.GameId
			}
		}
	}
	updated, err := h.store.UpdateEntry(r.Context(), e, in.TagIDs)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if errors.Is(err, store.ErrTagNotFound) {
		problem(w, r, http.StatusNotFound, "tag_not_found", "a tag id does not exist")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	h.respondEntry(w, r, bearer, updated, http.StatusOK)
}

// DeleteEntry removes an entry (tag links cascade).
func (h *Handlers) DeleteEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	w.WriteHeader(http.StatusNoContent)
}

// ReorderEntry moves a backlog entry between two neighbor entries
// (the drag result). Ranks are server-generated; the client only
// names the drop slot's neighbors.
func (h *Handlers) ReorderEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.ReorderRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.AfterId == nil && body.BeforeId == nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "at least one of after_id/before_id is required")
		return
	}
	if (body.AfterId != nil && *body.AfterId == entryId) || (body.BeforeId != nil && *body.BeforeId == entryId) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "an entry cannot neighbor itself")
		return
	}
	moved, err := h.store.Reorder(r.Context(), userID, entryId, body.AfterId, body.BeforeId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	case errors.Is(err, store.ErrNotInBacklog):
		problem(w, r, http.StatusConflict, "not_in_backlog", "the entry and both neighbors must be backlog entries")
		return
	case errors.Is(err, store.ErrConflictingOrder):
		problem(w, r, http.StatusConflict, "conflicting_order", "the neighbors do not straddle; refresh the list and retry")
		return
	case err != nil:
		problem(w, r, http.StatusInternalServerError, "internal", "reorder failed")
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	h.respondEntry(w, r, bearer, moved, http.StatusOK)
}

// listParams validates and converts the generated query params (the
// generated layer binds but does not enforce enum membership or
// ranges). Returns filters, groupBy, limit, offset, and the 400
// detail (empty means valid).
func listParams(params api.ListEntriesParams) (store.Filters, string, int, int, string) {
	f := store.Filters{Sort: "created_at", Order: "desc"}
	if params.ItemType != nil {
		for _, v := range *params.ItemType {
			if !itemTypeVals[string(v)] {
				return f, "", 0, 0, "item_type contains an unknown value"
			}
			f.ItemTypes = append(f.ItemTypes, string(v))
		}
	}
	if params.Status != nil {
		for _, v := range *params.Status {
			if !statusVals[string(v)] {
				return f, "", 0, 0, "status contains an unknown value"
			}
			f.Statuses = append(f.Statuses, string(v))
		}
	}
	if params.Packaging != nil {
		for _, v := range *params.Packaging {
			if !packagingVals[string(v)] {
				return f, "", 0, 0, "packaging contains an unknown value"
			}
			f.Packagings = append(f.Packagings, string(v))
		}
	}
	if params.Region != nil {
		for _, v := range *params.Region {
			if !regionVals[string(v)] {
				return f, "", 0, 0, "region contains an unknown value"
			}
			f.Regions = append(f.Regions, string(v))
		}
	}
	if params.ItemCondition != nil {
		for _, v := range *params.ItemCondition {
			if !conditionVals[string(v)] {
				return f, "", 0, 0, "item_condition contains an unknown value"
			}
			f.ItemConditions = append(f.ItemConditions, string(v))
		}
	}
	if params.PlatformId != nil {
		f.PlatformIDs = *params.PlatformId
	}
	if params.TagId != nil {
		f.TagIDs = *params.TagId
	}
	if params.Sort != nil {
		if !sortVals[string(*params.Sort)] {
			return f, "", 0, 0, "sort is not a known value"
		}
		f.Sort = string(*params.Sort)
	}
	if params.Order != nil {
		if !orderVals[string(*params.Order)] {
			return f, "", 0, 0, "order must be asc or desc"
		}
		f.Order = string(*params.Order)
	}
	groupBy := ""
	if params.GroupBy != nil {
		if !groupVals[string(*params.GroupBy)] {
			return f, "", 0, 0, "group_by is not a known value"
		}
		groupBy = string(*params.GroupBy)
	}
	limit, offset := 200, 0
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > 500 {
			return f, "", 0, 0, "limit must be between 1 and 500"
		}
		limit = *params.Limit
	}
	if params.Offset != nil {
		if *params.Offset < 0 {
			return f, "", 0, 0, "offset must not be negative"
		}
		offset = *params.Offset
	}
	return f, groupBy, limit, offset, ""
}

// castSlice re-types a generated enum slice onto its mirror from
// another operation; the dashboard params repeat the entries-list
// contract, only the generated Go types differ.
func castSlice[Dst ~string, Src ~string](src *[]Src) *[]Dst {
	if src == nil {
		return nil
	}
	out := make([]Dst, len(*src))
	for i, v := range *src {
		out[i] = Dst(v)
	}
	return &out
}

// dashboardFilters funnels the dashboard's filter params through the
// entries-list validator (same dimensions, same 400 details); sort,
// order, grouping, and paging ride the validator's defaults and are
// ignored by the aggregates.
func dashboardFilters(p api.GetDashboardParams) (store.Filters, string) {
	f, _, _, _, detail := listParams(api.ListEntriesParams{
		ItemType:      castSlice[api.ListEntriesParamsItemType](p.ItemType),
		Status:        castSlice[api.ListEntriesParamsStatus](p.Status),
		Packaging:     castSlice[api.ListEntriesParamsPackaging](p.Packaging),
		Region:        castSlice[api.ListEntriesParamsRegion](p.Region),
		ItemCondition: castSlice[api.ListEntriesParamsItemCondition](p.ItemCondition),
		PlatformId:    p.PlatformId,
		TagId:         p.TagId,
	})
	return f, detail
}

// sortEntriesByValue re-sorts in memory after price composition:
// pinned first, then value with nulls last, then the standard
// tiebreak. Stable, so equal keys keep the SQL base order.
func sortEntriesByValue(entries []store.Entry, values map[uuid.UUID]*int64, order string) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Pinned != entries[j].Pinned {
			return entries[i].Pinned
		}
		vi, vj := values[entries[i].ID], values[entries[j].ID]
		switch {
		case vi == nil && vj == nil:
			// fall through to the tiebreak
		case vi == nil:
			return false // nulls last in both directions
		case vj == nil:
			return true
		case *vi != *vj:
			if order == "desc" {
				return *vi > *vj
			}
			return *vi < *vj
		}
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].ID.String() < entries[j].ID.String()
	})
}

// groupLabels names the group(s) an entry belongs to; group_by=tag
// repeats a multi-tagged entry in each of its tag groups.
func groupLabels(e store.Entry, groupBy string) []string {
	switch groupBy {
	case "platform":
		if e.PlatformName != nil {
			return []string{*e.PlatformName}
		}
		return []string{"Unknown"}
	case "status":
		return []string{e.Status}
	case "item_type":
		return []string{e.ItemType}
	case "location":
		if e.StorageLocation != nil && *e.StorageLocation != "" {
			return []string{*e.StorageLocation}
		}
		return []string{"Unassigned"}
	default: // tag
		if len(e.Tags) == 0 {
			return []string{"Untagged"}
		}
		labels := make([]string, len(e.Tags))
		for i, t := range e.Tags {
			labels[i] = t.Name
		}
		return labels
	}
}

var catchAllLabels = map[string]bool{"Unknown": true, "Unassigned": true, "Untagged": true}

// buildGroups partitions the sorted entries, preserving order within
// each group; groups sort by label ascending with the catch-all last.
func buildGroups(entries []store.Entry, apiEntries []api.Entry, groupBy string) []api.EntryGroup {
	byLabel := map[string][]api.Entry{}
	for i, e := range entries {
		for _, label := range groupLabels(e, groupBy) {
			byLabel[label] = append(byLabel[label], apiEntries[i])
		}
	}
	labels := make([]string, 0, len(byLabel))
	for label := range byLabel {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		ci, cj := catchAllLabels[labels[i]], catchAllLabels[labels[j]]
		if ci != cj {
			return cj // catch-all sorts last
		}
		return strings.ToLower(labels[i]) < strings.ToLower(labels[j])
	})
	groups := make([]api.EntryGroup, len(labels))
	for i, label := range labels {
		groups[i] = api.EntryGroup{Key: label, Label: label, Entries: byLabel[label]}
	}
	return groups
}

// ListEntries answers one page of the filter x sort x group matrix.
// The full filtered set is fetched and sorted (person-scale by
// design: pagination bounds payloads, not queries), total_count is
// taken, then the page sliced. Prices arrive in one batched call -
// over every effective id when sorting by value (the order needs them
// all), otherwise over the page only. Enrichment being down degrades
// to pricing_available=false, never a failure.
func (h *Handlers) ListEntries(w http.ResponseWriter, r *http.Request, params api.ListEntriesParams) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	f, groupBy, limit, offset, detail := listParams(params)
	if detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_param", detail)
		return
	}
	entries, err := h.store.ListEntries(r.Context(), userID, f)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}

	pricingAvailable := true
	values := map[uuid.UUID]*int64{}
	compose := func(subset []store.Entry) {
		var ids []uuid.UUID
		for _, e := range subset {
			if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
				ids = append(ids, *id)
			}
		}
		if len(ids) == 0 {
			return
		}
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, ids)
		if err != nil {
			pricingAvailable = false
			h.logger.WarnContext(r.Context(), "list value composition unavailable", "err", err)
			return
		}
		for _, e := range subset {
			if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
				if p, okPrice := prices[id.String()]; okPrice {
					values[e.ID] = valueForPackaging(e.Packaging, p)
				}
			}
		}
	}
	if f.Sort == "value" {
		compose(entries)
		sortEntriesByValue(entries, values, f.Order)
	}

	total := len(entries)
	page := entries[min(offset, total):min(offset+limit, total)]
	if f.Sort != "value" {
		compose(page)
	}

	apiEntries := make([]api.Entry, len(page))
	for i, e := range page {
		apiEntries[i] = toAPIEntry(e, values[e.ID])
	}
	out := api.EntryList{PricingAvailable: pricingAvailable, TotalCount: total}
	if groupBy == "" {
		out.Entries = &apiEntries
	} else {
		groups := buildGroups(page, apiEntries, groupBy)
		out.Groups = &groups
	}
	writeJSON(w, http.StatusOK, out)
}

func toAPITag(t store.Tag) api.Tag {
	return api.Tag{Id: t.ID, Name: t.Name, EntryCount: t.EntryCount}
}

// toAPIView maps a stored view; Params round-trips verbatim.
func toAPIView(v store.View) (api.SavedView, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(v.Params, &params); err != nil {
		return api.SavedView{}, err
	}
	return api.SavedView{
		Id: v.ID, Name: v.Name, Params: params,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}, nil
}

// maxViewParamsBytes caps the opaque view document.
const maxViewParamsBytes = 8192

func validateTagName(name string) string {
	if strings.TrimSpace(name) == "" || len(name) > 50 {
		return "name must be 1-50 characters"
	}
	return ""
}

// ListTags lists the caller's tags with usage counts.
func (h *Handlers) ListTags(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	tags, err := h.store.ListTags(r.Context(), userID)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	out := make([]api.Tag, len(tags))
	for i, t := range tags {
		out[i] = toAPITag(t)
	}
	writeJSON(w, http.StatusOK, map[string][]api.Tag{"tags": out})
}

// CreateTag creates a tag (unique per user, case-insensitively).
func (h *Handlers) CreateTag(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.TagCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if detail := validateTagName(body.Name); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	tag, err := h.store.CreateTag(r.Context(), userID, body.Name)
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "tag_exists", "a tag with that name already exists")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, toAPITag(tag))
}

// RenameTag renames a tag.
func (h *Handlers) RenameTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.TagCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if detail := validateTagName(body.Name); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	tag, err := h.store.RenameTag(r.Context(), userID, tagId, body.Name)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "tag_not_found", "no such tag")
		return
	}
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "tag_exists", "a tag with that name already exists")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "rename failed")
		return
	}
	writeJSON(w, http.StatusOK, toAPITag(tag))
}

// DeleteTag deletes a tag; entry links cascade, entries survive.
func (h *Handlers) DeleteTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteTag(r.Context(), userID, tagId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "tag_not_found", "no such tag")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// viewBody decodes and validates a ViewCreate; the marshaled params
// come back for storage.
func viewBody(w http.ResponseWriter, r *http.Request) (api.ViewCreate, []byte, bool) {
	var body api.ViewCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return body, nil, false
	}
	if strings.TrimSpace(body.Name) == "" || len(body.Name) > 100 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "name must be 1-100 characters")
		return body, nil, false
	}
	params, err := json.Marshal(body.Params)
	if err != nil || body.Params == nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "params must be a JSON object")
		return body, nil, false
	}
	if len(params) > maxViewParamsBytes {
		problem(w, r, http.StatusBadRequest, "invalid_body", "params is too large")
		return body, nil, false
	}
	return body, params, true
}

func (h *Handlers) respondView(w http.ResponseWriter, r *http.Request, v store.View, status int) {
	out, err := toAPIView(v)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "view encoding failed")
		return
	}
	writeJSON(w, status, out)
}

// ListViews lists the caller's saved views.
func (h *Handlers) ListViews(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	views, err := h.store.ListViews(r.Context(), userID)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	out := make([]api.SavedView, len(views))
	for i, v := range views {
		av, err := toAPIView(v)
		if err != nil {
			problem(w, r, http.StatusInternalServerError, "internal", "view encoding failed")
			return
		}
		out[i] = av
	}
	writeJSON(w, http.StatusOK, map[string][]api.SavedView{"views": out})
}

// CreateView saves a view (an opaque frontend params document).
func (h *Handlers) CreateView(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	body, params, ok := viewBody(w, r)
	if !ok {
		return
	}
	v, err := h.store.CreateView(r.Context(), userID, body.Name, params)
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "view_exists", "a view with that name already exists")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	h.respondView(w, r, v, http.StatusCreated)
}

// UpdateView replaces a saved view's name and params.
func (h *Handlers) UpdateView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	body, params, ok := viewBody(w, r)
	if !ok {
		return
	}
	v, err := h.store.UpdateView(r.Context(), userID, viewId, body.Name, params)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "view_not_found", "no such view")
		return
	}
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "view_exists", "a view with that name already exists")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	h.respondView(w, r, v, http.StatusOK)
}

// DeleteView deletes a saved view.
func (h *Handlers) DeleteView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteView(r.Context(), userID, viewId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "view_not_found", "no such view")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetDashboard composes SQL aggregates with one batched enrichment
// price call, cached briefly per user. Enrichment being down degrades
// pricing (available=false) and skips the cache write so recovery is
// visible immediately. Filtered requests skip the cache both ways:
// the unfiltered dashboard is the hot default view, while filter
// combinations are unbounded and cheap to compute live.
func (h *Handlers) GetDashboard(w http.ResponseWriter, r *http.Request, params api.GetDashboardParams) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	f, detail := dashboardFilters(params)
	if detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_param", detail)
		return
	}
	sub := userID.String()
	if !f.Filtered() {
		if body, err := h.cache.GetDashboard(r.Context(), sub); err != nil {
			h.failOpen(r.Context(), "dashboard_get", err)
		} else if body != nil {
			writeRawJSON(w, body)
			return
		}
	}

	counts, err := h.store.DashboardCounts(r.Context(), userID, f)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "aggregation failed")
		return
	}
	rows, err := h.store.PricingRows(r.Context(), userID, f)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "aggregation failed")
		return
	}

	pricing := api.DashboardPricing{Available: true}
	var ids []uuid.UUID
	for _, row := range rows {
		if id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID); id != nil {
			ids = append(ids, *id)
		} else {
			pricing.ExcludedEntries++
		}
	}
	if len(ids) > 0 {
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, ids)
		if err != nil {
			pricing.Available = false
			h.logger.WarnContext(r.Context(), "dashboard pricing unavailable", "err", err)
		} else {
			var total int64
			for _, row := range rows {
				id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID)
				if id == nil {
					continue
				}
				p, okPrice := prices[id.String()]
				v := (*int64)(nil)
				if okPrice {
					v = valueForPackaging(row.Packaging, p)
				}
				if v != nil {
					total += *v
					pricing.PricedEntries++
				} else {
					pricing.UnpricedEntries++
				}
			}
			pricing.TotalValueCents = &total
		}
	} else {
		zero := int64(0)
		pricing.TotalValueCents = &zero
	}

	byPlatform := make([]api.PlatformCount, len(counts.ByPlatform))
	for i, p := range counts.ByPlatform {
		name := p.Name
		if name == "" {
			name = "Unknown"
		}
		byPlatform[i] = api.PlatformCount{Name: name, Count: p.Count}
	}
	spend := make([]api.CurrencySpend, len(counts.Spend))
	for i, s := range counts.Spend {
		spend[i] = api.CurrencySpend{Currency: s.Currency, TotalCents: s.TotalCents}
	}
	dash := api.Dashboard{
		TotalEntries: counts.Total,
		ByStatus:     counts.ByStatus,
		ByItemType:   counts.ByItemType,
		ByPlatform:   byPlatform,
		Spend:        spend,
		Pricing:      pricing,
	}
	body, err := json.Marshal(dash)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "encoding failed")
		return
	}
	if pricing.Available && !f.Filtered() {
		if err := h.cache.PutDashboard(r.Context(), sub, body, h.dashboardTTL); err != nil {
			h.failOpen(r.Context(), "dashboard_put", err)
		}
	}
	writeRawJSON(w, body)
}

// valueHistoryDays fixes the composition window; a window parameter
// can be added later without breaking the contract.
const valueHistoryDays = 90

// pointForPackaging picks the packaging-matched price field from one
// snapshot; nil when the snapshot lists none for that condition.
func pointForPackaging(packaging string, p enrichapi.PricePoint) *int64 {
	switch packaging {
	case "sealed":
		return p.NewCents
	case "cib":
		return p.CibCents
	default:
		return p.LooseCents
	}
}

// composeValueSeries builds one point per day - the union of snapshot
// days across the given series. Each entry contributes its
// packaging-matched price from its effective product's latest snapshot
// on or before the day (prices carry forward between snapshots);
// entries whose product has no snapshot yet contribute nothing that
// day. Series arrive oldest-first from the client.
func composeValueSeries(rows []store.PricingRow, series map[string][]enrichapi.PricePoint) []api.ValuePoint {
	daySet := map[time.Time]bool{}
	for _, points := range series {
		for _, p := range points {
			daySet[p.CapturedAt.UTC().Truncate(24*time.Hour)] = true
		}
	}
	days := make([]time.Time, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	out := make([]api.ValuePoint, 0, len(days))
	for _, day := range days {
		var total int64
		for _, row := range rows {
			id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID)
			if id == nil {
				continue
			}
			points := series[id.String()]
			var latest *enrichapi.PricePoint
			for i := range points {
				if points[i].CapturedAt.UTC().Truncate(24 * time.Hour).After(day) {
					break
				}
				latest = &points[i]
			}
			if latest == nil {
				continue
			}
			if v := pointForPackaging(row.Packaging, *latest); v != nil {
				total += *v
			}
		}
		out = append(out, api.ValuePoint{Date: openapi_types.Date{Time: day}, ValueCents: total})
	}
	return out
}

// GetValueHistory answers the caller's collection value over the last
// ninety days: the CURRENT entry set valued at historical snapshot
// prices (the composition does not reconstruct past collection
// contents). Cached and invalidated exactly like the dashboard; a
// degraded answer is served but never cached.
func (h *Handlers) GetValueHistory(w http.ResponseWriter, r *http.Request) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	sub := userID.String()
	if body, err := h.cache.GetValueHistory(r.Context(), sub); err != nil {
		h.failOpen(r.Context(), "value_history_get", err)
	} else if body != nil {
		writeRawJSON(w, body)
		return
	}

	// Value history is always whole-collection: snapshots record
	// aggregate history, so no filter narrows this composition.
	rows, err := h.store.PricingRows(r.Context(), userID, store.Filters{})
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "aggregation failed")
		return
	}
	var ids []uuid.UUID
	for _, row := range rows {
		if id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID); id != nil {
			ids = append(ids, *id)
		}
	}
	vh := api.ValueHistory{Available: true, Points: []api.ValuePoint{}}
	if len(ids) > 0 {
		series, err := h.enrichment.PriceHistory(r.Context(), bearer, ids, valueHistoryDays)
		if err != nil {
			vh.Available = false
			h.logger.WarnContext(r.Context(), "value history unavailable", "err", err)
		} else {
			vh.Points = composeValueSeries(rows, series)
		}
	}
	body, err := json.Marshal(vh)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "encoding failed")
		return
	}
	if vh.Available {
		if err := h.cache.PutValueHistory(r.Context(), sub, body, h.dashboardTTL); err != nil {
			h.failOpen(r.Context(), "value_history_put", err)
		}
	}
	writeRawJSON(w, body)
}

// GetLibrarySummary answers the deduplicated game library, shaped for
// the enrichment scoring contract.
func (h *Handlers) GetLibrarySummary(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	lib, err := h.store.LibrarySummary(r.Context(), userID)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "summary failed")
		return
	}
	games := make([]api.LibraryGame, len(lib))
	for i, g := range lib {
		games[i] = api.LibraryGame{IgdbGameId: g.IGDBGameID, Rating: g.Rating}
		if g.AllDropped {
			dropped := "dropped"
			games[i].Status = &dropped
		}
	}
	writeJSON(w, http.StatusOK, api.LibrarySummary{Library: games})
}
