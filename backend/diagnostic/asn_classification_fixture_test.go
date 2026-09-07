package diagnostic

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Producer-owned classification witnesses. Every canonical prefix contributes
// both boundaries, adjacent addresses, and each interior host bit (and inverse).
// This is deterministic policy evidence, not a live route/ownership observation.
func TestASNClassificationFixtureFreshness(t *testing.T) {
	type row struct {
		Address string `json:"address"`
		Public  bool   `json:"public"`
		Private bool   `json:"private"`
	}
	rows := []row{}
	seen := map[string]bool{}
	add := func(value *big.Int, width int) {
		if value.Sign() < 0 || value.BitLen() > width {
			return
		}
		ip := net.IP(value.FillBytes(make([]byte, width/8)))
		address := ip.String()
		if seen[address] {
			return
		}
		seen[address] = true
		rows = append(rows, row{address, IsPublicDiagnosticIP(ip), ip.IsPrivate()})
	}
	for _, network := range blockedDiagnosticNetworks {
		prefix, width := network.Mask.Size()
		base := new(big.Int).SetBytes(network.IP)
		size := new(big.Int).Lsh(big.NewInt(1), uint(width-prefix))
		last := new(big.Int).Sub(new(big.Int).Add(base, size), big.NewInt(1))
		for _, value := range []*big.Int{base, last, new(big.Int).Sub(base, big.NewInt(1)), new(big.Int).Add(last, big.NewInt(1))} {
			add(value, width)
		}
		for bit := 0; bit < width-prefix; bit++ {
			offset := new(big.Int).Lsh(big.NewInt(1), uint(bit))
			add(new(big.Int).Add(base, offset), width)
			add(new(big.Int).Sub(last, offset), width)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888", "fec0::1", "::ffff:10.1.2.3", "::ffff:8.8.8.8"} {
		ip := net.ParseIP(address)
		rows = append(rows, row{address, IsPublicDiagnosticIP(ip), ip.IsPrivate()})
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join("..", "..", "testdata", "asn-classification.json")
	if os.Getenv("UPDATE_REPORT_FIXTURES") == "1" {
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("ASN classification fixture stale; regenerate with UPDATE_REPORT_FIXTURES=1")
	}
	t.Logf("%d producer classification witnesses", len(rows))
}
