package download

import "strings"

// ResolveVersion is the version to actually fetch for id: the pin from its own trunk.yaml
// enabled: entry (`id@version`, matching pkg/trunk/config's own enabledIDs/checkEnabled parsing
// of that syntax) if present, else fallback (the definition's KnownGoodVersion).
func ResolveVersion(enabled []string, id, fallback string) string {
	for _, e := range enabled {
		entryID, version, hasVersion := strings.Cut(e, "@")
		if entryID == id && hasVersion {
			return version
		}
	}
	return fallback
}
