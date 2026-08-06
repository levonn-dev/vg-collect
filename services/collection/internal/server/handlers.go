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

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

var _ api.ServerInterface = (*Handlers)(nil)

// maxBodyBytes caps request bodies; the largest legitimate body is an
// entry update with long notes, far under this.
const maxBodyBytes = 64 * 1024

// Submission abuse caps (contract-documented). Cancelled rows persist
// precisely so the rolling window counts every creation - a
// cancel/recreate loop cannot evade it.
const (
	submissionPendingCap = 10
	submissionDailyCap   = 20
	submissionRateWindow = 24 * time.Hour
)

var (
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

	regionVals    = map[string]bool{"ntsc_u": true, "ntsc_j": true, "pal": true, "region_free": true}
	packagingVals = map[string]bool{"sealed": true, "cib": true, "loose": true}
	conditionVals = map[string]bool{"mint": true, "near_mint": true, "very_good": true, "good": true, "acceptable": true, "poor": true}
	statusVals    = map[string]bool{"backlog": true, "playing": true, "beaten": true, "completed": true, "dropped": true, "shelved": true}
	pricingVals   = map[string]bool{"auto": true, "proxy": true, "custom": true, "disabled": true}
	itemTypeVals  = map[string]bool{"game": true, "console": true, "accessory": true}
	sortVals      = map[string]bool{"name": true, "release_date": true, "purchased_at": true, "created_at": true, "value": true, "paid": true, "rating": true, "backlog_rank": true}
	orderVals     = map[string]bool{"asc": true, "desc": true}
	groupVals     = map[string]bool{"platform": true, "status": true, "item_type": true, "location": true, "tag": true}
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

// regionChains orders IGDB regions per entry region: the TV-standard
// territory first, its siblings, then worldwide. Language-market
// regions (china, korea, brazil) are deliberately in no chain - their
// dates reflect localization, not the territory standard.
var regionChains = map[string][]string{
	"ntsc_u": {"north_america", "worldwide"},
	"ntsc_j": {"japan", "asia", "worldwide"},
	"pal":    {"europe", "australia", "new_zealand", "worldwide"},
}

// pickReleaseDate resolves an entry's snapshotted date: the first
// chain hit for its region among the product's per-region dates, else
// the platform-level scalar. nil (nothing known) stores NULL, exactly
// today's no-date behavior.
func pickReleaseDate(meta *enrichapi.IgdbMeta, region string) *time.Time {
	if meta == nil {
		return nil
	}
	if meta.ReleaseDates != nil {
		byRegion := make(map[string]time.Time, len(*meta.ReleaseDates))
		for _, rd := range *meta.ReleaseDates {
			// Self-defense: keep the earliest date per region rather than
			// whichever row happens to build last. Enrichment's own
			// projection already guarantees one earliest-per-region row
			// today (platformReleaseDates dedupes), but nothing here
			// enforces that contract against a future producer emitting
			// duplicate region rows.
			if cur, ok := byRegion[rd.Region]; !ok || rd.Date.Before(cur) {
				byRegion[rd.Region] = rd.Date.Time
			}
		}
		for _, want := range regionChains[region] {
			if d, ok := byRegion[want]; ok {
				return &d
			}
		}
	}
	return dateToTime(meta.FirstReleaseDate)
}

// localizationChains orders IGDB localization regions per entry
// region; the first bundle present wins. Values are game_localizations
// region identifiers (ja-JP), NOT the release_region names (japan) -
// two provider vocabularies answering different questions, which is
// why this table stays separate from regionChains. ntsc_u and
// region_free have no chain: the canonical presentation is theirs.
var localizationChains = map[string][]string{
	"ntsc_j": {"ja-JP"},
	"pal":    {"EU"},
}

// pickLocalization resolves an entry's region-picked presentation
// from the product's localization bundles: nil fields mean "no
// localized form" and display falls back to the canonical snapshot.
func pickLocalization(meta *enrichapi.IgdbMeta, region string) (name, translit, cover *string) {
	if meta == nil || meta.Localizations == nil {
		return nil, nil, nil
	}
	byRegion := make(map[string]enrichapi.Localization, len(*meta.Localizations))
	for _, l := range *meta.Localizations {
		byRegion[l.Region] = l
	}
	for _, want := range localizationChains[region] {
		l, ok := byRegion[want]
		if !ok {
			continue
		}
		nonEmpty := func(s *string) *string {
			if s == nil || *s == "" {
				return nil
			}
			return s
		}
		return nonEmpty(l.Name), nonEmpty(l.Translit), nonEmpty(l.CoverUrl)
	}
	return nil, nil, nil
}

// catalogSnapshot derives the entry snapshot from a product. The
// precedence rule: provider blocks (platform, igdb) win per-field
// where present; community facts fill what providers do not supply.
// Shared by product-backed creation (community-lane picks included)
// and submission adoption.
func catalogSnapshot(product enrichapi.Product, region string) store.CatalogSnapshot {
	snap := store.CatalogSnapshot{
		ProductID:   product.Id,
		ItemType:    string(product.Type),
		DisplayName: product.Name,
	}
	if product.Platform != nil {
		snap.PlatformIGDBID = &product.Platform.IgdbPlatformId
		snap.PlatformName = &product.Platform.Name
	} else if product.Community != nil {
		snap.PlatformName = product.Community.PlatformName
	}
	if product.Igdb != nil {
		snap.IGDBGameID = &product.Igdb.GameId
		snap.FirstReleaseDate = pickReleaseDate(product.Igdb, region)
		snap.CoverURL = product.Igdb.CoverUrl
		snap.LocalizedName, snap.LocalizedNameTranslit, snap.LocalizedCoverURL = pickLocalization(product.Igdb, region)
	} else if product.Community != nil && product.Community.FirstReleaseDate != nil {
		d := product.Community.FirstReleaseDate.Time
		snap.FirstReleaseDate = &d
	}
	// Hardware has no igdb block and some games ship no cover; the
	// platform logo is the next-best entry image.
	if (snap.CoverURL == nil || *snap.CoverURL == "") && product.Platform != nil {
		snap.CoverURL = product.Platform.LogoUrl
	}
	// A community product's own cover fills in when neither a provider
	// cover nor a platform logo is present (per-field precedence, same
	// as platform_name/first_release_date above).
	if (snap.CoverURL == nil || *snap.CoverURL == "") && product.Community != nil && product.Community.CoverUrl != nil && *product.Community.CoverUrl != "" {
		snap.CoverURL = product.Community.CoverUrl
	}
	return snap
}

func datesEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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
		Region:                     string(b.Region),
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
		Region:                     string(b.Region),
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
		Region:                     api.EntryRegion(e.Region),
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
		body.CoverUrl != nil || body.PlatformIgdbId != nil) {
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

	// A region-only change (no repoint) still needs a fresh date pick:
	// the same product's dates are keyed by region. Game-backed only -
	// current.IGDBGameID is nil for hardware and for any product-backed
	// entry with no igdb block.
	if pickProd == nil && current.ProductID != nil && current.IGDBGameID != nil && in.Region != current.Region {
		prod, err := h.enrichment.GetProduct(r.Context(), bearer, *current.ProductID)
		if err != nil {
			// Products are never deleted, so any failure here reads as
			// an availability problem, same as the repoint arm.
			problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
			return
		}
		pickProd = &prod
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
	if repoint {
		e.ProductID = body.ProductId
	}
	if pickProd != nil {
		e.FirstReleaseDate = pickReleaseDate(pickProd.Igdb, e.Region)
		// A region with no localized form clears what the old one
		// stored, rather than keeping a title the entry no longer has.
		e.LocalizedName, e.LocalizedNameTranslit, e.LocalizedCoverURL = pickLocalization(pickProd.Igdb, e.Region)
	}
	if custom {
		e.DisplayName = *body.DisplayName
		e.PlatformName = body.PlatformName
		e.FirstReleaseDate = dateToTime(body.FirstReleaseDate)
		e.PlatformIGDBID = body.PlatformIgdbId
		e.CoverURL = body.CoverUrl
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

func toAPISubmission(s store.Submission) api.Submission {
	out := api.Submission{
		Id: s.ID, EntryId: s.EntryID, Status: api.SubmissionStatus(s.Status),
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	out.RejectReason = s.RejectReason
	out.ProductId = s.ProductID
	out.ReviewedAt = s.ReviewedAt
	out.ResolutionAckAt = s.ResolutionAckAt
	return out
}

// CreateSubmission files a custom entry into the catalog review
// queue, under the per-user abuse caps.
func (h *Handlers) CreateSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	entry, err := h.store.GetEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "get failed", err)
		return
	}
	if entry.ProductID != nil {
		problem(w, r, http.StatusBadRequest, "entry_not_custom", "only custom entries submit to the catalog")
		return
	}
	pending, err := h.store.CountPendingSubmissions(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, "count failed", err)
		return
	}
	if pending >= submissionPendingCap {
		h.logger.WarnContext(r.Context(), "submission cap hit", "user_id", userID, "cap", "pending")
		problem(w, r, http.StatusTooManyRequests, "too_many_pending_submissions",
			fmt.Sprintf("at most %d submissions may be pending; wait for review or cancel one", submissionPendingCap))
		return
	}
	recent, err := h.store.CountSubmissionsSince(r.Context(), userID, time.Now().UTC().Add(-submissionRateWindow))
	if err != nil {
		h.internalError(w, r, "count failed", err)
		return
	}
	if recent >= submissionDailyCap {
		h.logger.WarnContext(r.Context(), "submission cap hit", "user_id", userID, "cap", "rate")
		problem(w, r, http.StatusTooManyRequests, "submission_rate_limited",
			fmt.Sprintf("at most %d submissions per rolling 24h; try again later", submissionDailyCap))
		return
	}
	sub, err := h.store.CreateSubmission(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrSubmissionPending) {
		problem(w, r, http.StatusConflict, "submission_pending", "a submission is already pending for this entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "create failed", err)
		return
	}
	h.logger.InfoContext(r.Context(), "submission created", "submission_id", sub.ID, "entry_id", sub.EntryID)
	h.submissionEvent(r.Context(), "created")
	writeJSON(w, http.StatusCreated, toAPISubmission(sub))
}

// GetSubmission serves the entry page's latest-submission read.
func (h *Handlers) GetSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetEntry(r.Context(), userID, entryId); errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	} else if err != nil {
		h.internalError(w, r, "get failed", err)
		return
	}
	sub, err := h.store.LatestSubmissionForEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "the entry has no submissions")
		return
	}
	if err != nil {
		h.internalError(w, r, "get failed", err)
		return
	}
	writeJSON(w, http.StatusOK, toAPISubmission(sub))
}

// AckSubmissionResolution stamps the caller's approved submission for
// the entry so the approval banner stops reappearing. The two-step
// ownership idiom mirrors GetSubmission: a foreign or missing entry is
// entry_not_found, an entry with no approved submission is
// submission_not_found. Idempotent - an already-acked submission is a
// 204 no-op.
func (h *Handlers) AckSubmissionResolution(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if _, err := h.store.GetEntry(r.Context(), userID, entryId); errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	} else if err != nil {
		h.internalError(w, r, "get failed", err)
		return
	}
	sub, err := h.store.LatestApprovedSubmissionForEntry(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "the entry has no approved submission")
		return
	}
	if err != nil {
		h.internalError(w, r, "get failed", err)
		return
	}
	if sub.ResolutionAckAt == nil {
		if err := h.store.AckSubmissionResolution(r.Context(), sub.ID); err != nil {
			h.internalError(w, r, "ack failed", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// CancelSubmission flips the entry's pending submission to cancelled.
func (h *Handlers) CancelSubmission(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	err := h.store.CancelSubmission(r.Context(), userID, entryId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "nothing is pending for this entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "cancel failed", err)
		return
	}
	h.submissionEvent(r.Context(), "cancelled")
	w.WriteHeader(http.StatusNoContent)
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

func toAPITag(t store.Tag) api.Tag {
	return api.Tag{Id: t.ID, Name: t.Name, EntryCount: t.EntryCount}
}

// toAPIView maps a stored view; Params round-trips verbatim.
func toAPIView(v store.View) (api.SavedView, error) {
	var params map[string]any
	if err := json.Unmarshal(v.Params, &params); err != nil {
		return api.SavedView{}, err
	}
	return api.SavedView{
		Id: v.ID, Name: v.Name, Slug: v.Slug,
		Visibility:  api.SavedViewVisibility(v.Visibility),
		PublishedAt: v.PublishedAt,
		Params:      params,
		CreatedAt:   v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}, nil
}

// maxViewParamsBytes caps the opaque view document.
const maxViewParamsBytes = 8192

func validateTagName(name string) string {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 50 {
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
		h.internalError(w, r, "list failed", err)
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
	if errors.Is(err, store.ErrUserTagCapExceeded) {
		problem(w, r, http.StatusTooManyRequests, "cap_exceeded",
			fmt.Sprintf("at most %d tags per user; delete a tag to create another", store.TagCap))
		return
	}
	if err != nil {
		h.internalError(w, r, "create failed", err)
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
		h.internalError(w, r, "rename failed", err)
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
		h.internalError(w, r, "delete failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// viewBody decodes and validates a ViewCreate; the marshaled params
// come back for storage, along with the resolved visibility (default
// private).
func viewBody(w http.ResponseWriter, r *http.Request) (api.ViewCreate, []byte, string, bool) {
	var body api.ViewCreate
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return body, nil, "", false
	}
	if strings.TrimSpace(body.Name) == "" || utf8.RuneCountInString(body.Name) > 100 {
		problem(w, r, http.StatusBadRequest, "invalid_body", "name must be 1-100 characters")
		return body, nil, "", false
	}
	params, err := json.Marshal(body.Params)
	if err != nil || body.Params == nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "params must be a JSON object")
		return body, nil, "", false
	}
	if len(params) > maxViewParamsBytes { // marshaled bytes, not characters: a storage cap, not a maxLength
		problem(w, r, http.StatusBadRequest, "invalid_body", "params is too large")
		return body, nil, "", false
	}
	visibility := "private"
	if body.Visibility != nil {
		// The generated enum type is a plain string underneath (no
		// UnmarshalJSON validation), so an invalid value must be
		// rejected here -- otherwise it reaches the store and only
		// the DB CHECK constraint catches it, surfacing as a 500
		// instead of a 400.
		switch *body.Visibility {
		case api.ViewCreateVisibilityPrivate, api.ViewCreateVisibilityUnlisted, api.ViewCreateVisibilityListed:
			visibility = string(*body.Visibility)
		default:
			problem(w, r, http.StatusBadRequest, "invalid_body", "visibility must be one of private, unlisted, listed")
			return body, nil, "", false
		}
	}
	return body, params, visibility, true
}

func (h *Handlers) respondView(w http.ResponseWriter, r *http.Request, v store.View, status int) {
	out, err := toAPIView(v)
	if err != nil {
		h.internalError(w, r, "view encoding failed", err)
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
		h.internalError(w, r, "list failed", err)
		return
	}
	if len(views) == 0 {
		// First visit (or factory reset): give the two starter
		// shelves, then re-read so the response includes them.
		if err := h.store.SeedDefaultViews(r.Context(), userID); err != nil {
			h.internalError(w, r, "seed failed", err)
			return
		}
		if views, err = h.store.ListViews(r.Context(), userID); err != nil {
			h.internalError(w, r, "list failed", err)
			return
		}
	}
	out := make([]api.SavedView, len(views))
	for i, v := range views {
		av, err := toAPIView(v)
		if err != nil {
			h.internalError(w, r, "view encoding failed", err)
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
	body, params, visibility, ok := viewBody(w, r)
	if !ok {
		return
	}
	v, err := h.store.CreateView(r.Context(), userID, body.Name, params, visibility)
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "view_exists", "a view with that name already exists")
		return
	}
	if err != nil {
		h.internalError(w, r, "create failed", err)
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
	body, params, visibility, ok := viewBody(w, r)
	if !ok {
		return
	}
	v, err := h.store.UpdateView(r.Context(), userID, viewId, body.Name, params, visibility)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "view_not_found", "no such view")
		return
	}
	if errors.Is(err, store.ErrNameTaken) {
		problem(w, r, http.StatusConflict, "view_exists", "a view with that name already exists")
		return
	}
	if err != nil {
		h.internalError(w, r, "update failed", err)
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
		h.internalError(w, r, "delete failed", err)
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
		body, err := h.cache.GetDashboard(r.Context(), sub)
		if err != nil {
			h.failOpen(r.Context(), "dashboard_get", err)
		}
		h.cacheLookup(r.Context(), "dashboard", body != nil)
		if body != nil {
			writeRawJSON(w, body)
			return
		}
	}

	counts, err := h.store.DashboardCounts(r.Context(), userID, f)
	if err != nil {
		h.internalError(w, r, "aggregation failed", err)
		return
	}
	rows, err := h.store.PricingRows(r.Context(), userID, f)
	if err != nil {
		h.internalError(w, r, "aggregation failed", err)
		return
	}

	pricing := api.DashboardPricing{Available: true}
	var ids []uuid.UUID
	var customTotal int64
	customPriced := 0
	for _, row := range rows {
		if row.PricingMode == "custom" {
			// The DB CHECK guarantees the value under custom mode.
			customTotal += *row.CustomValueCents
			customPriced++
			continue
		}
		if id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID); id != nil {
			ids = append(ids, *id)
		} else {
			pricing.ExcludedEntries++
		}
	}
	pricing.PricedEntries = customPriced
	if len(ids) > 0 {
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, ids)
		h.composeEvent(r.Context(), "dashboard", err)
		if err != nil {
			pricing.Available = false
			h.logger.WarnContext(r.Context(), "dashboard pricing unavailable", "err", err)
		} else {
			total := customTotal
			for _, row := range rows {
				if row.PricingMode == "custom" {
					continue
				}
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
		pricing.TotalValueCents = &customTotal
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
		h.internalError(w, r, "encoding failed", err)
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

// ComposeValueSeries builds one point per day - the union of snapshot
// days plus each custom-priced row's set-at day (clamped into the
// window). Product-priced entries contribute their packaging-matched
// price from the latest snapshot on or before the day; custom-priced
// entries contribute their amount from their set-at day forward.
// Prices carry forward between points; entries with nothing known yet
// contribute nothing that day. Each product's points are sorted
// oldest-first internally, regardless of the order series arrives in.
// Exported for tests.
func ComposeValueSeries(rows []store.PricingRow, series map[string][]enrichapi.PricePoint, windowStart time.Time) []api.ValuePoint {
	for _, points := range series {
		sort.SliceStable(points, func(i, j int) bool { return points[i].CapturedAt.Before(points[j].CapturedAt) })
	}
	windowDay := windowStart.UTC().Truncate(24 * time.Hour)
	daySet := map[time.Time]bool{}
	for _, points := range series {
		for _, p := range points {
			daySet[p.CapturedAt.UTC().Truncate(24*time.Hour)] = true
		}
	}
	customDay := func(row store.PricingRow) time.Time {
		d := row.CustomValueSetAt.UTC().Truncate(24 * time.Hour)
		if d.Before(windowDay) {
			return windowDay
		}
		return d
	}
	for _, row := range rows {
		if row.PricingMode == "custom" {
			daySet[customDay(row)] = true
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
			if row.PricingMode == "custom" {
				if !customDay(row).After(day) {
					total += *row.CustomValueCents
				}
				continue
			}
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
	body, err := h.cache.GetValueHistory(r.Context(), sub)
	if err != nil {
		h.failOpen(r.Context(), "value_history_get", err)
	}
	h.cacheLookup(r.Context(), "value_history", body != nil)
	if body != nil {
		writeRawJSON(w, body)
		return
	}

	// Value history is always whole-collection: snapshots record
	// aggregate history, so no filter narrows this composition.
	rows, err := h.store.PricingRows(r.Context(), userID, store.Filters{})
	if err != nil {
		h.internalError(w, r, "aggregation failed", err)
		return
	}
	var ids []uuid.UUID
	for _, row := range rows {
		if row.PricingMode == "custom" {
			continue
		}
		if id := effectiveProductID(row.PricingMode, row.ProductID, row.PricingProductID); id != nil {
			ids = append(ids, *id)
		}
	}
	vh := api.ValueHistory{Available: true, Points: []api.ValuePoint{}}
	series := map[string][]enrichapi.PricePoint{}
	if len(ids) > 0 {
		var err error
		series, err = h.enrichment.PriceHistory(r.Context(), bearer, ids, valueHistoryDays)
		h.composeEvent(r.Context(), "value_history", err)
		if err != nil {
			vh.Available = false
			h.logger.WarnContext(r.Context(), "value history unavailable", "err", err)
		}
	}
	if vh.Available {
		windowStart := time.Now().UTC().AddDate(0, 0, -valueHistoryDays)
		vh.Points = ComposeValueSeries(rows, series, windowStart)
	}
	body, err = json.Marshal(vh)
	if err != nil {
		h.internalError(w, r, "encoding failed", err)
		return
	}
	if vh.Available {
		if err := h.cache.PutValueHistory(r.Context(), sub, body, h.dashboardTTL); err != nil {
			h.failOpen(r.Context(), "value_history_put", err)
		}
	}
	writeRawJSON(w, body)
}

// CountProductReferences is the admin delete's safety read: only this
// service can see entries, so the bff asks here - across all users -
// before asking enrichment to delete a product.
func (h *Handlers) CountProductReferences(w http.ResponseWriter, r *http.Request, productId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	n, err := h.store.CountEntriesByProduct(r.Context(), productId)
	if err != nil {
		h.internalError(w, r, "count failed", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		EntryCount int64 `json:"entry_count"`
	}{n})
}

// ListSubmissions pages the pending queue with live proposals.
func (h *Handlers) ListSubmissions(w http.ResponseWriter, r *http.Request, params api.ListSubmissionsParams) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	limit := 200
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	offset := 0
	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}
	rows, total, err := h.store.ListPendingSubmissions(r.Context(), limit, offset)
	if err != nil {
		h.internalError(w, r, "list failed", err)
		return
	}
	page := api.AdminSubmissionsPage{Submissions: make([]api.AdminSubmission, 0, len(rows)), TotalCount: total}
	for _, row := range rows {
		as := api.AdminSubmission{
			Id: row.ID, EntryId: row.EntryID, UserId: row.UserID,
			Status:      api.AdminSubmissionStatus(row.Status),
			DisplayName: row.DisplayName, ItemType: api.AdminSubmissionItemType(row.ItemType),
			Region:    api.AdminSubmissionRegion(row.Region),
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		as.PlatformName = row.PlatformName
		as.Edition = row.Edition
		as.CoverUrl = row.CoverURL
		if row.FirstReleaseDate != nil {
			d := openapi_types.Date{Time: *row.FirstReleaseDate}
			as.FirstReleaseDate = &d
		}
		page.Submissions = append(page.Submissions, as)
	}
	writeJSON(w, http.StatusOK, page)
}

// SubmitVerdict resolves one pending submission. approve_new is the
// two-phase orchestration: mint (or reuse a prior attempt's recorded
// mint), record on the still-pending row, then adopt+approve - a
// crash between phases retries without twin mints. The only orphan
// window is mint-succeeds-before-record; the guarded product delete
// mops it.
func (h *Handlers) SubmitVerdict(w http.ResponseWriter, r *http.Request, submissionId openapi_types.UUID) {
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	_, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body api.VerdictRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	sub, err := h.store.GetSubmission(r.Context(), submissionId)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "submission_not_found", "no such submission")
		return
	}
	if err != nil {
		h.internalError(w, r, "get failed", err)
		return
	}
	if sub.Status != "pending" {
		problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
		return
	}

	switch string(body.Action) {
	case "reject":
		if body.Reason == nil || strings.TrimSpace(*body.Reason) == "" {
			problem(w, r, http.StatusBadRequest, "invalid_body", "reject requires a reason")
			return
		}
		out, err := h.store.RejectSubmission(r.Context(), sub.ID, strings.TrimSpace(*body.Reason))
		if errors.Is(err, store.ErrSubmissionResolved) {
			problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
			return
		}
		if err != nil {
			h.internalError(w, r, "reject failed", err)
			return
		}
		h.logger.InfoContext(r.Context(), "submission verdict",
			"submission_id", out.ID, "entry_id", out.EntryID, "action", "reject")
		h.submissionEvent(r.Context(), "rejected")
		writeJSON(w, http.StatusOK, toAPISubmission(out))
	case "approve_existing":
		if body.ProductId == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "approve_existing requires product_id")
			return
		}
		h.adoptAndApprove(w, r, bearer, sub, *body.ProductId, "approve_existing")
	case "approve_new":
		if body.Product == nil {
			problem(w, r, http.StatusBadRequest, "invalid_body", "approve_new requires product")
			return
		}
		// Reuse the id a prior approve_new minted and recorded, so a retry
		// never double-mints. Corner: if that recorded product was deleted
		// before the retry, approve_new 404s here; reject, or approve_existing
		// (which overwrites the recorded id), is the escape.
		productID := sub.ProductID
		if productID == nil {
			minted, err := h.enrichment.CreateCommunityProduct(r.Context(), bearer, mintRequest(*body.Product))
			if err != nil {
				problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
				return
			}
			if err := h.store.RecordSubmissionProduct(r.Context(), sub.ID, minted.Id); errors.Is(err, store.ErrSubmissionResolved) {
				problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
				return
			} else if err != nil {
				h.internalError(w, r, "record failed", err)
				return
			}
			productID = &minted.Id
		}
		h.adoptAndApprove(w, r, bearer, sub, *productID, "approve_new")
	default:
		problem(w, r, http.StatusBadRequest, "invalid_body", "action must be approve_new, approve_existing or reject")
	}
}

// adoptAndApprove is the shared verdict tail: fetch the product,
// snapshot it onto the submitter's entry, resolve the row - one
// transaction for the last two. action names the verdict arm for the
// admin audit log.
func (h *Handlers) adoptAndApprove(w http.ResponseWriter, r *http.Request, bearer string, sub store.Submission, productID uuid.UUID, action string) {
	product, err := h.enrichment.GetProduct(r.Context(), bearer, productID)
	if errors.Is(err, enrichmentclient.ErrUnknownProduct) {
		problem(w, r, http.StatusNotFound, "unknown_product", "no such product in the catalog")
		return
	}
	if err != nil {
		problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the catalog cannot be reached")
		return
	}
	entry, err := h.store.GetEntry(r.Context(), sub.UserID, sub.EntryID)
	if errors.Is(err, store.ErrNotFound) {
		problem(w, r, http.StatusNotFound, "entry_not_found", "no such entry")
		return
	}
	if err != nil {
		h.internalError(w, r, "entry load failed", err)
		return
	}
	out, err := h.store.ApproveSubmission(r.Context(), sub.ID, catalogSnapshot(product, entry.Region))
	if errors.Is(err, store.ErrSubmissionResolved) {
		problem(w, r, http.StatusConflict, "submission_resolved", "another admin already resolved this submission")
		return
	}
	if err != nil {
		h.internalError(w, r, "approve failed", err)
		return
	}
	h.logger.InfoContext(r.Context(), "submission verdict",
		"submission_id", out.ID, "entry_id", out.EntryID, "action", action, "product_id", productID)
	h.submissionEvent(r.Context(), "approved")
	h.invalidateDashboard(r.Context(), sub.UserID)
	writeJSON(w, http.StatusOK, toAPISubmission(out))
}

// mintRequest maps the verdict's curated fields onto enrichment's
// mint request.
func mintRequest(p api.CommunityProductSpec) enrichapi.CreateCommunityProductJSONRequestBody {
	out := enrichapi.CreateCommunityProductJSONRequestBody{
		Type: enrichapi.CommunityProductCreateType(p.Type),
		Name: p.Name,
	}
	out.PlatformName = p.PlatformName
	out.Region = p.Region
	out.Edition = p.Edition
	out.FirstReleaseDate = p.FirstReleaseDate
	out.CoverUrl = p.CoverUrl
	return out
}

// PurgeUserData is the collection leg of account deletion.
func (h *Handlers) PurgeUserData(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if err := h.store.PurgeUserData(r.Context(), userID); err != nil {
		h.internalError(w, r, "purge failed", err)
		return
	}
	h.invalidateDashboard(r.Context(), userID)
	w.WriteHeader(http.StatusNoContent)
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
		h.internalError(w, r, "summary failed", err)
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

// InternalResnapshot recomputes every game-backed entry's
// product-derived snapshot fields: the region-picked release date and
// the localized presentation trio (name, transliteration, cover url).
// Hand-routed outside the contract but behind the normal JWT guard;
// the caller's bearer rides the enrichment hops. Idempotent and
// re-runnable - rows are written only when the recomputed date or
// trio differs from what is stored.
func (h *Handlers) InternalResnapshot(w http.ResponseWriter, r *http.Request) {
	_, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	refs, err := h.store.ListGameBackedRefs(r.Context())
	if err != nil {
		h.internalError(w, r, "list failed", err)
		return
	}
	byProduct := make(map[uuid.UUID][]store.GameEntryRef)
	for _, ref := range refs {
		byProduct[ref.ProductID] = append(byProduct[ref.ProductID], ref)
	}
	var seen, failed, updated int
	for pid, group := range byProduct {
		seen++
		prod, err := h.enrichment.GetProduct(r.Context(), bearer, pid)
		if err != nil {
			failed++
			h.logger.WarnContext(r.Context(), "resnapshot: product fetch failed", "product", pid, "err", err)
			continue
		}
		for _, ref := range group {
			pick := pickReleaseDate(prod.Igdb, ref.Region)
			name, translit, cover := pickLocalization(prod.Igdb, ref.Region)
			if datesEqual(pick, ref.FirstReleaseDate) &&
				strPtrEqual(name, ref.LocalizedName) &&
				strPtrEqual(translit, ref.LocalizedNameTranslit) &&
				strPtrEqual(cover, ref.LocalizedCoverURL) {
				continue
			}
			if err := h.store.SetSnapshotFields(r.Context(), ref.EntryID, pick, name, translit, cover); err != nil {
				h.logger.WarnContext(r.Context(), "resnapshot: entry update failed", "entry", ref.EntryID, "err", err)
				continue
			}
			updated++
		}
	}
	h.logger.InfoContext(r.Context(), "resnapshot complete",
		"products_seen", seen, "products_failed", failed, "entries_updated", updated)
	writeJSON(w, http.StatusOK, map[string]int{
		"products_seen": seen, "products_failed": failed, "entries_updated": updated,
	})
}

// InternalNormalizePlatforms canonicalizes free-text custom-entry
// platforms: every entry with a platform_name but no platform_igdb_id
// is matched (case-insensitive, trimmed, exact-or-alias - never fuzzy,
// so nothing is silently misfiled) against the enrichment platform
// catalog and stamped with the canonical igdb id + name. Re-runnable:
// stamped rows leave the selection set. Admin-gated - stricter than
// the resnapshot lever's JWT-only guard, since a mass cross-user write
// earns the role check (resnapshot is deliberately left as-is). The
// caller's bearer rides the enrichment hop.
//
// Not in the contract and not relayed by the bff, so bruno and the
// gateway never reach it - run it by hand against the collection
// service. With the dev stack up and the admin fixture role already
// granted (task grant-fixture-admin):
//
//	kubectl -n vgkeep port-forward svc/collection 8085:8080 &
//	TOKEN=$(curl -s -X POST http://localhost:8082/oauth/dev/token \
//	  -H 'Content-Type: application/json' -d '{"user":"admin"}' \
//	  | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
//	curl -s -X POST http://localhost:8085/internal/normalize-platforms \
//	  -H "Authorization: Bearer $TOKEN"
//
// Answers {"scanned":N,"normalized":N,"skipped":N}.
func (h *Handlers) InternalNormalizePlatforms(w http.ResponseWriter, r *http.Request) {
	_, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	claims, _ := jwtauth.FromContext(r.Context())
	if !claims.HasRole("admin") {
		problem(w, r, http.StatusForbidden, "forbidden", "role admin required")
		return
	}
	platforms, err := h.enrichment.ListPlatforms(r.Context(), bearer)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "enrichment_unavailable", "the platform catalog cannot be reached")
		return
	}
	type canon struct {
		igdbID int64
		name   string
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	byKey := make(map[string]canon, len(platforms)*2)
	for _, p := range platforms {
		byKey[norm(p.Name)] = canon{p.IGDBID, p.Name}
		for _, a := range p.Aliases {
			if _, taken := byKey[norm(a)]; !taken {
				byKey[norm(a)] = canon{p.IGDBID, p.Name}
			}
		}
	}
	refs, err := h.store.ListNameOnlyPlatformEntries(r.Context())
	if err != nil {
		h.internalError(w, r, "list failed", err)
		return
	}
	var normalized, skipped int
	for _, ref := range refs {
		c, matched := byKey[norm(ref.PlatformName)]
		if !matched {
			skipped++
			continue
		}
		if err := h.store.SetEntryPlatformIdentity(r.Context(), ref.EntryID, c.igdbID, c.name); err != nil {
			h.logger.WarnContext(r.Context(), "normalize: entry update failed", "entry", ref.EntryID, "err", err)
			continue
		}
		normalized++
	}
	h.logger.InfoContext(r.Context(), "normalize-platforms complete",
		"scanned", len(refs), "normalized", normalized, "skipped", skipped)
	writeJSON(w, http.StatusOK, map[string]int{
		"scanned": len(refs), "normalized": normalized, "skipped": skipped,
	})
}
