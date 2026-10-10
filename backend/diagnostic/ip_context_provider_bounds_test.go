package diagnostic

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func contextTokenCount(t *testing.T, s string) int {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	count := 0
	for {
		_, e := d.Token()
		if e == io.EOF {
			return count
		}
		if e != nil {
			t.Fatal(e)
		}
		count++
	}
}
func TestIPContextProviderByteDepthTokenAndStructureBounds(t *testing.T) {
	minimal := `{"objectClassName":"ip network","startAddress":"1.1.1.0","endAddress":"1.1.1.255"}`
	unknown := func(v string) string { return strings.TrimSuffix(minimal, "}") + `,"unknown":` + v + `}` }
	tokenEmpty := unknown(`[]`)
	n := 16384 - contextTokenCount(t, tokenEmpty)
	tokens := unknown(`[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `]`)
	if contextTokenCount(t, tokens) != 16384 {
		t.Fatal("token fixture")
	}
	entity := `{"roles":["registrant"],"vcardArray":["vcard",[["kind",{},"text","org"],["fn",{},"text","Organization"]]]}`
	withEntities := func(n int) string {
		return strings.TrimSuffix(minimal, "}") + `,"entities":[` + strings.TrimSuffix(strings.Repeat(entity+",", n), ",") + `]}`
	}
	event := `{"eventAction":"registration","eventDate":"2020-01-01T00:00:00Z"}`
	withEvents := func(n int) string {
		return strings.TrimSuffix(minimal, "}") + `,"events":[` + strings.TrimSuffix(strings.Repeat(event+",", n), ",") + `]}`
	}
	withRows := func(n int) string {
		return strings.TrimSuffix(minimal, "}") + `,"entities":[{"roles":["registrant"],"vcardArray":["vcard",[["kind",{},"text","org"],["fn",{},"text","Organization"],` + strings.TrimSuffix(strings.Repeat(`["x",{},"text","ignored"],`, n-2), ",") + `]]}]}`
	}
	for _, tc := range []struct{ name, body, status string }{
		{"bytes-exact", minimal + strings.Repeat(" ", 262144-len(minimal)), "ok"}, {"bytes-over", minimal + strings.Repeat(" ", 262145-len(minimal)), "invalid_response"},
		{"depth-exact", unknown(strings.Repeat("[", 15) + strings.Repeat("]", 15)), "ok"}, {"depth-over", unknown(strings.Repeat("[", 16) + strings.Repeat("]", 16)), "invalid_response"},
		{"tokens-exact", tokens, "ok"}, {"tokens-over", unknown(`[` + strings.TrimSuffix(strings.Repeat("0,", n+1), ",") + `]`), "invalid_response"},
		{"vcard-rows-exact", withRows(16), "ok"}, {"vcard-rows-over", withRows(17), "invalid_response"},
		{"entities-exact", withEntities(32), "ok"}, {"entities-over", withEntities(33), "invalid_response"}, {"events-exact", withEvents(32), "ok"}, {"events-over", withEvents(33), "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewIPContextService(IPContextOptions{Resolver: contextTestResolver{}, HTTP: func(c context.Context, u string) (int, []byte, error) {
				if strings.Contains(u, "network-info") {
					return 404, nil, nil
				}
				return 200, []byte(tc.body), nil
			}})
			v, e := s.Lookup(context.Background(), "1.1.1.1", time.Now())
			if e != nil || v.Registration.Status != tc.status {
				t.Fatalf("got %+v %v", v.Registration, e)
			}
			contextWaitIdle(t, s)
		})
	}
}
