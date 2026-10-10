package diagnostic

import (
	"embed"
	"encoding/json"
	"net/netip"
)

//go:embed ip_context_data/*.json
var ipContextBootstrapFiles embed.FS
var contextRegistryBases = map[string]string{"https://rdap.apnic.net/": "apnic", "https://rdap.arin.net/registry/": "arin", "https://rdap.db.ripe.net/": "ripe", "https://rdap.lacnic.net/rdap/": "lacnic", "https://rdap.afrinic.net/rdap/": "afrinic"}

type contextBootstrapRow struct {
	prefix         netip.Prefix
	base, registry string
}

var contextBootstrap = loadContextBootstrap()

func loadContextBootstrap() []contextBootstrapRow {
	rows := []contextBootstrapRow{}
	for _, file := range []string{"ip_context_data/iana-v4.json", "ip_context_data/iana-v6.json"} {
		data, err := ipContextBootstrapFiles.ReadFile(file)
		if err != nil {
			panic("missing pinned bootstrap")
		}
		var document struct {
			Services [][][]string `json:"services"`
		}
		if json.Unmarshal(data, &document) != nil {
			panic("invalid pinned bootstrap")
		}
		for _, service := range document.Services {
			if len(service) != 2 {
				panic("invalid pinned service")
			}
			base, registry := "", ""
			for _, candidate := range service[1] {
				if known := contextRegistryBases[candidate]; known != "" {
					base, registry = candidate, known
					break
				}
			}
			if base == "" {
				panic("unreviewed registry")
			}
			for _, cidr := range service[0] {
				prefix, err := netip.ParsePrefix(cidr)
				if err != nil || prefix != prefix.Masked() {
					panic("invalid pinned prefix")
				}
				rows = append(rows, contextBootstrapRow{prefix, base, registry})
			}
		}
	}
	return rows
}
func contextRDAPRoute(address string) (string, string) {
	ip, ok := canonicalContextAddress(address)
	if !ok {
		return "", ""
	}
	base, registry := "", ""
	bits := -1
	for _, row := range contextBootstrap {
		if row.prefix.Contains(ip) && row.prefix.Bits() > bits {
			bits = row.prefix.Bits()
			base, registry = row.base, row.registry
		}
	}
	return base, registry
}
