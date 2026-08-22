// Catalog snapshot derivation: region-picked release dates,
// localization, and credits computed from enrichment products.

package server

import (
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// pickReleaseDate resolves an entry's snapshotted date: the first
// chain hit for its region among the product's per-region dates, else
// the platform-level scalar. nil (nothing known) stores NULL, exactly
// today's no-date behavior.
func pickReleaseDate(meta *common.IgdbMeta, region string) *time.Time {
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
			if cur, ok := byRegion[string(rd.Region)]; !ok || rd.Date.Before(cur) {
				byRegion[string(rd.Region)] = rd.Date.Time
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

// pickLocalization resolves an entry's region-picked presentation
// from the product's localization bundles: nil fields mean "no
// localized form" and display falls back to the canonical snapshot.
func pickLocalization(meta *common.IgdbMeta, region string) (name, translit, cover *string) {
	if meta == nil || meta.Localizations == nil {
		return nil, nil, nil
	}
	byRegion := make(map[string]common.Localization, len(*meta.Localizations))
	for _, l := range *meta.Localizations {
		byRegion[l.Region] = l
	}
	for _, want := range regionkit.LocalizationChains[region] {
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

// regionCorrectMember reports whether an entry region needs no
// re-resolve against its product's current mapping: unmatched members
// always re-resolve; matched ones only when the listing's console
// class disagrees with the entry region's class. The class guard is
// what protects a deliberate in-region manual pick (a hand-chosen JP
// variant listing on an ntsc_j entry) from being swept away.
func regionCorrectMember(prod *enrichapi.Product, region string) bool {
	return prod.Pricecharting != nil && regionkit.ConsoleRegion(prod.Pricecharting.ConsoleName) == regionkit.RegionClass(region)
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
	snap.Developers, snap.Publishers = pickCredits(product)
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

// pickCredits derives the credit arrays: IGDB company credits split
// by role in wire order where the product carries them, the community
// block's curated lists filling per role where the provider left one
// empty (the same per-field precedence as the cover chain above).
// Credits are game identity, not region-scoped - no chain table.
func pickCredits(product enrichapi.Product) (developers, publishers []string) {
	if product.Igdb != nil {
		for _, c := range product.Igdb.Companies {
			if c.Developer {
				developers = append(developers, c.Name)
			}
			if c.Publisher {
				publishers = append(publishers, c.Name)
			}
		}
	}
	if product.Community != nil {
		if developers == nil && product.Community.Developers != nil && len(*product.Community.Developers) > 0 {
			developers = *product.Community.Developers
		}
		if publishers == nil && product.Community.Publishers != nil && len(*product.Community.Publishers) > 0 {
			publishers = *product.Community.Publishers
		}
	}
	return developers, publishers
}
