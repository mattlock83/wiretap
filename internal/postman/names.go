package postman

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/bundle"
)

func mockCollectionName(run *bundle.Run, id string) string {
	// Schema references preserve dependency display names even for omitted specs.
	// Prefer a supplied display name to the recorder's default of the ID itself.
	names := []string{}
	for _, schema := range run.Schemas {
		name := strings.TrimSpace(schema.AssetName)
		if schema.Role == "dependency" && schema.AssetID == id && name != "" && name != id {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return id
	}
	sort.Strings(names)
	return names[0] + " - " + id
}

func exportFilename(role, id string, used map[string]bool) string {
	stem := role
	if id != "" {
		var slug strings.Builder
		for _, r := range id {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
				slug.WriteRune(r)
			case r >= 'A' && r <= 'Z':
				slug.WriteRune(r + ('a' - 'A'))
			default:
				slug.WriteByte('-')
			}
		}
		part := strings.Trim(slug.String(), ".-_")
		if len(part) > 80 {
			part = part[:80]
		}
		if part == "" {
			part = "system"
		}
		if part != id {
			part += "-" + filenameHash(id)
		}
		stem += "-" + part
	}
	const extension = ".postman_collection.json"
	filename := stem + extension
	for counter := 1; used[strings.ToLower(filename)]; counter++ {
		filename = fmt.Sprintf("%s-%s-%d%s", stem, filenameHash(id), counter, extension)
	}
	used[strings.ToLower(filename)] = true
	return filename
}

func filenameHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:6])
}

func collectionName(run *bundle.Run) string {
	name, id := "", applicationID(run)
	if run.Application != nil {
		name = strings.TrimSpace(run.Application.Name)
	}
	if name != "" && id != "" {
		return name + " - " + id
	}
	if name != "" {
		return name
	}
	if id != "" {
		return id
	}
	return "Wiretap " + run.RunID
}

func applicationID(run *bundle.Run) string {
	if run.Application != nil {
		if id := strings.TrimSpace(run.Application.ID); id != "" {
			return id
		}
	}
	return strings.TrimSpace(run.APIID)
}
