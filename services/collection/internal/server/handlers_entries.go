// Entry CRUD, listing, and bulk update: input validation, catalog
// snapshot application, and value composition for collection entries.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

var (
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

	packagingVals       = map[string]bool{"sealed": true, "cib": true, "loose": true}
	conditionVals       = map[string]bool{"mint": true, "near_mint": true, "very_good": true, "good": true, "acceptable": true, "poor": true}
	statusVals          = map[string]bool{"backlog": true, "playing": true, "beaten": true, "completed": true, "dropped": true, "shelved": true}
	pricingVals         = map[string]bool{"auto": true, "proxy": true, "custom": true, "disabled": true}
	matchProvenanceVals = map[string]bool{"auto": true, "user": true}
	itemTypeVals        = map[string]bool{"game": true, "console": true, "accessory": true}
	sortVals            = map[string]bool{"name": true, "release_date": true, "purchased_at": true, "created_at": true, "value": true, "paid": true, "rating": true, "backlog_rank": true}
	orderVals           = map[string]bool{"asc": true, "desc": true}
	groupVals           = map[string]bool{"platform": true, "status": true, "item_type": true, "location": true, "tag": true}
)

// entryInput is the shared mutable field set of the create and update
// bodies, unwrapped to plain values (defaults applied) so one
// validator serves both operations.
type entryInput struct {
	Region                     string
	Edition                    *string
	Packaging                  string
	HasBox                     bool
	HasManual                  bool
	BoxCondition               *string
	ManualCondition            *string
	ItemCondition              *string
	PricePaidCents             *int64
	Currency                   string
	PurchasedAt                *time.Time
	PurchasedFrom              *string
	PricingMode                string
	MatchProvenance            string
	PricingProductID           *uuid.UUID
	CustomValueCents           *int64
	CustomValueEnteredCents    *int64
	CustomValueEnteredCurrency *string
	Status                     string
	Rating                     *int
	Notes                      *string
	StorageLocation            *string
	Pinned                     bool
	TagIDs                     []uuid.UUID
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
		Region:                     strings.TrimSpace(b.Region),
		Edition:                    b.Edition,
		Packaging:                  string(b.Packaging),
		HasBox:                     b.HasBox != nil && *b.HasBox,
		HasManual:                  b.HasManual != nil && *b.HasManual,
		BoxCondition:               (*string)(b.BoxCondition),
		ManualCondition:            (*string)(b.ManualCondition),
		ItemCondition:              (*string)(b.ItemCondition),
		PricePaidCents:             b.PricePaidCents,
		Currency:                   strDeref(b.Currency, "USD"),
		PurchasedAt:                dateToTime(b.PurchasedAt),
		PurchasedFrom:              b.PurchasedFrom,
		PricingMode:                strDeref((*string)(b.PricingMode), "auto"),
		PricingProductID:           b.PricingProductId,
		CustomValueCents:           b.CustomValueCents,
		CustomValueEnteredCents:    b.CustomValueEnteredCents,
		CustomValueEnteredCurrency: b.CustomValueEnteredCurrency,
		Status:                     strDeref((*string)(b.Status), "backlog"),
		Rating:                     b.Rating,
		Notes:                      b.Notes,
		StorageLocation:            b.StorageLocation,
		Pinned:                     b.Pinned != nil && *b.Pinned,
		TagIDs:                     uuidsFrom(b.TagIds),
	}
}

// updateInput unwraps an EntryUpdate (full replacement: absent
// optional fields clear).
func updateInput(b api.EntryUpdate) entryInput {
	return entryInput{
		Region:                     strings.TrimSpace(b.Region),
		Edition:                    b.Edition,
		Packaging:                  string(b.Packaging),
		HasBox:                     b.HasBox != nil && *b.HasBox,
		HasManual:                  b.HasManual != nil && *b.HasManual,
		BoxCondition:               (*string)(b.BoxCondition),
		ManualCondition:            (*string)(b.ManualCondition),
		ItemCondition:              (*string)(b.ItemCondition),
		PricePaidCents:             b.PricePaidCents,
		Currency:                   strDeref(b.Currency, "USD"),
		PurchasedAt:                dateToTime(b.PurchasedAt),
		PurchasedFrom:              b.PurchasedFrom,
		PricingMode:                string(b.PricingMode),
		PricingProductID:           b.PricingProductId,
		CustomValueCents:           b.CustomValueCents,
		CustomValueEnteredCents:    b.CustomValueEnteredCents,
		CustomValueEnteredCurrency: b.CustomValueEnteredCurrency,
		Status:                     string(b.Status),
		Rating:                     b.Rating,
		Notes:                      b.Notes,
		StorageLocation:            b.StorageLocation,
		Pinned:                     b.Pinned,
		TagIDs:                     uuidsFrom(b.TagIds),
	}
}

// validCoverURL enforces the cover-link shape: https only, at most 512
// chars. The image is never fetched server-side (SSRF surface); the
// client renders it with a broken-image fallback.
func validCoverURL(s string) bool {
	return len(s) <= 512 && strings.HasPrefix(s, "https://")
}

// validateEntryInput enforces the body rules the generated layer does
// not; a non-empty return is the 400 detail.
func validateEntryInput(in entryInput) string {
	if strings.TrimSpace(in.Region) == "" {
		return "region must not be empty"
	}
	if utf8.RuneCountInString(in.Region) > 32 {
		return "region must be at most 32 characters"
	}
	if !packagingVals[in.Packaging] {
		return "packaging must be one of sealed, cib, loose"
	}
	if !statusVals[in.Status] {
		return "status is not a known value"
	}
	if !pricingVals[in.PricingMode] {
		return "pricing_mode must be one of auto, proxy, custom, disabled"
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
	if in.CustomValueCents != nil && *in.CustomValueCents < 0 {
		return "custom_value_cents must not be negative"
	}
	if in.CustomValueCents != nil && *in.CustomValueCents > 1000000000 {
		return "custom_value_cents must not exceed 1000000000"
	}
	if in.PricingMode == "custom" && in.CustomValueCents == nil {
		return "pricing_mode custom requires custom_value_cents"
	}
	if (in.CustomValueEnteredCents == nil) != (in.CustomValueEnteredCurrency == nil) {
		return "custom_value_entered_cents and custom_value_entered_currency must be provided together"
	}
	if in.CustomValueEnteredCents != nil {
		if in.CustomValueCents == nil {
			return "custom_value_entered requires custom_value_cents"
		}
		if *in.CustomValueEnteredCents < 0 {
			return "custom_value_entered_cents must not be negative"
		}
		if *in.CustomValueEnteredCents > 1000000000 {
			return "custom_value_entered_cents must not exceed 1000000000"
		}
		if !currencyRe.MatchString(*in.CustomValueEnteredCurrency) {
			return "custom_value_entered_currency must be a 3-letter uppercase code"
		}
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
		if lim.v != nil && utf8.RuneCountInString(*lim.v) > lim.max {
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
	e.CustomValueCents = in.CustomValueCents
	e.CustomValueEnteredCents = in.CustomValueEnteredCents
	e.CustomValueEnteredCurrency = in.CustomValueEnteredCurrency
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
		Id:                         e.ID,
		ProductId:                  e.ProductID,
		ItemType:                   api.EntryItemType(e.ItemType),
		MediaType:                  api.EntryMediaType(e.MediaType),
		DisplayName:                e.DisplayName,
		CoverUrl:                   e.CoverURL,
		LocalizedName:              e.LocalizedName,
		LocalizedNameTranslit:      e.LocalizedNameTranslit,
		LocalizedCoverUrl:          e.LocalizedCoverURL,
		IgdbGameId:                 e.IGDBGameID,
		Region:                     e.Region,
		Edition:                    e.Edition,
		Packaging:                  api.EntryPackaging(e.Packaging),
		HasBox:                     e.HasBox,
		HasManual:                  e.HasManual,
		BoxCondition:               (*api.EntryBoxCondition)(e.BoxCondition),
		ManualCondition:            (*api.EntryManualCondition)(e.ManualCondition),
		ItemCondition:              (*api.EntryItemCondition)(e.ItemCondition),
		PricePaidCents:             e.PricePaidCents,
		Currency:                   e.Currency,
		PurchasedFrom:              e.PurchasedFrom,
		PricingMode:                api.EntryPricingMode(e.PricingMode),
		PricingProductId:           e.PricingProductID,
		CustomValueCents:           e.CustomValueCents,
		CustomValueSetAt:           e.CustomValueSetAt,
		CustomValueEnteredCents:    e.CustomValueEnteredCents,
		CustomValueEnteredCurrency: e.CustomValueEnteredCurrency,
		RegionMismatchAckAt:        e.RegionMismatchAckAt,
		Status:                     api.EntryStatus(e.Status),
		Rating:                     e.Rating,
		Notes:                      e.Notes,
		StorageLocation:            e.StorageLocation,
		Pinned:                     e.Pinned,
		BacklogRank:                e.BacklogRank,
		Source:                     api.EntrySource(e.Source),
		ExternalRef:                e.ExternalRef,
		ValueCents:                 valueCents,
		CreatedAt:                  e.CreatedAt,
		UpdatedAt:                  e.UpdatedAt,
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
	if len(e.Developers) > 0 {
		out.Developers = &e.Developers
	}
	if len(e.Publishers) > 0 {
		out.Publishers = &e.Publishers
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
	if e.PricingMode == "custom" {
		value = e.CustomValueCents
	} else if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, []uuid.UUID{*id})
		h.composeEvent(r.Context(), "entry", err)
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
		if body.DisplayName != nil || body.ItemType != nil || body.PlatformName != nil ||
			body.FirstReleaseDate != nil || body.CoverUrl != nil || body.PlatformIgdbId != nil {
			return "catalog fields are snapshotted from the product; omit display_name/item_type/platform_name/first_release_date/cover_url/platform_igdb_id when product_id is set"
		}
		return ""
	}
	if body.DisplayName == nil || strings.TrimSpace(*body.DisplayName) == "" {
		return "custom entries (no product_id) require display_name"
	}
	if utf8.RuneCountInString(*body.DisplayName) > 200 {
		return "display_name is too long"
	}
	if body.ItemType == nil || !itemTypeVals[string(*body.ItemType)] {
		return "custom entries (no product_id) require item_type (game, console, or accessory)"
	}
	if body.PlatformName != nil && (strings.TrimSpace(*body.PlatformName) == "" || utf8.RuneCountInString(*body.PlatformName) > 100) {
		return "platform_name must be 1-100 characters"
	}
	if body.PlatformIgdbId != nil && (body.PlatformName == nil || strings.TrimSpace(*body.PlatformName) == "") {
		return "platform_igdb_id requires platform_name"
	}
	if body.CoverUrl != nil && *body.CoverUrl != "" && !validCoverURL(*body.CoverUrl) {
		return "cover_url must be an https URL up to 512 characters"
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
	in.MatchProvenance = strDeref((*string)(body.MatchProvenance), "auto")
	if !matchProvenanceVals[in.MatchProvenance] {
		problem(w, r, http.StatusBadRequest, "invalid_body", "match_provenance must be one of auto, user")
		return
	}
	if custom {
		if body.PricingMode == nil {
			in.PricingMode = "disabled" // no own product to price against
		} else if in.PricingMode == "auto" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "custom entries cannot use pricing_mode auto; choose proxy, custom, or disabled")
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
		e.PlatformIGDBID = body.PlatformIgdbId
		e.CoverURL = body.CoverUrl
		e.MatchProvenance = in.MatchProvenance
		devs, detail := normalizeCredits("developers", body.Developers)
		if detail != "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", detail)
			return
		}
		pubs, detail := normalizeCredits("publishers", body.Publishers)
		if detail != "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", detail)
			return
		}
		e.Developers, e.Publishers = devs, pubs
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
		snap := catalogSnapshot(product, in.Region)
		e.ProductID = &snap.ProductID
		e.ItemType = snap.ItemType
		e.DisplayName = snap.DisplayName
		e.PlatformIGDBID = snap.PlatformIGDBID
		e.PlatformName = snap.PlatformName
		e.FirstReleaseDate = snap.FirstReleaseDate
		e.IGDBGameID = snap.IGDBGameID
		e.CoverURL = snap.CoverURL
		e.LocalizedName = snap.LocalizedName
		e.LocalizedNameTranslit = snap.LocalizedNameTranslit
		e.LocalizedCoverURL = snap.LocalizedCoverURL
		e.Developers = snap.Developers
		e.Publishers = snap.Publishers
		e.MatchProvenance = in.MatchProvenance
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
		h.internalError(w, r, "create failed", err)
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
		h.internalError(w, r, "get failed", err)
		return
	}
	h.respondEntry(w, r, bearer, e, http.StatusOK)
}

// UpdateEntry replaces the mutable state (the edit form submits the
// whole entry; absent optional fields clear). media_type and the
// catalog snapshot are immutable; product_id accepts only the narrow
// re-match documented on EntryUpdate.
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
		h.internalError(w, r, "get failed", err)
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
	if !custom && (body.DisplayName != nil || body.PlatformName != nil || body.FirstReleaseDate != nil ||
		body.CoverUrl != nil || body.PlatformIgdbId != nil || body.Developers != nil || body.Publishers != nil) {
		problem(w, r, http.StatusBadRequest, "invalid_body", "catalog fields are immutable on product-backed entries")
		return
	}
	if custom {
		if body.DisplayName == nil || strings.TrimSpace(*body.DisplayName) == "" || utf8.RuneCountInString(*body.DisplayName) > 200 {
			problem(w, r, http.StatusBadRequest, "invalid_body", "custom entries require display_name (1-200 characters)")
			return
		}
		if body.PlatformName != nil && (strings.TrimSpace(*body.PlatformName) == "" || utf8.RuneCountInString(*body.PlatformName) > 100) {
			problem(w, r, http.StatusBadRequest, "invalid_body", "platform_name must be 1-100 characters")
			return
		}
		// Full replacement: platform_igdb_id with no usable
		// platform_name IN THIS BODY is invalid regardless of the
		// current row - a nil body.PlatformName would clear the name
		// while keeping the id, tripping the DB's platform pairing
		// CHECK.
		if body.PlatformIgdbId != nil && (body.PlatformName == nil || strings.TrimSpace(*body.PlatformName) == "") {
			problem(w, r, http.StatusBadRequest, "invalid_body", "platform_igdb_id requires platform_name")
			return
		}
		if body.CoverUrl != nil && *body.CoverUrl != "" && !validCoverURL(*body.CoverUrl) {
			problem(w, r, http.StatusBadRequest, "invalid_body", "cover_url must be an https URL up to 512 characters")
			return
		}
		if in.PricingMode == "auto" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "custom entries cannot use pricing_mode auto; choose proxy, custom, or disabled")
			return
		}
	}

	// The snapshotted date and the localized presentation are both
	// region-scoped: a repoint or a region change re-picks them from
	// the product. Game-backed entries only - hardware has no igdb
	// block, and a console's region edit must not depend on enrichment
	// being up.
	var pickProd *enrichapi.Product

	// Narrow re-match: product_id may move an auto-priced entry off an
	// unmatched game product onto a product of the same family (same
	// igdb game and platform). Anything else is invalid_product_change;
	// the same id as stored is a no-op so full-state resends stay
	// idempotent. Snapshotted display fields stay: the family shares
	// name, platform, and cover.
	repoint := false
	if body.ProductId != nil {
		if custom {
			problem(w, r, http.StatusBadRequest, "invalid_product_change", "custom entries have no catalog product")
			return
		}
		repoint = *body.ProductId != *current.ProductID
	}
	if repoint {
		if in.PricingMode != "auto" {
			problem(w, r, http.StatusBadRequest, "invalid_product_change", "product_id can only re-match an auto-priced entry")
			return
		}
		curProd, err := h.enrichment.GetProduct(r.Context(), bearer, *current.ProductID)
		if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
			problem(w, r, http.StatusBadRequest, "invalid_product_change", "the entry's current product is not in the catalog")
			return
		}
		if err != nil {
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		if curProd.Type != "game" || curProd.Pricecharting != nil {
			problem(w, r, http.StatusBadRequest, "invalid_product_change", "product_id can only re-match an entry whose product is an unmatched game")
			return
		}
		newProd, err := h.enrichment.GetProduct(r.Context(), bearer, *body.ProductId)
		if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
			problem(w, r, http.StatusBadRequest, "invalid_product_change", "the new product does not exist in the catalog")
			return
		}
		if err != nil {
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		if newProd.Type != "game" || newProd.Igdb == nil || curProd.Igdb == nil ||
			newProd.Igdb.GameId != curProd.Igdb.GameId ||
			newProd.Platform == nil || curProd.Platform == nil ||
			newProd.Platform.IgdbPlatformId != curProd.Platform.IgdbPlatformId {
			problem(w, r, http.StatusBadRequest, "invalid_product_change", "the new product must be the same game and platform")
			return
		}
		pickProd = &newProd
	}

	// A region-only change (no explicit repoint) still needs a fresh
	// pick: the same product's dates and localized presentation are
	// keyed by region. Game-backed only - current.IGDBGameID is nil for
	// hardware and for any product-backed entry with no igdb block.
	// Auto-priced entries additionally follow their region to the
	// region-correct sibling member (the listing is game identity, so a
	// JP copy's price is a different member): guarded by the console
	// class so a deliberate in-region manual pick is never re-resolved
	// away, class-compatible members skip the resolve hop, and
	// current.MatchProvenance == "auto" below additionally fences off a
	// cross-class pick the user made by hand (the class guard alone
	// would not stop that case).
	var regionRepoint *uuid.UUID
	if pickProd == nil && current.ProductID != nil && current.IGDBGameID != nil && in.Region != current.Region {
		prod, err := h.enrichment.GetProduct(r.Context(), bearer, *current.ProductID)
		if err != nil {
			// Products are never deleted, so any failure here reads as
			// an availability problem, same as the repoint arm.
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		pickProd = &prod
		if in.PricingMode == "auto" && current.MatchProvenance == "auto" && current.PlatformIGDBID != nil && !regionCorrectMember(&prod, in.Region) {
			resolved, err := h.enrichment.Resolve(r.Context(), bearer, enrichapi.ResolveRequest{
				Type: "game", IgdbGameId: current.IGDBGameID,
				PlatformIgdbId: current.PlatformIGDBID, Region: &in.Region,
			})
			if err != nil {
				problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
				return
			}
			if resolved.Id != *current.ProductID {
				id := resolved.Id
				regionRepoint = &id
				pickProd = &resolved
			}
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
	// MatchProvenance survives via this struct copy: applyInput and
	// every arm below leave it alone except the narrow re-match, which
	// stamps "user" explicitly. TestUpdateEntry_PlainEditPreservesProvenance
	// pins a plain edit, a display re-pick, and an automated region
	// repoint all leaving it untouched.
	applyInput(&e, in)
	if repoint {
		e.ProductID = body.ProductId
		e.MatchProvenance = "user"
	}
	if regionRepoint != nil {
		e.ProductID = regionRepoint
	}
	if pickProd != nil {
		e.FirstReleaseDate = pickReleaseDate(pickProd.Igdb, e.Region)
		// A region with no localized form clears what the old one
		// stored, rather than keeping a title the entry no longer has.
		e.LocalizedName, e.LocalizedNameTranslit, e.LocalizedCoverURL = pickLocalization(pickProd.Igdb, e.Region)
		// Credits are not region-scoped, but a repoint is a product
		// change and the fresh fetch is in hand: rewrite them too.
		e.Developers, e.Publishers = pickCredits(*pickProd)
	}
	if custom {
		e.DisplayName = *body.DisplayName
		e.PlatformName = body.PlatformName
		e.FirstReleaseDate = dateToTime(body.FirstReleaseDate)
		e.PlatformIGDBID = body.PlatformIgdbId
		e.CoverURL = body.CoverUrl
		devs, detail := normalizeCredits("developers", body.Developers)
		if detail != "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", detail)
			return
		}
		pubs, detail := normalizeCredits("publishers", body.Publishers)
		if detail != "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", detail)
			return
		}
		// Full replacement, like every custom display fact: an absent
		// field clears.
		e.Developers, e.Publishers = devs, pubs
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
		h.internalError(w, r, "update failed", err)
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
		h.internalError(w, r, "delete failed", err)
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	w.WriteHeader(http.StatusNoContent)
}

// AckEntryRegionMismatch dismisses the region-mismatch banner for the
// entry's current (region, product_id) choice. Same ownership shape
// as DeleteEntry: the store's WHERE scopes to the caller, so a
// foreign or missing entry both surface as 404. Never touches the
// dashboard cache - the ack changes no aggregated field.
func (h *Handlers) AckEntryRegionMismatch(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.AckRegionMismatch(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "ack failed", err)
		return
	}
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
		h.internalError(w, r, "reorder failed", err)
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	h.respondEntry(w, r, bearer, moved, http.StatusOK)
}

// bulkTagArrayCap bounds add_tag_ids/remove_tag_ids and mirrors the
// per-entry tag ceiling store.BulkUpdateEntries enforces after the
// write (store's own entryTagCap) - the same number by design,
// referenced here for the guard and the cap-exceeded message.
const bulkTagArrayCap = 50

// validateBulkUpdate enforces the contract's bulk-update body rules
// the generated layer does not (array bounds, enum membership, the
// at-least-one-action requirement); a non-empty return is the 400
// detail.
func validateBulkUpdate(body api.BulkUpdateRequest) string {
	if len(body.EntryIds) < 1 || len(body.EntryIds) > 200 {
		return "entry_ids must contain between 1 and 200 entries"
	}
	if body.AddTagIds != nil && len(*body.AddTagIds) > bulkTagArrayCap {
		return "add_tag_ids must contain at most 50 entries"
	}
	if body.RemoveTagIds != nil && len(*body.RemoveTagIds) > bulkTagArrayCap {
		return "remove_tag_ids must contain at most 50 entries"
	}
	if body.Status != nil && !statusVals[string(*body.Status)] {
		return "status is not a known value"
	}
	if body.StorageLocation != nil && utf8.RuneCountInString(*body.StorageLocation) > 200 {
		return "storage_location is too long"
	}
	if body.AddTagIds == nil && body.RemoveTagIds == nil && body.Status == nil && body.StorageLocation == nil {
		return "at least one of add_tag_ids, remove_tag_ids, status, storage_location is required"
	}
	return ""
}

// BulkUpdateEntries applies a batch of tag/status/storage-location
// changes across the caller's own entries in one transaction
// (entry_ids the caller does not own are silently excluded, same
// ownership-filtering posture as tag attachment). See
// validateBulkUpdate for the guard rules and store.BulkUpdateEntries
// for the transaction shape, including the per-entry tag cap.
func (h *Handlers) BulkUpdateEntries(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.BulkUpdateRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if detail := validateBulkUpdate(body); detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_body", detail)
		return
	}
	actions := store.BulkActions{
		AddTagIDs:       uuidsFrom(body.AddTagIds),
		RemoveTagIDs:    uuidsFrom(body.RemoveTagIds),
		StorageLocation: body.StorageLocation,
	}
	if body.Status != nil {
		s := string(*body.Status)
		actions.Status = &s
	}

	count, err := h.store.BulkUpdateEntries(r.Context(), userID, body.EntryIds, actions)
	if errors.Is(err, store.ErrTagCapExceeded) {
		problem(w, r, http.StatusBadRequest, "tag_cap_exceeded",
			fmt.Sprintf("an entry would exceed the %d-tag cap; remove some tags first", bulkTagArrayCap))
		return
	}
	if err != nil {
		h.internalError(w, r, "bulk update failed", err)
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	writeJSON(w, http.StatusOK, api.BulkUpdateResult{UpdatedCount: count})
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
			f.Regions = append(f.Regions, string(v))
		}
	}
	// Credits are open-world snapshot facts (IGDB and community names
	// alike): no allowed set to gate against, same as region.
	if params.Developer != nil {
		f.Developers = *params.Developer
	}
	if params.Publisher != nil {
		f.Publishers = *params.Publisher
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
		h.internalError(w, r, "list failed", err)
		return
	}

	pricingAvailable := true
	values := map[uuid.UUID]*int64{}
	compose := func(subset []store.Entry) {
		var ids []uuid.UUID
		for _, e := range subset {
			if e.PricingMode == "custom" {
				values[e.ID] = e.CustomValueCents
				continue
			}
			if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
				ids = append(ids, *id)
			}
		}
		if len(ids) == 0 {
			return
		}
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, ids)
		h.composeEvent(r.Context(), "list", err)
		if err != nil {
			pricingAvailable = false
			h.logger.WarnContext(r.Context(), "list value composition unavailable", "err", err)
			return
		}
		for _, e := range subset {
			if e.PricingMode == "custom" {
				continue
			}
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
	// offset has no contract upper bound; clamp into range before adding
	// limit so the sum can never overflow.
	start := min(offset, total)
	page := entries[start:min(start+limit, total)]
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
