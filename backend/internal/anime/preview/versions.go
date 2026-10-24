package preview

import (
	"strconv"
	"strings"

	"strm115/backend/internal/releaseparse"
)

// VersionPolicyVersion is part of preview fingerprints, independently of the
// metadata matching engine. Existing authorized downloads retain their choice.
const VersionPolicyVersion int64 = 1

// EpisodeEdition is scoped by the caller to one source/publisher. Specifications
// remain distinct: a different language, resolution or codec is not an upgrade.
func EpisodeEdition(parsed releaseparse.Result) string {
	if parsed.Kind != "episode" || parsed.Episode == nil || parsed.SpecAmbiguous {
		return ""
	}
	return strconv.Itoa(*parsed.Episode) + ":" + releaseparse.SpecKey(parsed.Spec)
}

// ReleaseVersion never guesses an unnumbered correction's revision. Multiple
// conflicting revision numbers also need confirmation, rather than max().
func ReleaseVersion(label string) (int, bool) {
	if label == "" {
		return 1, true
	}
	version := 0
	for _, part := range strings.Split(strings.ToLower(label), " / ") {
		prefix := ""
		if strings.HasPrefix(part, "rev") {
			prefix = "rev"
		} else if strings.HasPrefix(part, "v") {
			prefix = "v"
		} else {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(part, prefix))
		if err != nil || n < 1 || n > 999 || version != 0 && version != n {
			return 0, false
		}
		version = n
	}
	return version, version > 0
}

// PreferCurrentVersions considers only eligible rows, independent of feed order
// and pagination. Losers remain visible, but cannot grant a second download.
// Equal revisions choose a stable post key; this is not a newer-version claim.
func PreferCurrentVersions(items []Item) {
	winners := make(map[string]int, len(items))
	versions := make([]int, len(items))
	for i := range items {
		item := &items[i]
		if !item.Selectable {
			continue
		}
		key := EpisodeEdition(item.Release.Parse)
		if key == "" {
			// Conflicting specifications cannot safely distinguish independent
			// editions, so never let ambiguity bypass the one-version policy.
			if item.Release.Parse.SpecAmbiguous {
				item.Decision, item.ReasonCode, item.Selectable = "needs_confirmation", "RELEASE_EDITION_UNCLEAR", false
				item.Steps = append(item.Steps, Step{"version", item.Decision, item.ReasonCode})
			}
			continue
		}
		n, known := ReleaseVersion(item.Release.Parse.VersionLabel)
		if !known {
			item.Decision, item.ReasonCode, item.Selectable = "needs_confirmation", "RELEASE_VERSION_UNCLEAR", false
			item.Steps = append(item.Steps, Step{"version", item.Decision, item.ReasonCode})
			continue
		}
		versions[i] = n
		old, exists := winners[key]
		if !exists || n > versions[old] || n == versions[old] && item.Release.PostKey < items[old].Release.PostKey {
			winners[key] = i
		}
	}
	for i := range items {
		item := &items[i]
		key := EpisodeEdition(item.Release.Parse)
		winner, exists := winners[key]
		if !item.Selectable || !exists || winner == i {
			continue
		}
		item.ReasonCode = "RELEASE_VERSION_SUPERSEDED"
		if versions[winner] == versions[i] {
			item.ReasonCode = "RELEASE_VERSION_DUPLICATE"
		}
		item.Decision, item.Selectable = "filtered", false
		item.Steps = append(item.Steps, Step{"version", item.Decision, item.ReasonCode})
	}
}
