package diagnostic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestIPContextPinnedBootstrapProvenance(t *testing.T) {
	data, err := ipContextBootstrapFiles.ReadFile("ip_context_data/provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Snapshots []struct {
			File        string `json:"file"`
			URL         string `json:"source_url"`
			Publication string `json:"publication"`
			Original    string `json:"original_fetched_sha256"`
			Vendored    string `json:"vendored_sha256"`
			Retrieved   string `json:"retrieved_on"`
		} `json:"snapshots"`
	}
	if json.Unmarshal(data, &p) != nil || len(p.Snapshots) != 2 {
		t.Fatal("provenance")
	}
	for _, pin := range p.Snapshots {
		body, e := ipContextBootstrapFiles.ReadFile("ip_context_data/" + pin.File)
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256(body)
		if hex.EncodeToString(hash[:]) != pin.Vendored || pin.Vendored == pin.Original || len(pin.Original) != 64 || pin.Retrieved != "2026-10-10" {
			t.Fatal("incorrect artifact identity")
		}
		var document struct {
			Publication string `json:"publication"`
		}
		if json.Unmarshal(body, &document) != nil || document.Publication != pin.Publication {
			t.Fatal("publication mismatch")
		}
	}
}
